package files

import (
	"errors"
	"testing"
)

func TestErrorsExported(t *testing.T) {
	if ErrNoFiles == nil || ErrUnsupported == nil {
		t.Fatal("errors must be non-nil")
	}
}

func TestEntry_Zero(t *testing.T) {
	var e Entry
	if e.Path != "" || e.IsDir {
		t.Fatalf("zero Entry malformed: %+v", e)
	}
}

func TestErrNoFiles_IsComparable(t *testing.T) {
	err := ErrNoFiles
	if !errors.Is(err, ErrNoFiles) {
		t.Fatal("errors.Is fails")
	}
}
