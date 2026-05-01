package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ThroughputMonitor struct {
	conn       *Connection
	interval   time.Duration
	enabled    bool
	events     chan ThroughputMsg
	portEvents chan PortTrafficMsg
	stopCh     chan struct{}
	lastRx     uint64
	lastTx     uint64
	lastTime   time.Time
	lastPorts  map[int]portBytes
	failed     bool
}

type portBytes struct {
	sent     uint64
	received uint64
}

var devRe = regexp.MustCompile(`^\s*(\S+):\s*(\d+)\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+(\d+)`)

var ssBytesRe = regexp.MustCompile(`bytes_sent:(\d+)`)
var ssRecvRe = regexp.MustCompile(`bytes_received:(\d+)`)

func NewThroughputMonitor(conn *Connection, interval time.Duration) *ThroughputMonitor {
	return &ThroughputMonitor{
		conn:       conn,
		interval:   interval,
		enabled:    false,
		events:     make(chan ThroughputMsg, 32),
		portEvents: make(chan PortTrafficMsg, 32),
		stopCh:     make(chan struct{}),
		lastPorts:  make(map[int]portBytes),
	}
}

func (tm *ThroughputMonitor) Events() <-chan ThroughputMsg {
	return tm.events
}

func (tm *ThroughputMonitor) PortEvents() <-chan PortTrafficMsg {
	return tm.portEvents
}

func (tm *ThroughputMonitor) Run() {
	if !tm.enabled {
		<-tm.stopCh
		return
	}

	tm.sample()

	ticker := time.NewTicker(tm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-tm.stopCh:
			return
		case <-ticker.C:
			if tm.failed {
				return
			}
			tm.sample()
		}
	}
}

func (tm *ThroughputMonitor) Stop() {
	select {
	case <-tm.stopCh:
	default:
		close(tm.stopCh)
	}
}

func (tm *ThroughputMonitor) sample() {
	output, err := tm.conn.RunRemote("cat /proc/net/dev 2>/dev/null; echo '---SPLIT---'; ss -t -i 2>/dev/null")
	if err != nil {
		tm.failed = true
		return
	}

	parts := strings.SplitN(string(output), "---SPLIT---", 2)

	rx, tx := parseNetDev(parts[0])
	now := time.Now()

	if !tm.lastTime.IsZero() && tx >= tm.lastTx && rx >= tm.lastRx {
		elapsed := now.Sub(tm.lastTime).Seconds()
		if elapsed > 0 {
			upload := float64(tx-tm.lastTx) / elapsed
			download := float64(rx-tm.lastRx) / elapsed
			select {
			case tm.events <- ThroughputMsg{Upload: upload, Download: download}:
			default:
			}
		}
	}

	tm.lastRx = rx
	tm.lastTx = tx

	if len(parts) == 2 {
		current := parseSSTraffic(parts[1])
		if len(tm.lastPorts) > 0 && !tm.lastTime.IsZero() {
			elapsed := now.Sub(tm.lastTime).Seconds()
			if elapsed > 0 {
				portInfo := make(map[int]PortTrafficInfo)
				for port, cur := range current {
					prev, ok := tm.lastPorts[port]
					if !ok || cur.sent < prev.sent || cur.received < prev.received {
						continue
					}
					up := float64(cur.sent-prev.sent) / elapsed
					down := float64(cur.received-prev.received) / elapsed
					if up > 0 || down > 0 {
						portInfo[port] = PortTrafficInfo{Port: port, Upload: up, Download: down}
					}
				}
				if len(portInfo) > 0 {
					select {
					case tm.portEvents <- PortTrafficMsg{Ports: portInfo}:
					default:
					}
				}
			}
		}
		tm.lastPorts = current
	}

	tm.lastTime = now
}

func parseNetDev(output string) (rx, tx uint64) {
	for _, line := range strings.Split(output, "\n") {
		m := devRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		iface := strings.TrimRight(m[1], ":")
		if iface == "lo" {
			continue
		}
		rxBytes, _ := strconv.ParseUint(m[2], 10, 64)
		txBytes, _ := strconv.ParseUint(m[3], 10, 64)
		rx += rxBytes
		tx += txBytes
	}
	return
}

func parseSSTraffic(output string) map[int]portBytes {
	result := make(map[int]portBytes)
	lines := strings.Split(output, "\n")

	portRe := regexp.MustCompile(`\s+(\S+):(\d+)\s+(\S+):(\d+)\s*$`)

	var currentPort int
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.HasPrefix(line, "ESTAB") || strings.HasPrefix(line, "CLOSE-WAIT") || strings.HasPrefix(line, "TIME-WAIT") {
			m := portRe.FindStringSubmatch(line)
			if m != nil {
				p, err := strconv.Atoi(m[2])
				if err == nil {
					currentPort = p
				} else {
					currentPort = 0
				}
			} else {
				currentPort = 0
			}
			continue
		}

		if currentPort > 0 && strings.Contains(line, "bytes_sent:") {
			var sent, recv uint64
			if m := ssBytesRe.FindStringSubmatch(line); m != nil {
				sent, _ = strconv.ParseUint(m[1], 10, 64)
			}
			if m := ssRecvRe.FindStringSubmatch(line); m != nil {
				recv, _ = strconv.ParseUint(m[1], 10, 64)
			}
			prev := result[currentPort]
			prev.sent += sent
			prev.received += recv
			result[currentPort] = prev
			currentPort = 0
		}
	}
	return result
}
