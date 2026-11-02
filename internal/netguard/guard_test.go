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
		name         string
		scheme       string
		ip           string
		allowPrivate bool
		blocked      bool
	}{
		{"https public", "https", "8.8.8.8", false, false},
		{"http public", "http", "8.8.8.8", false, true},
		{"https metadata", "https", "169.254.169.254", false, true},
		{"https metadata with opt-in", "https", "169.254.169.254", true, true},
		{"https private default deny", "https", "192.168.1.10", false, true},
		{"http private default deny", "http", "192.168.1.10", false, true},
		{"http rfc1918 opt-in", "http", "192.168.1.10", true, false},
		{"http loopback opt-in", "http", "127.0.0.1", true, false},
		{"https loopback default deny", "https", "127.0.0.1", false, true},
		{"https cgnat opt-in", "https", "100.64.0.1", true, false},
		{"https alibaba metadata", "https", "100.100.100.200", true, true},
		{"https ipv6 ula opt-in", "https", "fd00::1", true, false},
		{"https ipv6 metadata", "https", "fd00:ec2::254", true, true},
		{"https nat64", "https", "64:ff9b::a9fe:a9fe", true, true},
		{"https ipv6 link-local", "https", "fe80::1", true, true},
		{"https ipv4 multicast", "https", "224.0.0.1", true, true},
		{"ftp", "ftp", "8.8.8.8", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDestination(tc.scheme, []net.IP{net.ParseIP(tc.ip)}, tc.allowPrivate)
			if tc.blocked != errors.Is(err, ErrBlocked) {
				t.Fatalf("err=%v blocked=%v", err, tc.blocked)
			}
		})
	}
}

func TestClientAllowsLoopbackHTTPWithOptIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewClient(2*time.Second, 2*time.Second, WithPrivateUpstreams())
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestClientBlocksLoopbackByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	if _, err := NewClient(2*time.Second, 2*time.Second).Get(server.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
}

func TestClientBlocksPublicCleartext(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://8.8.8.8/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(2*time.Second, 2*time.Second, WithPrivateUpstreams()).Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
}

func TestClientBlocksMetadataAddress(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(2*time.Second, 2*time.Second, WithPrivateUpstreams()).Do(req); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
}
