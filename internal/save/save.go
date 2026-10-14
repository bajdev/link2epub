// Package save decides where a book is written and writes it there safely.
//
// It implements the -SAVE rules from the README: folder versus file targets,
// the .epub / .kepub.epub extension, creation of missing folders, and
// overwrite protection unless -FORCE is given. Writes are atomic, so a failed
// run never leaves a partial file behind.
package save

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	extEPUB  = ".epub"
	extKEPUB = ".kepub.epub"

	// maxSlugLen caps generated filenames well below filesystem limits.
	maxSlugLen = 80

	// fallbackName is used when a title produces an empty slug, for example
	// a title written entirely in a non-Latin script.
	fallbackName = "article"
)

// ErrExists reports that the output file already exists and overwriting was
// not allowed.
var ErrExists = errors.New("already exists")

// ErrKEPUBName reports a .kepub.epub output path without KEPUB output
// enabled. Kobo would open such a plain EPUB in its KEPUB renderer.
var ErrKEPUBName = errors.New("a .kepub.epub file needs -KEPUB (or use a .epub filename)")

// Target is a resolved -SAVE value. The final filename is known up front for
// file targets; for folder targets it depends on the article title.
type Target struct {
	file string // absolute file path, set for file targets
	dir  string // absolute folder, set for folder targets
	ext  string // extension used for folder targets

	// Note is a message for the user about an adjustment made to the path,
	// or empty.
	Note string
}

// Resolve interprets the -SAVE argument. kepub selects the .kepub.epub
// extension.
func Resolve(arg string, kepub bool) (Target, error) {
	if arg == "" {
		return Target{}, errors.New("empty path")
	}
	arg, err := expandHome(arg)
	if err != nil {
		return Target{}, err
	}

	ext := extEPUB
	if kepub {
		ext = extKEPUB
	}

	if isFolder(arg) {
		dir, err := filepath.Abs(arg)
		if err != nil {
			return Target{}, err
		}
		return Target{dir: dir, ext: ext}, nil
	}

	var note string
	lower := strings.ToLower(arg)
	switch {
	case strings.HasSuffix(lower, extKEPUB):
		if !kepub {
			return Target{}, fmt.Errorf("%s: %w", arg, ErrKEPUBName)
		}
	case strings.HasSuffix(lower, extEPUB):
		if kepub {
			renamed := arg[:len(arg)-len(extEPUB)] + extKEPUB
			note = fmt.Sprintf("saving as %s so Kobo opens it with the KEPUB renderer", filepath.Base(renamed))
			arg = renamed
		}
	default:
		arg += ext
	}

	file, err := filepath.Abs(arg)
	if err != nil {
		return Target{}, err
	}
	return Target{file: file, Note: note}, nil
}

// Known reports whether the output filename is known before the article
// title is, which allows checking for an existing file before fetching.
func (t Target) Known() bool {
	return t.file != ""
}

// Path returns the output file path. title is only used for folder targets.
func (t Target) Path(title string) string {
	if t.file != "" {
		return t.file
	}
	return filepath.Join(t.dir, Slug(title)+t.ext)
}

// isFolder reports whether arg names a folder: it ends in a path separator
// or is an existing directory.
func isFolder(arg string) bool {
	if os.IsPathSeparator(arg[len(arg)-1]) {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}

// expandHome replaces a leading "~" with the user's home directory, for
// paths the shell did not expand (for example, quoted ones).
func expandHome(arg string) (string, error) {
	if arg != "~" && !(len(arg) > 1 && arg[0] == '~' && os.IsPathSeparator(arg[1])) {
		return arg, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding ~: %w", err)
	}
	return home + arg[1:], nil
}

// Slug turns a title into a safe filename stem: lowercase ASCII letters and
// digits separated by single dashes, at most 80 characters. Accents are
// removed ("Café" → "cafe"). It never contains path separators, so a title
// cannot move the file out of the chosen folder.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(title) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// Combining accent left over from NFD; drop it.
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(unicode.ToLower(r))
		default:
			dash = true
		}
	}
	s := b.String()
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	if s == "" {
		return fallbackName
	}
	return s
}

// CheckExists returns an error wrapping ErrExists if path already exists.
// It is the early check made before work is done; Write repeats the check
// atomically.
func CheckExists(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.IsDir():
		return fmt.Errorf("%s is a folder", path)
	default:
		return fmt.Errorf("%s %w", path, ErrExists)
	}
}

// Write writes src to path, creating missing folders. Unless force is set,
// an existing file is never modified and an error wrapping ErrExists is
// returned. The content is written to a temporary file and renamed into
// place, so path never holds a partial book; on failure, nothing the run
// created is left behind.
func Write(path string, src io.WriterTo, force bool) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating folder: %w", err)
	}

	// Without force, claim the name first. O_EXCL fails if the file exists,
	// including one created by another program since CheckExists ran.
	reserved := false
	if !force {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s %w", path, ErrExists)
		}
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			os.Remove(path)
			return err
		}
		reserved = true
	}

	tmp, err := os.CreateTemp(dir, ".link2epub-*.tmp")
	if err != nil {
		if reserved {
			os.Remove(path)
		}
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			if reserved {
				os.Remove(path)
			}
		}
	}()

	if _, err := src.WriteTo(tmp); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp uses mode 0600; books should be readable like any other
	// file. Best effort: FAT32 (a Kobo over USB) has no permissions.
	_ = os.Chmod(tmp.Name(), 0o644)

	return os.Rename(tmp.Name(), path)
}
