package main

import (
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
	active   map[int]PortInfo
	excluded map[int]bool
	pinned   map[int]bool
	stopCh   chan struct{}
	events   chan PortEventMsg
}

// ssRe captures port and optional process name from ss -tlnp output.
// Example: LISTEN 0 511 127.0.0.1:3001 0.0.0.0:* users:(("node",pid=42728,fd=28))
var ssRe = regexp.MustCompile(`:(\d+)\s+\S+:\*\s*(?:users:\(\("([^"]*)")?`)

// netstatRe captures port and optional process name from netstat -tlnp output.
// Example: tcp 0 0 127.0.0.1:24175 0.0.0.0:* LISTEN 369925/node
var netstatRe = regexp.MustCompile(`:(\d+)\s+.*?LISTEN\s+(?:\d+/(\S+)|-)`)

var processAliases = map[string]string{
	"MainThread": "python",
	"main":       "go",
}

func normalizeProcess(name string) string {
	if alias, ok := processAliases[name]; ok {
		return alias
	}
	return name
}

func NewPortScanner(cfg *Config, conn *Connection, extraPorts []int, reverseRemotePorts []int) *PortScanner {
	excluded := make(map[int]bool)
	for _, p := range cfg.Ports.Exclude {
		excluded[p] = true
	}
	for _, p := range reverseRemotePorts {
		excluded[p] = true
	}
	pinned := make(map[int]bool)
	ps := &PortScanner{
		cfg:      cfg,
		conn:     conn,
		active:   make(map[int]PortInfo),
		excluded: excluded,
		pinned:   pinned,
		stopCh:   make(chan struct{}),
		events:   make(chan PortEventMsg, 32),
	}
	for _, port := range extraPorts {
		if excluded[port] {
			continue
		}
		pinned[port] = true
		if err := conn.Forward(port, port); err == nil {
			ps.active[port] = PortInfo{Port: port, Process: "preset"}
			ps.sendEvent(PortEventMsg{
				Time:    time.Now(),
				Port:    port,
				Process: "preset",
				Action:  "forwarded",
			})
		}
	}
	if len(ps.active) > 0 {
		ps.saveState()
	}
	return ps
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

func (ps *PortScanner) Events() <-chan PortEventMsg {
	return ps.events
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

func (ps *PortScanner) ActivePortInfos() []PortInfo {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	infos := make([]PortInfo, 0, len(ps.active))
	for _, info := range ps.active {
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Port < infos[j].Port
	})
	return infos
}

func (ps *PortScanner) sendEvent(evt PortEventMsg) {
	select {
	case ps.events <- evt:
	default:
	}
	Notify("baton", evt.Action+": port "+strconv.Itoa(evt.Port))
}

func parseLine(line string) (port int, process string, ok bool) {
	if !strings.Contains(line, "LISTEN") {
		return 0, "", false
	}

	// Detect format: ss output starts with LISTEN, netstat starts with tcp/tcp6
	isSS := strings.HasPrefix(strings.TrimSpace(line), "LISTEN")

	if isSS {
		if m := ssRe.FindStringSubmatch(line); m != nil {
			p, err := strconv.Atoi(m[1])
			if err != nil || p == 0 {
				return 0, "", false
			}
			proc := ""
			if len(m) > 2 {
				proc = normalizeProcess(m[2])
			}
			return p, proc, true
		}
	} else {
		if m := netstatRe.FindStringSubmatch(line); m != nil {
			p, err := strconv.Atoi(m[1])
			if err != nil || p == 0 {
				return 0, "", false
			}
			proc := ""
			if len(m) > 2 {
				proc = normalizeProcess(m[2])
			}
			return p, proc, true
		}
	}

	return 0, "", false
}

func (ps *PortScanner) scan() {
	output, err := ps.conn.RunRemote("ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null")
	if err != nil {
		return
	}

	discovered := make(map[int]PortInfo)
	for _, line := range strings.Split(string(output), "\n") {
		port, process, ok := parseLine(line)
		if !ok {
			continue
		}
		if ps.excluded[port] {
			continue
		}
		if port < 1024 && port != 80 && port != 443 {
			continue
		}
		if _, exists := discovered[port]; !exists {
			discovered[port] = PortInfo{Port: port, Process: process}
		}
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	for port, info := range discovered {
		if _, exists := ps.active[port]; !exists {
			if err := ps.conn.Forward(port, port); err == nil {
				ps.active[port] = info
				ps.saveState()
				ps.sendEvent(PortEventMsg{
					Time:    time.Now(),
					Port:    port,
					Process: info.Process,
					Action:  "forwarded",
				})
			}
		}
	}

	for port := range ps.active {
		if _, exists := discovered[port]; !exists && !ps.pinned[port] {
			if err := ps.conn.CancelForward(port, port); err == nil {
				info := ps.active[port]
				delete(ps.active, port)
				ps.saveState()
				ps.sendEvent(PortEventMsg{
					Time:    time.Now(),
					Port:    port,
					Process: info.Process,
					Action:  "removed",
				})
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
