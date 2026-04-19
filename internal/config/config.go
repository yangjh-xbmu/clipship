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

type Config struct {
	DefaultHost string          `toml:"default_host"`
	Hosts       map[string]Host `toml:"hosts"`
}

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
		h.Filename = "clip_{ts}.png"
	}
	if h.Port == 0 {
		h.Port = 22
	}
	return name, h, nil
}

const sample = `# clipship config
default_host = "example"

[hosts.example]
addr      = "example.local"     # hostname or IP
user      = "you"                # SSH user
port      = 22
identity  = "~/.ssh/id_ed25519"  # private key path
remote_dir = "/tmp/clipship"     # where PNGs land
filename   = "clip_{ts}.png"     # {ts} = yyyyMMdd_HHmmss
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
