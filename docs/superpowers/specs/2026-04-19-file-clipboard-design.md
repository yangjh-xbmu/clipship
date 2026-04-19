---
title: clipship 文件剪贴板 feature 设计
date: 2026-04-19
status: approved
owner: yangjh-xbmu
version_target: clipship v0.4.0
---

# clipship 文件剪贴板 feature 设计

## 1. 背景

clipship v0.3 解决了远程 SSH 开发场景下「桌面剪贴板图片 → 远程 shell」的问题。`/clip` skill 已经把 Workflow B（daemon + pull + SSH tunnel）跑通，是当前最高频的入口。

但剪贴板里除了图片还有别的内容：用户在文件管理器（Windows Explorer / macOS Finder）里 Ctrl/Cmd+C 选中的**文件或目录**。这类内容在剪贴板上不是文件字节，而是「路径列表」（Windows `CF_HDROP`、macOS `public.file-url`、Linux `text/uri-list`）。当前 clipship 完全不处理，Claude Code 远程会话无法感知。

本 spec 把 clipship 扩展到支持「文件剪贴板」，目标是让 `/clip file` 这类指令能把桌面选中的任意文件（PDF、源码、视频、整个目录）拉回 Claude 运行的远程主机，完全沿用现有的 daemon + SSH tunnel 机制。

## 2. 目标

- **Pull 方向**（远程 SSH 开发场景）：桌面剪贴板包含文件/目录时，`/clip` skill 能一键拉到远程 Claude 会话
- **单文件** 和 **多文件/含目录** 都支持；数量不限
- **协议升级**：引入命令前缀 + TYPE 响应头，支持三类请求 `GET png` / `GET file` / `GET auto`
- **单一 skill**：合并为 `/clip`，按参数（`/clip` / `/clip png` / `/clip file`）分支；默认走 `pull-auto`，daemon 自动识别内容类型
- **Windows-first**：第一版只在 Windows 上 ship 真实实现（workstation 的目标平台），macOS/Linux 返回 `unsupported`
- **TDD 全覆盖**：剪贴板读取用 interface 抽象 + fake 实现，协议/tar/sanitize 等逻辑走表驱动测试

## 3. 非目标（本版不做）

- **Push 方向**：远程 → 桌面推文件。只做 Pull
- **非文件内容类型**：URL（http(s)://…）、纯文本落盘成 `.txt`、其它剪贴板格式，都不命中
- **macOS / Linux 真实实现**：本版 stub；下一版补
- **`dump-file` / `dump-clip` 一次性子命令**：不做，所有文件场景必须经 daemon
- **大文件流式限流、优先级**：本版只做「软限制 + --force 绕过」，不做分块/队列
- **向后兼容旧裸 PNG 协议**：v0.3 的 daemon/client 互不兼容 v0.4，版本一起升级

## 4. 架构

```
┌─────────────────────────┐    SSH -L 19983       ┌────────────────────────────────┐
│ Workstation (Windows)   │ <═══════════════════► │ Remote dev host (Claude Code)  │
│                         │                       │                                │
│  clipship daemon        │                       │  clipship pull-auto/pull-file/ │
│   ├─ clipboard/files    │                       │            pull                │
│   │  (CF_HDROP→paths)   │                       │   ├─ proto.WriteRequest        │
│   ├─ pack (tar encoder) │                       │   ├─ proto.ReadHeader          │
│   └─ proto writer       │                       │   ├─ pack.UnpackTar / 单文件落盘 │
│                         │                       │   ├─ sanitize + session_dir    │
└─────────────────────────┘                       │   └─ JSON stdout               │
                                                  │                                │
                                                  │  /clip skill (Bash wrapper)    │
                                                  │   └─ tunnel + dispatch + Read  │
                                                  └────────────────────────────────┘
```

### 职责边界

- **daemon**：读本机剪贴板 + 按协议应答。不知道调用方是谁、tunnel 在哪里、数据往哪去
- **pull 客户端（`clipship pull-*`）**：说协议、落盘、输出 JSON。不碰 SSH tunnel
- **`/clip` skill**：管 SSH tunnel 生命周期、按参数分发子命令、解析 JSON、调用 Read 工具

### 版本

- clipship v0.4.0：破坏性协议升级。daemon 和所有 client 必须同版本
- 迁移：README 声明 v0.4 不兼容 v0.3 daemon；用户需同时升级两端

## 5. 组件与接口

### 5.1 新增包 `internal/clipboard/files`

```go
package files

type Entry struct {
    Path  string // 绝对路径
    IsDir bool
}

// ReadFiles 返回当前剪贴板上的文件/目录条目列表。
func ReadFiles() ([]Entry, error)

var ErrNoFiles     = errors.New("clipboard has no files")
var ErrUnsupported = errors.New("file clipboard unsupported on this os")
```

实现分平台（和主包 `internal/clipboard/clipboard.go` 的 build tag 风格一致）：

| 文件                   | 内容                                                       |
|------------------------|------------------------------------------------------------|
| `files_windows.go`     | `syscall` + `user32/OpenClipboard` + `CF_HDROP` 解析路径列表 |
| `files_darwin.go`      | 直接返回 `ErrUnsupported`（stub）                            |
| `files_linux.go`       | 直接返回 `ErrUnsupported`（stub）                            |
| `files_fake.go`        | build tag `clipship_fake`；读 `CLIPSHIP_FAKE_FILES` 环境变量 |

Windows 实现不依赖 cgo，纯 syscall。依赖 `golang.org/x/sys/windows`。

### 5.2 新增包 `internal/proto`

daemon 和 client 共用协议编解码。

```go
package proto

type Request struct {
    Kind  string // "png" | "file" | "auto"
    Force bool   // 对 file/auto 有效，绕过 max_bytes
}

type Response struct {
    Kind string // "png" | "file" | "tar" | "err"
    Name string // 仅 file
    Size int64  // file/tar 必填；png 可为 0（= 到 EOF）
    Err  string // 仅 err
}

func WriteRequest(w io.Writer, req Request) error
func ReadRequest(r *bufio.Reader) (Request, error)
func WriteHeader(w io.Writer, resp Response) error
func ReadHeader(r *bufio.Reader) (Response, error) // Body 留给调用方从 r 继续读
```

线上格式（逐行文本头 + 原始字节 body）：

请求（client → daemon）：
```
GET png\n
GET file\n            # 等价 force=false
GET file force\n
GET auto\n
GET auto force\n
```

响应（daemon → client）：
```
TYPE png\n<png bytes 到 EOF>
TYPE file <name> <size>\n<size 字节>
TYPE tar <size>\n<size 字节 tar 流>
ERR <msg>\n
```

约束：
- `<name>` 使用 URL-percent-encoded UTF-8（避免空格/特殊字符破坏分词）
- `<size>` 是十进制字节数
- `ERR ` 前缀不会和 `TYPE ` 混淆

### 5.3 新增包 `internal/pack`

```go
package pack

// PackTar 按 Entry 列表打包，返回可读流 + 总未压缩字节数。
// 递归展开 IsDir=true 的 Entry。
// 遇到总字节数超过 maxBytes 且 force=false 时返回 ErrTooLarge。
func PackTar(entries []files.Entry, maxBytes int64, force bool) (io.ReadCloser, int64, error)

// UnpackTar 从 r 解包到 destDir，返回解包出的所有文件绝对路径。
// sanitize 用于把 Windows 文件名合法但 Linux 不合法的字符替换为下划线。
func UnpackTar(r io.Reader, destDir string, sanitize func(string) string) ([]string, error)

var ErrTooLarge = errors.New("too large")
```

tar 选用 Go 标准库 `archive/tar`。

### 5.4 `internal/clipboard`（主包）

已有 `ReadPNG` / `WriteText` 保留。新增 size 输出：

```go
func ReadPNGSize() (int64, error) // 用于 TYPE png <size>
```

或保持 `TYPE png\n` 不带 size，客户端读到 EOF（和现状一致，省事）。**采用后者**：`TYPE png\n<bytes 到 EOF>`，size 省略。

### 5.5 改造 `internal/server`（daemon）

```go
func Run(addr string, opts Options) error

type Options struct {
    MaxBytes int64 // 软限制，0 = 使用默认 500MB
    ClipboardImage   func() ([]byte, error)                 // 默认 = clipboard.ReadPNG
    ClipboardFiles   func() ([]files.Entry, error)          // 默认 = files.ReadFiles
}
```

handler 分发：

```
handle(conn):
  req = proto.ReadRequest(conn)
  switch req.Kind:
    case "png":  handlePNG(conn)
    case "file": handleFile(conn, req.Force)
    case "auto": handleAuto(conn, req.Force)
```

`handleAuto`：先调 `ClipboardFiles()`；非 `ErrNoFiles` 的错误直接 `ERR`；`ErrNoFiles` 则 fallback 到 `handlePNG`。

`handleFile`：
- 单文件（1 个 Entry 且 `!IsDir`）→ `TYPE file <name> <size>\n<bytes>`
- 多文件 / 含目录 → `pack.PackTar(entries, maxBytes, force)` → `TYPE tar <size>\n<tar 流>`
- `ErrNoFiles` → `ERR clipboard has no files\n`
- `ErrTooLarge` → `ERR too large: <actual> > <limit>, retry with --force\n`
- `ErrUnsupported` → `ERR file clipboard unsupported on <os>\n`

### 5.6 改造 `internal/client`

拆为三个公开函数：

```go
func PullPNG(addr, localDir, filenameTmpl string) (path string, bytes int64, err error)

type FileResult struct {
    SessionDir string
    Files      []string
    Bytes      int64
}
func PullFile(addr, destParentDir string, force bool) (FileResult, error)

type AutoResult struct {
    Kind       string // "png" | "file"
    PNG        *struct{ Path string; Bytes int64 }
    File       *FileResult
}
func PullAuto(addr, pngLocalDir, pngFilenameTmpl, destParentDir string, force bool) (AutoResult, error)
```

落盘规则：
- **PNG**：`<localDir>/<filename>`（同旧逻辑）
- **单文件**：`<destParentDir>/<ts>/<sanitized_name>`（子目录确保一致性，便于之后 skill 遍历）
- **多文件/含目录（tar）**：`<destParentDir>/<ts>/` 下解包出完整结构
- **sanitize 规则**（仅作用于每一级 path 的 basename，不动分隔符本身）：把 Windows 合法但 Linux 不合法的字符（`:` `*` `?` `"` `<` `>` `|` 以及 trailing space、trailing dot）替换为 `_`；路径分隔符 `/` 和 `\` 在解包前已由 `archive/tar` 和 `filepath.FromSlash` 规范化，不在本规则范围内。session 内 basename 撞名 append `(1) (2) ...` 后缀

### 5.7 改造 `cmd/clipship/main.go`

新增子命令：
- `clipship pull-file [--force]`
- `clipship pull-auto [--force]`
- `clipship pull` 保留（内部改调 `PullPNG`，协议升级）

所有 `pull-*` 子命令 stdout 统一输出 JSON（一行）：

```json
{"kind":"png","path":"/home/you/.clipship/inbox/clip_20260419_153012.png","bytes":45678}
```
```json
{"kind":"file","session_dir":"/home/you/.clipship/inbox/files/20260419_153012","files":["/home/you/.clipship/inbox/files/20260419_153012/report.pdf"],"bytes":234567}
```
```json
{"kind":"file","session_dir":"/home/you/.clipship/inbox/files/20260419_153012","files":["/home/you/.clipship/inbox/files/20260419_153012/src/a.go","/home/you/.clipship/inbox/files/20260419_153012/src/b.go"],"bytes":1234}
```

失败：非零 exit code + stderr 打印 `error: ...`，stdout 不输出 JSON。

### 5.8 `/clip` skill 改造

单 skill，按参数分发：

```
/clip          → clipship pull-auto
/clip png      → clipship pull
/clip file     → clipship pull-file
```

skill 解析 stdout JSON，按 `kind` 分支：
- `kind=png`：对 `path` 调 Read
- `kind=file`：遍历 `files[]`；若 `len(files)` 大于 5 或 `bytes > 10MB`，先向用户确认再 Read

SSH tunnel 管理逻辑复用现状（探测 19983 → `ssh -L`，死了重建）。

### 5.9 配置新增字段

`[pull]` 段：

```toml
[pull]
connect   = "127.0.0.1:19983"
local_dir = "~/.clipship/inbox"           # PNG 保存目录（沿用）
files_dir = "~/.clipship/inbox/files"     # 文件 session 目录的父目录（新增）
max_bytes = 524288000                     # 500 MB 软限制（新增）
filename  = "clip_{ts}.png"               # PNG 文件名模板（沿用）
```

`[daemon]` 段：

```toml
[daemon]
listen    = "127.0.0.1:19983"
max_bytes = 524288000                     # 可选；优先级高于 pull 侧请求
```

daemon 侧 `max_bytes` 是权威上限（防止 client 说谎）；client 的 `--force` 只在 daemon 允许的范围内生效。

## 6. 数据流（关键路径）

### 6.1 `/clip file` 成功路径（多文件）

```
user types "/clip file" in Claude Code
  ↓
skill: ensure SSH tunnel to workstation (127.0.0.1:19983 → workstation:19983)
  ↓
skill: exec `clipship pull-file`
  ↓
client: dial 127.0.0.1:19983
client: write "GET file\n"
  ↓
daemon: read request → handleFile(force=false)
daemon: clipboard/files.ReadFiles() → [foo.go, bar/ (dir)]
daemon: detect multi + has dir → pack.PackTar(...) → 2.3MB tar stream
daemon: write "TYPE tar 2345678\n" + tar bytes
daemon: close conn
  ↓
client: ReadHeader → {Kind:tar, Size:2345678}
client: mkdir ~/.clipship/inbox/files/20260419_153012/
client: pack.UnpackTar(conn, destDir, sanitize) → [foo.go, bar/baz.txt, ...]
client: stdout:
  {"kind":"file","session_dir":"/home/you/.clipship/inbox/files/20260419_153012",
   "files":[".../foo.go",".../bar/baz.txt"],"bytes":2345678}
  ↓
skill: parse JSON → loop files[] → Read each
```

### 6.2 `/clip` 空剪贴板

```
clipship pull-auto → GET auto
daemon handleAuto:
  ClipboardFiles() → ErrNoFiles
  → fallback to handlePNG
  clipboard.ReadPNG() → ErrNoImage
  → ERR clipboard has neither image nor files\n
client: stderr `error: daemon: clipboard has neither image nor files`, exit 1
skill: show user actionable hint
```

### 6.3 超限绕过

```
GET file → ERR too large: 1200000000 > 524288000, retry with --force
client: stderr + exit 1
skill: ask user「这批文件 1.2GB，确认拉取？」→ 确认后 exec `clipship pull-file --force`
client: GET file force → daemon pass PackTar with force=true → 正常流程
```

## 7. 错误处理

| 场景                           | daemon 响应                                              | client 处理                     | skill 给用户的提示                      |
|--------------------------------|----------------------------------------------------------|---------------------------------|----------------------------------------|
| 剪贴板无图无文件               | `ERR clipboard has neither image nor files\n`            | exit 1 + stderr                 | 「剪贴板为空，请复制图片或文件后重试」     |
| 剪贴板只有图（`/clip file` 时）| `ERR clipboard has no files\n`                           | exit 1 + stderr                 | 「剪贴板是图片，试试 /clip 或 /clip png」   |
| 只有文件（`/clip png` 时）     | `ERR clipboard has no image\n`                           | exit 1 + stderr                 | 「剪贴板是文件，试试 /clip 或 /clip file」  |
| 超出 max_bytes                 | `ERR too large: <actual> > <limit>, retry with --force\n`| exit 1 + 结构化 stderr          | 展示大小 + 询问是否强拉                  |
| OS 不支持（macOS/Linux 首版）  | `ERR file clipboard unsupported on darwin\n`             | exit 1 + stderr                 | 「file clipboard 首版仅支持 Windows」    |
| SSH tunnel 断                  | client dial 错误                                         | exit 1                          | 沿用现有 /clip 提示表                    |
| daemon 未启动                  | client dial refused                                      | exit 1                          | 「请在 workstation 启动 clipship daemon」  |
| tar 流中途坏                   | daemon 已写部分 tar bytes                                | `UnpackTar` 报错 + 清理 session 目录 | 「传输中断，已清理不完整文件，请重试」   |
| 撞名                           | 无（打包前已 sanitize）                                  | client 内部 append `(1)` 等       | —                                      |

**原则**：
- 所有错误必须带 actionable 提示，禁止吞错误
- client 写盘遇错时删除已写入的 session 目录（不留脏数据）
- daemon 的错误消息不泄漏路径细节到远程（只给错误类别）

## 8. 测试策略（TDD 全覆盖）

### 8.1 测试金字塔

| 层              | 覆盖内容                                                                 | 框架                    |
|-----------------|-------------------------------------------------------------------------|-------------------------|
| **单元测试**    | `proto` 编解码、`pack` tar 打包解包、sanitize、撞名解决、JSON 输出      | `go test -race`，表驱动 |
| **集成测试**    | daemon + client 真实 TCP 走全协议（`GET png/file/auto`、所有错误分支） | `go test -race`，httptest 风格启真 TCP |
| **平台测试**    | Windows `files_windows.go` CF_HDROP 解析（mock clipboard API）          | `go test` with build tag `windows`    |
| **端到端烟囱**  | 手写脚本：本机跑 daemon，模拟复制文件，真实 SSH tunnel + pull          | `scripts/e2e_windows.ps1`（可选）     |

### 8.2 Interface 抽象（支撑 TDD）

daemon `server.Options` 注入 `ClipboardImage` / `ClipboardFiles`；测试里替换为 fake 实现（返回预设数据/错误）。

`files` 包通过 build tag `clipship_fake` 启用 `files_fake.go`，读 `CLIPSHIP_FAKE_FILES`（冒号分隔路径列表）供手动端到端调试。

### 8.3 TDD 顺序（Outside-In）

每个任务先写测试再写实现。顺序：

1. `proto` 包（最底层，无依赖）
2. `pack` 包（依赖 `files.Entry` 类型定义）
3. `files` 包接口 + fake 实现
4. `server` 升级 + 单元/集成测试
5. `client` 三入口 + 集成测试
6. `files_windows.go` 真实实现 + Windows 平台测试
7. `cmd/clipship` 子命令 + JSON 输出 smoke test
8. `/clip` skill 更新

### 8.4 覆盖率门槛

- 单元测试 80%+（`proto`/`pack`/`client` 落盘逻辑）
- 集成测试覆盖所有协议分支（3 种 GET × 成功/ErrNoFiles/ErrTooLarge/ErrUnsupported）
- Windows 真实实现覆盖：单文件、多文件、含目录、UNC 路径、中文路径

## 9. 交付节奏（Windows-First 切片）

**里程碑 1（内部 PR，不对外发布）：骨架 + 协议**
- `proto` 包（编解码 + 全量单测）
- `pack` 包（tar 打包解包 + sanitize + 全量单测）
- `files` 包接口 + 全平台 stub（`ErrUnsupported`）+ fake 实现
- `server` 升级 `handlePNG` 跑通新协议 `TYPE png\n`
- `client.PullPNG` 升级跑通新协议，`clipship pull` 回归测试通过
- 到此 PNG 走新协议跑通；file 相关子命令尚未注册到 CLI，用户无法触达

**里程碑 2（内部 PR）：Windows 文件实现**
- `files_windows.go` 真实 CF_HDROP 实现 + 平台测试
- `server.handleFile` / `handleAuto` 完整逻辑
- `client.PullFile` / `PullAuto`
- `cmd/clipship` 新子命令 + JSON 输出
- Windows 端到端 smoke test 通过

**v0.4.0（对外发布）：skill 与文档**
- `/clip` skill 合并（png/file/auto 分支）
- README 更新：v0.4 破坏性变更说明、新 skill 用法、迁移指南
- SESSION_LOG 归档
- GitHub Release + 预编译 Windows 二进制

**v0.5（后续，不在本 spec 范围）**
- macOS `files_darwin.go`（cgo + NSPasteboard）
- Linux `files_linux.go`（X11 + Wayland）
- 若有需求：Push 方向 / 进度显示 / 分块传输

## 10. 风险与未决

| 风险                                   | 影响                    | 缓解                                                     |
|----------------------------------------|-------------------------|----------------------------------------------------------|
| Windows CF_HDROP 对 Unicode/长路径的处理 | Windows 实现踩坑        | 专项测试：UNC 路径、中文、长路径、network drive          |
| tar 流中途断，远程留半成品              | 脏数据                  | `UnpackTar` 失败时删除 session 目录                     |
| `/clip file` 被触发但剪贴板是图片       | 用户困惑                | daemon 返回明确 ERR；skill 建议换成 `/clip` 或 `/clip png` |
| max_bytes 配置漂移（daemon 和 client 不一致）| 用户困惑              | daemon 侧权威；错误信息含 daemon 实际上限               |
| 新协议破坏 v0.3 用户                    | 升级痛                  | README 明确破坏性；version 输出从 0.3.0 跳到 0.4.0        |

**未决（交给 writing-plans 或实现时决策）**：
- Windows CF_HDROP 是否需要手动 `OleInitialize`？调研后定
- URL-percent-encoding `<name>` 的具体字符集（保守起见 RFC 3986 unreserved）
- Skill 的「大文件/多文件二次确认」阈值默认（先写 5 files 或 10MB，可调）
