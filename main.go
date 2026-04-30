package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
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
			fmt.Fprintln(os.Stderr, "usage: baton connect <host>")
			os.Exit(1)
		}
		host := cfg.Connection.Host
		if len(os.Args) >= 3 {
			host = os.Args[2]
		}
		runConnect(cfg, host)

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
  baton connect <host>       Start SSH connection + all services
  baton send <file> [dest]   Upload file to remote
  baton ports                List forwarded ports
  baton status               Show connection status
  baton disconnect           Clean shutdown
  baton version              Print version

Config: ~/.baton.toml
`, version)
}

func runConnect(cfg *Config, host string) {
	conn := NewConnection(cfg, host)

	if err := conn.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "connection failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("connected to %s\n", host)

	scanner := NewPortScanner(cfg, conn)
	go scanner.Run()
	fmt.Printf("port scanner started (interval: %s)\n", cfg.Ports.ScanInterval)

	web := NewWebUI(cfg, conn)
	go func() {
		if err := web.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "web ui failed: %v\n", err)
		}
	}()
	fmt.Printf("web ui: http://localhost:%d\n", cfg.Web.Port)

	fmt.Println("\nbaton is running. Ctrl+C to disconnect.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nshutting down...")
	scanner.Stop()
	web.Stop()
	conn.Stop()
	fmt.Println("disconnected.")
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

func runStatus(cfg *Config) {
	conn := NewConnection(cfg, cfg.Connection.Host)
	alive := conn.IsAlive()
	if alive {
		fmt.Printf("connected: %s\n", cfg.Connection.Host)
		fmt.Printf("socket:    %s\n", cfg.Connection.ControlSocket)
		fmt.Printf("web ui:    http://localhost:%d\n", cfg.Web.Port)
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
