package client

import (
	"archive/tar"
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangjh-xbmu/clipship/internal/proto"
)

func fakeDaemon(t *testing.T, handler func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handler(c)
		}
	}()
	return ln.Addr().String()
}

func tarOf(pairs map[string]string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range pairs {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	return buf.Bytes()
}

func TestPullPNG_Success(t *testing.T) {
	png := "\x89PNGFAKE"
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		r := bufio.NewReader(c)
		req, _ := proto.ReadRequest(r)
		if req.Kind != "png" {
			t.Errorf("got req %+v", req)
		}
		_ = proto.WriteHeader(c, proto.Response{Kind: "png"})
		c.Write([]byte(png))
	})

	dir := t.TempDir()
	path, bytes, err := PullPNG(addr, dir, "clip_{ts}.png")
	if err != nil {
		t.Fatal(err)
	}
	if bytes != int64(len(png)) {
		t.Fatalf("bytes = %d", bytes)
	}
	got, _ := os.ReadFile(path)
	if string(got) != png {
		t.Fatalf("content = %q", got)
	}
	if !strings.HasPrefix(filepath.Base(path), "clip_") || !strings.HasSuffix(path, ".png") {
		t.Fatalf("filename = %s", path)
	}
}

func TestPullPNG_DaemonErr(t *testing.T) {
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		_ = proto.WriteHeader(c, proto.Response{Kind: "err", Err: "clipboard has no image"})
	})
	_, _, err := PullPNG(addr, t.TempDir(), "x.png")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "clipboard has no image") {
		t.Fatalf("err = %v", err)
	}
}

func TestPullPNG_ConnectionRefused(t *testing.T) {
	_, _, err := PullPNG("127.0.0.1:1", t.TempDir(), "x.png")
	if err == nil {
		t.Fatal("want dial error")
	}
}

func TestPullPNG_ReadsFullBody(t *testing.T) {
	body := strings.Repeat("A", 123456)
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		_ = proto.WriteHeader(c, proto.Response{Kind: "png"})
		io.WriteString(c, body)
	})
	dir := t.TempDir()
	p, n, err := PullPNG(addr, dir, "x.png")
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(body)) {
		t.Fatalf("n = %d", n)
	}
	got, _ := os.ReadFile(p)
	if string(got) != body {
		t.Fatalf("truncated: len=%d", len(got))
	}
}

func TestPullFile_Single(t *testing.T) {
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		proto.WriteHeader(c, proto.Response{Kind: "file", Name: "report.pdf", Size: 5})
		c.Write([]byte("HELLO"))
	})
	res, err := PullFile(addr, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("files = %v", res.Files)
	}
	if filepath.Base(res.Files[0]) != "report.pdf" {
		t.Fatalf("basename = %s", filepath.Base(res.Files[0]))
	}
	got, _ := os.ReadFile(res.Files[0])
	if string(got) != "HELLO" {
		t.Fatalf("content = %q", got)
	}
	if res.Bytes != 5 {
		t.Fatalf("bytes = %d", res.Bytes)
	}
}

func TestPullFile_Tar(t *testing.T) {
	body := tarOf(map[string]string{"a.txt": "AA", "sub/b.txt": "BB"})
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		proto.WriteHeader(c, proto.Response{Kind: "tar", Size: int64(len(body))})
		c.Write(body)
	})
	res, err := PullFile(addr, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %v", res.Files)
	}
}

func TestPullFile_Err(t *testing.T) {
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		proto.WriteHeader(c, proto.Response{Kind: "err", Err: "clipboard has no files"})
	})
	_, err := PullFile(addr, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "no files") {
		t.Fatalf("err = %v", err)
	}
}

func TestPullAuto_ReturnsPNG(t *testing.T) {
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		proto.WriteHeader(c, proto.Response{Kind: "png"})
		c.Write([]byte("PNGBODY"))
	})
	dir := t.TempDir()
	res, err := PullAuto(addr, dir, "clip_{ts}.png", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "png" || res.PNG == nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestPullAuto_ReturnsFile(t *testing.T) {
	body := tarOf(map[string]string{"x.txt": "x"})
	addr := fakeDaemon(t, func(c net.Conn) {
		defer c.Close()
		bufio.NewReader(c).ReadString('\n')
		proto.WriteHeader(c, proto.Response{Kind: "tar", Size: int64(len(body))})
		c.Write(body)
	})
	res, err := PullAuto(addr, t.TempDir(), "x.png", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "file" || res.File == nil {
		t.Fatalf("res = %+v", res)
	}
}
