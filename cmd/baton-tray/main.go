//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"github.com/sqweek/dialog"
)

var (
	controlSocket string
	host          string
	mStatus       *systray.MenuItem
	mReconnect    *systray.MenuItem
)

func main() {
	controlSocket = "/tmp/baton.sock"
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetTitle("⚡")
	systray.SetTooltip("baton")

	mStatus = systray.AddMenuItem("Not connected", "Connection status")
	mStatus.Disable()

	systray.AddSeparator()

	mSend := systray.AddMenuItem("Send File...", "Upload a file to remote")
	mForward := systray.AddMenuItem("Forward Port...", "Forward a local port")

	systray.AddSeparator()

	mReconnect = systray.AddMenuItem("Auto-reconnect: on", "Toggle auto-reconnect")

	systray.AddSeparator()

	mPorts := systray.AddMenuItem("Forwarded Ports", "Currently forwarded ports")
	mPorts.Disable()

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit baton tray")

	go pollStatus()

	go func() {
		for {
			select {
			case <-mSend.ClickedCh:
				handleSend()
			case <-mForward.ClickedCh:
				handleForward()
			case <-mReconnect.ClickedCh:
				handleReconnectToggle()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onExit() {}

func isConnected() bool {
	if host == "" {
		host = detectHost()
	}
	if host == "" {
		return false
	}
	cmd := exec.Command("ssh", "-S", controlSocket, "-O", "check", host)
	return cmd.Run() == nil
}

func detectHost() string {
	data, err := os.ReadFile(controlSocket + ".host")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func pollStatus() {
	for {
		if isConnected() {
			systray.SetTitle("🔗")
			mStatus.SetTitle("🟢 Connected: " + host)

			ports := getForwardedPorts()
			if len(ports) > 0 {
				mStatus.SetTitle(fmt.Sprintf("🟢 Connected: %s (%d ports)", host, len(ports)))
			}
		} else {
			systray.SetTitle("⚡")
			mStatus.SetTitle("🔴 Disconnected")
		}
		time.Sleep(5 * time.Second)
	}
}

func getForwardedPorts() []int {
	data, err := os.ReadFile(controlSocket + ".ports")
	if err != nil {
		return nil
	}
	var ports []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if p, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			ports = append(ports, p)
		}
	}
	return ports
}

func handleSend() {
	path, err := dialog.File().Title("Send file to remote").Load()
	if err != nil {
		return
	}

	cmd := exec.Command("baton", "send", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		dialog.Message("Upload failed: %s\n%s", err, string(output)).Error()
		return
	}

	remotePath := strings.TrimSpace(string(output))
	dialog.Message("Uploaded %s\n→ %s", filepath.Base(path), remotePath).Info()
}

func handleForward() {
	portStr, err := dialog.Message("Enter port number to forward:").Title("Forward Port").YesNo()
	if err || !portStr {
		return
	}
	// dialog doesn't have text input, so we use a simpler approach
	// User can use the TUI 'f' key for interactive port forwarding
	dialog.Message("Use 'f' key in the baton TUI to forward ports interactively,\nor add ports to ~/.baton.toml under [ports] extra = [...]").Info()
}

func handleReconnectToggle() {
	// Toggle is tracked in the TUI process; tray shows current state
	dialog.Message("Toggle auto-reconnect from the baton TUI with the 'r' key.").Info()
}
