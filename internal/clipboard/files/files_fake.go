//go:build clipship_fake

package files

import (
	"os"
	"strings"
)

// ReadFiles (fake) reads the OS PathListSeparator-separated CLIPSHIP_FAKE_FILES
// env var. Empty env => ErrNoFiles.
func ReadFiles() ([]Entry, error) {
	raw := os.Getenv("CLIPSHIP_FAKE_FILES")
	if raw == "" {
		return nil, ErrNoFiles
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	clean := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil, ErrNoFiles
	}
	return entriesFromPaths(clean), nil
}
