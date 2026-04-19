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
  clipship daemon               serve PNG / files on a local TCP socket (persistent)
  clipship pull                 fetch PNG from a daemon; stdout=JSON
  clipship pull-file [--force]  fetch files from a daemon; stdout=JSON
  clipship pull-auto [--force]  fetch whatever is on the clipboard; stdout=JSON

  clipship send [host]          upload clipboard PNG to [host] via SFTP
  clipship dump-png             write current clipboard PNG to stdout (one-shot)

  clipship init                 write a sample config file
  clipship doctor [host]        run SFTP health checks for the send workflow
  clipship version              print version

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
		User:     host.User,
		Addr:     host.Addr,
		Port:     host.Port,
		Identity: host.Identity,
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
		User:     host.User,
		Addr:     host.Addr,
		Port:     host.Port,
		Identity: host.Identity,
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

type fileJSON struct {
	Kind       string   `json:"kind"`
	SessionDir string   `json:"session_dir"`
	Files      []string `json:"files"`
	Bytes      int64    `json:"bytes"`
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
	return writeJSON(fileJSON{Kind: "file", SessionDir: res.SessionDir, Files: res.Files, Bytes: res.Bytes})
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
		return writeJSON(fileJSON{Kind: "file", SessionDir: res.File.SessionDir, Files: res.File.Files, Bytes: res.File.Bytes})
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
