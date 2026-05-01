package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Transferer struct {
	cfg    *Config
	events chan TransferDoneMsg
}

func NewTransferer(cfg *Config) *Transferer {
	return &Transferer{
		cfg:    cfg,
		events: make(chan TransferDoneMsg, 16),
	}
}

func (t *Transferer) Events() <-chan TransferDoneMsg {
	return t.events
}

func (t *Transferer) Transfer(localPath, remoteDest string) (string, error) {
	start := time.Now()

	info, _ := os.Stat(localPath)
	var size int64
	if info != nil {
		size = info.Size()
	}

	remotePath, err := transferFile(t.cfg, localPath, remoteDest)

	evt := TransferDoneMsg{
		Time:       time.Now(),
		Filename:   filepath.Base(localPath),
		RemotePath: remotePath,
		Size:       size,
		Duration:   time.Since(start),
		Err:        err,
	}
	select {
	case t.events <- evt:
	default:
	}

	return remotePath, err
}

func TransferFile(cfg *Config, localPath, remoteDest string) (string, error) {
	return transferFile(cfg, localPath, remoteDest)
}

func transferFile(cfg *Config, localPath, remoteDest string) (string, error) {
	filename := filepath.Base(localPath)

	if !strings.HasSuffix(remoteDest, "/") {
		remoteDest += "/"
	}

	remotePath := remoteDest + filename

	cmd := exec.Command("scp",
		"-o", "ControlPath=none",
		"-o", "StrictHostKeyChecking=accept-new",
		localPath,
		fmt.Sprintf("%s:%s", cfg.Connection.Host, remotePath),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("scp failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return remotePath, nil
}

func CopyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func ListForwardedPorts(cfg *Config) ([]int, error) {
	stateFile := cfg.Connection.ControlSocket + ".ports"
	data, err := readPortState(stateFile)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readPortState(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}

	var ports []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var port int
		if _, err := fmt.Sscanf(line, "%d", &port); err == nil {
			ports = append(ports, port)
		}
	}
	return ports, nil
}
