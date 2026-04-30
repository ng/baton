package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ThroughputMonitor struct {
	conn     *Connection
	interval time.Duration
	events   chan ThroughputMsg
	stopCh   chan struct{}
	lastRx   uint64
	lastTx   uint64
	lastTime time.Time
	failed   bool
}

// devRe parses a line from /proc/net/dev.
// Format: iface: rx_bytes rx_packets ... tx_bytes tx_packets ...
var devRe = regexp.MustCompile(`^\s*(\S+):\s*(\d+)\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+(\d+)`)

func NewThroughputMonitor(conn *Connection, interval time.Duration) *ThroughputMonitor {
	return &ThroughputMonitor{
		conn:     conn,
		interval: interval,
		events:   make(chan ThroughputMsg, 32),
		stopCh:   make(chan struct{}),
	}
}

func (tm *ThroughputMonitor) Events() <-chan ThroughputMsg {
	return tm.events
}

func (tm *ThroughputMonitor) Run() {
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
	output, err := tm.conn.RunRemote("cat /proc/net/dev 2>/dev/null")
	if err != nil {
		tm.failed = true
		return
	}

	rx, tx := parseNetDev(string(output))
	now := time.Now()

	if tm.lastTime.IsZero() {
		tm.lastRx = rx
		tm.lastTx = tx
		tm.lastTime = now
		return
	}

	elapsed := now.Sub(tm.lastTime).Seconds()
	if elapsed <= 0 {
		return
	}

	upload := float64(tx-tm.lastTx) / elapsed
	download := float64(rx-tm.lastRx) / elapsed

	if upload < 0 {
		upload = 0
	}
	if download < 0 {
		download = 0
	}

	tm.lastRx = rx
	tm.lastTx = tx
	tm.lastTime = now

	select {
	case tm.events <- ThroughputMsg{Upload: upload, Download: download}:
	default:
	}
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
