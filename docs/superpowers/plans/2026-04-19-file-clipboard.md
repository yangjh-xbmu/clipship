# clipship v0.4 文件剪贴板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 clipship v0.4 里给 Pull 方向新增「任意文件剪贴板」支持：把桌面文件管理器 Ctrl/Cmd+C 选中的文件、多文件、目录，经 daemon + SSH tunnel 拉到远程 Claude 会话。

**Architecture:** Windows-First 切片交付。协议升级为「命令前缀 + TYPE 响应头」（`GET png|file|auto` → `TYPE png|file|tar|ERR`）；新增子命令 `pull-file` / `pull-auto`，统一 JSON stdout；`/clip` skill 合并为一个入口（`/clip` / `/clip png` / `/clip file`）。读取逻辑 interface 化，支持 fake 实现以 TDD 全覆盖；Windows 首版走 `CF_HDROP` + `DragQueryFileW`，macOS/Linux 返回 `ErrUnsupported`。

**Tech Stack:** Go 1.22+、`archive/tar`、`golang.org/x/sys/windows`（新增依赖）、`net` 原生 TCP、`encoding/json`、`syscall`。测试：`go test -race` + 表驱动 + 启真实 TCP listener 做集成测。

**Spec reference:** `docs/superpowers/specs/2026-04-19-file-clipboard-design.md`

---

## 文件布局

### 新建

| 路径 | 职责 |
|------|------|
| `internal/proto/proto.go` | Request/Response 类型；WriteRequest/ReadRequest/WriteHeader/ReadHeader |
| `internal/proto/proto_test.go` | 协议编解码的表驱动测试 |
| `internal/pack/sanitize.go` | 文件名 sanitize + 同 session 撞名后缀 |
| `internal/pack/sanitize_test.go` | sanitize/撞名的表驱动测试 |
| `internal/pack/pack.go` | PackTar / UnpackTar |
| `internal/pack/pack_test.go` | 打包解包测试 |
| `internal/clipboard/files/files.go` | Entry / ErrNoFiles / ErrUnsupported / ReadFiles 声明 |
| `internal/clipboard/files/files_windows.go` | Windows CF_HDROP 真实实现 |
| `internal/clipboard/files/files_darwin.go` | macOS stub（返回 ErrUnsupported） |
| `internal/clipboard/files/files_linux.go` | Linux stub（返回 ErrUnsupported） |
| `internal/clipboard/files/files_fake.go` | build tag `clipship_fake` 时启用，读 `CLIPSHIP_FAKE_FILES` |
| `internal/clipboard/files/files_test.go` | 跨平台错误类型测试 |
| `internal/clipboard/files/entries_test.go` | `entriesFromPaths` 纯逻辑测试（跨平台） |
| `internal/config/config_test.go` | 配置 default 与 resolve 测试 |
| `internal/server/server_test.go` | daemon 集成测试（启真实 TCP） |
| `internal/client/client_test.go` | client 集成测试（对接假 daemon） |
| `scripts/e2e_windows.ps1` | Windows 端到端手动 smoke test 脚本 |

### 修改

| 路径 | 变更 |
|------|------|
| `internal/clipboard/clipboard.go` | 保持不变（仍提供 `ReadPNG`） |
| `internal/server/server.go` | 重写：`Options` 注入、请求分发、handlePNG/handleFile/handleAuto |
| `internal/client/client.go` | 拆为 `PullPNG` / `PullFile` / `PullAuto`（旧 `Pull` 改名） |
| `internal/config/config.go` | `Pull` 加 `FilesDir` / `MaxBytes`；`Daemon` 加 `MaxBytes` |
| `cmd/clipship/main.go` | 新增 `pull-file` / `pull-auto` 子命令；全部 pull 家族统一 JSON stdout；`pull` 内部改调 `PullPNG` |
| `go.mod` / `go.sum` | 新增 `golang.org/x/sys/windows` |
| `README.md` | v0.4 破坏性说明、新协议、新子命令、新 skill、迁移指南 |

---

## 执行规则

- 每个 Task 内按 Step 顺序执行；每个 Step 是一个最小动作
- 每个 Task 至少 commit 一次（多数 Task 在最后一步 commit）
- 测试命令统一：`go test -race ./...`；单包定向：`go test -race ./internal/<pkg>/...`
- 里程碑 1 完成后**不发布**；里程碑 2 完成后**不发布**；里程碑 3 的最后一个 Task 才 tag + release
- 遇到 Windows-specific 代码（Task 13/14），在 macOS 开发机上编译可以通过 `GOOS=windows go vet ./...` 验证
- 禁止 `--no-verify` 跳过 commit hook；失败就修

---

# 里程碑 1：协议与 PNG 新链路（内部）

## Task 1: `proto` 请求编解码

**Files:**
- Create: `internal/proto/proto.go`
- Create: `internal/proto/proto_test.go`

- [ ] **Step 1: 写失败测试（请求往返 + 边界）**

创建 `internal/proto/proto_test.go`：

```go
package proto

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteReadRequest_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Request
		wire string
	}{
		{"png", Request{Kind: "png"}, "GET png\n"},
		{"file", Request{Kind: "file"}, "GET file\n"},
		{"file force", Request{Kind: "file", Force: true}, "GET file force\n"},
		{"auto", Request{Kind: "auto"}, "GET auto\n"},
		{"auto force", Request{Kind: "auto", Force: true}, "GET auto force\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteRequest(&buf, tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := buf.String(); got != tc.wire {
				t.Fatalf("wire = %q, want %q", got, tc.wire)
			}
			got, err := ReadRequest(bufio.NewReader(strings.NewReader(tc.wire)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tc.in {
				t.Fatalf("round-trip = %+v, want %+v", got, tc.in)
			}
		})
	}
}

func TestReadRequest_Errors(t *testing.T) {
	cases := []struct {
		name string
		wire string
	}{
		{"no GET prefix", "PNG\n"},
		{"unknown kind", "GET zip\n"},
		{"extra garbage", "GET png extra\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadRequest(bufio.NewReader(strings.NewReader(tc.wire)))
			if err == nil {
				t.Fatalf("want error for %q", tc.wire)
			}
		})
	}
}

func TestReadRequest_EOF(t *testing.T) {
	_, err := ReadRequest(bufio.NewReader(strings.NewReader("")))
	if !errors.Is(err, io.EOF) && err == nil {
		t.Fatalf("want non-nil error on EOF, got nil")
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/proto/...`
Expected: FAIL（包不存在）

- [ ] **Step 3: 实现最小代码**

创建 `internal/proto/proto.go`：

```go
// Package proto defines the wire protocol between clipship daemon and pull
// clients. Requests are a single text line: `GET <kind> [force]\n`.
// Responses start with a header line and are followed by raw bytes.
package proto

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Request is what a pull client sends to the daemon.
type Request struct {
	Kind  string // "png" | "file" | "auto"
	Force bool   // ignore daemon-side soft size limit
}

// Response describes the kind/shape of a daemon response header.
//   Kind "png"  -> body is PNG bytes until EOF
//   Kind "file" -> body is Size bytes, original filename = Name
//   Kind "tar"  -> body is Size bytes, a tar stream
//   Kind "err"  -> no body; Err is the message
type Response struct {
	Kind string
	Name string // set for "file"
	Size int64  // set for "file" / "tar"
	Err  string // set for "err"
}

var validKinds = map[string]bool{"png": true, "file": true, "auto": true}

// WriteRequest serializes req to w as a single line.
func WriteRequest(w io.Writer, req Request) error {
	if !validKinds[req.Kind] {
		return fmt.Errorf("proto: invalid request kind %q", req.Kind)
	}
	line := "GET " + req.Kind
	if req.Force {
		line += " force"
	}
	line += "\n"
	_, err := io.WriteString(w, line)
	return err
}

// ReadRequest parses a single request line from r.
func ReadRequest(r *bufio.Reader) (Request, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if line == "" {
			return Request{}, err
		}
		// fall through and try to parse what we got; most callers treat partial as error
	}
	line = strings.TrimRight(line, "\r\n")
	parts := strings.Split(line, " ")
	if len(parts) < 2 || parts[0] != "GET" {
		return Request{}, fmt.Errorf("proto: malformed request %q", line)
	}
	kind := parts[1]
	if !validKinds[kind] {
		return Request{}, fmt.Errorf("proto: unknown kind %q", kind)
	}
	force := false
	switch len(parts) {
	case 2:
	case 3:
		if parts[2] != "force" {
			return Request{}, fmt.Errorf("proto: unknown modifier %q", parts[2])
		}
		force = true
	default:
		return Request{}, fmt.Errorf("proto: too many tokens in %q", line)
	}
	return Request{Kind: kind, Force: force}, nil
}
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/proto/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/proto/proto.go internal/proto/proto_test.go
git commit -m "feat(proto): add Request encoding + tests"
```

---

## Task 2: `proto` 响应头编解码（TYPE / ERR）

**Files:**
- Modify: `internal/proto/proto.go`
- Modify: `internal/proto/proto_test.go`

- [ ] **Step 1: 追加失败测试**

在 `internal/proto/proto_test.go` 末尾追加：

```go
import "net/url" // <-- 若已存在 import 合并到已有 import 块

func TestWriteReadHeader_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Response
		wire string
	}{
		{"png", Response{Kind: "png"}, "TYPE png\n"},
		{"file simple", Response{Kind: "file", Name: "report.pdf", Size: 1024}, "TYPE file report.pdf 1024\n"},
		{"file spaces", Response{Kind: "file", Name: "my report.pdf", Size: 10}, "TYPE file my%20report.pdf 10\n"},
		{"file unicode", Response{Kind: "file", Name: "报告.pdf", Size: 0}, "TYPE file %E6%8A%A5%E5%91%8A.pdf 0\n"},
		{"tar", Response{Kind: "tar", Size: 2048}, "TYPE tar 2048\n"},
		{"err", Response{Kind: "err", Err: "clipboard has no files"}, "ERR clipboard has no files\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteHeader(&buf, tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := buf.String(); got != tc.wire {
				t.Fatalf("wire = %q, want %q", got, tc.wire)
			}
			got, err := ReadHeader(bufio.NewReader(strings.NewReader(tc.wire)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tc.in {
				t.Fatalf("round-trip = %+v, want %+v", got, tc.in)
			}
		})
	}
}

func TestReadHeader_Malformed(t *testing.T) {
	cases := []string{
		"garbage\n",
		"TYPE\n",
		"TYPE zip 10\n",
		"TYPE file missingsize\n",
		"TYPE file badsize abc\n",
		"TYPE tar abc\n",
	}
	for _, w := range cases {
		if _, err := ReadHeader(bufio.NewReader(strings.NewReader(w))); err == nil {
			t.Fatalf("want error for %q", w)
		}
	}
}

// ensure percent-encoding round-trips through url.PathEscape/Unescape
func TestEncodeName_PathEscape(t *testing.T) {
	raw := "a b/c:d?e.txt"
	enc := url.PathEscape(raw)
	dec, err := url.PathUnescape(enc)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	if dec != raw {
		t.Fatalf("round-trip mismatch: got %q want %q", dec, raw)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/proto/...`
Expected: FAIL（`WriteHeader` / `ReadHeader` 未定义）

- [ ] **Step 3: 追加实现**

在 `internal/proto/proto.go` 末尾追加（并在文件顶部 import 块中加 `"net/url"`、`"strconv"`）：

```go
import (
	// 合并已有 import
	"net/url"
	"strconv"
)

// WriteHeader writes a single response header line (no body).
func WriteHeader(w io.Writer, resp Response) error {
	switch resp.Kind {
	case "png":
		_, err := io.WriteString(w, "TYPE png\n")
		return err
	case "file":
		enc := url.PathEscape(resp.Name)
		_, err := fmt.Fprintf(w, "TYPE file %s %d\n", enc, resp.Size)
		return err
	case "tar":
		_, err := fmt.Fprintf(w, "TYPE tar %d\n", resp.Size)
		return err
	case "err":
		_, err := fmt.Fprintf(w, "ERR %s\n", resp.Err)
		return err
	default:
		return fmt.Errorf("proto: invalid response kind %q", resp.Kind)
	}
}

// ReadHeader parses a single response header line from r.
func ReadHeader(r *bufio.Reader) (Response, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return Response{}, err
	}
	line = strings.TrimRight(line, "\r\n")

	if strings.HasPrefix(line, "ERR ") {
		return Response{Kind: "err", Err: strings.TrimPrefix(line, "ERR ")}, nil
	}
	if !strings.HasPrefix(line, "TYPE ") {
		return Response{}, fmt.Errorf("proto: malformed header %q", line)
	}
	rest := strings.TrimPrefix(line, "TYPE ")
	parts := strings.Split(rest, " ")
	switch parts[0] {
	case "png":
		if len(parts) != 1 {
			return Response{}, fmt.Errorf("proto: png header has extra tokens: %q", line)
		}
		return Response{Kind: "png"}, nil
	case "file":
		if len(parts) != 3 {
			return Response{}, fmt.Errorf("proto: file header wants name+size: %q", line)
		}
		name, err := url.PathUnescape(parts[1])
		if err != nil {
			return Response{}, fmt.Errorf("proto: decode name: %w", err)
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return Response{}, fmt.Errorf("proto: parse file size: %w", err)
		}
		return Response{Kind: "file", Name: name, Size: size}, nil
	case "tar":
		if len(parts) != 2 {
			return Response{}, fmt.Errorf("proto: tar header wants size: %q", line)
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Response{}, fmt.Errorf("proto: parse tar size: %w", err)
		}
		return Response{Kind: "tar", Size: size}, nil
	default:
		return Response{}, fmt.Errorf("proto: unknown TYPE kind %q", parts[0])
	}
}
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/proto/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/proto/proto.go internal/proto/proto_test.go
git commit -m "feat(proto): add response header/ERR encoding + tests"
```

---

## Task 3: `pack` sanitize + 撞名

**Files:**
- Create: `internal/pack/sanitize.go`
- Create: `internal/pack/sanitize_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/pack/sanitize_test.go`：

```go
package pack

import "testing"

func TestSanitizeBasename(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"report.pdf", "report.pdf"},
		{"my:file.txt", "my_file.txt"},
		{`what?name*.pdf`, "what_name_.pdf"},
		{`<pointy>"brackets|pipe`, "_pointy__brackets_pipe"},
		{"trailing ", "trailing_"},
		{"trailing.", "trailing_"},
		{"  name  ", "  name_"}, // only trailing space collapses; leading preserved
		{"", "_"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SanitizeBasename(tc.in)
			if got != tc.out {
				t.Fatalf("SanitizeBasename(%q) = %q, want %q", tc.in, got, tc.out)
			}
		})
	}
}

func TestResolveName_NoCollision(t *testing.T) {
	seen := make(map[string]bool)
	got := ResolveName(seen, "a.txt")
	if got != "a.txt" {
		t.Fatalf("got %q, want a.txt", got)
	}
	if !seen["a.txt"] {
		t.Fatal("expected seen[a.txt]=true")
	}
}

func TestResolveName_Collision(t *testing.T) {
	seen := map[string]bool{"a.txt": true}
	got := ResolveName(seen, "a.txt")
	if got != "a (1).txt" {
		t.Fatalf("got %q, want a (1).txt", got)
	}
	seen[got] = true
	got2 := ResolveName(seen, "a.txt")
	if got2 != "a (2).txt" {
		t.Fatalf("got %q, want a (2).txt", got2)
	}
}

func TestResolveName_NoExt(t *testing.T) {
	seen := map[string]bool{"LICENSE": true}
	got := ResolveName(seen, "LICENSE")
	if got != "LICENSE (1)" {
		t.Fatalf("got %q, want LICENSE (1)", got)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/pack/...`
Expected: FAIL（包/函数不存在）

- [ ] **Step 3: 实现**

创建 `internal/pack/sanitize.go`：

```go
// Package pack handles tar packing/unpacking and filename sanitization used by
// the file clipboard pull path.
package pack

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Chars that are legal on Windows file systems but problematic on many Linux
// fs or break shell handling. We replace them with underscore.
const invalidChars = `:*?"<>|`

// SanitizeBasename replaces illegal characters in a single path component and
// collapses trailing space/dot to underscore. It does not touch path separators.
func SanitizeBasename(name string) string {
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if strings.ContainsRune(invalidChars, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	// Collapse trailing space/dot runs (Windows strips them; make result stable).
	if n := len(out); n > 0 {
		last := out[n-1]
		if last == ' ' || last == '.' {
			out = out[:n-1] + "_"
		}
	}
	return out
}

// ResolveName returns a name that does not collide with anything in `seen`,
// appending `(1)`, `(2)` … before the extension. It also marks the returned
// name as seen.
func ResolveName(seen map[string]bool, name string) string {
	if !seen[name] {
		seen[name] = true
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !seen[candidate] {
			seen[candidate] = true
			return candidate
		}
	}
}
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/pack/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pack/sanitize.go internal/pack/sanitize_test.go
git commit -m "feat(pack): add filename sanitize + collision resolver"
```

---

## Task 4: `pack.PackTar` 打包（单文件 / 多文件 / 目录 / 软限制）

**Files:**
- Create: `internal/pack/pack.go`
- Create: `internal/pack/pack_test.go`

- [ ] **Step 1: 建一个小辅助把 `files.Entry` 类型依赖解耦**

先新建 `internal/clipboard/files/files.go`（完整的 files 包在 Task 6 展开，这里只先放下 Entry 和错误，让 pack 能引用）：

```go
package files

import "errors"

type Entry struct {
	Path  string
	IsDir bool
}

var (
	ErrNoFiles     = errors.New("clipboard has no files")
	ErrUnsupported = errors.New("file clipboard unsupported on this os")
)
```

- [ ] **Step 2: 写失败测试**

创建 `internal/pack/pack_test.go`：

```go
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
	p := writeTmp(t, tmp, "big.bin", "0123456789") // 10 bytes
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
```

- [ ] **Step 3: 运行，确认失败**

Run: `go test -race ./internal/pack/... ./internal/clipboard/files/...`
Expected: FAIL（`PackTar`、`ErrTooLarge` 未定义）

- [ ] **Step 4: 实现**

创建 `internal/pack/pack.go`：

```go
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
// Returns (stream, totalBytes, nil) on success. The stream is backed by an
// in-memory buffer so size is known up-front — acceptable because we expect
// clipboard content to be bounded by maxBytes.
func PackTar(entries []files.Entry, maxBytes int64, force bool) (io.ReadCloser, int64, error) {
	// 1) resolve entries to a flat list of (tar-internal-path, absolute-path).
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

	// 2) limit check
	if !force && maxBytes > 0 && total > maxBytes {
		return nil, total, fmt.Errorf("%w: %d > %d", ErrTooLarge, total, maxBytes)
	}

	// 3) build tar into a buffer
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
```

- [ ] **Step 5: 运行，确认通过**

Run: `go test -race ./internal/pack/... ./internal/clipboard/files/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/pack/pack.go internal/pack/pack_test.go internal/clipboard/files/files.go
git commit -m "feat(pack): implement PackTar with soft size limit + force"
```

---

## Task 5: `pack.UnpackTar`

**Files:**
- Modify: `internal/pack/pack.go`
- Modify: `internal/pack/pack_test.go`

- [ ] **Step 1: 追加失败测试**

在 `internal/pack/pack_test.go` 末尾追加：

```go
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
	// Build a tar where the header name has a `:` (legal on Windows-source, not on ext4).
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
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/pack/...`
Expected: FAIL（`UnpackTar` 未定义）

- [ ] **Step 3: 实现追加**

在 `internal/pack/pack.go` 末尾追加：

```go
// UnpackTar reads a tar stream from r and extracts regular files under destDir.
// Each path segment (basename + any intermediate directory names) is passed
// through sanitize. Returns absolute paths of the extracted regular files.
// Any header whose Name escapes destDir after cleaning is rejected.
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
		// Sanitize every path component.
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
		// paranoia: confirm target is under destDir
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
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/pack/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pack/pack.go internal/pack/pack_test.go
git commit -m "feat(pack): implement UnpackTar with sanitize + traversal guard"
```

---

## Task 6: `clipboard/files` 包骨架与 fake/stubs

**Files:**
- Create: `internal/clipboard/files/files_darwin.go`
- Create: `internal/clipboard/files/files_linux.go`
- Create: `internal/clipboard/files/files_windows.go` (stub)
- Create: `internal/clipboard/files/files_fake.go`
- Create: `internal/clipboard/files/files_test.go`
- Create: `internal/clipboard/files/entries_test.go`
- Modify: `internal/clipboard/files/files.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/clipboard/files/files_test.go`（跨平台的错误类型断言，不真的读剪贴板）：

```go
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
```

创建 `internal/clipboard/files/entries_test.go`（纯函数 entriesFromPaths 跨平台测试）：

```go
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
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/clipboard/files/...`
Expected: FAIL（`entriesFromPaths` 未定义，`ReadFiles` 在当前 OS 未定义）

- [ ] **Step 3: 修改 `files.go` 添加 `entriesFromPaths`**

替换 `internal/clipboard/files/files.go` 全文为：

```go
// Package files reads file/directory path lists from the OS clipboard.
// Format maps: Windows CF_HDROP, macOS public.file-url, Linux text/uri-list.
package files

import (
	"errors"
	"os"
)

type Entry struct {
	Path  string
	IsDir bool
}

var (
	ErrNoFiles     = errors.New("clipboard has no files")
	ErrUnsupported = errors.New("file clipboard unsupported on this os")
)

// entriesFromPaths stats each path and fills IsDir. Missing/unreachable paths
// are returned with IsDir=false (caller decides what to do).
func entriesFromPaths(paths []string) []Entry {
	out := make([]Entry, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		isDir := err == nil && info.IsDir()
		out = append(out, Entry{Path: p, IsDir: isDir})
	}
	return out
}
```

- [ ] **Step 4: 创建 stubs**

创建 `internal/clipboard/files/files_darwin.go`：

```go
//go:build darwin && !clipship_fake

package files

func ReadFiles() ([]Entry, error) {
	return nil, ErrUnsupported
}
```

创建 `internal/clipboard/files/files_linux.go`：

```go
//go:build linux && !clipship_fake

package files

func ReadFiles() ([]Entry, error) {
	return nil, ErrUnsupported
}
```

创建 `internal/clipboard/files/files_windows.go` (stub，Task 13 替换为真实实现)：

```go
//go:build windows && !clipship_fake

package files

// Stub – real implementation lands in Task 13.
func ReadFiles() ([]Entry, error) {
	return nil, ErrUnsupported
}
```

创建 `internal/clipboard/files/files_fake.go`：

```go
//go:build clipship_fake

package files

import (
	"os"
	"strings"
)

// ReadFiles (fake) reads the colon-separated CLIPSHIP_FAKE_FILES env var.
// Empty env => ErrNoFiles. Useful for local end-to-end debugging and CI.
func ReadFiles() ([]Entry, error) {
	raw := os.Getenv("CLIPSHIP_FAKE_FILES")
	if raw == "" {
		return nil, ErrNoFiles
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	clean := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil, ErrNoFiles
	}
	return entriesFromPaths(clean), nil
}
```

- [ ] **Step 5: 运行，确认通过**

Run: `go test -race ./internal/clipboard/files/...`
Expected: PASS（stubs 编译通过，`entriesFromPaths` 测试通过）

Run: `go build -tags clipship_fake ./...`
Expected: 编译成功

- [ ] **Step 6: Commit**

```bash
git add internal/clipboard/files/
git commit -m "feat(files): add ReadFiles interface with stubs + fake"
```

---

## Task 7: `config` 新字段 + 测试

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/config/config_test.go`：

```go
package config

import "testing"

func TestResolvePull_Defaults(t *testing.T) {
	c := &Config{}
	p := ResolvePull(c)
	if p.Connect != DefaultAddr {
		t.Fatalf("connect = %q", p.Connect)
	}
	if p.LocalDir != DefaultLocalDir {
		t.Fatalf("local_dir = %q", p.LocalDir)
	}
	if p.Filename != DefaultFilename {
		t.Fatalf("filename = %q", p.Filename)
	}
	if p.FilesDir != DefaultFilesDir {
		t.Fatalf("files_dir = %q", p.FilesDir)
	}
	if p.MaxBytes != DefaultMaxBytes {
		t.Fatalf("max_bytes = %d", p.MaxBytes)
	}
}

func TestResolvePull_UserOverrides(t *testing.T) {
	c := &Config{Pull: Pull{
		Connect:  "1.2.3.4:5",
		FilesDir: "/tmp/f",
		MaxBytes: 123,
	}}
	p := ResolvePull(c)
	if p.Connect != "1.2.3.4:5" || p.FilesDir != "/tmp/f" || p.MaxBytes != 123 {
		t.Fatalf("overrides lost: %+v", p)
	}
}

func TestResolveDaemon_MaxBytesDefault(t *testing.T) {
	c := &Config{}
	d := ResolveDaemon(c)
	if d.MaxBytes != DefaultMaxBytes {
		t.Fatalf("daemon max_bytes = %d", d.MaxBytes)
	}
}

func TestResolveDaemon_MaxBytesOverride(t *testing.T) {
	c := &Config{Daemon: Daemon{MaxBytes: 99}}
	d := ResolveDaemon(c)
	if d.MaxBytes != 99 {
		t.Fatalf("daemon max_bytes = %d", d.MaxBytes)
	}
}
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/config/...`
Expected: FAIL（字段/常量未定义）

- [ ] **Step 3: 实现修改**

编辑 `internal/config/config.go`：

1. 修改 `Pull` 结构：
```go
type Pull struct {
	Connect  string `toml:"connect"`
	LocalDir string `toml:"local_dir"`
	Filename string `toml:"filename"`
	FilesDir string `toml:"files_dir"`
	MaxBytes int64  `toml:"max_bytes"`
}
```

2. 修改 `Daemon` 结构：
```go
type Daemon struct {
	Listen   string `toml:"listen"`
	MaxBytes int64  `toml:"max_bytes"`
}
```

3. 在常量块加：
```go
const (
	DefaultAddr     = "127.0.0.1:19983"
	DefaultLocalDir = "~/.clipship/inbox"
	DefaultFilename = "clip_{ts}.png"
	DefaultFilesDir = "~/.clipship/inbox/files"
	DefaultMaxBytes = int64(500 * 1024 * 1024) // 500 MB
)
```

4. 更新 `ResolvePull`：
```go
func ResolvePull(c *Config) Pull {
	p := c.Pull
	if p.Connect == "" {
		p.Connect = DefaultAddr
	}
	if p.LocalDir == "" {
		p.LocalDir = DefaultLocalDir
	}
	if p.Filename == "" {
		p.Filename = DefaultFilename
	}
	if p.FilesDir == "" {
		p.FilesDir = DefaultFilesDir
	}
	if p.MaxBytes == 0 {
		p.MaxBytes = DefaultMaxBytes
	}
	return p
}
```

5. 更新 `ResolveDaemon`：
```go
func ResolveDaemon(c *Config) Daemon {
	d := c.Daemon
	if d.Listen == "" {
		d.Listen = DefaultAddr
	}
	if d.MaxBytes == 0 {
		d.MaxBytes = DefaultMaxBytes
	}
	return d
}
```

6. 更新 `sample` 常量，在 `[pull]` 段加 `files_dir`、`max_bytes`；在 `[daemon]` 段加 `max_bytes`（注释掉，作为文档）。完整替换：
```go
const sample = `# clipship config
# Pick the blocks you need — nothing is required if you accept defaults.

# --- send: upload clipboard PNG to a remote host via SFTP -------------------
# default_host = "example"
#
# [hosts.example]
# addr       = "example.local"
# user       = "you"
# port       = 22
# identity   = "~/.ssh/your_ssh_key"
# remote_dir = "/tmp/clipship"
# filename   = "clip_{ts}.png"

# --- daemon: run on the LOCAL desktop machine (the clipboard owner) ---------
[daemon]
listen    = "127.0.0.1:19983"
# max_bytes = 524288000      # 500 MB hard ceiling for file pulls

# --- pull: run on the REMOTE dev machine (where Claude runs) ----------------
[pull]
connect   = "127.0.0.1:19983"
local_dir = "~/.clipship/inbox"                    # PNG output dir
files_dir = "~/.clipship/inbox/files"              # per-session file output dir
max_bytes = 524288000                              # soft limit; bypass with --force
filename  = "clip_{ts}.png"
`
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/config/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add files_dir + max_bytes for pull/daemon"
```

---

## Task 8: `server` 重构 — Options + 请求分发 + handlePNG 新协议

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/server_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/server/server_test.go`：

```go
package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/yangjh-xbmu/clipship/internal/clipboard/files"
	"github.com/yangjh-xbmu/clipship/internal/proto"
)

// startTest spins up a listener on a random port and returns its addr + a
// teardown func. opts may omit clipboard callbacks.
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
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/server/...`
Expected: FAIL（`Options`、`serveListener` 未定义；当前 `server.go` 没有 Options 注入）

- [ ] **Step 3: 重写 `internal/server/server.go`**

完整替换：

```go
package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
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
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
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

// handleFile / handleAuto are stubbed here so the file builds; real logic lands
// in Task 9.
func handleFile(conn net.Conn, opts Options, force bool) {
	_ = pack.ErrTooLarge // keep import alive; Task 9 removes this line
	_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: files.ErrUnsupported.Error()})
}

func handleAuto(conn net.Conn, opts Options, force bool) {
	handlePNG(conn, opts)
}
```

注：`package clipboard` 的 `ReadPNG` 已有；`files.ReadFiles` 在非 Windows 上返回 `ErrUnsupported`，所以 daemon 此刻跑在 macOS 也能编译通过。

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/server/...`
Expected: PASS

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat(server): add Options + dispatch + handlePNG on new proto"
```

---

## Task 9: `server.handleFile` + `handleAuto` 完整逻辑

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`

- [ ] **Step 1: 追加失败测试**

在 `internal/server/server_test.go` 末尾追加：

```go
import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
)

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
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/server/...`
Expected: FAIL（handleFile/handleAuto 尚未按新逻辑返回）

- [ ] **Step 3: 替换 `handleFile` / `handleAuto`**

在 `internal/server/server.go` 里用以下版本**替换** Task 8 的 stub 版本：

```go
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

	// Single plain file -> TYPE file
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

	// Multi file or contains directory -> pack as tar.
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
		// Delegate to file path without re-reading clipboard.
		origOpts := opts
		origOpts.ClipboardFiles = func() ([]files.Entry, error) { return entries, nil }
		handleFile(conn, origOpts, force)
		return
	}
	if err != nil && !errors.Is(err, files.ErrNoFiles) && !errors.Is(err, files.ErrUnsupported) {
		_ = proto.WriteHeader(conn, proto.Response{Kind: "err", Err: err.Error()})
		return
	}
	// Fall back to PNG.
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
```

也需要在 `import` 块加上 `"errors"`, `"os"`, `"path/filepath"`（如未导入）。清理 Task 8 里引入的 `_ = pack.ErrTooLarge` 占位行（若还在）。

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/server/...`
Expected: PASS（全部 10 个左右测试）

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat(server): handleFile + handleAuto with tar, size limit, fallback"
```

---

## Task 10: `client.PullPNG` 升级到新协议

**Files:**
- Modify: `internal/client/client.go`
- Create: `internal/client/client_test.go`

- [ ] **Step 1: 写失败测试（client 对接 fake server）**

创建 `internal/client/client_test.go`：

```go
package client

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangjh-xbmu/clipship/internal/proto"
)

// fakeDaemon returns a function that registers a handler and an addr.
// handler is invoked in a goroutine per connection; it owns the conn.
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

// ensure io.Reader cooperation with bufio (if client uses ReadAll on raw conn
// we'll miss byte in bufio). This just confirms client reads everything.
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
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/client/...`
Expected: FAIL（`PullPNG` 未定义；当前是 `Pull`）

- [ ] **Step 3: 替换 `internal/client/client.go`**

整个文件重写：

```go
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

	"github.com/yangjh-xbmu/clipship/internal/proto"
)

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
		// ok
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

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}
```

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/client/...`
Expected: PASS

注：此时 `cmd/clipship/main.go` 仍调用旧 `client.Pull`，会编译失败。下一步修。

- [ ] **Step 5: 临时过渡 `cmd/clipship/main.go` 的 `runPull`**

在 `cmd/clipship/main.go` 替换 `runPull` 以及 `import` 里 `client`：

```go
func runPull() error {
	cfg := config.LoadOrEmpty()
	p := config.ResolvePull(cfg)
	path, _, err := client.PullPNG(p.Connect, p.LocalDir, p.Filename)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
```

（JSON 输出将在 Task 12 一起改；这里先保持 `pull` 的旧 stdout 行为以解编译）

Run: `go build ./...`
Expected: 编译成功

Run: `go test -race ./...`
Expected: PASS 全部

- [ ] **Step 6: Commit**

```bash
git add internal/client/ cmd/clipship/main.go
git commit -m "feat(client): rewrite Pull as PullPNG on new protocol"
```

---

## Task 11: `client.PullFile` + `PullAuto`

**Files:**
- Modify: `internal/client/client.go`
- Modify: `internal/client/client_test.go`

- [ ] **Step 1: 追加失败测试**

在 `internal/client/client_test.go` 末尾追加：

```go
import (
	"archive/tar"
	"bytes"
)

// helper: write a tar containing name=content pairs
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
```

- [ ] **Step 2: 运行，确认失败**

Run: `go test -race ./internal/client/...`
Expected: FAIL

- [ ] **Step 3: 追加实现**

在 `internal/client/client.go` 末尾追加（在 `package client` 块内）：

```go
import (
	// 追加到已有 import 块
	"github.com/yangjh-xbmu/clipship/internal/clipboard/files"
	"github.com/yangjh-xbmu/clipship/internal/pack"
)

type FileResult struct {
	SessionDir string   `json:"session_dir"`
	Files      []string `json:"files"`
	Bytes      int64    `json:"bytes"`
}

type AutoResult struct {
	Kind string      `json:"kind"` // "png" | "file"
	PNG  *PNGResult  `json:"png,omitempty"`
	File *FileResult `json:"file,omitempty"`
}

type PNGResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// PullFile dials addr, speaks `GET file [force]`, and writes the payload into
// <destParentDir>/<ts>/. Returns FileResult with absolute paths of extracted files.
func PullFile(addr, destParentDir string, force bool) (FileResult, error) {
	return pullFile(addr, destParentDir, force, "file")
}

func pullFile(addr, destParentDir string, force bool, kind string) (FileResult, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return FileResult{}, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

	if err := proto.WriteRequest(conn, proto.Request{Kind: kind, Force: force}); err != nil {
		return FileResult{}, err
	}
	r := bufio.NewReader(conn)
	h, err := proto.ReadHeader(r)
	if err != nil {
		return FileResult{}, fmt.Errorf("header: %w", err)
	}

	sessionDir, err := makeSessionDir(destParentDir)
	if err != nil {
		return FileResult{}, err
	}

	switch h.Kind {
	case "err":
		return FileResult{}, fmt.Errorf("daemon: %s", h.Err)
	case "file":
		return writeSingleFile(sessionDir, h.Name, h.Size, r)
	case "tar":
		limited := io.LimitReader(r, h.Size)
		paths, err := pack.UnpackTar(limited, sessionDir, pack.SanitizeBasename)
		if err != nil {
			_ = os.RemoveAll(sessionDir) // clean partial
			return FileResult{}, fmt.Errorf("unpack: %w", err)
		}
		return FileResult{SessionDir: sessionDir, Files: paths, Bytes: h.Size}, nil
	default:
		return FileResult{}, fmt.Errorf("unexpected kind %q for file request", h.Kind)
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
		os.Remove(dest)
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

// satisfy import even if unused in short term
var _ = files.ErrNoFiles
```

注：`import` 块需包含已有项加 `"github.com/yangjh-xbmu/clipship/internal/clipboard/files"` 和 `"github.com/yangjh-xbmu/clipship/internal/pack"`。

- [ ] **Step 4: 运行，确认通过**

Run: `go test -race ./internal/client/... ./internal/server/...`
Expected: PASS

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 5: Commit**

```bash
git add internal/client/
git commit -m "feat(client): add PullFile + PullAuto with tar unpack"
```

---

## Task 12: `cmd/clipship` 新子命令 + JSON stdout

**Files:**
- Modify: `cmd/clipship/main.go`

- [ ] **Step 1: 规划**

需要做：
1. `pull` 升级：stdout 输出 `{"kind":"png","path":...,"bytes":...}`
2. 新增 `pull-file [--force]`：输出 `{"kind":"file","session_dir":...,"files":[...],"bytes":...}`
3. 新增 `pull-auto [--force]`：输出 `AutoResult` 的 JSON
4. `runDaemon` 传入 `MaxBytes` 到 `server.Options`
5. `usage` 更新

- [ ] **Step 2: 编辑 `main.go`**

全文替换 `cmd/clipship/main.go` 为：

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yangjh-xbmu/clipship/internal/client"
	"github.com/yangjh-xbmu/clipship/internal/clipboard"
	"github.com/yangjh-xbmu/clipship/internal/config"
	"github.com/yangjh-xbmu/clipship/internal/server"
	"github.com/yangjh-xbmu/clipship/internal/transfer"
)

const version = "0.4.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "send":
		err = runSend(os.Args[2:])
	case "pull":
		err = runPull()
	case "pull-file":
		err = runPullFile(os.Args[2:])
	case "pull-auto":
		err = runPullAuto(os.Args[2:])
	case "daemon":
		err = runDaemon()
	case "dump-png":
		err = runDumpPNG()
	case "init":
		err = runInit()
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("clipship", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`clipship — move clipboard content between local and SSH-connected hosts

Usage:
  clipship daemon             serve PNG / files on a local TCP socket (persistent)
  clipship pull               fetch PNG from a daemon (via ssh -L tunnel); stdout=JSON
  clipship pull-file [--force]  fetch files from a daemon; stdout=JSON
  clipship pull-auto [--force]  fetch whatever is on clipboard; stdout=JSON

  clipship send [host]        upload clipboard PNG to [host] via SFTP
  clipship dump-png           write current clipboard PNG to stdout
  clipship init               write a sample config file
  clipship doctor [host]      run SFTP health checks for the send workflow
  clipship version            print version

Config:
  ` + mustConfigPath())
}

func mustConfigPath() string {
	p, err := config.Path()
	if err != nil {
		return "(unknown)"
	}
	return p
}

func runInit() error {
	p, created, err := config.WriteSample()
	if err != nil {
		return err
	}
	if !created {
		fmt.Println("config already exists:", p)
		return nil
	}
	fmt.Println("wrote sample config:", p)
	fmt.Println("edit it, then run: clipship send")
	return nil
}

func runSend(args []string) error {
	cfg, p, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config (%s): %w (run `clipship init`)", p, err)
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	name, host, err := config.Resolve(cfg, name)
	if err != nil {
		return err
	}
	img, err := clipboard.ReadPNG()
	if err != nil {
		return err
	}
	c, err := transfer.Dial(transfer.Target{
		User: host.User, Addr: host.Addr, Port: host.Port, Identity: host.Identity,
	})
	if err != nil {
		return err
	}
	defer c.Close()
	filename := renderFilename(host.Filename, name)
	remotePath, err := c.Upload(host.RemoteDir, filename, img)
	if err != nil {
		return err
	}
	if err := clipboard.WriteText(remotePath); err != nil {
		fmt.Fprintln(os.Stderr, "warn: could not write path to clipboard:", err)
	}
	fmt.Printf("✓ %s  (%d bytes, path copied to clipboard)\n", remotePath, len(img))
	return nil
}

func runDoctor(args []string) error {
	cfg, p, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config (%s): %w", p, err)
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	name, host, err := config.Resolve(cfg, name)
	if err != nil {
		return err
	}
	fmt.Printf("host: %s  (%s@%s:%d)\n", name, host.User, host.Addr, host.Port)
	c, err := transfer.Dial(transfer.Target{
		User: host.User, Addr: host.Addr, Port: host.Port, Identity: host.Identity,
	})
	if err != nil {
		return fmt.Errorf("ssh: %w", err)
	}
	defer c.Close()
	fmt.Println("ssh: ok")
	if err := c.MkdirAll(host.RemoteDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", host.RemoteDir, err)
	}
	fmt.Println("remote_dir writable: ok")
	if _, err := clipboard.ReadPNG(); err != nil {
		fmt.Println("clipboard image: none (copy a screenshot, then retry)")
	} else {
		fmt.Println("clipboard image: ok")
	}
	return nil
}

func runDumpPNG() error {
	img, err := clipboard.ReadPNG()
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(img)
	return err
}

func runDaemon() error {
	cfg := config.LoadOrEmpty()
	d := config.ResolveDaemon(cfg)
	return server.Run(d.Listen, server.Options{MaxBytes: d.MaxBytes})
}

type pngJSON struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

func runPull() error {
	cfg := config.LoadOrEmpty()
	p := config.ResolvePull(cfg)
	path, n, err := client.PullPNG(p.Connect, p.LocalDir, p.Filename)
	if err != nil {
		return err
	}
	return writeJSON(pngJSON{Kind: "png", Path: path, Bytes: n})
}

func runPullFile(args []string) error {
	force := parseForce(args)
	cfg := config.LoadOrEmpty()
	p := config.ResolvePull(cfg)
	res, err := client.PullFile(p.Connect, p.FilesDir, force)
	if err != nil {
		return err
	}
	out := struct {
		Kind       string   `json:"kind"`
		SessionDir string   `json:"session_dir"`
		Files      []string `json:"files"`
		Bytes      int64    `json:"bytes"`
	}{"file", res.SessionDir, res.Files, res.Bytes}
	return writeJSON(out)
}

func runPullAuto(args []string) error {
	force := parseForce(args)
	cfg := config.LoadOrEmpty()
	p := config.ResolvePull(cfg)
	res, err := client.PullAuto(p.Connect, p.LocalDir, p.Filename, p.FilesDir, force)
	if err != nil {
		return err
	}
	switch res.Kind {
	case "png":
		return writeJSON(pngJSON{Kind: "png", Path: res.PNG.Path, Bytes: res.PNG.Bytes})
	case "file":
		out := struct {
			Kind       string   `json:"kind"`
			SessionDir string   `json:"session_dir"`
			Files      []string `json:"files"`
			Bytes      int64    `json:"bytes"`
		}{"file", res.File.SessionDir, res.File.Files, res.File.Bytes}
		return writeJSON(out)
	default:
		return fmt.Errorf("unexpected auto kind %q", res.Kind)
	}
}

func parseForce(args []string) bool {
	for _, a := range args {
		if a == "--force" || a == "-f" {
			return true
		}
	}
	return false
}

func writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	b = append(b, '\n')
	_, err = os.Stdout.Write(b)
	return err
}

func renderFilename(tmpl, host string) string {
	if tmpl == "" {
		tmpl = "clip_{ts}.png"
	}
	ts := time.Now().Format("20060102_150405")
	out := strings.ReplaceAll(tmpl, "{ts}", ts)
	out = strings.ReplaceAll(out, "{host}", host)
	return out
}
```

- [ ] **Step 3: 运行构建 + 测试**

Run: `go build ./...`
Expected: 编译成功

Run: `go test -race ./...`
Expected: PASS 全部

- [ ] **Step 4: 手工验证 JSON 输出**

Run（启 daemon）：`go run ./cmd/clipship daemon &`
等 1 秒后另一个 shell：
- 复制一张截图
- Run: `go run ./cmd/clipship pull`
Expected: stdout 一行 JSON，`kind=png`，`path` 指向 `~/.clipship/inbox/`

（非 Windows 上 `pull-file` 会返回 `ErrUnsupported`，这是预期）

Run: `pkill -f 'clipship daemon'`

- [ ] **Step 5: Commit**

```bash
git add cmd/clipship/main.go
git commit -m "feat(cmd): add pull-file / pull-auto + JSON stdout + bump to 0.4.0"
```

**里程碑 1 完成。不发布。**

---

# 里程碑 2：Windows 真实实现（内部）

## Task 13: `files_windows.go` 读取 CF_HDROP

**Files:**
- Modify: `internal/clipboard/files/files_windows.go`
- Modify: `go.mod` / `go.sum`

- [ ] **Step 1: 加依赖**

Run: `go get golang.org/x/sys/windows@latest`
Expected: `go.mod` 新增 `golang.org/x/sys`

- [ ] **Step 2: 写失败「can-compile」测试**

创建/替换 `internal/clipboard/files/files_windows_test.go`：

```go
//go:build windows

package files

import "testing"

// Compile-time smoke: the function must be callable; real clipboard behavior
// is exercised by scripts/e2e_windows.ps1, not by CI.
func TestReadFiles_CompileSmoke(t *testing.T) {
	_, err := ReadFiles()
	// In CI / headless sessions the clipboard has nothing.
	// We just need: err must be either nil or one of the known sentinels.
	if err != nil && err != ErrNoFiles && err != ErrUnsupported {
		t.Logf("ReadFiles returned %v (accepted; real check is manual)", err)
	}
}
```

- [ ] **Step 3: 实现**

替换 `internal/clipboard/files/files_windows.go`：

```go
//go:build windows && !clipship_fake

package files

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfHDROP = 15
)

var (
	modUser32  = windows.NewLazySystemDLL("user32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")
	modKernel  = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = modUser32.NewProc("OpenClipboard")
	procCloseClipboard   = modUser32.NewProc("CloseClipboard")
	procGetClipboardData = modUser32.NewProc("GetClipboardData")

	procDragQueryFileW = modShell32.NewProc("DragQueryFileW")

	procGlobalLock   = modKernel.NewProc("GlobalLock")
	procGlobalUnlock = modKernel.NewProc("GlobalUnlock")
)

// ReadFiles reads CF_HDROP from the clipboard and returns an Entry per path.
func ReadFiles() ([]Entry, error) {
	ok, _, _ := procOpenClipboard.Call(0)
	if ok == 0 {
		return nil, fmt.Errorf("OpenClipboard failed: %w", syscall.GetLastError())
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(uintptr(cfHDROP))
	if h == 0 {
		return nil, ErrNoFiles
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("GlobalLock failed: %w", syscall.GetLastError())
	}
	defer procGlobalUnlock.Call(h)

	// DragQueryFileW(h, 0xFFFFFFFF, nil, 0) -> count
	count, _, _ := procDragQueryFileW.Call(p, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, ErrNoFiles
	}

	paths := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		// First call: get required length (not counting null terminator)
		length, _, _ := procDragQueryFileW.Call(p, i, 0, 0)
		if length == 0 {
			continue
		}
		buf := make([]uint16, length+1)
		n, _, _ := procDragQueryFileW.Call(
			p, i,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
		)
		if n == 0 {
			continue
		}
		paths = append(paths, windows.UTF16ToString(buf[:n]))
	}
	if len(paths) == 0 {
		return nil, ErrNoFiles
	}
	return entriesFromPaths(paths), nil
}
```

- [ ] **Step 4: 跨平台编译验证**

Run (on macOS dev machine):
```
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=amd64 go test -c -o /tmp/clipship_win.test ./internal/clipboard/files/
```
Expected: 编译成功，产出 `.test` 二进制

- [ ] **Step 5: Commit**

```bash
git add internal/clipboard/files/files_windows.go internal/clipboard/files/files_windows_test.go go.mod go.sum
git commit -m "feat(files): implement Windows CF_HDROP ReadFiles"
```

---

## Task 14: Windows 端到端 smoke test 脚本

**Files:**
- Create: `scripts/e2e_windows.ps1`

- [ ] **Step 1: 写脚本**

创建 `scripts/e2e_windows.ps1`：

```powershell
# clipship v0.4 file clipboard end-to-end smoke test (Windows).
#
# Prereqs:
#   - run from repo root in PowerShell
#   - go on PATH, $env:GOPATH/bin on PATH is handy
#
# What it does:
#   1. builds clipship
#   2. starts `clipship daemon` in background
#   3. copies a known file to the OS clipboard
#   4. runs `clipship pull-file` against 127.0.0.1:19983
#   5. verifies stdout JSON + extracted file content
#   6. shuts down daemon

$ErrorActionPreference = "Stop"

Write-Host "-- build"
go build -o clipship.exe ./cmd/clipship
if (-not (Test-Path .\clipship.exe)) { throw "build failed" }

Write-Host "-- start daemon"
$daemon = Start-Process -FilePath .\clipship.exe -ArgumentList "daemon" -PassThru -WindowStyle Hidden
Start-Sleep -Milliseconds 500

try {
    Write-Host "-- make a test file and copy to clipboard"
    $testDir = Join-Path $env:TEMP "clipship-e2e-$([guid]::NewGuid())"
    New-Item -ItemType Directory -Path $testDir | Out-Null
    $srcFile = Join-Path $testDir "hello.txt"
    "hello from clipship e2e $(Get-Date -Format o)" | Set-Content -Path $srcFile -Encoding UTF8

    # Place CF_HDROP on the clipboard
    Add-Type -AssemblyName System.Windows.Forms
    $paths = New-Object System.Collections.Specialized.StringCollection
    $paths.Add($srcFile) | Out-Null
    [System.Windows.Forms.Clipboard]::SetFileDropList($paths)

    Write-Host "-- run pull-file"
    $json = & .\clipship.exe pull-file
    if ($LASTEXITCODE -ne 0) { throw "pull-file failed: exit $LASTEXITCODE" }
    Write-Host "stdout: $json"

    $parsed = $json | ConvertFrom-Json
    if ($parsed.kind -ne "file") { throw "kind = $($parsed.kind)" }
    if ($parsed.files.Count -ne 1) { throw "files count = $($parsed.files.Count)" }

    $pulled = $parsed.files[0]
    $src = Get-Content $srcFile -Raw
    $dst = Get-Content $pulled -Raw
    if ($src -ne $dst) { throw "content mismatch: src=$src dst=$dst" }

    Write-Host "-- SUCCESS"
} finally {
    Write-Host "-- stop daemon"
    Stop-Process -Id $daemon.Id -Force -ErrorAction SilentlyContinue
}
```

- [ ] **Step 2: 文档引用**

无文件改动，直接下一步。

- [ ] **Step 3: Commit**

```bash
git add scripts/e2e_windows.ps1
git commit -m "test(e2e): add Windows smoke test script for pull-file"
```

- [ ] **Step 4: 手动执行（需在 Windows 机器上）**

在 Windows 机上 clone 仓库后：
```powershell
powershell -ExecutionPolicy Bypass -File scripts/e2e_windows.ps1
```
Expected 最后一行： `-- SUCCESS`

**里程碑 2 完成。不发布。**

---

# 里程碑 3：skill + 文档 + 发布

## Task 15: `/clip` skill 更新 + README 重写

**Files:**
- Modify: `README.md`

- [ ] **Step 1: README 改写**

用下面的内容**整体替换** `README.md`。核心变化：
- 顶部新增「v0.4 breaking changes」块
- 新增 Workflow B 的 `pull-file` / `pull-auto` 说明
- 协议段落改写为 `GET <kind>` + `TYPE <kind> [meta]`
- `/clip` skill spec 改写为单 skill + 参数分发

```markdown
# clipship

Move clipboard content (PNG or files) between your local desktop and an SSH-connected remote shell. Single Go binary, no daemons you don't start, works on Windows (file support) / macOS (PNG) / Linux (PNG).

**v0.4 breaking changes.** Protocol upgraded to a request/response format with a `TYPE` header. v0.4 daemon is **not** compatible with v0.3 client (or vice versa) — upgrade both sides together.

---

## Two content types, three workflows

```
┌─────────────────────────────────────────────────────────────────────────┐
│ A) send : desktop → remote via SFTP      (PNG only)                     │
│    clipship send      -> uploads PNG, returns path                      │
├─────────────────────────────────────────────────────────────────────────┤
│ B) pull : remote → desktop via daemon + SSH tunnel                      │
│    clipship pull         -> PNG                                         │
│    clipship pull-file    -> any file(s) / folder selected in Explorer   │
│    clipship pull-auto    -> whichever is on clipboard (default)         │
│    pairs with the Claude Code /clip skill (see below)                   │
└─────────────────────────────────────────────────────────────────────────┘
```

**File clipboard support is Windows-only in v0.4.** macOS / Linux return `ERR file clipboard unsupported on <os>`. PNG works everywhere.

---

## Install

### From source
```bash
go install github.com/yangjh-xbmu/clipship/cmd/clipship@latest
```

### Prebuilt binary
Grab from [Releases](https://github.com/yangjh-xbmu/clipship/releases) and drop on `PATH`.

Targets: `darwin/amd64`, `darwin/arm64`, `windows/amd64`. Linux needs cgo + libx11-dev (build from source, PNG only).

---

## Workflow A — `send` (unchanged)

See v0.3 docs (same as before): `clipship init` → edit config → `clipship send`.

---

## Workflow B — daemon + pull (updated)

### Daemon (runs on the desktop)

```
$ clipship daemon
clipship daemon listening on 127.0.0.1:19983
```

### Protocol (v0.4)

Client writes one line:
```
GET png|file|auto [force]\n
```
Daemon replies with a header line, then bytes:
```
TYPE png\n<png bytes to EOF>
TYPE file <url-escaped-name> <size>\n<size bytes>
TYPE tar <size>\n<size bytes of tar stream>
ERR <message>\n
```

### Pull commands (on the remote)

Each command outputs one JSON line on stdout:

```bash
$ clipship pull
{"kind":"png","path":"/home/you/.clipship/inbox/clip_20260419_153012.png","bytes":45678}

$ clipship pull-file
{"kind":"file","session_dir":"/home/you/.clipship/inbox/files/20260419_153012","files":["/home/you/.clipship/inbox/files/20260419_153012/report.pdf"],"bytes":234567}

$ clipship pull-auto
# ^ returns whichever of the above matches the clipboard state
```

Multi-file / folder pulls produce multiple entries in `files[]`, all under one `session_dir`.

### Size limits

Daemon refuses file pulls above `max_bytes` (default 500 MB) unless the client passes `--force`:
```bash
$ clipship pull-file --force
```
Daemon's `[daemon] max_bytes` is the hard ceiling; `[pull] max_bytes` is the client's comfort level.

### SSH tunnel

Same as v0.3 — forward `127.0.0.1:19983 → desktop:19983`, **never use `localhost`** (daemon is IPv4-only).

### Config additions

```toml
[daemon]
listen    = "127.0.0.1:19983"
# max_bytes = 524288000    # optional hard ceiling

[pull]
connect   = "127.0.0.1:19983"
local_dir = "~/.clipship/inbox"          # PNG output
files_dir = "~/.clipship/inbox/files"    # per-session file output
max_bytes = 524288000                    # 500 MB soft limit
filename  = "clip_{ts}.png"              # PNG filename template
```

---

## Claude Code integration — the `/clip` skill

One skill, three modes:

- `/clip` — auto (server picks based on clipboard state)
- `/clip png` — force PNG
- `/clip file` — force files

Put this in `~/.claude/skills/clip/SKILL.md`:

```markdown
---
name: clip
description: "Pull whatever's on the desktop clipboard (PNG or files) from an SSH-connected workstation via clipship daemon + SSH tunnel. Triggers: 'see this screenshot', 'clipboard image', '/clip', 'paste screenshot', 'grab this file', 'pull the file I just copied', '/clip file', '/clip png'."
user-invocable: true
allowed-tools: Bash, Read
effort: low
---

# /clip — pull workstation clipboard (PNG or files)

## Architecture

The workstation (SSH alias in `~/.clipship/workstation`, default `win`) runs `clipship daemon` in its interactive desktop session on `127.0.0.1:19983`. This host (where Claude runs) forwards to it with `ssh -L 19983:127.0.0.1:19983 -N -f <alias>`.

## Arguments

- (none)      → auto; daemon returns PNG or files based on clipboard
- `png`       → force PNG (same as v0.3)
- `file`      → force files; useful when clipboard also has an image preview

## Steps

### 1. Read config
```bash
HOST="$(cat ~/.clipship/workstation 2>/dev/null || echo win)"
```

### 2. Ensure tunnel (reuse if possible)
```bash
if ! lsof -iTCP:19983 -sTCP:LISTEN >/dev/null 2>&1; then
  ssh -L 19983:127.0.0.1:19983 -N -f "$HOST" 2>/tmp/clipship-tun-$$
  sleep 0.3
fi
```
Always `127.0.0.1`, never `localhost`.

### 3. Dispatch

```bash
ARG="${1:-}"
case "$ARG" in
  png)  CMD="clipship pull"      ;;
  file) CMD="clipship pull-file" ;;
  *)    CMD="clipship pull-auto" ;;
esac
JSON="$($CMD 2>/tmp/clipship-err-$$)"
RC=$?
```

### 4. Branch on result

If `RC != 0`, map stderr to a hint (table below) and stop.

Otherwise parse the JSON:

```bash
KIND="$(printf '%s' "$JSON" | python3 -c 'import sys,json; print(json.load(sys.stdin)["kind"])')"
if [ "$KIND" = "png" ]; then
  PATH_="$(printf '%s' "$JSON" | python3 -c 'import sys,json; print(json.load(sys.stdin)["path"])')"
  # Use the Read tool on "$PATH_"
elif [ "$KIND" = "file" ]; then
  FILES="$(printf '%s' "$JSON" | python3 -c 'import sys,json
d=json.load(sys.stdin)
print("\n".join(d["files"]))')"
  # For each line in "$FILES": if user's follow-up clearly targets one file, Read that one; otherwise Read them all unless >5 files or >10MB (then ask first).
fi
```

### Failure → user hint

| ERR contains                         | Hint                                                       |
|--------------------------------------|------------------------------------------------------------|
| `clipboard has no image`             | Take a screenshot (Win+Shift+S), retry                     |
| `clipboard has no files`             | Copy files in File Explorer (Ctrl+C), retry                |
| `clipboard has neither image nor …`  | Copy something first, retry                                |
| `too large`                          | Show size + ask user to confirm, then retry with `--force` |
| `empty response from daemon`         | Check forward uses 127.0.0.1, not localhost                |
| `dial 127.0.0.1:19983 ... refused`   | Start the daemon on the workstation                        |
| `file clipboard unsupported`         | Workstation OS isn't Windows; file pulls aren't available   |

Do not auto-retry, do not alternate hosts, do not start the daemon remotely.

## Do not
- Start the workstation daemon from this skill
- Modify SSH config
- Clean up the inbox (cron does)
- Tear down the tunnel
```

---

## Commands summary

| Command                        | Purpose                                            |
|--------------------------------|----------------------------------------------------|
| `clipship send [host]`         | Upload desktop clipboard PNG to host via SFTP      |
| `clipship daemon`              | Serve clipboard PNG/files on localhost:19983       |
| `clipship pull`                | Fetch PNG (JSON stdout)                            |
| `clipship pull-file [--force]` | Fetch files (JSON stdout)                          |
| `clipship pull-auto [--force]` | Fetch whichever type is on clipboard (JSON stdout) |
| `clipship init`                | Write a sample config file                         |
| `clipship doctor [host]`       | Health check for `send`                            |
| `clipship dump-png`            | Write current clipboard PNG to stdout              |
| `clipship version`             | Print version                                      |

## Migration from v0.3

1. Upgrade **both** daemon host and pull host to 0.4 together.
2. Replace your `/clip` skill with the one above (v0.3 skill parses raw PNG stdout; v0.4 outputs JSON).
3. If you want file support, the daemon must run on Windows; macOS/Linux daemons still serve PNG.

## Why not a shell alias around `scp`?
Same reason as v0.3 — clipboard access is the annoying part. v0.4 adds `CF_HDROP` handling on top.

## License
MIT. See [LICENSE](./LICENSE).
```

- [ ] **Step 2: 验证**

Run: `go build ./... && go test -race ./...`
Expected: 仍然 PASS

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: rewrite README for v0.4 file clipboard + /clip skill"
```

---

## Task 16: 版本号到 0.4.0 + 验证 + Release

**Files:**
- （版本常量在 Task 12 已从 `0.3.0` 改到 `0.4.0`；这一步只做发布流程）

- [ ] **Step 1: 确认版本**

Run: `go run ./cmd/clipship version`
Expected: `clipship 0.4.0`

- [ ] **Step 2: 最终回归**

Run:
```
go vet ./...
go test -race ./...
go build ./...
GOOS=windows GOARCH=amd64 go build ./cmd/clipship
```
Expected: 全部成功

- [ ] **Step 3: SESSION_LOG 归档**

编辑 `SESSION_LOG.md`，把现有完成/发现/待办搬到新建的 `SESSION_ARCHIVE.md` 末尾（用 `>>> 2026-04-19 清盘` 分隔），重置 SESSION_LOG 留新空框架。不在本 plan 强制要求，保留给最后 `/done` 调用处理。

- [ ] **Step 4: Tag + Push**

```bash
git tag -a v0.4.0 -m "v0.4.0: file clipboard (Windows), /clip skill unified"
git push
git push --tags
```

- [ ] **Step 5: GitHub Release**

```bash
gh release create v0.4.0 \
  --title "v0.4.0 — File Clipboard (Windows) + Unified /clip Skill" \
  --notes-file - <<'EOF'
## Highlights

- **Pull arbitrary files** from a Windows workstation to your remote Claude Code session
- New subcommands: `pull-file`, `pull-auto` (alongside the existing `pull` for PNG)
- Unified `/clip` skill: `/clip`, `/clip png`, `/clip file`
- JSON stdout for all pull subcommands

## Breaking

- Protocol upgraded (v0.4 daemon ↔ v0.3 client are incompatible). Upgrade both sides together.
- `/clip` skill rewritten (parses JSON now).

See [README](./README.md#migration-from-v03) for migration steps.

## Known limits

- File clipboard is Windows-only in v0.4 (macOS / Linux daemons still serve PNG fine).
- Files larger than 500 MB require `--force`; raise `[daemon] max_bytes` in config to change the ceiling.
EOF
```

- [ ] **Step 6: 上传预编译二进制（可选）**

```bash
GOOS=windows GOARCH=amd64 go build -o clipship_windows_amd64.exe ./cmd/clipship
GOOS=darwin GOARCH=arm64 go build -o clipship_darwin_arm64 ./cmd/clipship
GOOS=darwin GOARCH=amd64 go build -o clipship_darwin_amd64 ./cmd/clipship
gh release upload v0.4.0 clipship_windows_amd64.exe clipship_darwin_arm64 clipship_darwin_amd64
```

**里程碑 3 完成。v0.4.0 发布。**

---

## Appendix: Self-Review checklist

plan 作者自审（已完成）：

- [x] **Spec 覆盖率**：
  - §4 架构 → 整个 plan 结构
  - §5.1 clipboard/files → Task 6 + Task 13
  - §5.2 proto → Task 1 + Task 2
  - §5.3 pack → Task 3 + Task 4 + Task 5
  - §5.4 clipboard.ReadPNG → 未改动（保留）
  - §5.5 server → Task 8 + Task 9
  - §5.6 client → Task 10 + Task 11
  - §5.7 cmd/clipship → Task 12
  - §5.8 /clip skill → Task 15
  - §5.9 config → Task 7
  - §6 数据流 → Task 9/11 测试覆盖
  - §7 错误处理 → Task 8/9/11 分支
  - §8 测试策略（TDD + interface + fake） → 每个 Task 的 Step 1-2 + Task 6 fake
  - §9 交付节奏 → 里程碑 1/2/3
  - §10 风险（Unicode/长路径/tar 中断/脏数据） → Task 11 中 `os.RemoveAll(sessionDir)` 清理、Task 13 两阶段长度查询

- [x] **Placeholder 扫描**：无 TBD/TODO；每个 Step 都带完整代码或精确命令

- [x] **类型一致性**：`PullPNG`/`PullFile`/`PullAuto`、`FileResult`/`AutoResult`/`PNGResult`、`Options`/`Request`/`Response`、`Entry`/`ErrNoFiles`/`ErrUnsupported`、`PackTar`/`UnpackTar`/`SanitizeBasename`/`ResolveName`/`ErrTooLarge` 在所有 Task 中一致
