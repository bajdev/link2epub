package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testFetcher returns a Fetcher with retries that don't slow tests down.
func testFetcher() *Fetcher {
	f := New()
	f.Backoff = time.Millisecond
	return f
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestFetchHTML(t *testing.T) {
	var gotUA, gotAccept string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.UserAgent(), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<p>Héllo</p>"))
	})

	page, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL+"/a"))
	if err != nil {
		t.Fatal(err)
	}
	if page.HTML != "<p>Héllo</p>" {
		t.Errorf("HTML = %q", page.HTML)
	}
	if page.URL.String() != srv.URL+"/a" {
		t.Errorf("URL = %q", page.URL)
	}
	if !strings.Contains(gotUA, "link2epub") {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if !strings.HasPrefix(gotAccept, "text/html") {
		t.Errorf("Accept = %q", gotAccept)
	}
}

func TestFetchFollowsRedirects(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>moved</p>"))
	})

	page, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL+"/old"))
	if err != nil {
		t.Fatal(err)
	}
	if page.URL.Path != "/new" {
		t.Errorf("final URL = %q, want path /new", page.URL)
	}
}

func TestFetchNotFoundIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	})

	_, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL))
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("error = %v, want a 404 StatusError", err)
	}
	if !strings.Contains(err.Error(), "returned 404 Not Found") {
		t.Errorf("error = %q", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server called %d times, want 1", n)
	}
}

func TestFetchRetries(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		var calls atomic.Int32
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) < 3 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(code)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<p>ok</p>"))
		})

		page, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL))
		if err != nil {
			t.Fatalf("%d: %v", code, err)
		}
		if page.HTML != "<p>ok</p>" || calls.Load() != 3 {
			t.Errorf("%d: HTML = %q after %d calls, want ok after 3", code, page.HTML, calls.Load())
		}
	}
}

func TestFetchGivesUpAfterRetries(t *testing.T) {
	var calls atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL))
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusInternalServerError {
		t.Fatalf("error = %v, want a 500 StatusError", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("server called %d times, want 3 (1 + 2 retries)", n)
	}
}

func TestFetchRejectsNonHTML(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.7"))
	})

	_, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "not a web page") {
		t.Fatalf("error = %v, want a not-a-web-page error", err)
	}
}

func TestFetchSniffsMissingContentType(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil // stop net/http from adding one
		w.Write([]byte("<!DOCTYPE html><html><body><p>sniffed</p></body></html>"))
	})

	page, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.HTML, "sniffed") {
		t.Errorf("HTML = %q", page.HTML)
	}
}

func TestFetchRejectsLargePages(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.Repeat("x", 2<<20)))
	})

	f := testFetcher()
	f.MaxBytes = 1 << 20
	_, err := f.Fetch(context.Background(), mustParse(t, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "larger than 1 MB") {
		t.Fatalf("error = %v, want a size error", err)
	}
}

func TestFetchDecodesCharsets(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        string
	}{
		{
			name:        "ISO-8859-1 from header",
			contentType: "text/html; charset=iso-8859-1",
			body:        []byte("<p>caf\xe9</p>"),
			want:        "<p>café</p>",
		},
		{
			name:        "Shift_JIS from meta tag",
			contentType: "text/html",
			body:        append([]byte(`<html><head><meta charset="shift_jis"></head><body><p>`), append([]byte("\x93\xfa\x96\x7b"), []byte("</p></body></html>")...)...),
			want:        "<p>日本</p>",
		},
		{
			name:        "windows-1252 quotes",
			contentType: "text/html; charset=windows-1252",
			body:        []byte("<p>\x93quoted\x94</p>"),
			want:        "<p>“quoted”</p>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.Write(tt.body)
			})
			page, err := testFetcher().Fetch(context.Background(), mustParse(t, srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(page.HTML, tt.want) {
				t.Errorf("HTML = %q, want it to contain %q", page.HTML, tt.want)
			}
		})
	}
}

func TestFetchCancelled(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	f := testFetcher()
	f.Backoff = time.Hour // cancellation must interrupt the wait
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := f.Fetch(ctx, mustParse(t, srv.URL))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("Fetch took %v after cancellation", time.Since(start))
	}
}
