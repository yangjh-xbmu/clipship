package server

import (
	"archive/tar"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangjh-xbmu/clipship/internal/clipboard/files"
	"github.com/yangjh-xbmu/clipship/internal/proto"
)

func startTest(t *testing.T, opts Options) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = serveListener(ln, opts)
	}()
	return ln.Addr().String(), func() {
		ln.Close()
		<-done
	}
}

func TestHandlePNG_Success(t *testing.T) {
	addr, stop := startTest(t, Options{
		ClipboardImage: func() ([]byte, error) { return []byte("\x89PNG\r\nfake"), nil },
		ClipboardFiles: func() ([]files.Entry, error) { return nil, files.ErrNoFiles },
	})
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET png\n"))

	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != "png" {
		t.Fatalf("kind = %q", h.Kind)
	}
	body, _ := io.ReadAll(r)
	if string(body) != "\x89PNG\r\nfake" {
		t.Fatalf("body = %q", body)
	}
}

func TestHandlePNG_Error(t *testing.T) {
	addr, stop := startTest(t, Options{
		ClipboardImage: func() ([]byte, error) { return nil, fmt.Errorf("clipboard has no image") },
		ClipboardFiles: func() ([]files.Entry, error) { return nil, files.ErrNoFiles },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	_, _ = conn.Write([]byte("GET png\n"))

	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != "err" || h.Err == "" {
		t.Fatalf("header = %+v", h)
	}
}

func TestMalformedRequest_Err(t *testing.T) {
	addr, stop := startTest(t, Options{
		ClipboardImage: func() ([]byte, error) { return nil, nil },
		ClipboardFiles: func() ([]files.Entry, error) { return nil, files.ErrNoFiles },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	_, _ = conn.Write([]byte("HELLO\n"))
	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != "err" {
		t.Fatalf("want err, got %+v", h)
	}
}

func TestHandleFile_SingleFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "a.txt")
	os.WriteFile(p, []byte("hi"), 0o644)

	addr, stop := startTest(t, Options{
		ClipboardFiles: func() ([]files.Entry, error) { return []files.Entry{{Path: p}}, nil },
		ClipboardImage: func() ([]byte, error) { return nil, errors.New("no image") },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET file\n"))

	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != "file" || h.Name != "a.txt" || h.Size != 2 {
		t.Fatalf("header = %+v", h)
	}
	body := make([]byte, h.Size)
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi" {
		t.Fatalf("body = %q", body)
	}
}

func TestHandleFile_MultiFileAsTar(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.txt"), []byte("B"), 0o644)

	addr, stop := startTest(t, Options{
		ClipboardFiles: func() ([]files.Entry, error) {
			return []files.Entry{
				{Path: filepath.Join(tmp, "a.txt")},
				{Path: filepath.Join(tmp, "b.txt")},
			}, nil
		},
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET file\n"))

	r := bufio.NewReader(conn)
	h, _ := proto.ReadHeader(r)
	if h.Kind != "tar" || h.Size <= 0 {
		t.Fatalf("header = %+v", h)
	}
	body := make([]byte, h.Size)
	io.ReadFull(r, body)
	tr := tar.NewReader(bytes.NewReader(body))
	names := map[string]bool{}
	for {
		th, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[th.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] {
		t.Fatalf("tar = %v", names)
	}
}

func TestHandleFile_NoFiles(t *testing.T) {
	addr, stop := startTest(t, Options{
		ClipboardFiles: func() ([]files.Entry, error) { return nil, files.ErrNoFiles },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET file\n"))
	r := bufio.NewReader(conn)
	h, _ := proto.ReadHeader(r)
	if h.Kind != "err" {
		t.Fatalf("want err, got %+v", h)
	}
}

func TestHandleFile_TooLarge(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "big.bin")
	os.WriteFile(p, make([]byte, 10), 0o644)

	addr, stop := startTest(t, Options{
		MaxBytes:       5,
		ClipboardFiles: func() ([]files.Entry, error) { return []files.Entry{{Path: p}}, nil },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET file\n"))
	r := bufio.NewReader(conn)
	h, _ := proto.ReadHeader(r)
	if h.Kind != "err" {
		t.Fatalf("want err, got %+v", h)
	}
}

func TestHandleFile_Force(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "big.bin")
	os.WriteFile(p, make([]byte, 10), 0o644)

	addr, stop := startTest(t, Options{
		MaxBytes:       5,
		ClipboardFiles: func() ([]files.Entry, error) { return []files.Entry{{Path: p}}, nil },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET file force\n"))
	r := bufio.NewReader(conn)
	h, _ := proto.ReadHeader(r)
	if h.Kind != "file" {
		t.Fatalf("want file, got %+v", h)
	}
}

func TestHandleAuto_FallsBackToPNG(t *testing.T) {
	addr, stop := startTest(t, Options{
		ClipboardFiles: func() ([]files.Entry, error) { return nil, files.ErrNoFiles },
		ClipboardImage: func() ([]byte, error) { return []byte("PNGDATA"), nil },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET auto\n"))
	r := bufio.NewReader(conn)
	h, _ := proto.ReadHeader(r)
	if h.Kind != "png" {
		t.Fatalf("want png, got %+v", h)
	}
}

func TestHandleAuto_PrefersFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "x.txt")
	os.WriteFile(p, []byte("X"), 0o644)
	addr, stop := startTest(t, Options{
		ClipboardFiles: func() ([]files.Entry, error) { return []files.Entry{{Path: p}}, nil },
		ClipboardImage: func() ([]byte, error) { return []byte("PNG"), nil },
	})
	defer stop()

	conn, _ := net.DialTimeout("tcp", addr, 2*time.Second)
	defer conn.Close()
	conn.Write([]byte("GET auto\n"))
	r := bufio.NewReader(conn)
	h, _ := proto.ReadHeader(r)
	if h.Kind != "file" {
		t.Fatalf("want file, got %+v", h)
	}
}
