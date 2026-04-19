package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/yangjh-xbmu/clipship/internal/clipboard"
	"github.com/yangjh-xbmu/clipship/internal/clipboard/files"
	"github.com/yangjh-xbmu/clipship/internal/config"
	"github.com/yangjh-xbmu/clipship/internal/pack"
	"github.com/yangjh-xbmu/clipship/internal/proto"
)

// Options configures the daemon. Nil callbacks fall back to the real OS clipboard.
type Options struct {
	MaxBytes       int64
	ClipboardImage func() ([]byte, error)
	ClipboardFiles func() ([]files.Entry, error)
}

// Run binds a TCP listener on addr and serves until fatal error.
func Run(addr string, opts Options) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	fmt.Printf("clipship daemon listening on %s\n", addr)
	return serveListener(ln, opts)
}

// serveListener is split out so tests can pass their own listener.
func serveListener(ln net.Listener, opts Options) error {
	defer ln.Close()
	opts = opts.withDefaults()
	for {
		conn, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go handle(conn, opts)
	}
}

func (o Options) withDefaults() Options {
	if o.MaxBytes == 0 {
		o.MaxBytes = config.DefaultMaxBytes
	}
	if o.ClipboardImage == nil {
		o.ClipboardImage = clipboard.ReadPNG
	}
	if o.ClipboardFiles == nil {
		o.ClipboardFiles = files.ReadFiles
	}
	return o
}

func handle(conn net.Conn, opts Options) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	r := bufio.NewReader(conn)
	req, err := proto.ReadRequest(r)
	if err != nil {
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: "bad request: " + err.Error()})
		return
	}

	switch req.Kind {
	case "png":
		handlePNG(conn, opts)
	case "file":
		handleFile(conn, opts, req.Force)
	case "auto":
		handleAuto(conn, opts, req.Force)
	default:
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: "unknown kind " + req.Kind})
	}
}

func handlePNG(conn net.Conn, opts Options) {
	img, err := opts.ClipboardImage()
	if err != nil {
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: err.Error()})
		return
	}
	if err := proto.WriteHeader(conn, proto.Response{Kind: "png"}); err != nil {
		return
	}
	_, _ = io.Copy(conn, bytes.NewReader(img))
}

func handleFile(conn net.Conn, opts Options, force bool) {
	entries, err := opts.ClipboardFiles()
	if err != nil {
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: err.Error()})
		return
	}
	if len(entries) == 0 {
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: files.ErrNoFiles.Error()})
		return
	}

	if len(entries) == 1 && !entries[0].IsDir {
		info, statErr := os.Stat(entries[0].Path)
		if statErr != nil {
			_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: "stat: " + statErr.Error()})
			return
		}
		if !force && opts.MaxBytes > 0 && info.Size() > opts.MaxBytes {
			_ = proto.WriteHeader(conn, proto.Response{
				Kind: "err",
				Err:  fmt.Sprintf("too large: %d > %d, retry with --force", info.Size(), opts.MaxBytes),
			})
			return
		}
		f, openErr := os.Open(entries[0].Path)
		if openErr != nil {
			_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: "open: " + openErr.Error()})
			return
		}
		defer f.Close()
		if err := proto.WriteHeader(conn, proto.Response{
			Kind: "file",
			Name: filepath.Base(entries[0].Path),
			Size: info.Size(),
		}); err != nil {
			return
		}
		_, _ = io.Copy(conn, f)
		return
	}

	rc, size, err := pack.PackTar(entries, opts.MaxBytes, force)
	if err != nil {
		if errors.Is(err, pack.ErrTooLarge) {
			_ = proto.WriteHeader(conn, proto.Response{
				Kind: "err",
				Err:  fmt.Sprintf("too large: %d > %d, retry with --force", size, opts.MaxBytes),
			})
			return
		}
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: "pack: " + err.Error()})
		return
	}
	defer rc.Close()
	if err := proto.WriteHeader(conn, proto.Response{Kind: "tar", Size: size}); err != nil {
		return
	}
	_, _ = io.Copy(conn, rc)
}

func handleAuto(conn net.Conn, opts Options, force bool) {
	entries, err := opts.ClipboardFiles()
	if err == nil && len(entries) > 0 {
		inline := opts
		inline.ClipboardFiles = func() ([]files.Entry, error) { return entries, nil }
		handleFile(conn, inline, force)
		return
	}
	if err != nil && !errors.Is(err, files.ErrNoFiles) && !errors.Is(err, files.ErrUnsupported) {
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: err.Error()})
		return
	}
	img, imgErr := opts.ClipboardImage()
	if imgErr != nil {
		_ = proto.WriteHeader(conn, proto.Response{
			Kind: "err",
			Err:  "clipboard has neither image nor files",
		})
		return
	}
	if err := proto.WriteHeader(conn, proto.Response{Kind: "png"}); err != nil {
		return
	}
	_, _ = io.Copy(conn, bytes.NewReader(img))
}
