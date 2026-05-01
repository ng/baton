package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Connection struct {
	cfg           *Config
	host          string
	cmd           *exec.Cmd
	ctrlCmd       *exec.Cmd
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

	if err := c.ensureInbox(); err != nil {
		c.sendEvent(fmt.Sprintf("warning: could not create inbox: %v", err))
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
			if err := c.startControl(); err != nil {
				c.sendEvent(fmt.Sprintf("warning: control connection failed: %v", err))
			}
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
		"-o", "ControlPath=none",
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
	return args
}

// startControl starts a second SSH connection with ControlMaster for
// dynamic forward management and remote command execution. This keeps
// the main data connection free of control socket overhead while avoiding
// the need to spawn fresh SSH processes for every monitoring call or
// dynamic port forward.
func (c *Connection) startControl() error {
	os.Remove(c.cfg.Connection.ControlSocket)
	args := []string{
		"-M",
		"-S", c.cfg.Connection.ControlSocket,
		"-N",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		c.host,
	}
	c.ctrlCmd = exec.Command("ssh", args...)
	c.ctrlCmd.Stderr = nil
	if err := c.ctrlCmd.Start(); err != nil {
		return fmt.Errorf("control ssh start: %w", err)
	}

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if c.ctrlAlive() {
			return nil
		}
	}

	c.ctrlCmd.Process.Kill()
	c.ctrlCmd = nil
	return fmt.Errorf("control connection timed out")
}

func (c *Connection) ctrlAlive() bool {
	cmd := exec.Command("ssh",
		"-S", c.cfg.Connection.ControlSocket,
		"-O", "check",
		c.host,
	)
	return cmd.Run() == nil
}

func (c *Connection) stopControl() {
	if c.ctrlCmd != nil && c.ctrlCmd.Process != nil {
		exit := exec.Command("ssh",
			"-S", c.cfg.Connection.ControlSocket,
			"-O", "exit",
			c.host,
		)
		if exit.Run() != nil {
			c.ctrlCmd.Process.Kill()
		}
		c.ctrlCmd.Wait()
		c.ctrlCmd = nil
	}
	os.Remove(c.cfg.Connection.ControlSocket)
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
	c.stopControl()
	os.Remove(c.cfg.Connection.ControlSocket + ".host")

	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
	return nil
}

// RunRemote executes a command on the remote host via the control socket.
func (c *Connection) RunRemote(command string) ([]byte, error) {
	if c.ctrlAlive() {
		cmd := exec.Command("ssh",
			"-S", c.cfg.Connection.ControlSocket,
			c.host,
			command,
		)
		return cmd.Output()
	}
	return c.RunRemoteDirect(command)
}

// RunRemoteDirect executes a command on the remote host over a fresh SSH
// connection, bypassing the control socket. Use only when the control
// connection is unavailable.
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
	c.stopControl()

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
			c.startControl()
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
