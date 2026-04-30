package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Connection struct {
	cfg     *Config
	host    string
	cmd     *exec.Cmd
	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

func NewConnection(cfg *Config, host string) *Connection {
	return &Connection{
		cfg:    cfg,
		host:   host,
		stopCh: make(chan struct{}),
	}
}

func (c *Connection) Start() error {
	if c.IsAlive() {
		return fmt.Errorf("already connected (socket %s exists)", c.cfg.Connection.ControlSocket)
	}

	if err := c.ensureInbox(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not create inbox: %v\n", err)
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
	c.cmd.Stderr = os.Stderr

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("ssh start: %w", err)
	}

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if c.IsAlive() {
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

				fmt.Fprintln(os.Stderr, "connection lost, reconnecting...")
				Notify("shuttle", "Connection lost, reconnecting...")

				for attempt := 1; attempt <= 10; attempt++ {
					select {
					case <-c.stopCh:
						return
					default:
					}

					if err := c.reconnect(); err == nil {
						fmt.Fprintln(os.Stderr, "reconnected.")
						Notify("shuttle", "Reconnected")
						break
					}
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
	c.cmd.Stderr = os.Stderr

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
