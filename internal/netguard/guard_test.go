package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckDestination(t *testing.T) {
	cases := []struct {
		name    string
		scheme  string
		ip      string
		blocked bool
	}{
		{"https public", "https", "8.8.8.8", false},
		{"http public", "http", "8.8.8.8", true},
		{"https metadata", "https", "169.254.169.254", true},
		{"http metadata", "http", "169.254.169.254", true},
		{"http rfc1918", "http", "192.168.1.10", false},
		{"http loopback", "http", "127.0.0.1", false},
		{"https alibaba metadata", "https", "100.100.100.200", true},
		{"https ipv6 ula", "https", "fd00::1", false},
		{"https ipv6 link-local", "https", "fe80::1", true},
		{"ftp", "ftp", "8.8.8.8", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDestination(tc.scheme, []net.IP{net.ParseIP(tc.ip)})
			if tc.blocked != errors.Is(err, ErrBlocked) {
				t.Fatalf("err=%v blocked=%v", err, tc.blocked)
			}
		})
	}
}

func TestClientAllowsLoopbackHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	resp, err := NewClient(2*time.Second, 2*time.Second).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestClientBlocksPublicCleartext(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://8.8.8.8/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(2*time.Second, 2*time.Second).Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
}

func TestClientBlocksMetadataAddress(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(2*time.Second, 2*time.Second).Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
}
