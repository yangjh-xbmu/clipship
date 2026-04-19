package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEntriesFromPaths(t *testing.T) {
	tmp := t.TempDir()
	fp := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(fp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := entriesFromPaths([]string{fp, dir})
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Path != fp || got[0].IsDir {
		t.Fatalf("entry 0 = %+v", got[0])
	}
	if got[1].Path != dir || !got[1].IsDir {
		t.Fatalf("entry 1 = %+v", got[1])
	}
}

func TestEntriesFromPaths_MissingStillIncluded(t *testing.T) {
	got := entriesFromPaths([]string{"/definitely/not/here"})
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].IsDir {
		t.Fatal("missing path should default to IsDir=false")
	}
}
