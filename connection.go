package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Connection struct {
	cfg            *Config
	host           string
	cmd            *exec.Cmd
	mu             sync.Mutex
	stopCh         chan struct{}
	stopped        bool
	autoReconnect  bool
	events         chan ConnEventMsg
	StartTime      time.Time
	reversePorts   []int
	localPorts     []int
}

func NewConnection(cfg *Config, host string, reversePorts []int, localPorts []int) *Connection {
	return &Connection{
		cfg:           cfg,
		host:          host,
		autoReconnect: true,
		stopCh:        make(chan struct{}),
		events:        make(chan ConnEventMsg, 32),
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
		return fmt.Errorf("already connected (socket %s exists)", c.cfg.Connection.ControlSocket)
	}

	if err := c.ensureInbox(); err != nil {
		c.sendEvent(fmt.Sprintf("warning: could not create inbox: %v", err))
	}

	args := []string{
		"-M",
		"-S", c.cfg.Connection.ControlSocket,
		"-N",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		"-R", fmt.Sprintf("%d:localhost:22", c.cfg.Connection.ReversePort),
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

func (c *Connection) IsAlive() bool {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "check",
		c.host,
	)
	return cmd.Run() == nil
}

func (c *Connection) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped {
		return nil
	}
	c.stopped = true
	close(c.stopCh)
	os.Remove(c.cfg.Connection.ControlSocket + ".host")

	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "exit",
		c.host,
	)
	if err := cmd.Run(); err != nil {
		if c.cmd != nil && c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
		return fmt.Errorf("ssh exit: %w", err)
	}
	return nil
}

// RunRemote executes a command on the remote host via the control socket.
// This contends with forwarded data channels; prefer RunRemoteDirect for
// periodic monitoring commands.
func (c *Connection) RunRemote(command string) ([]byte, error) {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		c.host,
		command,
	)
	return cmd.Output()
}

// RunRemoteDirect executes a command on the remote host over a fresh SSH
// connection that bypasses the ControlMaster socket entirely. Use this for
// periodic monitoring (throughput sampling, port scanning) so that control
// socket traffic does not stall forwarded data channels under high concurrency.
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
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "forward",
		"-L", fmt.Sprintf("0.0.0.0:%d:localhost:%d", localPort, remotePort),
		c.host,
	)
	return cmd.Run()
}

func (c *Connection) CancelForward(localPort, remotePort int) error {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "cancel",
		"-L", fmt.Sprintf("0.0.0.0:%d:localhost:%d", localPort, remotePort),
		c.host,
	)
	return cmd.Run()
}

func (c *Connection) ReverseForward(remotePort, localPort int) error {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "forward",
		"-R", fmt.Sprintf("%d:localhost:%d", remotePort, localPort),
		c.host,
	)
	return cmd.Run()
}

func (c *Connection) CancelReverseForward(remotePort, localPort int) error {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "cancel",
		"-R", fmt.Sprintf("%d:localhost:%d", remotePort, localPort),
		c.host,
	)
	return cmd.Run()
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
	os.Remove(c.cfg.Connection.ControlSocket)

	args := []string{
		"-M",
		"-S", c.cfg.Connection.ControlSocket,
		"-N",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		"-R", fmt.Sprintf("%d:localhost:22", c.cfg.Connection.ReversePort),
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
	_, err := c.RunRemote(fmt.Sprintf("mkdir -p %s", c.cfg.Transfer.Inbox))
	return err
}
