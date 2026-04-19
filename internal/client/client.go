package client

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Pull(addr, localDir, filenameTmpl string) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w (is the tunnel up? did the daemon start?)", addr, err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	data, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty response from daemon")
	}
	if bytes.HasPrefix(data, []byte("ERR ")) {
		msg := strings.TrimSpace(string(bytes.TrimPrefix(data, []byte("ERR "))))
		return "", fmt.Errorf("daemon: %s", msg)
	}

	dir := expandHome(localDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	ts := time.Now().Format("20060102_150405")
	name := strings.ReplaceAll(filenameTmpl, "{ts}", ts)
	if name == "" {
		name = "clip_" + ts + ".png"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	return p, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}
