// Package epubtest has test helpers for checking generated EPUB files.
package epubtest

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Book is an opened EPUB.
type Book struct {
	Files map[string]string // zip entry name → content
	Order []string          // zip entry names in archive order
}

// Open reads an EPUB and checks the container basics every reader relies
// on: "mimetype" is the first entry, stored uncompressed, and
// META-INF/container.xml exists.
func Open(t *testing.T, data []byte) *Book {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip file: %v", err)
	}
	b := &Book{Files: map[string]string{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		b.Files[f.Name] = string(content)
		b.Order = append(b.Order, f.Name)
	}

	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
		t.Errorf("first zip entry = %v, want mimetype", b.Order)
	} else if zr.File[0].Method != zip.Store {
		t.Errorf("mimetype is compressed, want stored")
	}
	if got := b.Files["mimetype"]; got != "application/epub+zip" {
		t.Errorf("mimetype = %q", got)
	}
	if _, ok := b.Files["META-INF/container.xml"]; !ok {
		t.Errorf("META-INF/container.xml missing")
	}
	return b
}

// File returns the content of the single entry whose name ends in suffix.
func (b *Book) File(t *testing.T, suffix string) string {
	t.Helper()
	var found []string
	for _, name := range b.Order {
		if strings.HasSuffix(name, suffix) {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		t.Fatalf("entries ending in %q: %v (all: %v)", suffix, found, b.Order)
	}
	return b.Files[found[0]]
}

// AssertWellFormed checks that every XHTML, OPF and NCX file in the book is
// well-formed XML, which reading systems require.
func (b *Book) AssertWellFormed(t *testing.T) {
	t.Helper()
	for _, name := range b.Order {
		switch filepath.Ext(name) {
		case ".xhtml", ".opf", ".ncx", ".xml":
			if err := WellFormed(b.Files[name]); err != nil {
				t.Errorf("%s is not well-formed XML: %v", name, err)
			}
		}
	}
}

// WellFormed reports whether s parses as XML in strict mode.
func WellFormed(s string) error {
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	d.Entity = nil // only the five predefined XML entities are allowed
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// EPUBCheck runs epubcheck on the book at path and fails the test if it
// reports errors. Set EPUBCHECK_JAR to the path of epubcheck.jar to enable
// it; otherwise the check is skipped. CI always sets it.
func EPUBCheck(t *testing.T, path string) {
	t.Helper()
	jar := os.Getenv("EPUBCHECK_JAR")
	if jar == "" {
		t.Log("EPUBCHECK_JAR not set; skipping epubcheck")
		return
	}
	out, err := exec.Command("java", "-jar", jar, "--quiet", path).CombinedOutput()
	if err != nil {
		t.Errorf("epubcheck failed: %v\n%s", err, out)
	}
}
