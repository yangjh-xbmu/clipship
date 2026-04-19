package client

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yangjh-xbmu/clipship/internal/pack"
	"github.com/yangjh-xbmu/clipship/internal/proto"
)

// PNGResult is the machine-readable result of a PNG pull.
type PNGResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// FileResult is the machine-readable result of a file/tar pull.
type FileResult struct {
	SessionDir string   `json:"session_dir"`
	Files      []string `json:"files"`
	Bytes      int64    `json:"bytes"`
}

// AutoResult wraps whichever branch PullAuto ended up taking.
type AutoResult struct {
	Kind string      `json:"kind"` // "png" | "file"
	PNG  *PNGResult  `json:"png,omitempty"`
	File *FileResult `json:"file,omitempty"`
}

// PullPNG dials addr, speaks `GET png`, writes the received PNG bytes to
// <localDir>/<filename>, and returns (path, bytesWritten, err).
func PullPNG(addr, localDir, filenameTmpl string) (string, int64, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "", 0, fmt.Errorf("dial %s: %w (is the tunnel up? did the daemon start?)", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	if err := proto.WriteRequest(conn, proto.Request{Kind: "png"}); err != nil {
		return "", 0, fmt.Errorf("write req: %w", err)
	}
	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		return "", 0, fmt.Errorf("read header: %w", err)
	}
	switch h.Kind {
	case "err":
		return "", 0, fmt.Errorf("daemon: %s", h.Err)
	case "png":
	default:
		return "", 0, fmt.Errorf("unexpected response kind %q", h.Kind)
	}

	dir := expandHome(localDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	ts := time.Now().Format("20060102_150405")
	name := strings.ReplaceAll(filenameTmpl, "{ts}", ts)
	if name == "" {
		name = "clip_" + ts + ".png"
	}
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, fmt.Errorf("create %s: %w", p, err)
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		return "", n, fmt.Errorf("write %s: %w", p, err)
	}
	if n == 0 {
		return "", 0, fmt.Errorf("empty response from daemon")
	}
	return p, n, nil
}

// PullFile dials addr, speaks `GET file [force]`, and writes the payload into
// <destParentDir>/<ts>/.
func PullFile(addr, destParentDir string, force bool) (FileResult, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return FileResult{}, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

	if err := proto.WriteRequest(conn, proto.Request{Kind: "file", Force: force}); err != nil {
		return FileResult{}, err
	}
	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		return FileResult{}, fmt.Errorf("header: %w", err)
	}

	switch h.Kind {
	case "err":
		return FileResult{}, fmt.Errorf("daemon: %s", h.Err)
	case "file":
		sessionDir, err := makeSessionDir(destParentDir)
		if err != nil {
			return FileResult{}, err
		}
		return writeSingleFile(sessionDir, h.Name, h.Size, r)
	case "tar":
		sessionDir, err := makeSessionDir(destParentDir)
		if err != nil {
			return FileResult{}, err
		}
		limited := io.LimitReader(r, h.Size)
		paths, err := pack.UnpackTar(limited, sessionDir, pack.SanitizeBasename)
		if err != nil {
			_ = os.RemoveAll(sessionDir)
			return FileResult{}, fmt.Errorf("unpack: %w", err)
		}
		return FileResult{SessionDir: sessionDir, Files: paths, Bytes: h.Size}, nil
	default:
		return FileResult{}, fmt.Errorf("unexpected kind %q for file request", h.Kind)
	}
}

// PullAuto dials with `GET auto`. Returns whichever payload the daemon chose.
func PullAuto(addr, pngLocalDir, pngFilenameTmpl, destParentDir string, force bool) (AutoResult, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return AutoResult{}, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

	if err := proto.WriteRequest(conn, proto.Request{Kind: "auto", Force: force}); err != nil {
		return AutoResult{}, err
	}
	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		return AutoResult{}, err
	}
	switch h.Kind {
	case "err":
		return AutoResult{}, fmt.Errorf("daemon: %s", h.Err)
	case "png":
		dir := expandHome(pngLocalDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return AutoResult{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
		ts := time.Now().Format("20060102_150405")
		name := strings.ReplaceAll(pngFilenameTmpl, "{ts}", ts)
		if name == "" {
			name = "clip_" + ts + ".png"
		}
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return AutoResult{}, err
		}
		defer f.Close()
		n, err := io.Copy(f, r)
		if err != nil {
			return AutoResult{}, err
		}
		return AutoResult{Kind: "png", PNG: &PNGResult{Path: p, Bytes: n}}, nil
	case "file":
		sessionDir, err := makeSessionDir(destParentDir)
		if err != nil {
			return AutoResult{}, err
		}
		res, err := writeSingleFile(sessionDir, h.Name, h.Size, r)
		if err != nil {
			return AutoResult{}, err
		}
		return AutoResult{Kind: "file", File: &res}, nil
	case "tar":
		sessionDir, err := makeSessionDir(destParentDir)
		if err != nil {
			return AutoResult{}, err
		}
		limited := io.LimitReader(r, h.Size)
		paths, err := pack.UnpackTar(limited, sessionDir, pack.SanitizeBasename)
		if err != nil {
			_ = os.RemoveAll(sessionDir)
			return AutoResult{}, fmt.Errorf("unpack: %w", err)
		}
		return AutoResult{Kind: "file", File: &FileResult{SessionDir: sessionDir, Files: paths, Bytes: h.Size}}, nil
	default:
		return AutoResult{}, fmt.Errorf("unexpected kind %q", h.Kind)
	}
}

func writeSingleFile(sessionDir, name string, size int64, r io.Reader) (FileResult, error) {
	safe := pack.SanitizeBasename(name)
	dest := filepath.Join(sessionDir, safe)
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return FileResult{}, fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()
	n, err := io.CopyN(f, r, size)
	if err != nil {
		return FileResult{}, fmt.Errorf("write %s: %w", dest, err)
	}
	return FileResult{SessionDir: sessionDir, Files: []string{dest}, Bytes: n}, nil
}

func makeSessionDir(parent string) (string, error) {
	dir := expandHome(parent)
	ts := time.Now().Format("20060102_150405")
	session := filepath.Join(dir, ts)
	if err := os.MkdirAll(session, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", session, err)
	}
	return session, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}
