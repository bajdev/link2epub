package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collect runs collectURLs and returns the URLs as strings and the notes.
func collect(t *testing.T, sources []urlSource, stdin string) ([]string, []string, error) {
	t.Helper()
	var notes []string
	urls, err := collectURLs(sources, strings.NewReader(stdin), func(msg string) { notes = append(notes, msg) })
	var got []string
	for _, u := range urls {
		got = append(got, u.String())
	}
	return got, notes, err
}

func writeList(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectURLsKeepsOrder(t *testing.T) {
	list := writeList(t, "\xef\xbb\xbf# weekend reading\r\nhttps://b.example/2\r\n\r\n   https://c.example/3   \r\n#https://skipped.example/\r\n")
	got, notes, err := collect(t, []urlSource{
		{url: "https://a.example/1"},
		{list: list},
		{url: "https://d.example/4"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://a.example/1", "https://b.example/2", "https://c.example/3", "https://d.example/4"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("URLs = %q, want %q", got, want)
	}
	if len(notes) != 0 {
		t.Errorf("notes = %q, want none", notes)
	}
}

func TestCollectURLsFromStdin(t *testing.T) {
	got, _, err := collect(t, []urlSource{{list: "-"}}, "https://a.example/1\nhttps://b.example/2\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("URLs = %q, want 2", got)
	}
}

func TestCollectURLsDropsRepeats(t *testing.T) {
	got, notes, err := collect(t, []urlSource{
		{url: "https://a.example/1"},
		{url: "https://b.example/2"},
		{url: "https://a.example/1"},
		{url: "https://a.example/1#comments"}, // the same article
		{url: "https://a.example/1?page=2"},   // a different one
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://a.example/1", "https://b.example/2", "https://a.example/1?page=2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("URLs = %q, want %q", got, want)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "https://a.example/1 is listed more than once") {
		t.Errorf("notes = %q, want two about repeats", notes)
	}
}

func TestCollectURLsErrors(t *testing.T) {
	tests := []struct {
		name    string
		sources []urlSource
		want    string
		flag    bool // reported with the usage text
	}{
		{"bad -URL", []urlSource{{url: "ftp://a.example/"}}, `-URL "ftp://a.example/": must start with http:// or https://`, true},
		{"bad line", []urlSource{{list: writeList(t, "https://a.example/\n\nfile:///etc/passwd\n")}},
			`line 3: "file:///etc/passwd": must start with http:// or https://`, false},
		{"empty list", []urlSource{{list: writeList(t, "# nothing yet\n\n")}}, "has no URLs in it", false},
		{"missing list", []urlSource{{list: filepath.Join(t.TempDir(), "nope.txt")}}, "-URLS: open", false},
		{"huge list", []urlSource{{list: writeList(t, strings.Repeat("x", maxListBytes+1))}}, "too large to be a list of URLs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := collect(t, tt.sources, "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one containing %q", err, tt.want)
			}
			if got := errors.As(err, new(flagError)); got != tt.flag {
				t.Errorf("flagError = %v, want %v", got, tt.flag)
			}
		})
	}
}

// numbered returns n distinct URLs, one per line.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "https://example.com/article/%d\n", i)
	}
	return b.String()
}

func TestCollectURLsLimit(t *testing.T) {
	// Exactly 50 is fine.
	if got, _, err := collect(t, []urlSource{{list: writeList(t, numbered(50))}}, ""); err != nil || len(got) != 50 {
		t.Errorf("50 URLs: got %d, error %v; want all 50", len(got), err)
	}

	// 51 is refused, with a message that says what to do.
	_, _, err := collect(t, []urlSource{{list: writeList(t, numbered(51))}}, "")
	want := "too many URLs: 51 given, but a book can hold at most 50. Split the list into several books."
	if err == nil || err.Error() != want {
		t.Errorf("51 URLs: error = %v, want %q", err, want)
	}

	// Repeats don't count towards the limit.
	got, _, err := collect(t, []urlSource{{list: writeList(t, numbered(50)+"https://example.com/article/7\n")}}, "")
	if err != nil || len(got) != 50 {
		t.Errorf("50 URLs plus a repeat: got %d, error %v; want 50", len(got), err)
	}
}
