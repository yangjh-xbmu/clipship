// Package files reads file/directory path lists from the OS clipboard.
// Format maps: Windows CF_HDROP, macOS public.file-url, Linux text/uri-list.
// The platform-specific ReadFiles implementations are in files_{goos}.go.
// A fake implementation (build tag `clipship_fake`) reads paths from the
// CLIPSHIP_FAKE_FILES env var for testing.
package files

import (
	"errors"
	"os"
)

// Entry is a clipboard file entry.
type Entry struct {
	Path  string
	IsDir bool
}

var (
	// ErrNoFiles indicates the clipboard currently holds no file paths.
	ErrNoFiles = errors.New("clipboard has no files")
	// ErrUnsupported indicates file clipboard reading is not implemented on
	// this OS/build.
	ErrUnsupported = errors.New("file clipboard unsupported on this os")
)

// entriesFromPaths stats each path and fills IsDir. Missing/unreachable paths
// are returned with IsDir=false (caller decides what to do).
func entriesFromPaths(paths []string) []Entry {
	out := make([]Entry, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		isDir := err == nil && info.IsDir()
		out = append(out, Entry{Path: p, IsDir: isDir})
	}
	return out
}
