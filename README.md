# clipship

Move clipboard content between your local desktop and an SSH-connected remote shell. Single Go binary, no daemons you don't start, works on Windows / macOS / Linux.

**Problem it solves.** You're running Claude Code / Codex / any terminal tool over SSH on a remote host. You screenshot or copy files on your desktop. You want them in front of the remote tool. Copy-pasting binary content through a terminal doesn't work.

**v0.4 highlight — file clipboard.** In addition to PNGs, clipship now streams whatever files you've copied to the OS clipboard (Explorer "Copy" on Windows). A single `clipship pull-auto` from the remote grabs either the current screenshot or the copied files, whichever exists.

**Three workflows, one binary:**

```
┌────────────────────────────────────────────────────────────────┐
│ A) push PNG: desktop → remote (SFTP)                           │
│   desktop:  clipship send       -> uploads PNG, returns path   │
│   paste path into remote REPL                                  │
├────────────────────────────────────────────────────────────────┤
│ B) pull PNG: remote → desktop (daemon + SSH tunnel)            │
│   desktop:  clipship daemon     -> serves clipboard on TCP     │
│   remote:   clipship pull       -> fetches PNG (JSON stdout)   │
├────────────────────────────────────────────────────────────────┤
│ C) pull files: remote → desktop (daemon + SSH tunnel) [v0.4]   │
│   desktop:  clipship daemon                                    │
│   remote:   clipship pull-file  -> fetches copied files        │
│   remote:   clipship pull-auto  -> PNG or files, whichever     │
└────────────────────────────────────────────────────────────────┘
```

B+C power the Claude Code `/clip` skill: your assistant asks for the current clipboard content, you don't leave the remote session.

---

## Install

### From source

```bash
go install github.com/yangjh-xbmu/clipship/cmd/clipship@latest
```

### Prebuilt binary

Grab one from [Releases](https://github.com/yangjh-xbmu/clipship/releases) and drop it on `PATH`.

Supported targets: `darwin/amd64`, `darwin/arm64`, `windows/amd64`. Linux needs cgo + libx11-dev (build from source).

---

## Workflow A — `send` (push desktop clipboard PNG to remote via SFTP)

```bash
clipship init                # writes sample config
clipship doctor              # verify SSH + remote dir + clipboard
clipship send                # reads clipboard PNG, uploads, returns path
```

`clipship send` copies the resulting remote path back into your clipboard. Paste it into the remote REPL, hit enter.

### Config for `send`

Location:

| OS      | Path                                                   |
|---------|--------------------------------------------------------|
| Windows | `%AppData%\clipship\config.toml`                       |
| macOS   | `~/Library/Application Support/clipship/config.toml`   |
| Linux   | `~/.config/clipship/config.toml`                       |

```toml
default_host = "workstation"

[hosts.workstation]
addr       = "workstation.example.ts.net"
user       = "you"
port       = 22
identity   = "~/.ssh/your_ssh_key"
remote_dir = "/Users/you/inbox"
filename   = "clip_{ts}.png"             # {ts}, {host}
```

Multiple `[hosts.<name>]` blocks; pick with `clipship send <name>`.

### Auth chain
1. `SSH_AUTH_SOCK` (ssh-agent), if set
2. `identity` key in config

Host keys verified against `~/.ssh/known_hosts` when present.

---

## Workflows B & C — `daemon` + `pull*` (remote pulls desktop clipboard)

Run `clipship daemon` **on the desktop** — inside your interactive session (e.g. launched from your terminal, or auto-spawned by your terminal emulator). The daemon must live in the interactive desktop session because only that session has real clipboard access on Windows. Services-session or non-interactive SSH sessions see an empty clipboard.

```bash
# on desktop (keep running):
clipship daemon
# → clipship daemon listening on 127.0.0.1:19983
```

From the remote host, build an SSH forward to the daemon's port, then pull:

```bash
# on remote (one-time setup in a separate shell):
ssh -L 19983:127.0.0.1:19983 -N -f desktop

# on remote — fetch PNG (Workflow B):
clipship pull
# → {"kind":"png","path":"/home/you/.clipship/inbox/clip_20260419_095500.png","bytes":45231}

# on remote — fetch files (Workflow C):
clipship pull-file
# → {"kind":"file","session_dir":"/home/you/.clipship/files/20260419_095510","files":["…/a.txt","…/b.pdf"],"bytes":102400}

# on remote — let the daemon decide (Workflow C):
clipship pull-auto
# → {"kind":"png",...}  or  {"kind":"file",...}
```

**Important:** forward to `127.0.0.1:19983`, **not** `localhost:19983`. The daemon only binds IPv4 `127.0.0.1`; `localhost` on some systems resolves to IPv6 `::1` and you'll see empty responses with no error.

### Machine-readable stdout

Every `pull*` subcommand prints a single JSON line on stdout (newline-terminated), with raw bytes written to disk — never to stdout. This makes the skill layer robust: the shell does no file-type sniffing.

- PNG: `{"kind":"png","path":"...","bytes":N}`
- Single file: `{"kind":"file","session_dir":"…/<ts>","files":["…/name"],"bytes":N}`
- Multiple files / directory: same shape, `files` has every leaf under `session_dir`
- `pull-auto` wraps either of the above

File basenames are sanitized (`:*?"<>|` → `_`, trailing space/dot stripped); collisions inside a session get `(1)`, `(2)`… suffixes.

### Size limit & `--force`

The daemon refuses payloads over `max_bytes` (default **500 MiB**) with `ERR too large: N > max`. `clipship pull-file --force` / `pull-auto --force` bypasses the check for one request. Raise the ceiling permanently in config (see below).

### Wire protocol (v0.4)

Client sends a single line: `GET <kind> [force]\n` where `<kind>` ∈ `{png, file, auto}`. Daemon replies with one header line then a body:

| Header line                                | Body                                             |
|--------------------------------------------|--------------------------------------------------|
| `TYPE png SIZE <n>\n`                      | `<n>` bytes of PNG                               |
| `TYPE file SIZE <n> NAME <url-escaped>\n`  | `<n>` bytes of the single file                   |
| `TYPE tar SIZE <n>\n`                      | `<n>` bytes of an uncompressed tar               |
| `ERR <message>\n`                          | (no body; connection closed)                     |

Tar archives use POSIX `ustar` with flat entries (basenames only, no path components). Name uses `url.PathEscape` so spaces/Unicode survive.

### Auto-starting the daemon on Windows (WezTerm)

If you use WezTerm, `wezterm.lua`:

```lua
if wezterm.target_triple:find('windows') then
  wezterm.on('gui-startup', function()
    local exe = (os.getenv('USERPROFILE') or '') .. '\\bin\\clipship.exe'
    wezterm.background_child_process({
      'powershell', '-NoProfile', '-WindowStyle', 'Hidden', '-Command',
      "if (-not (Get-NetTCPConnection -LocalPort 19983 -State Listen -ErrorAction SilentlyContinue)) { "
        .. "Start-Process -FilePath '" .. exe .. "' -ArgumentList 'daemon' -WindowStyle Hidden "
        .. "}"
    })
  end)
end
```

Daemon then lives as long as WezTerm is open, in the correct session, no visible window.

### Config for `daemon` / `pull*`

Optional — zero config runs on sensible defaults.

```toml
[daemon]
listen    = "127.0.0.1:19983"
max_bytes = 524288000                    # 500 MiB; 0 disables the check

[pull]
connect    = "127.0.0.1:19983"
local_dir  = "~/.clipship/inbox"         # PNGs land here
filename   = "clip_{ts}.png"
files_dir  = "~/.clipship/files"         # file/tar sessions land here as <ts>/
```

---

## Claude Code integration — the `/clip` skill

The recommended way to use Workflows B+C from Claude Code: define a skill so the assistant can fetch your current clipboard content (PNG **or** files) on demand, no tab switching.

### Prerequisites on the user's side
- `clipship` binary installed on both desktop and the remote host (where Claude runs)
- `clipship daemon` running on the desktop in the interactive session (see auto-start recipe above)
- SSH alias reachable from the remote to the desktop (e.g. over Tailscale), stored in `~/.clipship/workstation` (single line with the SSH alias, default `win` if file missing)
- SSH forward established ephemerally by the skill itself (skill reuses an existing one if present)

### Skill specification (drop-in for Claude Code)

Create `~/.claude/skills/clip/SKILL.md` with this content. LLMs reading this README can reproduce the skill verbatim.

````markdown
---
name: clip
description: "Pull whatever is on the workstation clipboard — a screenshot or a set of copied files — via clipship daemon + SSH tunnel, then Read the result. Trigger: user says 'see this screenshot', 'clipboard image', 'clip', '/clip', 'paste screenshot', 'the files I just copied', or asks about just-copied content without giving a path."
user-invocable: true
allowed-tools: Bash, Read
effort: low
---

# /clip — pull workstation clipboard (PNG or files) via SSH tunnel

## Architecture

The workstation (SSH alias in `~/.clipship/workstation`, default `win`) runs `clipship daemon` in its interactive desktop session, listening on `127.0.0.1:19983`. Only the interactive session has clipboard access on Windows.

This host (where Claude runs) forwards to the daemon via `ssh -L 19983:127.0.0.1:19983 -N -f <alias>` and pulls with `clipship pull-auto`, which returns either a PNG or a set of files depending on what the user last copied.

## Steps

### 1. Read config
```bash
HOST="$(cat ~/.clipship/workstation 2>/dev/null || echo win)"
```

### 2. Ensure the tunnel exists (reuse if possible)
```bash
if ! lsof -iTCP:19983 -sTCP:LISTEN >/dev/null 2>&1; then
  ssh -L 19983:127.0.0.1:19983 -N -f "$HOST" 2>/tmp/clipship-tun-$$
  sleep 0.3
fi
```
Always use `127.0.0.1`, never `localhost` — the daemon binds IPv4 only.

### 3. Pull
```bash
JSON="$(clipship pull-auto 2>/tmp/clipship-err-$$)"
RC=$?
```

### 4. Branch on `kind`
The stdout is one JSON line; parse it with `jq` (or `python -c`):

```bash
if [ $RC -ne 0 ]; then
  ERR="$(cat /tmp/clipship-err-$$)"
  # Map ERR to an actionable hint (see failure table below)
else
  KIND=$(echo "$JSON" | jq -r .kind)
  case "$KIND" in
    png)
      PATH_=$(echo "$JSON" | jq -r .path)
      # Use the Read tool on $PATH_ to load the image
      ;;
    file)
      # Iterate .files[] and Read each (if text) or describe to the user
      echo "$JSON" | jq -r '.files[]'
      ;;
  esac
fi
rm -f /tmp/clipship-err-$$ /tmp/clipship-tun-$$
```

If the user explicitly wants files, call `clipship pull-file` instead of `pull-auto`.

### Failure → user hint

| ERR contains                         | Hint                                                      |
|--------------------------------------|-----------------------------------------------------------|
| `clipboard has no image`             | Take a screenshot (Win+Shift+S / Cmd+Ctrl+Shift+4), retry |
| `clipboard has no files`             | Copy files in Explorer / Finder, retry                    |
| `too large`                          | Re-run with `--force`, or raise `max_bytes` in config     |
| `empty response from daemon`         | Check that the forward is on 127.0.0.1, not localhost     |
| `dial 127.0.0.1:19983 ... refused`   | Start the daemon on the workstation                       |
| `Connection refused` / `timeout`     | Check the SSH alias and reachability                      |

Do not retry automatically, do not try alternate hosts, do not start the daemon remotely.

## Do not
- Start the workstation daemon from this skill
- Modify SSH config
- Ship the image/files back to the user
- Clean up the inbox / files dir (leave that to cron)
- Tear down the tunnel (reuse it; it dies with the SSH connection)
````

You're done. `/clip` is now a first-class skill that pulls your live clipboard — screenshot or files — into the model's context in a round trip.

---

## Commands summary

| Command                          | Purpose                                                   |
|----------------------------------|-----------------------------------------------------------|
| `clipship send [host]`           | Upload desktop clipboard PNG to host via SFTP             |
| `clipship daemon`                | Serve desktop clipboard (PNG + files) on localhost:19983  |
| `clipship pull`                  | Fetch PNG from a daemon (JSON stdout)                     |
| `clipship pull-file [--force]`   | Fetch copied files from a daemon (JSON stdout)            |
| `clipship pull-auto [--force]`   | Fetch PNG or files, whichever the clipboard has           |
| `clipship init`                  | Write a sample config file                                |
| `clipship doctor [host]`         | Health check for `send`                                   |
| `clipship dump-png`              | Write current clipboard PNG to stdout (one-shot)          |
| `clipship version`               | Print version                                             |

## Migrating from v0.3

- **`clipship pull` stdout changed.** Old behavior printed the saved PNG path as bare text; v0.4 prints a JSON line (`{"kind":"png","path":"…","bytes":N}`). Shell scripts that consumed the path need `jq -r .path` (or similar).
- **Protocol changed.** v0.4 clients speak `GET png|file|auto`; v0.3 daemons return raw PNG without a header. Upgrade both ends.
- **New config keys.** `[daemon].max_bytes` and `[pull].files_dir`; both optional.
- **New subcommands.** `pull-file`, `pull-auto`.
- **`/clip` skill updated.** Use `pull-auto` with `jq` to branch on `kind`.

## Why not a shell alias around `scp`?

Reading PNGs and file lists out of the OS clipboard portably (Windows `CF_DIB` + `CF_HDROP`, macOS `NSPasteboard`, Linux X11/Wayland) is the annoying part — plus the Windows SSH session-isolation quirk (non-interactive sessions see an empty clipboard) means a naive `ssh workstation 'screenshot-to-stdout'` silently fails. `clipship` handles all of it in one binary.

## License

MIT. See [LICENSE](./LICENSE).
