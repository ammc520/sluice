package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/company/sluice/internal/dlp"
)

// TestConcurrentStreamingRequests exercises 100 streaming requests with 20
// chunks each through the real HTTP stack, DLP engine, quota service, audit
// writer, and fake upstream.
func TestConcurrentStreamingRequests(t *testing.T) {
	const (
		concurrency = 100
		chunks      = 20
	)
	env := newGatewayEnv(t, envSetup{upstream: openAIStream(chunks)})
	runtime.GC()
	before := runtime.NumGoroutine()

	client := &http.Client{Timeout: streamRequestTimeout}
	type result struct {
		status  int
		body    string
		elapsed time.Duration
		err     error
	}
	results := make(chan result, concurrency)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := chatBody(fmt.Sprintf("concurrent request %d", i), 32, true)
			ctx, cancel := context.WithTimeout(context.Background(), streamRequestTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				env.http.URL+"/v1/chat/completions", bytes.NewReader(body))
			if err != nil {
				results <- result{err: err}
				return
			}
			req.Header.Set(companyTokenHeader, "Bearer "+env.token)
			req.Header.Set("Content-Type", "application/json")
			reqStart := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer resp.Body.Close()
			out, err := io.ReadAll(resp.Body)
			results <- result{
				status:  resp.StatusCode,
				body:    string(out),
				elapsed: time.Since(reqStart),
				err:     err,
			}
		}(i)
	}
	wg.Wait()
	close(results)

	latencies := make([]time.Duration, 0, concurrency)
	for res := range results {
		if res.err != nil {
			t.Fatalf("request failed: %v", res.err)
		}
		if res.status != http.StatusOK {
			t.Fatalf("status %d body %s", res.status, res.body)
		}
		if !containsAll(res.body, "chunk-0", fmt.Sprintf("chunk-%d", chunks-1), "[DONE]") {
			t.Fatalf("stream incomplete: %s", res.body)
		}
		latencies = append(latencies, res.elapsed)
	}
	if len(latencies) != concurrency {
		t.Fatalf("completed %d requests, want %d", len(latencies), concurrency)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[int(float64(len(latencies))*0.95)-1]
	if p95 > 5*time.Second {
		t.Fatalf("p95 latency %s under local upstream", p95)
	}
	t.Logf("%d streaming requests in %s (p95 %s)", concurrency, time.Since(start), p95)

	for i := 0; i < concurrency; i++ {
		env.nextRequest(t)
	}

	deadline := time.Now().Add(10 * time.Second)
	streaming := 0
	for time.Now().Before(deadline) {
		records := env.auditRecords()
		streaming = 0
		for _, record := range records {
			if record.StatusCode == http.StatusOK && record.TotalTokens == 11 {
				streaming++
			}
		}
		if streaming >= concurrency {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if streaming < concurrency {
		t.Fatalf("persisted %d of %d streaming audit records", streaming, concurrency)
	}

	time.Sleep(time.Second)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+50 {
		t.Fatalf("possible goroutine leak: before %d after %d", before, after)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

// TestPerformanceBudgets pins the two latency budgets from the design:
// gateway overhead with an immediate upstream, and DLP inspection of a
// typical 10 KiB request. Run with -v to record p95 values for the live
// verification checklist.
func TestPerformanceBudgets(t *testing.T) {
	// Wall-clock budgets depend on the host, the database round-trip time, and
	// whether the test database is reached through Docker port mapping. On a
	// laptop they routinely exceed the budget even at the baseline commit, so
	// keep the check opt-in and run it on a quiet, production-like host.
	if os.Getenv("PERF_BUDGETS") != "1" {
		t.Skip("set PERF_BUDGETS=1 to run latency budget checks")
	}
	env := newGatewayEnv(t, envSetup{})
	body := chatBody(strings.Repeat("ordinary project text ", 500), 32, false) // ~10 KiB
	client := &http.Client{Timeout: requestTimeout}
	gatewayURL := env.http.URL + "/v1/chat/completions"
	directURL := env.upstream.URL + "/v1/chat/completions"

	send := func(url string, withAuth bool) time.Duration {
		start := time.Now()
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if withAuth {
			req.Header.Set(companyTokenHeader, "Bearer "+env.token)
		} else {
			req.Header.Set("Authorization", "Bearer upstream-secret-key")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %s: %v", url, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %s status %d", url, resp.StatusCode)
		}
		return time.Since(start)
	}

	for i := 0; i < 20; i++ {
		send(gatewayURL, true)
		send(directURL, false)
	}
	const samples = 200
	gatewayLatencies := make([]time.Duration, 0, samples)
	directLatencies := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		gatewayLatencies = append(gatewayLatencies, send(gatewayURL, true))
		directLatencies = append(directLatencies, send(directURL, false))
	}
	gatewayP95 := p95(gatewayLatencies)
	directP95 := p95(directLatencies)
	added := gatewayP95 - directP95
	if added < 0 {
		added = 0
	}
	t.Logf("gateway p95 %s, direct-upstream p95 %s, added %s (budget 10ms)",
		gatewayP95, directP95, added)
	if added > 10*time.Millisecond {
		t.Fatalf("gateway overhead p95 %s exceeds 10ms budget", added)
	}

	engine, errs := dlp.NewEngine(dlp.BuiltinRules())
	if len(errs) > 0 {
		t.Fatalf("compile DLP rules: %v", errs)
	}
	dlpBody := []byte(`{"model":"test-model","messages":[{"role":"user","content":"` +
		strings.Repeat("ordinary project text ", 500) + `"}]}`)
	for i := 0; i < 50; i++ {
		if _, err := engine.Inspect(dlpBody); err != nil {
			t.Fatal(err)
		}
	}
	dlpLatencies := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		start := time.Now()
		if _, err := engine.Inspect(dlpBody); err != nil {
			t.Fatal(err)
		}
		dlpLatencies = append(dlpLatencies, time.Since(start))
	}
	dlpP95 := p95(dlpLatencies)
	t.Logf("DLP 10KiB inspection p95 %s (budget 5ms)", dlpP95)
	if dlpP95 > 5*time.Millisecond {
		t.Fatalf("DLP p95 %s exceeds 5ms budget", dlpP95)
	}
}

func p95(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted))*0.95) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
