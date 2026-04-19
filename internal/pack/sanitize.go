// Package pack handles tar packing/unpacking and filename sanitization used by
// the file clipboard pull path.
package pack

import (
	"fmt"
	"path/filepath"
	"strings"
)

const invalidChars = `:*?"<>|`

// SanitizeBasename replaces illegal characters in a single path component and
// collapses a trailing space/dot to underscore. It does not touch path separators.
func SanitizeBasename(name string) string {
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if strings.ContainsRune(invalidChars, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	trimmed := strings.TrimRight(out, " .")
	if trimmed != out {
		out = trimmed + "_"
	}
	return out
}

// ResolveName returns a name that does not collide with anything in seen,
// appending `(1)`, `(2)` ... before the extension. It also marks the returned
// name as seen.
func ResolveName(seen map[string]bool, name string) string {
	if !seen[name] {
		seen[name] = true
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !seen[candidate] {
			seen[candidate] = true
			return candidate
		}
	}
}
