package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Host struct {
	RemoteDir string `toml:"remote_dir"`
	Filename  string `toml:"filename"`
	User      string `toml:"user"`
	Addr      string `toml:"addr"`
	Port      int    `toml:"port"`
	Identity  string `toml:"identity"`
}

type Daemon struct {
	Listen   string `toml:"listen"`
	MaxBytes int64  `toml:"max_bytes"`
}

type Pull struct {
	Connect  string `toml:"connect"`
	LocalDir string `toml:"local_dir"`
	Filename string `toml:"filename"`
	FilesDir string `toml:"files_dir"`
	MaxBytes int64  `toml:"max_bytes"`
}

type Config struct {
	DefaultHost string          `toml:"default_host"`
	Hosts       map[string]Host `toml:"hosts"`
	Daemon      Daemon          `toml:"daemon"`
	Pull        Pull            `toml:"pull"`
}

const (
	DefaultAddr     = "127.0.0.1:19983"
	DefaultLocalDir = "~/.clipship/inbox"
	DefaultFilename = "clip_{ts}.png"
	DefaultFilesDir = "~/.clipship/inbox/files"
	DefaultMaxBytes = int64(500 * 1024 * 1024) // 500 MB
)

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clipship", "config.toml"), nil
}

func Load() (*Config, string, error) {
	p, err := Path()
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, p, err
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, p, fmt.Errorf("parse %s: %w", p, err)
	}
	return &c, p, nil
}

// LoadOrEmpty returns a zero Config if the file is missing, so daemon/pull can
// run on defaults without requiring `clipship init` first.
func LoadOrEmpty() *Config {
	c, _, err := Load()
	if err != nil || c == nil {
		return &Config{}
	}
	return c
}

func Resolve(c *Config, name string) (string, Host, error) {
	if name == "" {
		name = c.DefaultHost
	}
	if name == "" {
		return "", Host{}, errors.New("no host given and default_host not set")
	}
	h, ok := c.Hosts[name]
	if !ok {
		return name, Host{}, fmt.Errorf("host %q not found in config", name)
	}
	if h.RemoteDir == "" {
		return name, h, fmt.Errorf("host %q: remote_dir is required", name)
	}
	if h.Filename == "" {
		h.Filename = DefaultFilename
	}
	if h.Port == 0 {
		h.Port = 22
	}
	return name, h, nil
}

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

const sample = `# clipship config
# Pick the blocks you need — nothing is required if you accept defaults.

# --- send: upload clipboard PNG to a remote host via SFTP -------------------
# default_host = "example"
#
# [hosts.example]
# addr      = "example.local"
# user      = "you"
# port      = 22
# identity  = "~/.ssh/your_ssh_key"
# remote_dir = "/tmp/clipship"
# filename   = "clip_{ts}.png"

# --- daemon: run on the LOCAL desktop machine (the one with the clipboard) --
# Serves PNG + file clipboard over a localhost TCP socket.
# Expose it to your remote dev host via ssh -R / RemoteForward.
[daemon]
listen    = "127.0.0.1:19983"
# max_bytes = 524288000      # 500 MB hard ceiling for file pulls

# --- pull: run on the REMOTE dev machine (the one where Claude Code runs) --
[pull]
connect   = "127.0.0.1:19983"
local_dir = "~/.clipship/inbox"                    # PNG output dir
files_dir = "~/.clipship/inbox/files"              # per-session file output dir
max_bytes = 524288000                              # soft limit; bypass with --force
filename  = "clip_{ts}.png"
`

func WriteSample() (string, bool, error) {
	p, err := Path()
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(p); err == nil {
		return p, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return p, false, err
	}
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		return p, false, err
	}
	return p, true, nil
}
