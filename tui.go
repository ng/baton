package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	outColor    = lipgloss.Color("39")
	inColor     = lipgloss.Color("78")
	redColor    = lipgloss.Color("196")
	dimColor    = lipgloss.Color("241")
	headerColor = lipgloss.Color("255")
	yellowColor = lipgloss.Color("220")
	greenColor  = lipgloss.Color("42")
	activeColor = lipgloss.Color("220")

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(headerColor)
	dimStyle    = lipgloss.NewStyle().Foreground(dimColor)
	outStyle    = lipgloss.NewStyle().Foreground(outColor)
	inStyle     = lipgloss.NewStyle().Foreground(inColor)
	greenStyle  = lipgloss.NewStyle().Foreground(greenColor)
	redStyle    = lipgloss.NewStyle().Foreground(redColor)
	yellowStyle = lipgloss.NewStyle().Foreground(yellowColor)
	activeStyle = lipgloss.NewStyle().Foreground(activeColor)

	accentColor = lipgloss.Color("63")

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
	Direction string
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
	preset      string
	cfg         *Config

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

	fwdMode  bool
	fwdInput string

	pasteBuffer string
	pasting     bool

	portFocus    int // 0=none, 1=local forwards, 2=reverse tunnels
	portSelected int

	userScrolled bool

	width  int
	height int

	connEvents        <-chan ConnEventMsg
	portEvents        <-chan PortEventMsg
	transferEvents    <-chan TransferDoneMsg
	throughputEvents  <-chan ThroughputMsg
	portTrafficEvents <-chan PortTrafficMsg
}

func newModel(cfg *Config, conn *Connection, scanner *PortScanner, transferer *Transferer, throughput *ThroughputMonitor, preset string, reverseInfos []PortInfo) model {
	vp := viewport.New(80, 10)
	vp.SetContent("")

	localName, _ := os.Hostname()
	if localName == "" {
		localName = "local"
	}

	tunnels := []PortInfo{
		{Port: cfg.Connection.ReversePort, Process: "ssh"},
	}
	tunnels = append(tunnels, reverseInfos...)

	now := time.Now()
	entries := []logEntry{
		{Time: now, Message: "connecting to " + conn.host + "...", Direction: "🔄"},
	}

	return model{
		connected:   false,
		host:        conn.host,
		localHost:   shortName(localName),
		preset:      preset,
		cfg:         cfg,

		autoReconnect: true,
		conn:          conn,

		reverseTunnels: tunnels,
		scanner:        scanner,
		transferer:     transferer,

		logViewport: vp,
		logEntries:  entries,

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

type fwdResultMsg struct {
	port int
	err  error
}

type disconnectResultMsg struct {
	port    int
	reverse bool
	err     error
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
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.Paste {
			m.pasting = true
			m.pasteBuffer += msg.String()
			return m, nil
		}
		if m.pasting {
			m.pasting = false
			path := strings.TrimSpace(m.pasteBuffer)
			m.pasteBuffer = ""
			if looksLikeFilePath(path) {
				m.addLog(time.Now(), "uploading "+filepath.Base(path)+"...", "→")
				t := m.transferer
				inbox := m.inboxPath
				return m, func() tea.Msg {
					_, err := t.Transfer(path, inbox)
					return sendResultMsg{filename: path, err: err}
				}
			}
		}
		if m.sendMode {
			return m.updateSendMode(msg)
		}
		if m.fwdMode {
			return m.updateFwdMode(msg)
		}
		if m.portFocus > 0 {
			return m.updatePortFocus(msg)
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "r":
			m.autoReconnect = !m.autoReconnect
			m.conn.SetAutoReconnect(m.autoReconnect)
			if m.autoReconnect {
				m.addLog(time.Now(), "auto-reconnect on", "🟢")
			} else {
				m.addLog(time.Now(), "auto-reconnect off", "🔴")
			}
			return m, nil
		case "s":
			m.sendMode = true
			m.sendInput = ""
			return m, nil
		case "f":
			m.fwdMode = true
			m.fwdInput = ""
			return m, nil
		case "tab":
			m.portFocus = 1
			m.portSelected = 0
			return m, nil
		case "t":
			return m, func() tea.Msg {
				exec.Command("baton-tray").Start()
				return nil
			}
		}
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		switch msg.String() {
		case "up", "k", "pgup":
			m.userScrolled = true
		case "down", "j", "pgdown", "end", "G":
			if m.logViewport.AtBottom() {
				m.userScrolled = false
			}
		}
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
		if strings.Contains(msg.Message, "connected to") {
			dir = "🟢"
		} else if strings.Contains(msg.Message, "reconnected") {
			dir = "🟢"
		} else if strings.Contains(msg.Message, "lost") {
			dir = "🔴"
		} else if strings.Contains(msg.Message, "reconnect") {
			dir = "🔄"
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
		maxSamples := m.width - 18
		if maxSamples < 60 {
			maxSamples = 60
		}
		m.uploadSamples = appendRing(m.uploadSamples, msg.Upload, maxSamples)
		m.downloadSamples = appendRing(m.downloadSamples, msg.Download, maxSamples)
		return m, waitForThroughput(m.throughputEvents)

	case PortTrafficMsg:
		for port, info := range msg.Ports {
			m.portTraffic[port] = info
		}
		return m, waitForPortTraffic(m.portTrafficEvents)

	case connectResultMsg:
		if msg.err != nil {
			m.addLog(time.Now(), fmt.Sprintf("connection failed: %v", msg.err), "🔴")
			return m, nil
		}
		now := time.Now()
		m.connected = true
		m.connectedAt = now
		m.addLog(now, "connected to "+m.host, "🟢")
		if m.preset != "" {
			if p, ok := m.cfg.Presets[m.preset]; ok {
				var parts []string
				for _, port := range p.Reverse {
					rp := port
					if port == 443 {
						rp = 4443
					}
					parts = append(parts, fmt.Sprintf(":%d", rp))
				}
				for _, port := range p.Ports {
					parts = append(parts, fmt.Sprintf(":%d", port))
				}
				if len(parts) > 0 {
					m.addLog(now, fmt.Sprintf("preset %s: %s", m.preset, strings.Join(parts, " ")), "→")
				}
			}
		}
		for _, info := range msg.reverseInfos {
			m.reverseTunnels = append(m.reverseTunnels, info)
			desc := info.Label
			if desc == "" {
				desc = "preset"
			}
			m.addLog(now, fmt.Sprintf("reverse :%d forwarded (%s)", info.Port, desc), "←")
		}
		return m, nil

	case sendResultMsg:
		if msg.err != nil {
			m.addLog(time.Now(), fmt.Sprintf("send failed: %s: %v", msg.filename, msg.err), "✕")
		}
		return m, nil

	case fwdResultMsg:
		if msg.err != nil {
			m.addLog(time.Now(), fmt.Sprintf("forward :%d failed: %v", msg.port, msg.err), "✕")
		} else {
			m.addLog(time.Now(), fmt.Sprintf("forwarding local :%d", msg.port), "→")
		}
		return m, nil

	case disconnectResultMsg:
		if msg.err != nil {
			m.addLog(time.Now(), fmt.Sprintf("disconnect :%d failed: %v", msg.port, msg.err), "✕")
		} else {
			m.addLog(time.Now(), fmt.Sprintf("disconnected :%d", msg.port), "←")
			if msg.reverse {
				for i, p := range m.reverseTunnels {
					if p.Port == msg.port {
						m.reverseTunnels = append(m.reverseTunnels[:i], m.reverseTunnels[i+1:]...)
						break
					}
				}
			}
		}
		if m.portSelected >= m.portListLen() && m.portSelected > 0 {
			m.portSelected--
		}
		return m, nil

	case sweepResultMsg:
		if len(msg.swept) > 0 {
			m.addLog(time.Now(), fmt.Sprintf("swept %d stale port(s)", len(msg.swept)), "←")
			m.localForwards = m.scanner.ActivePortInfos()
			if m.portSelected >= m.portListLen() && m.portSelected > 0 {
				m.portSelected--
			}
		} else {
			m.addLog(time.Now(), "no stale ports to sweep", " ")
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

func (m model) updateFwdMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.fwdMode = false
		m.fwdInput = ""
		return m, nil
	case "enter":
		input := strings.TrimSpace(m.fwdInput)
		m.fwdMode = false
		m.fwdInput = ""
		if input == "" {
			return m, nil
		}
		port := 0
		fmt.Sscanf(input, "%d", &port)
		if port <= 0 || port > 65535 {
			m.addLog(time.Now(), fmt.Sprintf("invalid port: %s", input), "✕")
			return m, nil
		}
		c := m.conn
		p := port
		return m, func() tea.Msg {
			err := c.Forward(p, p)
			return fwdResultMsg{port: p, err: err}
		}
	case "backspace":
		if len(m.fwdInput) > 0 {
			m.fwdInput = m.fwdInput[:len(m.fwdInput)-1]
		}
		return m, nil
	default:
		ch := msg.String()
		if len(ch) == 1 && ch[0] >= '0' && ch[0] <= '9' {
			m.fwdInput += ch
		}
		return m, nil
	}
}

func (m model) updatePortFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.portFocus = 0
		m.portSelected = 0
		return m, nil
	case "tab":
		if m.portFocus == 1 {
			m.portFocus = 2
		} else {
			m.portFocus = 1
		}
		m.portSelected = 0
		return m, nil
	case "up", "k":
		if m.portSelected > 0 {
			m.portSelected--
		}
		return m, nil
	case "down", "j":
		max := m.portListLen() - 1
		if m.portSelected < max {
			m.portSelected++
		}
		return m, nil
	case "d", "x", "backspace":
		return m.disconnectSelectedPort()
	case "c":
		return m.sweepStalePorts()
	case "t":
		return m, func() tea.Msg {
			exec.Command("baton-tray").Start()
			return nil
		}
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

type sweepResultMsg struct {
	swept []int
}

func (m model) sweepStalePorts() (tea.Model, tea.Cmd) {
	if m.portFocus != 1 {
		return m, nil
	}
	s := m.scanner
	return m, func() tea.Msg {
		swept := s.SweepStale()
		return sweepResultMsg{swept: swept}
	}
}

func (m model) portListLen() int {
	if m.portFocus == 1 {
		return len(m.localForwards)
	}
	return len(m.reverseTunnels)
}

func (m model) disconnectSelectedPort() (tea.Model, tea.Cmd) {
	if m.portFocus == 1 && m.portSelected < len(m.localForwards) {
		p := m.localForwards[m.portSelected]
		c := m.conn
		port := p.Port
		return m, func() tea.Msg {
			err := c.CancelForward(port, port)
			return disconnectResultMsg{port: port, reverse: false, err: err}
		}
	}
	if m.portFocus == 2 && m.portSelected < len(m.reverseTunnels) {
		p := m.reverseTunnels[m.portSelected]
		if p.Process == "ssh" {
			m.addLog(time.Now(), "cannot disconnect SSH reverse tunnel", "✕")
			return m, nil
		}
		c := m.conn
		port := p.Port
		return m, func() tea.Msg {
			err := c.CancelReverseForward(port, port)
			return disconnectResultMsg{port: port, reverse: true, err: err}
		}
	}
	return m, nil
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
		switch e.Direction {
		case "→":
			dir = outStyle.Render("→")
		case "←":
			dir = inStyle.Render("←")
		case "🔄":
			dir = yellowStyle.Render("🔄")
		case "🟢":
			dir = "🟢"
		case "🔴":
			dir = "🔴"
		case "✕":
			dir = redStyle.Render("✕")
		}
		msg := colorizeLogMessage(e.Message)
		lines = append(lines, fmt.Sprintf("  %s %s %s", ts, dir, msg))
	}
	content := strings.Join(lines, "\n")
	m.logViewport.SetContent(content)
	if !m.userScrolled {
		m.logViewport.GotoBottom()
	}
}

func (m *model) recalcLayout() {
	// Header(1) + blank(1) + Network(3) + blank(1) + bottom panes(rest) + blank(1) + footer(1)
	bottomHeight := m.height - 8
	if bottomHeight < 4 {
		bottomHeight = 4
	}
	activityWidth := (m.width * 3 / 5) - 1
	if activityWidth < 30 {
		activityWidth = 30
	}
	m.logViewport.Width = activityWidth
	m.logViewport.Height = bottomHeight - 1
	m.updateViewport()
}

func (m model) View() string {
	if m.width == 0 {
		return "initializing..."
	}

	var sections []string
	sections = append(sections, m.renderHeader())
	sections = append(sections, dimStyle.Render(strings.Repeat("─", m.width)))
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
	host := outStyle.Render(m.host)
	up := dimStyle.Render(uptime)

	reconnLabel := greenStyle.Render("🔗 auto-reconnect")
	if !m.autoReconnect {
		reconnLabel = yellowStyle.Render("⚡ auto-reconnect off")
	}

	leftPart := left + " ── " + host + up
	rightPart := reconnLabel
	gap := m.width - lipgloss.Width(leftPart) - lipgloss.Width(rightPart)
	if gap < 2 {
		gap = 2
	}

	return leftPart + strings.Repeat(" ", gap) + rightPart
}

func (m model) renderNetwork() string {
	sparkWidth := m.width - 20
	if sparkWidth < 10 {
		sparkWidth = 10
	}

	// Remote TX = data to Mac (user's download), Remote RX = data from Mac (user's upload)
	downRate := formatBytes(lastSample(m.uploadSamples))
	upRate := formatBytes(lastSample(m.downloadSamples))

	downSpark := lipgloss.NewStyle().Foreground(inColor).Render(
		renderSparkline(m.uploadSamples, sparkWidth))
	upSpark := lipgloss.NewStyle().Foreground(outColor).Render(
		renderSparkline(m.downloadSamples, sparkWidth))

	header := sectionTitle.Render("NETWORK")
	upLine := portStyle.Render(fmt.Sprintf("%s %10s %s", outStyle.Render("↑"), upRate, upSpark))
	downLine := portStyle.Render(fmt.Sprintf("%s %10s %s", inStyle.Render("↓"), downRate, downSpark))

	return header + "\n" + upLine + "\n" + downLine
}

func (m model) renderBottomPanes() string {
	activityWidth := m.width * 3 / 5
	portsWidth := m.width - activityWidth
	if activityWidth < 30 {
		activityWidth = 30
	}
	if portsWidth < 20 {
		portsWidth = 20
	}

	bottomHeight := m.height - 8
	if bottomHeight < 4 {
		bottomHeight = 4
	}

	activity := m.renderActivity(activityWidth, bottomHeight)
	ports := m.renderPortsColumn(portsWidth, bottomHeight)

	return lipgloss.JoinHorizontal(lipgloss.Top, activity, ports)
}

func (m model) renderActivity(width, height int) string {
	header := sectionTitle.Render("ACTIVITY")
	content := header + "\n" + m.logViewport.View()
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m model) renderPortsColumn(width, height int) string {
	localHeight := (height - 1) / 2
	remoteHeight := height - localHeight - 1

	local := m.renderLocalPorts(width, localHeight)
	remote := m.renderRemotePorts(width, remoteHeight)

	sep := dimStyle.Render(strings.Repeat("─", width))
	return lipgloss.JoinVertical(lipgloss.Left, local, sep, remote)
}

func (m model) renderLocalPorts(width, height int) string {
	focused := m.portFocus == 1
	headerArrow := outStyle.Render("→")
	if focused {
		headerArrow = headerStyle.Render("→")
	}
	header := sectionTitle.Render(shortName(m.host)) + " " + headerArrow + " " + sectionTitle.Render(m.localHost)
	var lines []string
	if len(m.localForwards) == 0 {
		lines = append(lines, portStyle.Render(dimStyle.Render("scanning...")))
	}
	for i, p := range m.localForwards {
		hasTraffic := false
		traffic := ""
		if t, ok := m.portTraffic[p.Port]; ok {
			total := t.Upload + t.Download
			if total > 0 {
				hasTraffic = true
				traffic = " " + dimStyle.Render(formatBytes(total))
			}
		}

		portStr := fmt.Sprintf(":%d", p.Port)
		desc := p.Label
		if desc == "" {
			desc = p.Process
		}
		if desc == "" {
			desc = "(unknown)"
		}
		pin := ""
		if p.Pinned {
			pin = " \U0001F4CC"
		}

		stale := ""
		if p.Stale {
			stale = " " + yellowStyle.Render("⊘")
		}

		var line string
		if hasTraffic {
			port := activeStyle.Render(portStr)
			line = fmt.Sprintf("%s%s  %s%s", port, pin, dimStyle.Render(desc), traffic)
		} else if p.Stale {
			port := dimStyle.Render(portStr)
			line = fmt.Sprintf("%s%s  %s%s", port, pin, dimStyle.Render(desc), stale)
		} else {
			port := outStyle.Render(portStr)
			line = fmt.Sprintf("%s%s  %s", port, pin, dimStyle.Render(desc))
		}

		if focused && i == m.portSelected {
			lines = append(lines, portStyle.Render(headerStyle.Render("▸ ")+line))
		} else {
			lines = append(lines, portStyle.Render("  "+line))
		}
	}

	content := header + "\n" + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m model) renderRemotePorts(width, height int) string {
	focused := m.portFocus == 2
	headerArrow := inStyle.Render("→")
	if focused {
		headerArrow = headerStyle.Render("→")
	}
	header := sectionTitle.Render(m.localHost) + " " + headerArrow + " " + sectionTitle.Render(shortName(m.host))
	var lines []string
	if len(m.reverseTunnels) == 0 {
		lines = append(lines, portStyle.Render(dimStyle.Render("none")))
	}
	for i, p := range m.reverseTunnels {
		port := inStyle.Render(fmt.Sprintf(":%d", p.Port))
		desc := p.Label
		if desc == "" {
			desc = p.Process
		}
		if desc == "" {
			desc = "preset"
		}
		pin := ""
		if p.Pinned {
			pin = " \U0001F4CC"
		}

		line := fmt.Sprintf("%s%s  %s", port, pin, dimStyle.Render(desc))
		if focused && i == m.portSelected {
			lines = append(lines, portStyle.Render(headerStyle.Render("▸ ")+line))
		} else {
			lines = append(lines, portStyle.Render("  "+line))
		}
	}

	content := header + "\n" + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Height(height).Render(content)
}

func (m model) renderFooter() string {
	if m.sendMode {
		cursor := outStyle.Render("█")
		return "  " + outStyle.Render("send:") + " " + m.sendInput + cursor + "  " + dimStyle.Render("enter send  esc cancel")
	}
	if m.fwdMode {
		cursor := outStyle.Render("█")
		return "  " + outStyle.Render("forward local port:") + " " + m.fwdInput + cursor + "  " + dimStyle.Render("enter forward  esc cancel")
	}
	if m.portFocus > 0 {
		panel := "local forwards"
		if m.portFocus == 2 {
			panel = "reverse tunnels"
		}
		return "  " + headerStyle.Render(panel) + "  " + dimStyle.Render("↑↓ select  tab switch  d disconnect  c sweep  esc back")
	}
	return dimStyle.Render("  ↑↓ scroll  tab ports  r reconnect  s send  f forward  t tray")
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

var portLogRe = regexp.MustCompile(`port (\d{2,5})\b|:(\d{2,5})\b`)

func colorizeLogMessage(msg string) string {
	return portLogRe.ReplaceAllStringFunc(msg, func(match string) string {
		if strings.HasPrefix(match, "port ") {
			num := strings.TrimPrefix(match, "port ")
			return "port " + activeStyle.Render(num)
		}
		if strings.HasPrefix(match, ":") {
			num := strings.TrimPrefix(match, ":")
			return ":" + activeStyle.Render(num)
		}
		return match
	})
}

func initialLogEntries(host, preset string, cfg *Config) []logEntry {
	now := time.Now()
	entries := []logEntry{
		{Time: now, Message: "connected to " + host, Direction: "🟢"},
	}
	if preset != "" {
		if p, ok := cfg.Presets[preset]; ok {
			var parts []string
			for _, port := range p.Reverse {
				remotePort := port
				if port == 443 {
					remotePort = 4443
				}
				parts = append(parts, fmt.Sprintf(":%d", remotePort))
			}
			for _, port := range p.Ports {
				parts = append(parts, fmt.Sprintf(":%d", port))
			}
			if len(parts) > 0 {
				entries = append(entries, logEntry{
					Time:      now,
					Message:   fmt.Sprintf("preset %s: %s", preset, strings.Join(parts, " ")),
					Direction: "→",
				})
			}
		}
	}
	if len(cfg.Ports.Extra) > 0 {
		ports := make([]string, len(cfg.Ports.Extra))
		for i, port := range cfg.Ports.Extra {
			ports[i] = fmt.Sprintf(":%d", port)
		}
		entries = append(entries, logEntry{
			Time:      now,
			Message:   fmt.Sprintf("extra ports: %s", strings.Join(ports, " ")),
			Direction: "→",
		})
	}
	return entries
}

func looksLikeFilePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") {
		return !strings.Contains(s, "\n")
	}
	return false
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
