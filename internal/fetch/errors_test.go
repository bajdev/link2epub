package fetch

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }

func TestExplain(t *testing.T) {
	wrap := func(inner error) error { return &url.Error{Op: "Get", URL: "https://news.example/story", Err: inner} }
	tests := []struct {
		name string
		err  error
		want string
		is   error // must still match with errors.Is, when set
	}{
		{"DNS", wrap(&net.DNSError{Name: "news.example", Err: "no such host", IsNotFound: true}),
			"can't find the server news.example; check the address and your internet connection", nil},
		{"refused", wrap(&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}),
			"news.example refused the connection", syscall.ECONNREFUSED},
		{"certificate", wrap(x509.UnknownAuthorityError{}),
			"news.example's security certificate isn't valid, so the connection isn't safe", nil},
		{"timeout", wrap(timeoutErr{}), "news.example took too long to respond", nil},
		{"unknown", wrap(errors.New("something odd")), `Get "https://news.example/story": something odd`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Explain(tt.err)
			if got.Error() != tt.want {
				t.Errorf("Explain = %q, want %q", got, tt.want)
			}
			if tt.is != nil && !errors.Is(got, tt.is) {
				t.Errorf("explained error no longer matches %v", tt.is)
			}
		})
	}
	if Explain(nil) != nil {
		t.Error("Explain(nil) should be nil")
	}
}

func TestFetchRefusedConnection(t *testing.T) {
	// A port that was just closed refuses connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	f := testFetcher()
	f.Retries = 0
	_, err = f.Fetch(context.Background(), mustParse(t, "http://"+addr+"/page"))
	if err == nil || !strings.Contains(err.Error(), "refused the connection") {
		t.Errorf("error = %v, want a refused-connection message", err)
	}
}

func TestStatusErrorHints(t *testing.T) {
	tests := map[int]string{
		http.StatusForbidden:       "returned 403 Forbidden; the site may need a login or may block automated downloads",
		http.StatusNotFound:        "returned 404 Not Found; check the address",
		http.StatusTooManyRequests: "returned 429 Too Many Requests; the site is limiting requests, so try again later",
		http.StatusBadGateway:      "returned 502 Bad Gateway",
	}
	for code, want := range tests {
		got := (&StatusError{URL: "https://news.example/story", Code: code}).Error()
		if got != "https://news.example/story "+want {
			t.Errorf("%d: %q", code, got)
		}
	}
}
