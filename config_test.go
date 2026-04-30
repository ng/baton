package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Connection.ReversePort != 19222 {
		t.Errorf("expected reverse port 19222, got %d", cfg.Connection.ReversePort)
	}
	if cfg.Connection.ControlSocket != "/tmp/baton.sock" {
		t.Errorf("expected control socket /tmp/baton.sock, got %s", cfg.Connection.ControlSocket)
	}
	if cfg.Transfer.Inbox != "/workspaces/.inbox" {
		t.Errorf("expected inbox /workspaces/.inbox, got %s", cfg.Transfer.Inbox)
	}
	if cfg.Ports.ScanInterval.Duration != 3*time.Second {
		t.Errorf("expected scan interval 3s, got %s", cfg.Ports.ScanInterval.Duration)
	}
	if cfg.Web.Port != 19876 {
		t.Errorf("expected web port 19876, got %d", cfg.Web.Port)
	}
	if len(cfg.Ports.Exclude) != 2 {
		t.Errorf("expected 2 excluded ports, got %d", len(cfg.Ports.Exclude))
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".baton.toml")

	content := `
[connection]
host = "test@gitpod.io"
reverse_port = 19333
control_socket = "/tmp/test-baton.sock"

[transfer]
inbox = "/tmp/test-inbox"
mac_user = "testuser"

[ports]
scan_interval = "5s"
exclude = [22, 80, 443]

[web]
port = 9999
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Connection.Host != "test@gitpod.io" {
		t.Errorf("expected host test@gitpod.io, got %s", cfg.Connection.Host)
	}
	if cfg.Connection.ReversePort != 19333 {
		t.Errorf("expected reverse port 19333, got %d", cfg.Connection.ReversePort)
	}
	if cfg.Transfer.MacUser != "testuser" {
		t.Errorf("expected mac_user testuser, got %s", cfg.Transfer.MacUser)
	}
	if cfg.Ports.ScanInterval.Duration != 5*time.Second {
		t.Errorf("expected scan interval 5s, got %s", cfg.Ports.ScanInterval.Duration)
	}
	if cfg.Web.Port != 9999 {
		t.Errorf("expected web port 9999, got %d", cfg.Web.Port)
	}
	if len(cfg.Ports.Exclude) != 3 {
		t.Errorf("expected 3 excluded ports, got %d", len(cfg.Ports.Exclude))
	}
}

func TestLoadConfigMissing(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	_, err := LoadConfig()
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestDurationUnmarshal(t *testing.T) {
	var d duration
	if err := d.UnmarshalText([]byte("10s")); err != nil {
		t.Fatal(err)
	}
	if d.Duration != 10*time.Second {
		t.Errorf("expected 10s, got %s", d.Duration)
	}

	if err := d.UnmarshalText([]byte("500ms")); err != nil {
		t.Fatal(err)
	}
	if d.Duration != 500*time.Millisecond {
		t.Errorf("expected 500ms, got %s", d.Duration)
	}

	if err := d.UnmarshalText([]byte("invalid")); err == nil {
		t.Error("expected error for invalid duration")
	}
}
