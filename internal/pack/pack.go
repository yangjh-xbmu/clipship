package pack

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yangjh-xbmu/clipship/internal/clipboard/files"
)

// ErrTooLarge is returned when the total size of packed content would exceed
// maxBytes and force=false.
var ErrTooLarge = errors.New("pack: content exceeds max size")

// PackTar walks entries and writes a tar stream to the returned ReadCloser.
// maxBytes=0 disables the limit. force=true bypasses the limit entirely.
// Returns (stream, totalBytes, nil) on success.
func PackTar(entries []files.Entry, maxBytes int64, force bool) (io.ReadCloser, int64, error) {
	type item struct {
		tarPath string
		abs     string
		size    int64
	}
	var items []item
	var total int64
	for _, e := range entries {
		if e.IsDir {
			root := filepath.Clean(e.Path)
			rootBase := filepath.Base(root)
			walkErr := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				rel, relErr := filepath.Rel(root, p)
				if relErr != nil {
					return relErr
				}
				tarPath := filepath.ToSlash(filepath.Join(rootBase, rel))
				items = append(items, item{tarPath: tarPath, abs: p, size: info.Size()})
				total += info.Size()
				return nil
			})
			if walkErr != nil {
				return nil, 0, fmt.Errorf("walk %s: %w", root, walkErr)
			}
		} else {
			info, err := os.Stat(e.Path)
			if err != nil {
				return nil, 0, fmt.Errorf("stat %s: %w", e.Path, err)
			}
			if info.IsDir() {
				return nil, 0, fmt.Errorf("entry %s is a directory but IsDir=false", e.Path)
			}
			items = append(items, item{
				tarPath: filepath.Base(e.Path),
				abs:     e.Path,
				size:    info.Size(),
			})
			total += info.Size()
		}
	}

	if !force && maxBytes > 0 && total > maxBytes {
		return nil, total, fmt.Errorf("%w: %d > %d", ErrTooLarge, total, maxBytes)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, it := range items {
		f, err := os.Open(it.abs)
		if err != nil {
			return nil, 0, fmt.Errorf("open %s: %w", it.abs, err)
		}
		h := &tar.Header{
			Name:     strings.TrimPrefix(it.tarPath, "/"),
			Mode:     0o644,
			Size:     it.size,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(h); err != nil {
			f.Close()
			return nil, 0, fmt.Errorf("tar header: %w", err)
		}
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return nil, 0, fmt.Errorf("tar copy %s: %w", it.abs, err)
		}
		f.Close()
	}
	if err := tw.Close(); err != nil {
		return nil, 0, fmt.Errorf("tar close: %w", err)
	}
	return io.NopCloser(&buf), int64(buf.Len()), nil
}

// UnpackTar reads a tar stream from r and extracts regular files under destDir.
// Each path segment is passed through sanitize. Any header whose Name escapes
// destDir after cleaning is rejected. Returns absolute paths of extracted files.
func UnpackTar(r io.Reader, destDir string, sanitize func(string) string) ([]string, error) {
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("abs %s: %w", destDir, err)
	}
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", absDest, err)
	}

	tr := tar.NewReader(r)
	var out []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, fmt.Errorf("tar read: %w", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		parts := strings.Split(filepath.ToSlash(h.Name), "/")
		sanitized := make([]string, 0, len(parts))
		for _, seg := range parts {
			if seg == "" || seg == "." {
				continue
			}
			if seg == ".." {
				return out, fmt.Errorf("unpack: path traversal in %q", h.Name)
			}
			sanitized = append(sanitized, sanitize(seg))
		}
		if len(sanitized) == 0 {
			continue
		}
		target := filepath.Join(append([]string{absDest}, sanitized...)...)
		rel, err := filepath.Rel(absDest, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return out, fmt.Errorf("unpack: path escapes dest: %s", h.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return out, fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return out, fmt.Errorf("create %s: %w", target, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return out, fmt.Errorf("write %s: %w", target, err)
		}
		f.Close()
		out = append(out, target)
	}
	return out, nil
}
