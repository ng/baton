package main

import "time"

type PortInfo struct {
	Port    int
	Process string
	Label   string
	Pinned  bool
	Stale   bool
}

type ConnEventMsg struct {
	Time    time.Time
	Message string
}

type ConnStatusMsg struct {
	Connected bool
	Host      string
	Since     time.Time
}

type PortEventMsg struct {
	Time    time.Time
	Port    int
	Process string
	Action  string // "forwarded" or "removed"
}

type TransferStartMsg struct {
	Time     time.Time
	Filename string
	Size     int64
}

type TransferDoneMsg struct {
	Time       time.Time
	Filename   string
	RemotePath string
	Size       int64
	Duration   time.Duration
	Err        error
}

type ThroughputMsg struct {
	Upload   float64
	Download float64
}

type PortTrafficInfo struct {
	Port     int
	Upload   float64
	Download float64
}

type PortTrafficMsg struct {
	Ports map[int]PortTrafficInfo
}

type TickMsg time.Time

type connectResultMsg struct {
	err          error
	reverseInfos []PortInfo
	reversePorts []int
}
