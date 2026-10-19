package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsPublic(t *testing.T) {
	tests := map[string]bool{
		"93.184.215.14":        true,
		"2606:4700:4700::1111": true,
		"127.0.0.1":            false, // loopback
		"::1":                  false,
		"10.1.2.3":             false, // private networks
		"172.16.0.1":           false,
		"192.168.1.1":          false,
		"fd12:3456::1":         false,
		"169.254.169.254":      false, // link-local: cloud metadata services
		"fe80::1":              false,
		"100.100.1.1":          false, // carrier-grade NAT, Tailscale
		"0.0.0.0":              false,
		"::":                   false,
		"224.0.0.1":            false, // multicast
		"255.255.255.255":      false,
		"::ffff:192.168.1.1":   false, // IPv4-mapped
	}
	for addr, want := range tests {
		if got := isPublic(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestIsLocal(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "192.168.1.20", "169.254.169.254"} {
		if !IsLocal(ctx, host) {
			t.Errorf("IsLocal(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"93.184.215.14", "2606:4700:4700::1111", "name.invalid"} {
		if IsLocal(ctx, host) {
			t.Errorf("IsLocal(%q) = true, want false", host)
		}
	}
}

func TestGuardBlocksLocalPage(t *testing.T) {
	var hits atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })

	f := New(false)
	f.Retries = 0
	_, err := f.Fetch(context.Background(), mustParse(t, srv.URL+"/page"))
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want a BlockedError", err)
	}
	if !strings.Contains(err.Error(), "is a local or private address (127.0.0.1)") {
		t.Errorf("error = %q, want an explanation", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server received %d requests, want none", hits.Load())
	}
}

func TestGuardBlocksRedirectToLocal(t *testing.T) {
	var localHits atomic.Int32
	local := serve(t, func(w http.ResponseWriter, r *http.Request) { localHits.Add(1) })
	public := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, local.URL+"/admin", http.StatusFound)
	})
	// Treat the "public" server as being on the internet.
	publicAddr := netip.MustParseAddrPort(strings.TrimPrefix(public.URL, "http://"))
	defer func(old func(netip.AddrPort) bool) { permitted = old }(permitted)
	permitted = func(ap netip.AddrPort) bool { return ap == publicAddr }

	f := New(false)
	f.Retries = 0
	_, err := f.Fetch(context.Background(), mustParse(t, public.URL+"/story"))
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want a BlockedError", err)
	}
	if localHits.Load() != 0 {
		t.Errorf("local server received %d requests, want none", localHits.Load())
	}
}

func TestFetchRefusesFileRedirect(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/hosts", http.StatusFound)
	})
	f := testFetcher()
	f.Retries = 0
	page, err := f.Fetch(context.Background(), mustParse(t, srv.URL+"/page"))
	if err == nil || page != nil {
		t.Fatalf("Fetch followed a redirect to a local file: page = %+v, err = %v", page, err)
	}
	if !strings.Contains(err.Error(), "unsupported protocol scheme") {
		t.Errorf("error = %v", err)
	}
}

func TestProxyAddrs(t *testing.T) {
	// Set both spellings: which one wins depends on the x/net version.
	for _, name := range []string{"HTTP_PROXY", "http_proxy"} {
		t.Setenv(name, "http://10.0.0.2")
	}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		t.Setenv(name, "proxy.lan:3128")
	}
	got := proxyAddrs()
	for _, want := range []string{"10.0.0.2:80", "proxy.lan:3128"} {
		if !got[want] {
			t.Errorf("proxyAddrs() = %v, missing %s", got, want)
		}
	}
}
