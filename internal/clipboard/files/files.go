// Package files reads file paths from the OS clipboard. The platform-specific
// implementations are split into files_windows.go / files_darwin.go /
// files_linux.go. A fake implementation (build tag `clipship_fake`) reads
// paths from the CLIPSHIP_FAKE_FILES env var for testing.
package files

import "errors"

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
