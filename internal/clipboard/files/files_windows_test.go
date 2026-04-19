//go:build windows

package files

import "testing"

// Compile-time smoke: the function must be callable; real clipboard behavior
// is exercised by scripts/e2e_windows.ps1, not by CI.
func TestReadFiles_CompileSmoke(t *testing.T) {
	_, err := ReadFiles()
	if err != nil && err != ErrNoFiles && err != ErrUnsupported {
		t.Logf("ReadFiles returned %v (accepted; real check is manual)", err)
	}
}
