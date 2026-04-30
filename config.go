package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Connection ConnectionConfig `toml:"connection"`
	Transfer   TransferConfig   `toml:"transfer"`
	Ports      PortsConfig      `toml:"ports"`
	Web        WebConfig        `toml:"web"`
}

type ConnectionConfig struct {
	Host          string `toml:"host"`
	ReversePort   int    `toml:"reverse_port"`
	ControlSocket string `toml:"control_socket"`
}

type TransferConfig struct {
	Inbox   string `toml:"inbox"`
	MacUser string `toml:"mac_user"`
}

type PortsConfig struct {
	ScanInterval duration `toml:"scan_interval"`
	Exclude      []int    `toml:"exclude"`
}

type WebConfig struct {
	Port int `toml:"port"`
}

type duration struct {
	time.Duration
}

func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

func DefaultConfig() *Config {
	return &Config{
		Connection: ConnectionConfig{
			ReversePort:   19222,
			ControlSocket: "/tmp/shuttle.sock",
		},
		Transfer: TransferConfig{
			Inbox: "/workspaces/.inbox",
		},
		Ports: PortsConfig{
			ScanInterval: duration{3 * time.Second},
			Exclude:      []int{22, 19222},
		},
		Web: WebConfig{
			Port: 19876,
		},
	}
}

func LoadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, ".shuttle.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if cfg.Transfer.MacUser == "" {
		cfg.Transfer.MacUser = os.Getenv("USER")
	}

	return cfg, nil
}
