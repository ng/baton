package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Connection struct {
	cfg           *Config
	host          string
	cmd           *exec.Cmd
	dynamicFwds   map[string]*exec.Cmd
	monCmd        *exec.Cmd
	monStdin      io.WriteCloser
	monReader     *bufio.Reader
	monMu         sync.Mutex
	mu            sync.Mutex
	stopCh        chan struct{}
	stopped       bool
	autoReconnect bool
	events        chan ConnEventMsg
	StartTime     time.Time
	reversePorts  []int
	localPorts    []int
}

func NewConnection(cfg *Config, host string, reversePorts []int, localPorts []int) *Connection {
	return &Connection{
		cfg:           cfg,
		host:          host,
		autoReconnect: true,
		stopCh:        make(chan struct{}),
		events:        make(chan ConnEventMsg, 32),
		dynamicFwds:   make(map[string]*exec.Cmd),
		reversePorts:  reversePorts,
		localPorts:    localPorts,
	}
}

func (c *Connection) sendEvent(msg string) {
	select {
	case c.events <- ConnEventMsg{Time: time.Now(), Message: msg}:
	default:
	}
}

func (c *Connection) Events() <-chan ConnEventMsg {
	return c.events
}

func (c *Connection) Start() error {
	if c.IsAlive() {
		return fmt.Errorf("already connected")
	}

	args := c.buildSSHArgs()
	c.cmd = exec.Command("ssh", args...)
	c.cmd.Stderr = nil

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("ssh start: %w", err)
	}

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if c.IsAlive() {
			c.StartTime = time.Now()
			os.WriteFile(c.cfg.Connection.ControlSocket+".host", []byte(c.host), 0644)
			c.sendEvent("connected to " + c.host)
			go c.watchAndReconnect()
			return nil
		}
	}

	c.cmd.Process.Kill()
	return fmt.Errorf("ssh connection timed out after 6s")
}

func (c *Connection) buildSSHArgs() []string {
	args := []string{
		"-N",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	for _, port := range c.reversePorts {
		remotePort := port
		if port == 443 {
			remotePort = 4443
		}
		args = append(args, "-R", fmt.Sprintf("%d:localhost:%d", remotePort, port))
	}
	for _, port := range c.localPorts {
		args = append(args, "-L", fmt.Sprintf("0.0.0.0:%d:localhost:%d", port, port))
	}
	args = append(args, c.host)
	return args
}

func (c *Connection) startMonitor() error {
	cmd := exec.Command("ssh",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		c.host,
		"exec bash -s",
	)
	cmd.Stderr = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.monCmd = cmd
	c.monStdin = stdin
	c.monReader = bufio.NewReaderSize(stdout, 1024*1024)
	return nil
}

func (c *Connection) stopMonitor() {
	if c.monStdin != nil {
		c.monStdin.Close()
	}
	if c.monCmd != nil && c.monCmd.Process != nil {
		c.monCmd.Process.Kill()
		c.monCmd.Wait()
		c.monCmd = nil
	}
}

func (c *Connection) monitorAlive() bool {
	return c.monCmd != nil && c.monCmd.Process != nil &&
		c.monCmd.Process.Signal(syscall.Signal(0)) == nil
}

func (c *Connection) IsAlive() bool {
	if c.cmd == nil || c.cmd.Process == nil {
		return false
	}
	return c.cmd.Process.Signal(syscall.Signal(0)) == nil
}

func (c *Connection) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return nil
	}
	c.stopped = true
	close(c.stopCh)
	c.stopMonitor()
	c.stopDynamicForwards()
	os.Remove(c.cfg.Connection.ControlSocket + ".host")

	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
	return nil
}

func (c *Connection) RunRemote(command string) ([]byte, error) {
	c.monMu.Lock()
	defer c.monMu.Unlock()

	if c.monitorAlive() {
		delim := fmt.Sprintf("__BATON_%d__", time.Now().UnixNano())
		_, err := fmt.Fprintf(c.monStdin, "%s; echo %s\n", command, delim)
		if err == nil {
			var buf bytes.Buffer
			for {
				line, err := c.monReader.ReadString('\n')
				if err != nil {
					break
				}
				if strings.TrimSpace(line) == delim {
					return buf.Bytes(), nil
				}
				buf.WriteString(line)
			}
		}
		c.stopMonitor()
	}

	return c.RunRemoteDirect(command)
}

func (c *Connection) RunRemoteDirect(command string) ([]byte, error) {
	cmd := exec.Command("ssh",
		"-o", "ControlPath=none",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=5",
		c.host,
		command,
	)
	return cmd.Output()
}

func (c *Connection) Forward(localPort, remotePort int) error {
	key := fmt.Sprintf("L:%d", localPort)
	c.mu.Lock()
	if existing, ok := c.dynamicFwds[key]; ok {
		existing.Process.Kill()
		existing.Wait()
		delete(c.dynamicFwds, key)
	}
	c.mu.Unlock()

	cmd := exec.Command("ssh",
		"-N",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ExitOnForwardFailure=yes",
		"-L", fmt.Sprintf("0.0.0.0:%d:localhost:%d", localPort, remotePort),
		c.host,
	)
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("forward ssh start: %w", err)
	}

	c.mu.Lock()
	c.dynamicFwds[key] = cmd
	c.mu.Unlock()

	go func() {
		cmd.Wait()
		c.mu.Lock()
		if c.dynamicFwds[key] == cmd {
			delete(c.dynamicFwds, key)
		}
		c.mu.Unlock()
	}()

	return nil
}

func (c *Connection) CancelForward(localPort, remotePort int) error {
	key := fmt.Sprintf("L:%d", localPort)
	c.mu.Lock()
	cmd, ok := c.dynamicFwds[key]
	if ok {
		delete(c.dynamicFwds, key)
	}
	c.mu.Unlock()

	if !ok {
		return fmt.Errorf("no forward for port %d", localPort)
	}
	cmd.Process.Kill()
	cmd.Wait()
	return nil
}

func (c *Connection) ReverseForward(remotePort, localPort int) error {
	key := fmt.Sprintf("R:%d", remotePort)
	c.mu.Lock()
	if existing, ok := c.dynamicFwds[key]; ok {
		existing.Process.Kill()
		existing.Wait()
		delete(c.dynamicFwds, key)
	}
	c.mu.Unlock()

	cmd := exec.Command("ssh",
		"-N",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ExitOnForwardFailure=yes",
		"-R", fmt.Sprintf("%d:localhost:%d", remotePort, localPort),
		c.host,
	)
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("reverse forward ssh start: %w", err)
	}

	c.mu.Lock()
	c.dynamicFwds[key] = cmd
	c.mu.Unlock()

	go func() {
		cmd.Wait()
		c.mu.Lock()
		if c.dynamicFwds[key] == cmd {
			delete(c.dynamicFwds, key)
		}
		c.mu.Unlock()
	}()

	return nil
}

func (c *Connection) CancelReverseForward(remotePort, localPort int) error {
	key := fmt.Sprintf("R:%d", remotePort)
	c.mu.Lock()
	cmd, ok := c.dynamicFwds[key]
	if ok {
		delete(c.dynamicFwds, key)
	}
	c.mu.Unlock()

	if !ok {
		return fmt.Errorf("no reverse forward for port %d", remotePort)
	}
	cmd.Process.Kill()
	cmd.Wait()
	return nil
}

func (c *Connection) stopDynamicForwards() {
	for key, cmd := range c.dynamicFwds {
		cmd.Process.Kill()
		cmd.Wait()
		delete(c.dynamicFwds, key)
	}
}

func (c *Connection) SetAutoReconnect(on bool) {
	c.mu.Lock()
	c.autoReconnect = on
	c.mu.Unlock()
}

func (c *Connection) AutoReconnect() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.autoReconnect
}

func (c *Connection) watchAndReconnect() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			if !c.IsAlive() {
				c.mu.Lock()
				if c.stopped {
					c.mu.Unlock()
					return
				}
				autoReconn := c.autoReconnect
				c.mu.Unlock()

				if !autoReconn {
					c.sendEvent("connection lost (auto-reconnect off)")
					Notify("baton", "Connection lost")
					continue
				}

				c.sendEvent("connection lost, reconnecting...")
				Notify("baton", "Connection lost, reconnecting...")

				for attempt := 1; attempt <= 10; attempt++ {
					select {
					case <-c.stopCh:
						return
					default:
					}

					c.mu.Lock()
					if !c.autoReconnect {
						c.mu.Unlock()
						c.sendEvent("auto-reconnect disabled, stopping retries")
						break
					}
					c.mu.Unlock()

					if err := c.reconnect(); err == nil {
						c.StartTime = time.Now()
						c.sendEvent("reconnected")
						Notify("baton", "Reconnected")
						break
					}
					c.sendEvent(fmt.Sprintf("reconnect attempt %d/10 failed", attempt))
					time.Sleep(time.Duration(attempt) * 2 * time.Second)
				}
			}
		}
	}
}

func (c *Connection) reconnect() error {
	c.stopMonitor()
	c.mu.Lock()
	c.stopDynamicForwards()
	c.mu.Unlock()

	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}

	args := c.buildSSHArgs()
	c.cmd = exec.Command("ssh", args...)
	c.cmd.Stderr = nil

	if err := c.cmd.Start(); err != nil {
		return err
	}

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if c.IsAlive() {
			return nil
		}
	}

	c.cmd.Process.Kill()
	return fmt.Errorf("reconnect timed out")
}

func (c *Connection) ensureInbox() error {
	_, err := c.RunRemoteDirect(fmt.Sprintf("mkdir -p %s", c.cfg.Transfer.Inbox))
	return err
}
