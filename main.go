package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: no config loaded: %v\n", err)
		cfg = DefaultConfig()
	}

	switch os.Args[1] {
	case "connect":
		if len(os.Args) < 3 && cfg.Connection.Host == "" {
			fmt.Fprintln(os.Stderr, "usage: baton connect <host> [--preset <name>]")
			os.Exit(1)
		}
		host := cfg.Connection.Host
		preset := ""
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			if args[i] == "--preset" && i+1 < len(args) {
				preset = args[i+1]
				i++
			} else if !strings.HasPrefix(args[i], "-") && host == "" {
				host = args[i]
			} else if !strings.HasPrefix(args[i], "-") {
				host = args[i]
			}
		}
		if host == "" {
			fmt.Fprintln(os.Stderr, "usage: baton connect <host> [--preset <name>]")
			os.Exit(1)
		}
		runConnect(cfg, host, preset)

	case "send":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: baton send <file> [remote-path]")
			os.Exit(1)
		}
		dest := cfg.Transfer.Inbox
		if len(os.Args) >= 4 {
			dest = os.Args[3]
		}
		runSend(cfg, os.Args[2], dest)

	case "ports":
		runPorts(cfg)

	case "status":
		runStatus(cfg)

	case "disconnect":
		runDisconnect(cfg)

	case "presets":
		runPresets(cfg)

	case "init":
		runInit()

	case "version":
		fmt.Printf("baton %s\n", version)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `baton %s — Mac ↔ Gitpod bridge

Usage:
  baton connect <host> [--preset <name>]   Start SSH connection with TUI dashboard
  baton send <file> [dest]                 Upload file to remote
  baton ports                              List forwarded ports
  baton presets                            List available port presets
  baton init                               Write default .baton.toml to current dir
  baton status                             Show connection status
  baton disconnect                         Clean shutdown
  baton version                            Print version

Config: .baton.toml (local) or ~/.baton.toml
`, version)
}

func runConnect(cfg *Config, host, preset string) {
	conn := NewConnection(cfg, host)

	fmt.Fprintf(os.Stderr, "connecting to %s...\n", host)
	if err := conn.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "connection failed: %v\n", err)
		os.Exit(1)
	}

	extraPorts := cfg.EffectiveExtra(preset)
	reversePorts := cfg.EffectiveReverse(preset)
	if preset != "" {
		if _, ok := cfg.Presets[preset]; !ok {
			fmt.Fprintf(os.Stderr, "warning: unknown preset %q\n", preset)
		} else {
			fmt.Fprintf(os.Stderr, "using preset: %s\n", preset)
		}
	}

	var reverseInfos []PortInfo
	var reverseRemotePorts []int
	for _, port := range reversePorts {
		remotePort := port
		if port == 443 {
			remotePort = 4443
		}
		if err := conn.ReverseForward(remotePort, port); err != nil {
			fmt.Fprintf(os.Stderr, "warning: reverse forward :%d failed: %v\n", port, err)
		} else {
			reverseInfos = append(reverseInfos, PortInfo{Port: remotePort, Process: "preset"})
			reverseRemotePorts = append(reverseRemotePorts, remotePort)
		}
	}

	scanner := NewPortScanner(cfg, conn, extraPorts, reverseRemotePorts)
	go scanner.Run()

	transferer := NewTransferer(cfg)

	throughput := NewThroughputMonitor(conn, 2*time.Second)
	go throughput.Run()

	m := newModel(cfg, conn, scanner, transferer, throughput, preset, reverseInfos)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
	}

	throughput.Stop()
	scanner.Stop()
	conn.Stop()
	fmt.Fprintln(os.Stderr, "disconnected.")
}

func runSend(cfg *Config, file, dest string) {
	conn := NewConnection(cfg, cfg.Connection.Host)
	if !conn.IsAlive() {
		fmt.Fprintln(os.Stderr, "no active connection. run 'baton connect' first.")
		os.Exit(1)
	}
	remotePath, err := TransferFile(cfg, file, dest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upload failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(remotePath)
	if err := CopyToClipboard(remotePath); err == nil {
		fmt.Fprintln(os.Stderr, "(copied to clipboard)")
	}
}

func runPorts(cfg *Config) {
	conn := NewConnection(cfg, cfg.Connection.Host)
	if !conn.IsAlive() {
		fmt.Fprintln(os.Stderr, "no active connection.")
		os.Exit(1)
	}
	ports, err := ListForwardedPorts(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(ports) == 0 {
		fmt.Println("no ports forwarded")
		return
	}
	fmt.Println("forwarded ports:")
	for _, p := range ports {
		fmt.Printf("  localhost:%d → remote:%d\n", p, p)
	}
}

func runPresets(cfg *Config) {
	if len(cfg.Presets) == 0 {
		fmt.Println("no presets configured")
		return
	}
	for name, preset := range cfg.Presets {
		desc := preset.Desc
		if desc == "" {
			desc = "no description"
		}
		fmt.Printf("  %s — %s\n", name, desc)
		for _, p := range preset.Reverse {
			fmt.Printf("    -R :%d (local → remote)\n", p)
		}
		for _, p := range preset.Ports {
			fmt.Printf("    -L :%d (remote → local)\n", p)
		}
	}
}

func runInit() {
	path := ".baton.toml"
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "%s already exists\n", path)
		os.Exit(1)
	}
	if err := WriteDefaultConfig(path); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", path)
}

func runStatus(cfg *Config) {
	conn := NewConnection(cfg, cfg.Connection.Host)
	alive := conn.IsAlive()
	if alive {
		fmt.Printf("connected: %s\n", cfg.Connection.Host)
		fmt.Printf("socket:    %s\n", cfg.Connection.ControlSocket)
	} else {
		fmt.Println("not connected")
	}
}

func runDisconnect(cfg *Config) {
	conn := NewConnection(cfg, cfg.Connection.Host)
	if err := conn.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "disconnect failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("disconnected.")
}
