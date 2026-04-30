package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Connection struct {
	cfg       *Config
	host      string
	cmd       *exec.Cmd
	mu        sync.Mutex
	stopCh    chan struct{}
	stopped   bool
	events    chan ConnEventMsg
	StartTime time.Time
}

func NewConnection(cfg *Config, host string) *Connection {
	return &Connection{
		cfg:    cfg,
		host:   host,
		stopCh: make(chan struct{}),
		events: make(chan ConnEventMsg, 32),
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
		"-o", "ExitOnForwardFailure=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-R", fmt.Sprintf("%d:localhost:22", c.cfg.Connection.ReversePort),
		c.host,
	}

	c.cmd = exec.Command("ssh", args...)
	c.cmd.Stderr = nil

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("ssh start: %w", err)
	}

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if c.IsAlive() {
			c.StartTime = time.Now()
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

func (c *Connection) RunRemote(command string) ([]byte, error) {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		c.host,
		command,
	)
	return cmd.Output()
}

func (c *Connection) Forward(localPort, remotePort int) error {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "forward",
		"-L", fmt.Sprintf("%d:localhost:%d", localPort, remotePort),
		c.host,
	)
	return cmd.Run()
}

func (c *Connection) CancelForward(localPort, remotePort int) error {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "cancel",
		"-L", fmt.Sprintf("%d:localhost:%d", localPort, remotePort),
		c.host,
	)
	return cmd.Run()
}

func (c *Connection) watchAndReconnect() {
	ticker := time.NewTicker(5 * time.Second)
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
				c.mu.Unlock()

				c.sendEvent("connection lost, reconnecting...")
				Notify("baton", "Connection lost, reconnecting...")

				for attempt := 1; attempt <= 10; attempt++ {
					select {
					case <-c.stopCh:
						return
					default:
					}

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
		"-o", "ExitOnForwardFailure=yes",
		"-R", fmt.Sprintf("%d:localhost:22", c.cfg.Connection.ReversePort),
		c.host,
	}

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
