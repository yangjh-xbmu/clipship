# clipship

Move clipboard images between your local desktop and an SSH-connected remote shell. Single Go binary, no daemons you don't start, works on Windows / macOS / Linux.

**Problem it solves.** You're running Claude Code / Codex / any terminal tool over SSH on a remote host. You screenshot something on your desktop. You want that image in front of the remote tool. Copy-pasting a PNG through a terminal doesn't work.

**Two workflows, one binary:**

```
┌────────────────────────────────────────────────────────────────┐
│ A) push: desktop → remote (SFTP)                               │
│   desktop:  clipship send       -> uploads PNG, returns path   │
│   paste path into remote REPL                                  │
├────────────────────────────────────────────────────────────────┤
│ B) pull: remote → desktop (daemon + SSH tunnel)                │
│   desktop:  clipship daemon     -> serves clipboard PNG on TCP │
│   remote:   clipship pull       -> fetches PNG through tunnel  │
│   best paired with a Claude Code skill (see below)             │
└────────────────────────────────────────────────────────────────┘
```

B is the mode the Claude Code `/clip` skill uses: your assistant asks for the current clipboard image, you don't leave the remote session.

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

## Workflow A — `send` (push desktop clipboard to remote via SFTP)

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

## Workflow B — `daemon` + `pull` (remote pulls desktop clipboard)

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

# on remote (each time you want the clipboard PNG):
clipship pull
# → /home/you/.clipship/inbox/clip_20260419_095500.png
```

**Important:** forward to `127.0.0.1:19983`, **not** `localhost:19983`. The daemon only binds IPv4 `127.0.0.1`; `localhost` on some systems resolves to IPv6 `::1` and you'll see empty responses with no error.

### Daemon protocol (intentionally tiny)

Client connects, server writes either raw PNG bytes then closes, or `ERR <message>\n` (the `ERR ` prefix can't collide with PNG magic `\x89PNG`). Client reads until EOF.

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

### Config for `daemon` / `pull`

Optional — zero config runs on sensible defaults.

```toml
[daemon]
listen = "127.0.0.1:19983"

[pull]
connect   = "127.0.0.1:19983"
local_dir = "~/.clipship/inbox"
filename  = "clip_{ts}.png"
```

---

## Claude Code integration — the `/clip` skill

The recommended way to use Workflow B from Claude Code: define a skill so the assistant can fetch your current screenshot on demand, no tab switching.

### Prerequisites on the user's side
- `clipship` binary installed on both desktop and the remote host (where Claude runs)
- `clipship daemon` running on the desktop in the interactive session (see auto-start recipe above)
- SSH alias reachable from the remote to the desktop (e.g. over Tailscale), stored in `~/.clipship/workstation` (single line with the SSH alias, default `win` if file missing)
- SSH forward established ephemerally by the skill itself (skill reuses an existing one if present)

### Skill specification (drop-in for Claude Code)

Create `~/.claude/skills/clip/SKILL.md` with this content. LLMs reading this README can reproduce the skill verbatim.

```markdown
---
name: clip
description: "Pull the current desktop clipboard PNG from an SSH-connected workstation (via clipship daemon + SSH tunnel) and Read it. Trigger: user says 'see this screenshot', 'clipboard image', 'clip', '/clip', 'paste screenshot', or asks about a just-taken screenshot without giving a path."
user-invocable: true
allowed-tools: Bash, Read
effort: low
---

# /clip — pull workstation clipboard PNG via SSH tunnel

## Architecture

The workstation (SSH alias in `~/.clipship/workstation`, default `win`) runs `clipship daemon` in its interactive desktop session, listening on `127.0.0.1:19983`. Only the interactive session has clipboard access on Windows.

This host (where Claude runs) forwards to the daemon via `ssh -L 19983:127.0.0.1:19983 -N -f <alias>` and pulls with `clipship pull`.

## Steps

### 1. Read config
```bash
HOST="$(cat ~/.clipship/workstation 2>/dev/null || echo win)"
INBOX="$HOME/.clipship/inbox"
mkdir -p "$INBOX"
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
OUT="$(clipship pull 2>/tmp/clipship-err-$$)"
RC=$?
```

### 4. Branch
```bash
if [ $RC -ne 0 ] || [ ! -s "$OUT" ]; then
  ERR="$(cat /tmp/clipship-err-$$ 2>/dev/null)"
  rm -f /tmp/clipship-err-$$ /tmp/clipship-tun-$$
  # Map ERR to an actionable hint (see failure table below)
else
  rm -f /tmp/clipship-err-$$ /tmp/clipship-tun-$$
  # Use the Read tool on $OUT to load the image, then return to the user's question
fi
```

### Failure → user hint

| ERR contains                      | Hint                                                      |
|-----------------------------------|-----------------------------------------------------------|
| `clipboard has no image`          | Take a screenshot (Win+Shift+S / Cmd+Ctrl+Shift+4), retry |
| `empty response from daemon`      | Check that the forward is on 127.0.0.1, not localhost     |
| `dial 127.0.0.1:19983 ... refused`| Start the daemon on the workstation                       |
| `Connection refused` / `timeout`  | Check the SSH alias and reachability                      |

Do not retry automatically, do not try alternate hosts, do not start the daemon remotely.

## Do not
- Start the workstation daemon from this skill
- Modify SSH config
- Ship the image back to the user
- Clean up the inbox (leave that to cron)
- Tear down the tunnel (reuse it; it dies with the SSH connection)
```

You're done. `/clip` is now a first-class skill that pulls your live clipboard into the model's context in a round trip.

---

## Commands summary

| Command                    | Purpose                                         |
|----------------------------|-------------------------------------------------|
| `clipship send [host]`     | Upload desktop clipboard PNG to host via SFTP   |
| `clipship daemon`          | Serve desktop clipboard PNG on localhost:19983  |
| `clipship pull`            | Fetch PNG from a daemon (via SSH forward)       |
| `clipship init`            | Write a sample config file                      |
| `clipship doctor [host]`   | Health check for `send`                         |
| `clipship dump-png`        | Write current clipboard PNG to stdout (one-shot, non-interactive SSH sessions can't use this on Windows) |
| `clipship version`         | Print version                                   |

## Why not a shell alias around `scp`?

Reading a PNG out of the OS clipboard portably (Windows `CF_DIB`, macOS `NSPasteboard`, Linux X11/Wayland) is the annoying part — plus the Windows SSH session-isolation quirk (non-interactive sessions see an empty clipboard) means a naive `ssh workstation 'screenshot-to-stdout'` silently fails. `clipship` handles all of it in one binary.

## License

MIT. See [LICENSE](./LICENSE).
