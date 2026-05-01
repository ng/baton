package main

import (
	"testing"
)

func TestSSRegex(t *testing.T) {
	tests := []struct {
		line    string
		port    int
		process string
		ok      bool
	}{
		{
			line:    `LISTEN 0  511  127.0.0.1:3001  0.0.0.0:*  users:(("node",pid=42728,fd=28))`,
			port:    3001,
			process: "node",
			ok:      true,
		},
		{
			line:    `LISTEN 0  128  0.0.0.0:5432  0.0.0.0:*  users:(("postgres",pid=1234,fd=5))`,
			port:    5432,
			process: "postgres",
			ok:      true,
		},
		{
			line:    `LISTEN 0  128  [::]:8080  [::]:*  users:(("MainThread",pid=999,fd=3))`,
			port:    8080,
			process: "python",
			ok:      true,
		},
		{
			line:    `LISTEN 0  128  127.0.0.1:9001  0.0.0.0:*`,
			port:    9001,
			process: "",
			ok:      true,
		},
		{
			line: `State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process`,
			ok:   false,
		},
	}

	for _, tt := range tests {
		port, process, ok := parseLine(tt.line)
		if ok != tt.ok {
			t.Errorf("parseLine(%q): ok=%v, want %v", tt.line, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if port != tt.port {
			t.Errorf("parseLine(%q): port=%d, want %d", tt.line, port, tt.port)
		}
		if process != tt.process {
			t.Errorf("parseLine(%q): process=%q, want %q", tt.line, process, tt.process)
		}
	}
}

func TestNetstatRegex(t *testing.T) {
	tests := []struct {
		line    string
		port    int
		process string
		ok      bool
	}{
		{
			line:    `tcp  0  0  127.0.0.1:24175  0.0.0.0:*  LISTEN  369925/node`,
			port:    24175,
			process: "node",
			ok:      true,
		},
		{
			line:    `tcp  0  0  127.0.0.1:5432  0.0.0.0:*  LISTEN  -`,
			port:    5432,
			process: "",
			ok:      true,
		},
		{
			line:    `tcp6 0  0  [::]:8080  [::]:*  LISTEN  1234/python3`,
			port:    8080,
			process: "python3",
			ok:      true,
		},
	}

	for _, tt := range tests {
		port, process, ok := parseLine(tt.line)
		if ok != tt.ok {
			t.Errorf("parseLine(%q): ok=%v, want %v", tt.line, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if port != tt.port {
			t.Errorf("parseLine(%q): port=%d, want %d", tt.line, port, tt.port)
		}
		if process != tt.process {
			t.Errorf("parseLine(%q): process=%q, want %q", tt.line, process, tt.process)
		}
	}
}

func TestPortScannerExclude(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ports.Exclude = []int{22, 8080, 19222}

	conn := &Connection{}
	ps := NewPortScanner(cfg, conn, nil, nil, "")

	if !ps.excluded[22] {
		t.Error("port 22 should be excluded")
	}
	if !ps.excluded[8080] {
		t.Error("port 8080 should be excluded")
	}
	if !ps.excluded[19222] {
		t.Error("port 19222 should be excluded")
	}
	if ps.excluded[3000] {
		t.Error("port 3000 should not be excluded")
	}
}

func TestNormalizeProcess(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MainThread", "python"},
		{"main", "go"},
		{"node", "node"},
		{"postgres", "postgres"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeProcess(tt.input)
		if got != tt.want {
			t.Errorf("normalizeProcess(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
