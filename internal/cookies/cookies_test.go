package cookies

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const netscape = "\xef\xbb\xbf# Netscape HTTP Cookie File\r\n" +
	"# https://curl.se/docs/http-cookies.html\r\n" +
	"\r\n" +
	".news.example\tTRUE\t/\tTRUE\t4102444800\tsession\tsecret-session-value\r\n" +
	"news.example\tFALSE\t/\tFALSE\t0\thostonly\tabc\r\n" +
	"#HttpOnly_.news.example\tTRUE\t/members\tTRUE\t4102444800\ttoken\tsecret-token\r\n" +
	"news.example\tFALSE\t/\tFALSE\t1000\texpired\told\r\n" +
	"other.example\tFALSE\t/\tFALSE\t0\tempty\t\r\n"

func names(t *testing.T, raw string, cs []Cookie) []string {
	t.Helper()
	jar, err := NewJar(cs)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	var out []string
	for _, c := range jar.Cookies(u) {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

func TestParseNetscape(t *testing.T) {
	cs, err := Parse([]byte(netscape))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 5 {
		t.Fatalf("got %d cookies, want 5", len(cs))
	}
	want := Cookie{Domain: "news.example", HostOnly: false, Path: "/members", Secure: true, HTTPOnly: true,
		Expires: time.Unix(4102444800, 0), Name: "token", Value: "secret-token"}
	if cs[2] != want {
		t.Errorf("HttpOnly cookie = %+v, want %+v", cs[2], want)
	}
	if !cs[1].HostOnly || !cs[1].Expires.IsZero() {
		t.Errorf("host-only session cookie = %+v", cs[1])
	}
	if cs[4].Value != "" {
		t.Errorf("empty value = %q", cs[4].Value)
	}
}

func TestJarSendsCookiesWhereABrowserWould(t *testing.T) {
	cs, err := Parse([]byte(netscape))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"https://news.example/story":         "hostonly,session",
		"https://news.example/members/story": "hostonly,session,token",
		"https://www.news.example/story":     "session",  // host-only cookie stays on news.example
		"http://news.example/story":          "hostonly", // secure cookies need HTTPS
		"https://other.example/":             "empty",
		"https://evil.example/":              "", // nothing leaks to other sites
		"https://news.example.evil.example/": "",
	}
	for raw, want := range tests {
		if got := strings.Join(names(t, raw, cs), ","); got != want {
			t.Errorf("%s gets cookies %q, want %q", raw, got, want)
		}
	}
}

func TestParseJSON(t *testing.T) {
	data := `[
	  {"domain": ".news.example", "hostOnly": false, "path": "/", "secure": true, "httpOnly": true,
	   "session": false, "expirationDate": 4102444800.5, "name": "session", "value": "secret", "storeId": "0"},
	  {"domain": "news.example", "path": "/", "secure": false, "session": true, "name": "prefs", "value": "dark"},
	  {"domain": "news.example", "hostOnly": true, "path": "/", "name": "old", "value": "x", "expirationDate": 1000}
	]`
	cs, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 {
		t.Fatalf("got %d cookies, want 3", len(cs))
	}
	if cs[0].HostOnly || !cs[0].Secure || !cs[0].HTTPOnly || cs[0].Expires.Unix() != 4102444800 {
		t.Errorf("first cookie = %+v", cs[0])
	}
	if !cs[1].HostOnly || !cs[1].Expires.IsZero() {
		t.Errorf("session cookie without a leading dot should be host-only with no expiry: %+v", cs[1])
	}
	if got := strings.Join(names(t, "https://www.news.example/", cs), ","); got != "session" {
		t.Errorf("subdomain gets %q, want only the domain cookie", got)
	}
	if got := strings.Join(names(t, "https://news.example/", cs), ","); got != "prefs,session" {
		t.Errorf("news.example gets %q (the expired cookie must be dropped)", got)
	}
}

func TestParseErrorsNeverShowValues(t *testing.T) {
	tests := map[string]string{
		"":                  "empty",
		"   \n":             "empty",
		"# only comments\n": "no cookies",
		"news.example TRUE / TRUE 0 n secretval\n":                      "line 1: expected 7 tab-separated fields",
		"news.example\tYES\t/\tTRUE\t0\tn\tsecretval\n":                 "line 1: include-subdomains field",
		"news.example\tTRUE\t/\tMAYBE\t0\tn\tsecretval\n":               "line 1: secure field",
		"news.example\tTRUE\t/\tTRUE\tsoon\tn\tsecretval\n":             "line 1: expiry field",
		"\tTRUE\t/\tTRUE\t0\tn\tsecretval\n":                            "line 1: missing domain",
		`[{"domain": "news.example", "name": "n", "value": "secretval"`: "not valid JSON",
		`{"domain": "news.example", "value": "secretval"}`:              "expected a JSON array",
		`[{"domain": "news.example", "value": "secretval"}]`:            "cookie 1: missing domain or name",
		`[]`: "no cookies",
	}
	for in, want := range tests {
		_, err := Parse([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want it to contain %q", in, err, want)
			continue
		}
		if strings.Contains(err.Error(), "secretval") {
			t.Errorf("Parse(%q) error reveals a cookie value: %v", in, err)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(netscape), 0o600); err != nil {
		t.Fatal(err)
	}
	jar, n, err := Load(path)
	if err != nil || n != 5 {
		t.Fatalf("Load: n = %d, err = %v", n, err)
	}
	u, _ := url.Parse("https://news.example/")
	if len(jar.Cookies(u)) != 2 {
		t.Errorf("jar holds %d cookies for news.example, want 2", len(jar.Cookies(u)))
	}
	if _, _, err := Load(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Error("Load of a missing file succeeded")
	}
}

func TestIPAddressCookies(t *testing.T) {
	// Local test servers run on IP addresses, which have no subdomains.
	cs, err := Parse([]byte("127.0.0.1\tTRUE\t/\tFALSE\t0\tsession\tabc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, "http://127.0.0.1:8080/page", cs), ","); got != "session" {
		t.Errorf("127.0.0.1 gets %q, want session", got)
	}
}
