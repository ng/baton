package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PortScanner struct {
	cfg      *Config
	conn     *Connection
	mu       sync.RWMutex
	active   map[int]bool
	excluded map[int]bool
	stopCh   chan struct{}
}

func NewPortScanner(cfg *Config, conn *Connection) *PortScanner {
	excluded := make(map[int]bool)
	for _, p := range cfg.Ports.Exclude {
		excluded[p] = true
	}
	return &PortScanner{
		cfg:      cfg,
		conn:     conn,
		active:   make(map[int]bool),
		excluded: excluded,
		stopCh:   make(chan struct{}),
	}
}

func (ps *PortScanner) Run() {
	ps.scan()

	ticker := time.NewTicker(ps.cfg.Ports.ScanInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ps.stopCh:
			return
		case <-ticker.C:
			ps.scan()
		}
	}
}

func (ps *PortScanner) Stop() {
	close(ps.stopCh)
}

func (ps *PortScanner) ActivePorts() []int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	ports := make([]int, 0, len(ps.active))
	for p := range ps.active {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

var portRe = regexp.MustCompile(`:(\d+)\s`)

func (ps *PortScanner) scan() {
	output, err := ps.conn.RunRemote("ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null")
	if err != nil {
		return
	}

	discovered := make(map[int]bool)
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}

		matches := portRe.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			port, err := strconv.Atoi(m[1])
			if err != nil || port == 0 {
				continue
			}
			if ps.excluded[port] {
				continue
			}
			if port < 1024 && port != 80 && port != 443 {
				continue
			}
			discovered[port] = true
			break
		}
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	for port := range discovered {
		if !ps.active[port] {
			if err := ps.conn.Forward(port, port); err == nil {
				ps.active[port] = true
				ps.saveState()
				fmt.Fprintf(os.Stderr, "→ forwarded port %d\n", port)
				Notify("shuttle", fmt.Sprintf("Port %d forwarded to localhost:%d", port, port))
			}
		}
	}

	for port := range ps.active {
		if !discovered[port] {
			if err := ps.conn.CancelForward(port, port); err == nil {
				delete(ps.active, port)
				ps.saveState()
				fmt.Fprintf(os.Stderr, "✕ removed forward for port %d\n", port)
			}
		}
	}
}

func (ps *PortScanner) saveState() {
	stateFile := ps.cfg.Connection.ControlSocket + ".ports"
	var lines []string
	for port := range ps.active {
		lines = append(lines, strconv.Itoa(port))
	}
	os.WriteFile(stateFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
