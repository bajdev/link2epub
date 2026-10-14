package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bajdev/link2epub/internal/epubtest"
	"github.com/bajdev/link2epub/internal/pipeline"
)

// fakeConverter returns a fixed book and counts how often it was called, so
// tests can check that nothing is fetched when arguments are rejected.
type fakeConverter struct {
	title string
	err   error
	calls int
}

func (f *fakeConverter) convert(ctx context.Context, u *url.URL) (*pipeline.Book, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &pipeline.Book{Title: f.title, Content: strings.NewReader("epub:" + f.title)}, nil
}

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, conv *fakeConverter, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr, conv.convert)
	return result{code, stdout.String(), stderr.String()}
}

const testURL = "https://example.com/article"

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "a.epub")
	tests := []struct {
		name string
		args []string
		want string // expected in stderr
	}{
		{"no arguments", nil, "-URL is required"},
		{"missing -SAVE", []string{"-URL", testURL}, "-SAVE is required"},
		{"missing -URL", []string{"-SAVE", out}, "-URL is required"},
		{"lowercase flag", []string{"-url", testURL, "-SAVE", out}, "flag provided but not defined: -url"},
		{"extra argument", []string{"-URL", testURL, "-SAVE", out, "extra"}, `unexpected argument "extra"`},
		{"ftp URL", []string{"-URL", "ftp://example.com/a", "-SAVE", out}, "must start with http:// or https://"},
		{"no scheme", []string{"-URL", "example.com/a", "-SAVE", out}, "must start with http:// or https://"},
		{"no host", []string{"-URL", "https://", "-SAVE", out}, "missing host name"},
		{"invalid URL", []string{"-URL", "https://exa mple.com/%zz", "-SAVE", out}, "not a valid URL"},
		{"kepub name without -KEPUB", []string{"-URL", testURL, "-SAVE", filepath.Join(dir, "a.kepub.epub")}, "needs -KEPUB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := &fakeConverter{title: "T"}
			r := runCLI(t, conv, tt.args...)
			if r.code != exitUsage {
				t.Errorf("exit code = %d, want %d", r.code, exitUsage)
			}
			if !strings.Contains(r.stderr, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", r.stderr, tt.want)
			}
			if !strings.Contains(r.stderr, "Usage: link2epub") {
				t.Errorf("stderr does not show usage: %q", r.stderr)
			}
			if conv.calls != 0 {
				t.Errorf("converter called %d times, want 0", conv.calls)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	r := runCLI(t, &fakeConverter{}, "-h")
	if r.code != exitOK {
		t.Errorf("exit code = %d, want %d", r.code, exitOK)
	}
	if !strings.Contains(r.stderr, "Usage: link2epub") {
		t.Errorf("stderr = %q, want usage", r.stderr)
	}
}

func TestSaveToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.epub")
	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", path)
	if r.code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != path {
		t.Errorf("stdout = %q, want %q", r.stdout, path)
	}
	assertFile(t, path, "epub:Title")
}

func TestSaveToFolderCreatesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Books", "Articles")
	r := runCLI(t, &fakeConverter{title: "My Great Article"}, "-URL", testURL, "-SAVE", dir+"/")
	if r.code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	want := filepath.Join(dir, "my-great-article.epub")
	if strings.TrimSpace(r.stdout) != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
	assertFile(t, want, "epub:My Great Article")
}

func TestExistingFileStopsBeforeFetching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.epub")
	writeFile(t, path, "old")
	conv := &fakeConverter{title: "Title"}

	r := runCLI(t, conv, "-URL", testURL, "-SAVE", path)
	if r.code != exitExists {
		t.Errorf("exit code = %d, want %d", r.code, exitExists)
	}
	if !strings.Contains(r.stderr, "already exists (use -FORCE to overwrite)") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if conv.calls != 0 {
		t.Errorf("converter called %d times, want 0", conv.calls)
	}
	assertFile(t, path, "old")
}

func TestExistingFileInFolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "title.epub")
	writeFile(t, path, "old")

	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", dir)
	if r.code != exitExists {
		t.Errorf("exit code = %d, want %d; stderr = %q", r.code, exitExists, r.stderr)
	}
	assertFile(t, path, "old")
}

func TestForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.epub")
	writeFile(t, path, "old")

	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", path, "-FORCE")
	if r.code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	assertFile(t, path, "epub:Title")

	// -FORCE also applies to folder targets.
	writeFile(t, filepath.Join(dir, "title.epub"), "old")
	r = runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", dir, "-FORCE")
	if r.code != exitOK {
		t.Fatalf("folder: exit code = %d, stderr = %q", r.code, r.stderr)
	}
	assertFile(t, filepath.Join(dir, "title.epub"), "epub:Title")
}

func TestConversionFailureWritesNothing(t *testing.T) {
	dir := t.TempDir()
	conv := &fakeConverter{err: errors.New("boom")}

	r := runCLI(t, conv, "-URL", testURL, "-SAVE", filepath.Join(dir, "a.epub"))
	if r.code != exitBuild {
		t.Errorf("exit code = %d, want %d", r.code, exitBuild)
	}
	if !strings.Contains(r.stderr, "error: boom") {
		t.Errorf("stderr = %q", r.stderr)
	}
	assertEmptyDir(t, dir)
}

func TestSavePathThroughFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Books"), "not a folder")

	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", filepath.Join(dir, "Books", "a.epub"))
	if r.code != exitBuild {
		t.Errorf("exit code = %d, want %d; stderr = %q", r.code, exitBuild, r.stderr)
	}
}

func TestKEPUBExtensionNote(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.kepub.epub"), "old")

	// The overwrite check uses the adjusted name, before anything is fetched.
	conv := &fakeConverter{title: "Title"}
	r := runCLI(t, conv, "-URL", testURL, "-SAVE", filepath.Join(dir, "a.epub"), "-KEPUB")
	if !strings.Contains(r.stderr, "note: saving as a.kepub.epub") {
		t.Errorf("stderr = %q, want a note about the new name", r.stderr)
	}
	if r.code != exitExists || conv.calls != 0 {
		t.Errorf("exit code = %d, converter calls = %d; want %d and 0", r.code, conv.calls, exitExists)
	}
}

func TestKEPUBNotImplemented(t *testing.T) {
	// Until milestone M5, -KEPUB is accepted but conversion fails cleanly.
	dir := t.TempDir()
	r := runCLI(t, &fakeConverter{title: "Title"}, "-URL", testURL, "-SAVE", dir, "-KEPUB")
	if r.code != exitBuild || !strings.Contains(r.stderr, "KEPUB conversion is not implemented yet") {
		t.Errorf("exit code = %d, stderr = %q", r.code, r.stderr)
	}
	assertEmptyDir(t, dir)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s contains %q, want %q", path, got, want)
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%s is not empty: %v", dir, entries)
	}
}

// TestEndToEnd runs the real pipeline against a local web server.
func TestEndToEnd(t *testing.T) {
	blog, err := os.ReadFile("../../testdata/pages/blog.html")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/posts/eink":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(blog)
		case "/app":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body><div id="root"></div><script src="/app.js"></script></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	realRun := func(args ...string) result {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), args, &stdout, &stderr, pipeline.Run)
		return result{code, stdout.String(), stderr.String()}
	}

	t.Run("article", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/posts/eink", "-SAVE", dir)
		if r.code != exitOK {
			t.Fatalf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		path := filepath.Join(dir, "why-e-ink-is-easier-on-the-eyes.epub")
		if strings.TrimSpace(r.stdout) != path {
			t.Errorf("stdout = %q, want %q", r.stdout, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		book := epubtest.Open(t, data)
		book.AssertWellFormed(t)
		chapter := book.File(t, "chapter.xhtml")
		for _, want := range []string{
			"<h1>Why E-Ink Is Easier on the Eyes</h1>",
			"Ada Lovelace · The Reading Desk · 14 March 2025",
			"Electronic paper does not shine light into your eyes.",
		} {
			if !strings.Contains(chapter, want) {
				t.Errorf("chapter is missing %q", want)
			}
		}
		if strings.Contains(chapter, "Great article, thanks!") {
			t.Errorf("chapter contains page comments")
		}
		epubtest.EPUBCheck(t, path)
	})

	t.Run("not found", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/missing", "-SAVE", dir)
		if r.code != exitFetch || !strings.Contains(r.stderr, "returned 404 Not Found") {
			t.Errorf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		assertEmptyDir(t, dir)
	})

	t.Run("no content", func(t *testing.T) {
		dir := t.TempDir()
		r := realRun("-URL", srv.URL+"/app", "-SAVE", dir)
		if r.code != exitNoContent || !strings.Contains(r.stderr, "no readable article content") {
			t.Errorf("exit code = %d, stderr = %q", r.code, r.stderr)
		}
		assertEmptyDir(t, dir)
	})
}
