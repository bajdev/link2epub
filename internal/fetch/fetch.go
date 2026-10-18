// Package fetch downloads a web page and decodes it to UTF-8.
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/html/charset"
)

// DefaultUserAgent looks like a desktop browser, because some sites refuse
// Go's default user agent, and says which tool is asking.
const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 (compatible; link2epub)"

// Page is a downloaded HTML page.
type Page struct {
	URL         *url.URL // final URL, after redirects
	HTML        string   // decoded to UTF-8
	ContentType string   // as sent by the server, or sniffed
}

// StatusError reports a response with a non-2xx status code.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s returned %d %s", e.URL, e.Code, http.StatusText(e.Code))
	switch e.Code {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += "; the site may need a login or may block automated downloads"
	case http.StatusNotFound, http.StatusGone:
		msg += "; check the address"
	case http.StatusTooManyRequests:
		msg += "; the site is limiting requests, so try again later"
	}
	return msg
}

// Fetcher downloads pages. The zero value is not usable; call New.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
	MaxBytes  int64         // largest page accepted
	Retries   int           // extra attempts after a 429, 5xx or network error
	Backoff   time.Duration // wait before the first retry; doubles each time
}

// New returns a Fetcher with the defaults from PLAN.md: a 60 second
// timeout, a 20 MB size limit and two retries. Its client refuses local and
// private addresses unless allowLocal is set; see IsLocal.
func New(allowLocal bool) *Fetcher {
	return &Fetcher{
		Client:    newClient(allowLocal),
		UserAgent: DefaultUserAgent,
		MaxBytes:  20 << 20,
		Retries:   2,
		Backoff:   time.Second,
	}
}

// maxRetryAfter caps how long a server's Retry-After header can make us wait.
const maxRetryAfter = 30 * time.Second

// Fetch downloads u and returns it decoded to UTF-8. It fails for non-2xx
// responses, for content that isn't HTML and for pages over MaxBytes.
func (f *Fetcher) Fetch(ctx context.Context, u *url.URL) (*Page, error) {
	wait := f.Backoff
	for attempt := 0; ; attempt++ {
		page, retryAfter, err := f.fetchOnce(ctx, u)
		if err == nil || retryAfter < 0 || attempt >= f.Retries {
			return page, err
		}
		if retryAfter > 0 {
			wait = min(retryAfter, maxRetryAfter)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// fetchOnce makes a single attempt. retryAfter is negative when the error
// is not worth retrying, zero to use the normal backoff, or the delay the
// server asked for.
func (f *Fetcher) fetchOnce(ctx context.Context, u *url.URL) (page *Page, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")

	resp, err := f.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, ctx.Err()
		}
		return nil, 0, Explain(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := &StatusError{URL: u.String(), Code: resp.StatusCode}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, parseRetryAfter(resp.Header.Get("Retry-After")), err
		}
		return nil, -1, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("reading %s: %w", u, err)
	}
	if int64(len(body)) > f.MaxBytes {
		return nil, -1, fmt.Errorf("%s is larger than %d MB", u, f.MaxBytes>>20)
	}

	// Sniff the content when the server sends no type or only the generic
	// application/octet-stream, as some misconfigured servers do for HTML.
	contentType := resp.Header.Get("Content-Type")
	if mediaType, _, _ := mime.ParseMediaType(contentType); mediaType == "" || mediaType == "application/octet-stream" {
		contentType = http.DetectContentType(body)
	}
	if !isHTML(contentType) {
		return nil, -1, fmt.Errorf("%s is not a web page (content type %q)", u, contentType)
	}

	// charset.NewReader picks the encoding from the Content-Type header, a
	// byte order mark or a <meta charset> tag, then converts to UTF-8.
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return nil, -1, fmt.Errorf("decoding %s: %w", u, err)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		return nil, -1, fmt.Errorf("decoding %s: %w", u, err)
	}

	return &Page{URL: resp.Request.URL, HTML: string(decoded), ContentType: contentType}, 0, nil
}

func isHTML(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mediaType == "text/html" || mediaType == "application/xhtml+xml")
}

// parseRetryAfter reads a Retry-After header given in seconds. HTTP dates
// and missing values fall back to the normal backoff.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
