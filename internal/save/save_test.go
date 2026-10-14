package save

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)

	tests := []struct {
		name     string
		arg      string
		kepub    bool
		wantFile string // set for file targets
		wantDir  string // set for folder targets
		wantNote bool
	}{
		{name: "epub file", arg: filepath.Join(dir, "a.epub"), wantFile: filepath.Join(dir, "a.epub")},
		{name: "uppercase extension kept", arg: filepath.Join(dir, "a.EPUB"), wantFile: filepath.Join(dir, "a.EPUB")},
		{name: "extension appended", arg: filepath.Join(dir, "a"), wantFile: filepath.Join(dir, "a.epub")},
		{name: "new path without slash is a file", arg: filepath.Join(dir, "new"), wantFile: filepath.Join(dir, "new.epub")},
		{name: "trailing slash is a folder", arg: filepath.Join(dir, "new") + sep, wantDir: filepath.Join(dir, "new")},
		{name: "forward slash is a folder", arg: filepath.Join(dir, "new") + "/", wantDir: filepath.Join(dir, "new")},
		{name: "existing folder", arg: existing, wantDir: existing},
		{name: "kepub folder", arg: existing, kepub: true, wantDir: existing},
		{name: "kepub file kept", arg: filepath.Join(dir, "a.kepub.epub"), kepub: true, wantFile: filepath.Join(dir, "a.kepub.epub")},
		{name: "kepub renames epub", arg: filepath.Join(dir, "a.epub"), kepub: true, wantFile: filepath.Join(dir, "a.kepub.epub"), wantNote: true},
		{name: "kepub extension appended", arg: filepath.Join(dir, "a"), kepub: true, wantFile: filepath.Join(dir, "a.kepub.epub")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.arg, tt.kepub)
			if err != nil {
				t.Fatalf("Resolve(%q) error: %v", tt.arg, err)
			}
			if tt.wantFile != "" {
				if !got.Known() || got.Path("ignored") != tt.wantFile {
					t.Errorf("Resolve(%q) = %+v, want file %q", tt.arg, got, tt.wantFile)
				}
			} else if got.Known() || got.dir != tt.wantDir {
				t.Errorf("Resolve(%q) = %+v, want folder %q", tt.arg, got, tt.wantDir)
			}
			if (got.Note != "") != tt.wantNote {
				t.Errorf("Resolve(%q) note = %q, want note: %v", tt.arg, got.Note, tt.wantNote)
			}
		})
	}
}

func TestResolveFolderNaming(t *testing.T) {
	dir := t.TempDir()
	for _, kepub := range []bool{false, true} {
		got, err := Resolve(dir, kepub)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(dir, "hello-world.epub")
		if kepub {
			want = filepath.Join(dir, "hello-world.kepub.epub")
		}
		if p := got.Path("Hello, World!"); p != want {
			t.Errorf("kepub=%v: Path = %q, want %q", kepub, p, want)
		}
	}
}

func TestResolveKEPUBNameWithoutFlag(t *testing.T) {
	_, err := Resolve(filepath.Join(t.TempDir(), "a.kepub.epub"), false)
	if !errors.Is(err, ErrKEPUBName) {
		t.Fatalf("error = %v, want ErrKEPUBName", err)
	}
}

func TestResolveHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows

	got, err := Resolve("~/Books/a.epub", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Books", "a.epub"); got.Path("") != want {
		t.Errorf("Path = %q, want %q", got.Path(""), want)
	}

	got, err = Resolve("~", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Known() || got.dir != home {
		t.Errorf("Resolve(~) = %+v, want folder %q", got, home)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello, World!", "hello-world"},
		{"  Leading and trailing  ", "leading-and-trailing"},
		{"Café crème brûlée", "cafe-creme-brulee"},
		{"Go 1.24: What's New?", "go-1-24-what-s-new"},
		{"../../etc/passwd", "etc-passwd"},
		{`a\b/c:d*e?f"g<h>i|j`, "a-b-c-d-e-f-g-h-i-j"},
		{"日本語のタイトル", "article"},
		{"", "article"},
		{"---", "article"},
		{strings.Repeat("ab ", 50), strings.TrimRight(strings.Repeat("ab-", 27)[:80], "-")},
	}
	for _, tt := range tests {
		if got := Slug(tt.in); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if got := Slug(tt.in); len(got) > maxSlugLen || strings.ContainsAny(got, `/\`) {
			t.Errorf("Slug(%q) = %q: too long or contains a separator", tt.in, got)
		}
	}
}

func TestCheckExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.epub")
	writeFile(t, file, "old")

	if err := CheckExists(filepath.Join(dir, "missing.epub")); err != nil {
		t.Errorf("missing file: %v", err)
	}
	if err := CheckExists(file); !errors.Is(err, ErrExists) {
		t.Errorf("existing file: error = %v, want ErrExists", err)
	}
	if err := CheckExists(dir); err == nil || errors.Is(err, ErrExists) {
		t.Errorf("folder: error = %v, want a non-ErrExists error", err)
	}
}

func TestWriteCreatesFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "book.epub")

	if err := Write(path, strings.NewReader("book"), false); err != nil {
		t.Fatal(err)
	}
	assertContent(t, path, "book")
	assertNoLeftovers(t, filepath.Dir(path), "book.epub")
}

func TestWriteRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")
	writeFile(t, path, "old")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	err := Write(path, strings.NewReader("new"), false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	assertContent(t, path, "old")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("modification time changed: %v, want %v", info.ModTime(), old)
	}
	assertNoLeftovers(t, dir, "book.epub")
}

func TestWriteForceReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")
	writeFile(t, path, "old")

	if err := Write(path, strings.NewReader("new"), true); err != nil {
		t.Fatal(err)
	}
	assertContent(t, path, "new")
	assertNoLeftovers(t, dir, "book.epub")
}

func TestWriteFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")
	writeFile(t, path, "old")

	err := Write(path, failingWriter{}, true)
	if err == nil {
		t.Fatal("Write succeeded, want error")
	}
	assertContent(t, path, "old")
	assertNoLeftovers(t, dir, "book.epub")
}

func TestWriteFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")

	if err := Write(path, failingWriter{}, false); err == nil {
		t.Fatal("Write succeeded, want error")
	}
	assertNoLeftovers(t, dir)
}

func TestWritePathThroughFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "Books")
	writeFile(t, blocker, "not a folder")

	err := Write(filepath.Join(blocker, "book.epub"), strings.NewReader("book"), false)
	if err == nil || errors.Is(err, ErrExists) {
		t.Fatalf("error = %v, want a folder creation error", err)
	}
	assertContent(t, blocker, "not a folder")
}

// failingWriter writes part of a book and then fails, like a build error
// half-way through.
type failingWriter struct{}

func (failingWriter) WriteTo(w io.Writer) (int64, error) {
	n, _ := w.Write([]byte("partial"))
	return int64(n), errors.New("build failed")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(want)) {
		t.Errorf("%s contains %q, want %q", path, got, want)
	}
}

// assertNoLeftovers checks that dir contains exactly the named files, so no
// temp files or reservations were left behind.
func assertNoLeftovers(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s contains %v, want %v", dir, got, want)
	}
}
