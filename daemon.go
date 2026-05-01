package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type DaemonEvent struct {
	Type       string                  `json:"type"`
	Time       time.Time               `json:"time"`
	Connected  *bool                   `json:"connected,omitempty"`
	Host       string                  `json:"host,omitempty"`
	Message    string                  `json:"message,omitempty"`
	Port       int                     `json:"port,omitempty"`
	Process    string                  `json:"process,omitempty"`
	Action     string                  `json:"action,omitempty"`
	Label      string                  `json:"label,omitempty"`
	Pinned     bool                    `json:"pinned,omitempty"`
	Filename   string                  `json:"filename,omitempty"`
	RemotePath string                  `json:"remote_path,omitempty"`
	Size       int64                   `json:"size,omitempty"`
	DurationMs float64                 `json:"duration_ms,omitempty"`
	Error      string                  `json:"error,omitempty"`
	Upload     float64                 `json:"upload,omitempty"`
	Download   float64                 `json:"download,omitempty"`
	Ports      []PortInfo              `json:"ports,omitempty"`
	PortTraffic map[int]PortTrafficInfo `json:"port_traffic,omitempty"`
	Preset     string                  `json:"preset,omitempty"`
	Inbox      string                  `json:"inbox,omitempty"`
	AutoReconn bool                    `json:"auto_reconnect,omitempty"`
}

type DaemonCommand struct {
	Cmd  string `json:"cmd"`
	Path string `json:"path,omitempty"`
	Port int    `json:"port,omitempty"`
}

func runDaemon(cfg *Config, host, preset string) {
	enc := json.NewEncoder(os.Stdout)

	emit := func(evt DaemonEvent) {
		if evt.Time.IsZero() {
			evt.Time = time.Now()
		}
		enc.Encode(evt)
	}

	if preset != "" {
		if _, ok := cfg.Presets[preset]; !ok {
			fmt.Fprintf(os.Stderr, "warning: unknown preset %q\n", preset)
		}
	}

	extraPorts := cfg.EffectiveExtra(preset)
	reversePorts := cfg.EffectiveReverse(preset)

	conn := NewConnection(cfg, host, reversePorts, extraPorts)
	scanner := NewPortScanner(cfg, conn, extraPorts, nil, preset)
	transferer := NewTransferer(cfg)
	throughput := NewThroughputMonitor(conn, 2*time.Second)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	doneCh := make(chan struct{})

	go func() {
		if err := conn.Start(); err != nil {
			emit(DaemonEvent{Type: "error", Message: fmt.Sprintf("connect failed: %v", err)})
			close(doneCh)
			return
		}

		var reverseRemotePorts []int
		for _, port := range reversePorts {
			remotePort := port
			if port == 443 {
				remotePort = 4443
			}
			reverseRemotePorts = append(reverseRemotePorts, remotePort)
		}

		scanner.AddExclusions(reverseRemotePorts)

		t := true
		emit(DaemonEvent{
			Type:       "ready",
			Host:       host,
			Preset:     preset,
			Connected:  &t,
			Inbox:      cfg.Transfer.Inbox,
			AutoReconn: true,
		})
	}()

	// Event relay goroutines
	go func() {
		for evt := range conn.Events() {
			connected := conn.IsAlive()
			emit(DaemonEvent{
				Type:      "conn_event",
				Message:   evt.Message,
				Host:      host,
				Connected: &connected,
			})
		}
	}()

	go func() {
		for evt := range scanner.Events() {
			label := cfg.PortLabel(preset, evt.Port)
			emit(DaemonEvent{
				Type:    "port_event",
				Port:    evt.Port,
				Process: evt.Process,
				Action:  evt.Action,
				Label:   label,
			})
		}
	}()

	go func() {
		for evt := range transferer.Events() {
			e := DaemonEvent{
				Type:       "transfer_done",
				Filename:   evt.Filename,
				RemotePath: evt.RemotePath,
				Size:       evt.Size,
				DurationMs: float64(evt.Duration.Milliseconds()),
			}
			if evt.Err != nil {
				e.Error = evt.Err.Error()
			}
			emit(e)
		}
	}()

	go func() {
		for evt := range throughput.Events() {
			emit(DaemonEvent{
				Type:     "throughput",
				Upload:   evt.Upload,
				Download: evt.Download,
			})
		}
	}()

	go func() {
		for evt := range throughput.PortEvents() {
			emit(DaemonEvent{
				Type:        "port_traffic",
				PortTraffic: evt.Ports,
			})
		}
	}()

	// Stdin command reader
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			var cmd DaemonCommand
			if err := json.Unmarshal(sc.Bytes(), &cmd); err != nil {
				continue
			}
			switch cmd.Cmd {
			case "send":
				go func(path string) {
					dest := cfg.Transfer.Inbox
					transferer.Transfer(path, dest)
				}(cmd.Path)
			case "forward":
				go func(port int) {
					if err := conn.Forward(port, port); err != nil {
						emit(DaemonEvent{
							Type:    "error",
							Message: fmt.Sprintf("forward :%d failed: %v", port, err),
						})
					} else {
						emit(DaemonEvent{
							Type:   "port_event",
							Port:   port,
							Action: "forwarded",
							Label:  cfg.PortLabel(preset, port),
						})
					}
				}(cmd.Port)
			case "cancel_forward":
				go func(port int) {
					conn.CancelForward(port, port)
				}(cmd.Port)
			case "reconnect_toggle":
				on := !conn.AutoReconnect()
				conn.SetAutoReconnect(on)
				emit(DaemonEvent{
					Type:       "conn_event",
					Message:    "auto-reconnect " + boolStr(on),
					AutoReconn: on,
				})
			case "status":
				alive := conn.IsAlive()
				emit(DaemonEvent{
					Type:       "conn_status",
					Host:       host,
					Connected:  &alive,
					Preset:     preset,
					AutoReconn: conn.AutoReconnect(),
					Ports:      scanner.ActivePortInfos(),
				})
			case "quit":
				close(doneCh)
				return
			}
		}
		close(doneCh)
	}()

	select {
	case <-sigCh:
	case <-doneCh:
	}

	throughput.Stop()
	scanner.Stop()
	conn.Stop()

	emit(DaemonEvent{Type: "shutdown", Message: "disconnected"})
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

