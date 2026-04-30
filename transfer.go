package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func TransferFile(cfg *Config, localPath, remoteDest string) (string, error) {
	filename := filepath.Base(localPath)

	if !strings.HasSuffix(remoteDest, "/") {
		remoteDest += "/"
	}

	remotePath := remoteDest + filename

	cmd := exec.Command("scp",
		"-o", fmt.Sprintf("ControlPath=%s", cfg.Connection.ControlSocket),
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
	cmd := exec.Command("cat", path)
	output, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	var ports []int
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
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
