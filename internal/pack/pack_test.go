package pack

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangjh-xbmu/clipship/internal/clipboard/files"
)

func writeTmp(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readAllTar(t *testing.T, tarBytes []byte) map[string]string {
	t.Helper()
	got := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = string(b)
	}
	return got
}

func TestPackTar_SingleFile(t *testing.T) {
	tmp := t.TempDir()
	p := writeTmp(t, tmp, "hello.txt", "hi")
	rc, size, err := PackTar([]files.Entry{{Path: p}}, 1024*1024, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) != size {
		t.Fatalf("size mismatch: reported %d, actual %d", size, len(got))
	}
	entries := readAllTar(t, got)
	if entries["hello.txt"] != "hi" {
		t.Fatalf("entries = %v", entries)
	}
}

func TestPackTar_MultiFileFlatten(t *testing.T) {
	tmp := t.TempDir()
	a := writeTmp(t, tmp, "a.txt", "A")
	b := writeTmp(t, tmp, "b.txt", "B")
	rc, _, err := PackTar([]files.Entry{{Path: a}, {Path: b}}, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	entries := readAllTar(t, got)
	if entries["a.txt"] != "A" || entries["b.txt"] != "B" {
		t.Fatalf("entries = %v", entries)
	}
}

func TestPackTar_Dir(t *testing.T) {
	tmp := t.TempDir()
	writeTmp(t, tmp, "proj/a.go", "1")
	writeTmp(t, tmp, "proj/sub/b.go", "2")
	proj := filepath.Join(tmp, "proj")
	rc, _, err := PackTar([]files.Entry{{Path: proj, IsDir: true}}, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	entries := readAllTar(t, got)
	if entries["proj/a.go"] != "1" {
		t.Fatalf("proj/a.go missing: %v", entries)
	}
	if entries["proj/sub/b.go"] != "2" {
		t.Fatalf("proj/sub/b.go missing: %v", entries)
	}
}

func TestPackTar_TooLarge(t *testing.T) {
	tmp := t.TempDir()
	p := writeTmp(t, tmp, "big.bin", "0123456789")
	_, _, err := PackTar([]files.Entry{{Path: p}}, 5, false)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestPackTar_ForceBypassesLimit(t *testing.T) {
	tmp := t.TempDir()
	p := writeTmp(t, tmp, "big.bin", "0123456789")
	rc, _, err := PackTar([]files.Entry{{Path: p}}, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestUnpackTar_BasicRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	writeTmp(t, tmp, "proj/a.txt", "A")
	writeTmp(t, tmp, "proj/sub/b.txt", "B")
	rc, _, err := PackTar([]files.Entry{{Path: filepath.Join(tmp, "proj"), IsDir: true}}, 1<<20, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	dest := t.TempDir()
	paths, err := UnpackTar(rc, dest, SanitizeBasename)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	gotA, _ := os.ReadFile(filepath.Join(dest, "proj", "a.txt"))
	gotB, _ := os.ReadFile(filepath.Join(dest, "proj", "sub", "b.txt"))
	if string(gotA) != "A" || string(gotB) != "B" {
		t.Fatalf("contents A=%q B=%q", gotA, gotB)
	}
}

func TestUnpackTar_SanitizesBasenames(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	h := &tar.Header{Name: "bad:name.txt", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}
	_ = tw.WriteHeader(h)
	tw.Write([]byte("abc"))
	tw.Close()

	dest := t.TempDir()
	paths, err := UnpackTar(io.NopCloser(&buf), dest, SanitizeBasename)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths = %v", paths)
	}
	if filepath.Base(paths[0]) != "bad_name.txt" {
		t.Fatalf("basename = %s", filepath.Base(paths[0]))
	}
}

func TestUnpackTar_RejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	h := &tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}
	_ = tw.WriteHeader(h)
	tw.Write([]byte("x"))
	tw.Close()

	_, err := UnpackTar(io.NopCloser(&buf), t.TempDir(), SanitizeBasename)
	if err == nil {
		t.Fatal("want error on traversal")
	}
}
