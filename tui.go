package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	accentColor    = lipgloss.Color("63")
	greenColor     = lipgloss.Color("42")
	redColor       = lipgloss.Color("196")
	dimColor       = lipgloss.Color("241")
	sparkUpColor   = lipgloss.Color("39")
	sparkDownColor = lipgloss.Color("78")
	headerColor    = lipgloss.Color("255")
	yellowColor    = lipgloss.Color("220")

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(headerColor)
	dimStyle    = lipgloss.NewStyle().Foreground(dimColor)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor)
	greenStyle  = lipgloss.NewStyle().Foreground(greenColor)
	redStyle    = lipgloss.NewStyle().Foreground(redColor)
	yellowStyle = lipgloss.NewStyle().Foreground(yellowColor)

	sectionTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			PaddingLeft(1)

	portStyle = lipgloss.NewStyle().
			PaddingLeft(3)
)

type logEntry struct {
	Time      time.Time
	Message   string
	Direction string // "→" outbound, "←" inbound
}

type transferEntry struct {
	Time       time.Time
	Filename   string
	RemotePath string
	Size       int64
}

type model struct {
	connected   bool
	host        string
	localHost   string
	connectedAt time.Time

	autoReconnect bool
	conn          *Connection

	reverseTunnels []PortInfo
	localForwards  []PortInfo
	scanner        *PortScanner

	logEntries  []logEntry
	logViewport viewport.Model

	uploadSamples   []float64
	downloadSamples []float64
	portTraffic     map[int]PortTrafficInfo

	inboxPath       string
	recentTransfers []transferEntry
	transferer      *Transferer

	sendMode  bool
	sendInput string

	width  int
	height int

	connEvents       <-chan ConnEventMsg
	portEvents       <-chan PortEventMsg
	transferEvents   <-chan TransferDoneMsg
	throughputEvents <-chan ThroughputMsg
	portTrafficEvents <-chan PortTrafficMsg
}

func newModel(cfg *Config, conn *Connection, scanner *PortScanner, transferer *Transferer, throughput *ThroughputMonitor) model {
	vp := viewport.New(80, 10)
	vp.SetContent("")

	localName, _ := os.Hostname()
	if localName == "" {
		localName = "local"
	}

	remoteHost := conn.host
	if at := strings.Index(remoteHost, "@"); at >= 0 {
		remoteHost = remoteHost[at+1:]
	}
	if colon := strings.Index(remoteHost, ":"); colon >= 0 {
		remoteHost = remoteHost[:colon]
	}

	return model{
		connected:   true,
		host:        conn.host,
		localHost:   shortName(localName),
		connectedAt: conn.StartTime,

		autoReconnect: true,
		conn:          conn,

		reverseTunnels: []PortInfo{
			{Port: cfg.Connection.ReversePort, Process: "ssh-reverse"},
		},
		scanner:    scanner,
		transferer: transferer,

		logViewport: vp,
		logEntries: []logEntry{
			{Time: time.Now(), Message: "connected to " + conn.host, Direction: "←"},
		},

		portTraffic: make(map[int]PortTrafficInfo),
		inboxPath:   cfg.Transfer.Inbox,

		connEvents:        conn.Events(),
		portEvents:        scanner.Events(),
		transferEvents:    transferer.Events(),
		throughputEvents:  throughput.Events(),
		portTrafficEvents: throughput.PortEvents(),
	}
}

func waitForConnEvent(ch <-chan ConnEventMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitForPortEvent(ch <-chan PortEventMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitForTransferEvent(ch <-chan TransferDoneMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitForThroughput(ch <-chan ThroughputMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func waitForPortTraffic(ch <-chan PortTrafficMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

type sendResultMsg struct {
	filename   string
	remotePath string
	err        error
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		waitForConnEvent(m.connEvents),
		waitForPortEvent(m.portEvents),
		waitForTransferEvent(m.transferEvents),
		waitForThroughput(m.throughputEvents),
		waitForPortTraffic(m.portTrafficEvents),
		tickCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.sendMode {
			return m.updateSendMode(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.autoReconnect = !m.autoReconnect
			m.conn.SetAutoReconnect(m.autoReconnect)
			state := "on"
			if !m.autoReconnect {
				state = "off"
			}
			m.addLog(time.Now(), "auto-reconnect "+state, "")
			return m, nil
		case "s":
			m.sendMode = true
			m.sendInput = ""
			return m, nil
		}
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		return m, cmd

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		return m, nil

	case TickMsg:
		m.localForwards = m.scanner.ActivePortInfos()
		return m, tickCmd()

	case ConnEventMsg:
		dir := "←"
		if strings.Contains(msg.Message, "reconnect") {
			dir = "→"
		}
		m.addLog(msg.Time, msg.Message, dir)
		if strings.Contains(msg.Message, "reconnected") || strings.Contains(msg.Message, "connected to") {
			m.connected = true
			m.connectedAt = msg.Time
		} else if strings.Contains(msg.Message, "lost") {
			m.connected = false
		}
		return m, waitForConnEvent(m.connEvents)

	case PortEventMsg:
		label := msg.Process
		if label == "" {
			label = "unknown"
		}
		dir := "→"
		if msg.Action == "removed" {
			dir = "←"
		}
		m.addLog(msg.Time, fmt.Sprintf("port %d %s (%s)", msg.Port, msg.Action, label), dir)
		m.localForwards = m.scanner.ActivePortInfos()
		return m, waitForPortEvent(m.portEvents)

	case TransferDoneMsg:
		if msg.Err != nil {
			m.addLog(msg.Time, fmt.Sprintf("transfer failed: %s: %v", msg.Filename, msg.Err), "✕")
		} else {
			m.addLog(msg.Time, fmt.Sprintf("sent %s → %s", msg.Filename, msg.RemotePath), "→")
			m.recentTransfers = append(m.recentTransfers, transferEntry{
				Time:       msg.Time,
				Filename:   msg.Filename,
				RemotePath: msg.RemotePath,
				Size:       msg.Size,
			})
			if len(m.recentTransfers) > 10 {
				m.recentTransfers = m.recentTransfers[len(m.recentTransfers)-10:]
			}
		}
		return m, waitForTransferEvent(m.transferEvents)

	case ThroughputMsg:
		m.uploadSamples = appendRing(m.uploadSamples, msg.Upload, 60)
		m.downloadSamples = appendRing(m.downloadSamples, msg.Download, 60)
		return m, waitForThroughput(m.throughputEvents)

	case PortTrafficMsg:
		for port, info := range msg.Ports {
			m.portTraffic[port] = info
		}
		return m, waitForPortTraffic(m.portTrafficEvents)

	case sendResultMsg:
		if msg.err != nil {
			m.addLog(time.Now(), fmt.Sprintf("send failed: %s: %v", msg.filename, msg.err), "✕")
		}
		return m, nil
	}

	return m, nil
}

func (m model) updateSendMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.sendMode = false
		m.sendInput = ""
		return m, nil
	case "enter":
		path := strings.TrimSpace(m.sendInput)
		m.sendMode = false
		m.sendInput = ""
		if path == "" {
			return m, nil
		}
		t := m.transferer
		inbox := m.inboxPath
		return m, func() tea.Msg {
			_, err := t.Transfer(path, inbox)
			return sendResultMsg{filename: path, err: err}
		}
	case "backspace":
		if len(m.sendInput) > 0 {
			m.sendInput = m.sendInput[:len(m.sendInput)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 || msg.String() == " " {
			m.sendInput += msg.String()
		}
		return m, nil
	}
}

func (m *model) addLog(t time.Time, msg string, direction string) {
	if direction == "" {
		direction = " "
	}
	m.logEntries = append(m.logEntries, logEntry{Time: t, Message: msg, Direction: direction})
	if len(m.logEntries) > 200 {
		m.logEntries = m.logEntries[len(m.logEntries)-200:]
	}
	m.updateViewport()
}

func (m *model) updateViewport() {
	var lines []string
	for _, e := range m.logEntries {
		ts := dimStyle.Render(e.Time.Format("15:04:05"))
		dir := dimStyle.Render(e.Direction)
		if e.Direction == "→" {
			dir = greenStyle.Render("→")
		} else if e.Direction == "←" {
			dir = accentStyle.Render("←")
		} else if e.Direction == "✕" {
			dir = redStyle.Render("✕")
		}
		lines = append(lines, fmt.Sprintf("  %s %s %s", ts, dir, e.Message))
	}
	content := strings.Join(lines, "\n")
	m.logViewport.SetContent(content)
	m.logViewport.GotoBottom()
}

func (m *model) recalcLayout() {
	// Header(1) + blank(1) + Network(3) + blank(1) + bottom panes(rest) + blank(1) + footer(1)
	bottomHeight := m.height - 8
	if bottomHeight < 4 {
		bottomHeight = 4
	}
	logWidth := (m.width / 3) - 2
	if logWidth < 20 {
		logWidth = 20
	}
	m.logViewport.Width = logWidth
	m.logViewport.Height = bottomHeight - 1
	m.updateViewport()
}

func (m model) View() string {
	if m.width == 0 {
		return "initializing..."
	}

	var sections []string
	sections = append(sections, m.renderHeader())
	sections = append(sections, "")
	sections = append(sections, m.renderNetwork())
	sections = append(sections, "")
	sections = append(sections, m.renderBottomPanes())
	sections = append(sections, "")
	sections = append(sections, m.renderFooter())

	return strings.Join(sections, "\n")
}

func (m model) renderHeader() string {
	dot := greenStyle.Render("●")
	status := "connected"
	if !m.connected {
		dot = redStyle.Render("●")
		status = "disconnected"
	}

	uptime := ""
	if m.connected && !m.connectedAt.IsZero() {
		d := time.Since(m.connectedAt)
		uptime = fmt.Sprintf(" ── uptime %s", formatDuration(d))
	}

	left := headerStyle.Render(" baton") + " " + dot + " " + dimStyle.Render(status)
	host := dimStyle.Render(m.host)
	up := dimStyle.Render(uptime)

	reconnLabel := greenStyle.Render("auto-reconnect on")
	if !m.autoReconnect {
		reconnLabel = yellowStyle.Render("auto-reconnect off")
	}

	leftPart := left + " ── " + host + up
	rightPart := reconnLabel + "  " + dimStyle.Render("q quit")
	gap := m.width - lipgloss.Width(leftPart) - lipgloss.Width(rightPart)
	if gap < 2 {
		gap = 2
	}

	return leftPart + strings.Repeat(" ", gap) + rightPart
}

func (m model) renderNetwork() string {
	sparkWidth := m.width - 18
	if sparkWidth < 10 {
		sparkWidth = 10
	}

	upRate := formatBytes(lastSample(m.uploadSamples))
	downRate := formatBytes(lastSample(m.downloadSamples))

	upSpark := lipgloss.NewStyle().Foreground(sparkUpColor).Render(
		renderSparkline(m.uploadSamples, sparkWidth))
	downSpark := lipgloss.NewStyle().Foreground(sparkDownColor).Render(
		renderSparkline(m.downloadSamples, sparkWidth))

	header := sectionTitle.Render("NETWORK")
	up := portStyle.Render(fmt.Sprintf("↑ %8s %s", upRate, upSpark))
	down := portStyle.Render(fmt.Sprintf("↓ %8s %s", downRate, downSpark))

	return header + "\n" + up + "\n" + down
}

func (m model) renderBottomPanes() string {
	colWidth := m.width / 3
	if colWidth < 20 {
		colWidth = 20
	}

	bottomHeight := m.height - 8
	if bottomHeight < 4 {
		bottomHeight = 4
	}

	activity := m.renderActivity(colWidth, bottomHeight)
	localPorts := m.renderLocalPorts(colWidth, bottomHeight)
	remotePorts := m.renderRemotePorts(colWidth, bottomHeight)

	return lipgloss.JoinHorizontal(lipgloss.Top, activity, localPorts, remotePorts)
}

func (m model) renderActivity(width, height int) string {
	header := sectionTitle.Render("ACTIVITY")
	content := header + "\n" + m.logViewport.View()
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m model) renderLocalPorts(width, height int) string {
	header := sectionTitle.Render(fmt.Sprintf("%s → %s", shortName(m.host), m.localHost))
	var lines []string
	if len(m.localForwards) == 0 {
		lines = append(lines, portStyle.Render(dimStyle.Render("scanning...")))
	}
	for _, p := range m.localForwards {
		port := greenStyle.Render(fmt.Sprintf(":%d", p.Port))
		proc := p.Process
		if proc == "" {
			proc = "(unknown)"
		}
		proc = dimStyle.Render(proc)

		traffic := ""
		if t, ok := m.portTraffic[p.Port]; ok {
			total := t.Upload + t.Download
			if total > 0 {
				traffic = " " + dimStyle.Render(formatBytes(total))
			}
		}

		lines = append(lines, portStyle.Render(fmt.Sprintf("→ %s  %s%s", port, proc, traffic)))
	}

	content := header + "\n" + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m model) renderRemotePorts(width, height int) string {
	header := sectionTitle.Render(fmt.Sprintf("%s ← %s", m.localHost, shortName(m.host)))
	var lines []string
	if len(m.reverseTunnels) == 0 {
		lines = append(lines, portStyle.Render(dimStyle.Render("none")))
	}
	for _, p := range m.reverseTunnels {
		port := accentStyle.Render(fmt.Sprintf(":%d", p.Port))
		proc := dimStyle.Render(p.Process)
		lines = append(lines, portStyle.Render(fmt.Sprintf("← %s  %s", port, proc)))
	}

	content := header + "\n" + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m model) renderFooter() string {
	if m.sendMode {
		cursor := accentStyle.Render("█")
		return "  " + accentStyle.Render("send:") + " " + m.sendInput + cursor + "  " + dimStyle.Render("enter send  esc cancel")
	}
	return dimStyle.Render("  ↑↓ scroll  r reconnect  s send  q quit")
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	min := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, min)
	}
	if min > 0 {
		return fmt.Sprintf("%dm%02ds", min, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

func lastSample(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	return samples[len(samples)-1]
}

func shortName(host string) string {
	if at := strings.Index(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if colon := strings.Index(host, ":"); colon >= 0 {
		host = host[:colon]
	}
	if dot := strings.Index(host, "."); dot >= 0 {
		host = host[:dot]
	}
	return host
}
