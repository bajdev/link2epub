// Package cookies loads browser cookies exported to a file, so pages behind
// a login or subscription can be fetched with the user's own session.
//
// Two export formats are accepted:
//
//   - Netscape cookies.txt, as written by curl, yt-dlp and browser
//     extensions such as "Get cookies.txt LOCALLY";
//   - a JSON array of cookie objects, as written by extensions such as
//     "Cookie-Editor" and "EditThisCookie".
//
// Cookie values are secrets: no error message from this package includes
// one.
package cookies

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Cookie is one exported browser cookie.
type Cookie struct {
	Domain   string // without a leading dot
	HostOnly bool   // sent only to Domain itself, not its subdomains
	Path     string
	Secure   bool
	HTTPOnly bool
	Expires  time.Time // zero for a session cookie
	Name     string
	Value    string
}

// Load reads a cookie export file in either supported format and returns
// a cookie jar holding its cookies, and how many cookies it held.
func Load(path string) (*cookiejar.Jar, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	cs, err := Parse(data)
	if err != nil {
		return nil, 0, err
	}
	jar, err := NewJar(cs)
	return jar, len(cs), err
}

// Parse reads cookies in Netscape cookies.txt or JSON format, choosing by
// the content.
func Parse(data []byte) ([]Cookie, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // UTF-8 byte order mark
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("the file is empty")
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		return parseJSON(trimmed)
	}
	return parseNetscape(data)
}

// parseNetscape reads the cookies.txt format: one cookie per line, seven
// tab-separated fields (domain, include-subdomains, path, secure, expiry,
// name, value). Lines starting with # are comments, except "#HttpOnly_",
// which marks an HttpOnly cookie.
func parseNetscape(data []byte) ([]Cookie, error) {
	var cs []Cookie
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		httpOnly := false
		if rest, ok := strings.CutPrefix(line, "#HttpOnly_"); ok {
			line, httpOnly = rest, true
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("line %d: expected 7 tab-separated fields, found %d; is this a Netscape cookies.txt file?", n, len(f))
		}
		subdomains, err := parseBool(f[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: include-subdomains field: %w", n, err)
		}
		secure, err := parseBool(f[3])
		if err != nil {
			return nil, fmt.Errorf("line %d: secure field: %w", n, err)
		}
		expiry, err := strconv.ParseInt(strings.TrimSpace(f[4]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: expiry field is not a number", n)
		}
		c := Cookie{
			Domain:   strings.TrimPrefix(strings.TrimSpace(f[0]), "."),
			HostOnly: !subdomains,
			Path:     f[2],
			Secure:   secure,
			HTTPOnly: httpOnly,
			Name:     f[5],
			Value:    f[6],
		}
		if expiry > 0 {
			c.Expires = time.Unix(expiry, 0)
		}
		if c.Domain == "" || c.Name == "" {
			return nil, fmt.Errorf("line %d: missing domain or cookie name", n)
		}
		cs = append(cs, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, errors.New("no cookies found")
	}
	return cs, nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	}
	return false, errors.New("expected TRUE or FALSE")
}

// jsonCookie is the shape used by Cookie-Editor, EditThisCookie and the
// browser extension cookie API they are built on.
type jsonCookie struct {
	Domain         string   `json:"domain"`
	HostOnly       *bool    `json:"hostOnly"`
	Path           string   `json:"path"`
	Secure         bool     `json:"secure"`
	HTTPOnly       bool     `json:"httpOnly"`
	Session        bool     `json:"session"`
	ExpirationDate *float64 `json:"expirationDate"`
	Name           string   `json:"name"`
	Value          string   `json:"value"`
}

func parseJSON(data []byte) ([]Cookie, error) {
	var raw []jsonCookie
	if err := json.Unmarshal(data, &raw); err != nil {
		// Report where, never what: the content may be a cookie value.
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return nil, fmt.Errorf("not valid JSON (error at byte %d)", syn.Offset)
		}
		return nil, errors.New("expected a JSON array of cookie objects")
	}
	var cs []Cookie
	for i, j := range raw {
		c := Cookie{
			Domain:   strings.TrimPrefix(strings.TrimSpace(j.Domain), "."),
			HostOnly: !strings.HasPrefix(strings.TrimSpace(j.Domain), "."),
			Path:     j.Path,
			Secure:   j.Secure,
			HTTPOnly: j.HTTPOnly,
			Name:     j.Name,
			Value:    j.Value,
		}
		if j.HostOnly != nil {
			c.HostOnly = *j.HostOnly
		}
		if j.ExpirationDate != nil && !j.Session && *j.ExpirationDate > 0 {
			sec, frac := math.Modf(*j.ExpirationDate)
			c.Expires = time.Unix(int64(sec), int64(frac*1e9))
		}
		if c.Domain == "" || c.Name == "" {
			return nil, fmt.Errorf("cookie %d: missing domain or name", i+1)
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 {
		return nil, errors.New("no cookies found")
	}
	return cs, nil
}

// NewJar returns a cookie jar holding cs. The jar sends each cookie only
// where a browser would: to its domain (and subdomains, unless it is
// host-only), under its path, over HTTPS only if it is secure, and not
// after it expires.
func NewJar(cs []Cookie) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		path := c.Path
		if path == "" || path[0] != '/' {
			path = "/"
		}
		hc := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
			Expires:  c.Expires,
		}
		// A Domain attribute makes the cookie apply to subdomains too. IP
		// addresses have no subdomains, and the jar only accepts host-only
		// cookies for them.
		if !c.HostOnly && net.ParseIP(c.Domain) == nil {
			hc.Domain = c.Domain
		}
		jar.SetCookies(&url.URL{Scheme: "https", Host: c.Domain, Path: path}, []*http.Cookie{hc})
	}
	return jar, nil
}
