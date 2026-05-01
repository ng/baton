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
	cfg         *Config
	conn        *Connection
	preset      string
	mu          sync.RWMutex
	active      map[int]PortInfo
	excluded    map[int]bool
	pinned      map[int]bool
	scanEnabled bool
	stopCh      chan struct{}
	events      chan PortEventMsg
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

func NewPortScanner(cfg *Config, conn *Connection, extraPorts []int, reverseRemotePorts []int, preset string) *PortScanner {
	excluded := make(map[int]bool)
	for _, p := range cfg.Ports.Exclude {
		excluded[p] = true
	}
	for _, p := range reverseRemotePorts {
		excluded[p] = true
	}
	pinned := make(map[int]bool)
	ps := &PortScanner{
		cfg:         cfg,
		conn:        conn,
		preset:      preset,
		active:      make(map[int]PortInfo),
		excluded:    excluded,
		pinned:      pinned,
		scanEnabled: true,
		stopCh:      make(chan struct{}),
		events:      make(chan PortEventMsg, 32),
	}
	for _, port := range extraPorts {
		if excluded[port] {
			continue
		}
		pinned[port] = true
	}
	return ps
}

func (ps *PortScanner) AddExclusions(ports []int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range ports {
		ps.excluded[p] = true
	}
}

func (ps *PortScanner) forwardPinned() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for port := range ps.pinned {
		label := ps.cfg.PortLabel(ps.preset, port)
		// Pinned ports are already baked into the initial SSH command as -L flags,
		// so we only register them here without calling conn.Forward() again.
		ps.active[port] = PortInfo{Port: port, Label: label, Pinned: true}
		ps.saveState()
		ps.sendEvent(PortEventMsg{
			Time:    time.Now(),
			Port:    port,
			Process: label,
			Action:  "forwarded",
		})
	}
}

func (ps *PortScanner) SetScanEnabled(on bool) {
	ps.mu.Lock()
	ps.scanEnabled = on
	ps.mu.Unlock()
}

func (ps *PortScanner) Run() {
	ps.forwardPinned()

	if !ps.scanEnabled {
		<-ps.stopCh
		return
	}

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

	var toForward []PortInfo
	for port, info := range discovered {
		if existing, exists := ps.active[port]; exists {
			if existing.Stale {
				existing.Stale = false
				ps.active[port] = existing
			}
			continue
		}
		if label := ps.cfg.PortLabel(ps.preset, port); label != "" {
			info.Label = label
		}
		toForward = append(toForward, info)
	}
	for _, info := range toForward {
		ps.active[info.Port] = info
		ps.saveState()
		ps.sendEvent(PortEventMsg{
			Time:    time.Now(),
			Port:    info.Port,
			Process: info.Process,
			Action:  "discovered",
		})
	}

	for port, info := range ps.active {
		if ps.pinned[port] {
			continue
		}
		if _, exists := discovered[port]; !exists && !info.Stale {
			info.Stale = true
			ps.active[port] = info
		}
	}
}

func (ps *PortScanner) SweepStale() []int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var swept []int
	for port, info := range ps.active {
		if info.Stale && !ps.pinned[port] {
			delete(ps.active, port)
			swept = append(swept, port)
			ps.sendEvent(PortEventMsg{
				Time:    time.Now(),
				Port:    port,
				Process: info.Process,
				Action:  "removed",
			})
		}
	}
	if len(swept) > 0 {
		ps.saveState()
	}
	return swept
}

func (ps *PortScanner) saveState() {
	stateFile := ps.cfg.Connection.ControlSocket + ".ports"
	var lines []string
	for port := range ps.active {
		lines = append(lines, strconv.Itoa(port))
	}
	os.WriteFile(stateFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
