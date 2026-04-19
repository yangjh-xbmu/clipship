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
