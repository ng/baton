package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Connection ConnectionConfig      `toml:"connection"`
	Transfer   TransferConfig        `toml:"transfer"`
	Ports      PortsConfig           `toml:"ports"`
	Presets    map[string]PortPreset `toml:"presets"`
}

type ConnectionConfig struct {
	Host          string `toml:"host"`
	ReversePort   int    `toml:"reverse_port"`
	ControlSocket string `toml:"control_socket"`
	Preset        string `toml:"preset,omitempty"`
}

type TransferConfig struct {
	Inbox   string `toml:"inbox"`
	MacUser string `toml:"mac_user"`
}

type PortsConfig struct {
	ScanInterval duration `toml:"scan_interval"`
	Exclude      []int    `toml:"exclude"`
	Extra        []int    `toml:"extra"`
}

type PortPreset struct {
	Ports   []int             `toml:"ports"`
	Reverse []int             `toml:"reverse"`
	Exclude []int             `toml:"exclude"`
	Desc    string            `toml:"desc"`
	Labels  map[string]string `toml:"labels"`
}

type duration struct {
	time.Duration
}

func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

func (d duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

func DefaultConfig() *Config {
	return &Config{
		Connection: ConnectionConfig{
			ReversePort:   19222,
			ControlSocket: "/tmp/baton.sock",
		},
		Transfer: TransferConfig{
			Inbox: "/workspaces/.inbox",
		},
		Ports: PortsConfig{
			ScanInterval: duration{3 * time.Second},
			Exclude:      []int{22, 19222},
		},
		Presets: map[string]PortPreset{
			"orchestra": {
				Desc:    "Orchestra platform services",
				Reverse: []int{443, 3000, 3306, 5432, 6007, 8000, 9000, 9010, 9020, 9030, 9040, 9050},
				Ports:   []int{3001, 9001},
				Labels: map[string]string{
					"443":  "caddy",
					"3000": "portcullis-ui",
					"3001": "vite-ui",
					"3306": "mysql",
					"5432": "postgres",
					"6007": "storybook",
					"8000": "data-service",
					"9000": "portcullis-api",
					"9001": "portcullis-api-debug",
					"9010": "protocoldescriber",
					"9020": "entityresolver",
					"9030": "labwareresolver",
					"9040": "cellculturebatchresolver",
					"9050": "experimentsresync",
				},
			},
		},
	}
}

func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	localPath := ".baton.toml"
	if data, err := os.ReadFile(localPath); err == nil {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", localPath, err)
		}
		if cfg.Transfer.MacUser == "" {
			cfg.Transfer.MacUser = os.Getenv("USER")
		}
		return cfg, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, ".baton.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if cfg.Transfer.MacUser == "" {
		cfg.Transfer.MacUser = os.Getenv("USER")
	}

	return cfg, nil
}

func WriteDefaultConfig(path string) error {
	cfg := DefaultConfig()
	return WriteConfig(cfg, path)
}

func WriteConfig(cfg *Config, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func (c *Config) ExtraPortsForPreset(name string) []int {
	preset, ok := c.Presets[name]
	if !ok {
		return nil
	}
	return preset.Ports
}

func (c *Config) EffectiveExtra(preset string) []int {
	seen := make(map[int]bool)
	var result []int
	for _, p := range c.Ports.Extra {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	if preset != "" {
		for _, p := range c.ExtraPortsForPreset(preset) {
			if !seen[p] {
				seen[p] = true
				result = append(result, p)
			}
		}
	}
	return result
}

func (c *Config) PortLabel(preset string, port int) string {
	if preset == "" {
		return ""
	}
	p, ok := c.Presets[preset]
	if !ok || p.Labels == nil {
		return ""
	}
	return p.Labels[strconv.Itoa(port)]
}

func (c *Config) SaveSession(host, preset string) {
	c.Connection.Host = host
	c.Connection.Preset = preset
	WriteConfig(c, ".baton.toml")
}

func (c *Config) EffectiveReverse(preset string) []int {
	if preset == "" {
		return nil
	}
	p, ok := c.Presets[preset]
	if !ok {
		return nil
	}
	seen := make(map[int]bool)
	var result []int
	for _, port := range p.Reverse {
		if !seen[port] {
			seen[port] = true
			result = append(result, port)
		}
	}
	return result
}
