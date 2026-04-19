# clipship

Paste clipboard images into a remote shell over SSH. Single-file cross-platform CLI for the Claude Code / Codex CLI / any-remote-REPL workflow.

**Problem it solves.** You are running Claude Code (or any terminal tool that reads local file paths) on a remote host over SSH. You take a screenshot on your laptop. You want that image in front of the remote tool. `scp` is friction every time.

**What clipship does.** `clipship send` reads a PNG from your local clipboard, SFTPs it to a configured remote directory, then copies the resulting remote path back to your clipboard. Paste the path into the remote REPL, hit enter.

```
┌─ laptop ───────────┐         ┌─ remote host ──────────┐
│ screenshot -> clip │  SFTP   │ /remote/dir/clip_*.png │
│ clipship send ─────┼────────>│                        │
│ <- remote path     │         │ (Claude Code reads it) │
└────────────────────┘         └────────────────────────┘
```

## Install

### From source

```bash
go install github.com/yangjh-xbmu/clipship/cmd/clipship@latest
```

### Prebuilt binary

Grab one from [Releases](https://github.com/yangjh-xbmu/clipship/releases) and drop it on `PATH`.

## Quick start

```bash
clipship init                     # writes sample config
$EDITOR "$(clipship --help | tail -1 | awk '{print $NF}')"   # or open the path printed by `init`
clipship doctor                   # verify SSH + remote dir + clipboard
clipship send                     # first run: copy a screenshot, then run this
```

Paste the resulting path into Claude Code / Codex / any REPL. Done.

## Config

Location:

| OS      | Path                                                   |
|---------|--------------------------------------------------------|
| Windows | `%AppData%\clipship\config.toml`                       |
| macOS   | `~/Library/Application Support/clipship/config.toml`   |
| Linux   | `~/.config/clipship/config.toml`                       |

```toml
default_host = "yeah"

[hosts.yeah]
addr       = "yangjh-yeah"               # hostname or IP
user       = "yangjh"
port       = 22
identity   = "~/.ssh/id_ed25519"
remote_dir = "/Users/yangjh/Desktop/repos/foo/tmp/clipboard"
filename   = "clip_{ts}.png"             # {ts} = yyyyMMdd_HHmmss, {host} = host name
```

You can define multiple `[hosts.<name>]` blocks and pick with `clipship send <name>`.

## Commands

| Command                    | What it does                                    |
|----------------------------|-------------------------------------------------|
| `clipship send [host]`     | Upload clipboard PNG, return remote path        |
| `clipship init`            | Write a sample config file                      |
| `clipship doctor [host]`   | End-to-end health check                         |
| `clipship version`         | Print version                                   |

## Auth

clipship tries, in order:
1. `SSH_AUTH_SOCK` (ssh-agent), if set.
2. The private key file at `identity` in config.

Host keys are verified against `~/.ssh/known_hosts` when it exists; otherwise the connection proceeds without pinning (a warning-level tradeoff for first-use ergonomics).

## Bind to a hotkey

**WezTerm** (`~/.wezterm.lua`):

```lua
config.keys = {
  { key = 'V', mods = 'CTRL|SHIFT', action = wezterm.action.SpawnCommandInNewTab {
      args = { 'clipship', 'send' },
  }},
}
```

**Windows (AutoHotkey v2)**:

```ahk
^!v::Run('clipship send', , 'Hide')
```

**macOS (Raycast / Alfred / Shortcuts)**: bind a workflow to run `clipship send`.

## Why not `scp` in a shell alias?

Reading a PNG out of the OS clipboard portably (Windows CF_DIB, macOS NSPasteboard, Linux X11/Wayland) is the annoying part. clipship ships a single binary that does it on all three.

## License

MIT. See [LICENSE](./LICENSE).
