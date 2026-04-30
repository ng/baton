package main

import (
	"fmt"
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

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(headerColor)
	dimStyle    = lipgloss.NewStyle().Foreground(dimColor)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor)
	greenStyle  = lipgloss.NewStyle().Foreground(greenColor)
	redStyle    = lipgloss.NewStyle().Foreground(redColor)

	sectionTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			PaddingLeft(1)

	portStyle = lipgloss.NewStyle().
			PaddingLeft(3)
)

type logEntry struct {
	Time    time.Time
	Message string
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
	connectedAt time.Time

	reverseTunnels []PortInfo
	localForwards  []PortInfo
	scanner        *PortScanner

	logEntries  []logEntry
	logViewport viewport.Model

	uploadSamples   []float64
	downloadSamples []float64

	inboxPath       string
	recentTransfers []transferEntry

	width  int
	height int

	connEvents       <-chan ConnEventMsg
	portEvents       <-chan PortEventMsg
	transferEvents   <-chan TransferDoneMsg
	throughputEvents <-chan ThroughputMsg
}

func newModel(cfg *Config, conn *Connection, scanner *PortScanner, transferer *Transferer, throughput *ThroughputMonitor) model {
	vp := viewport.New(80, 10)
	vp.SetContent("")

	return model{
		connected:   true,
		host:        conn.host,
		connectedAt: conn.StartTime,

		reverseTunnels: []PortInfo{
			{Port: cfg.Connection.ReversePort, Process: "ssh-reverse"},
		},
		scanner: scanner,

		logViewport: vp,
		logEntries: []logEntry{
			{Time: time.Now(), Message: "connected to " + conn.host},
		},

		inboxPath: cfg.Transfer.Inbox,

		connEvents:       conn.Events(),
		portEvents:       scanner.Events(),
		transferEvents:   transferer.Events(),
		throughputEvents: throughput.Events(),
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

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		waitForConnEvent(m.connEvents),
		waitForPortEvent(m.portEvents),
		waitForTransferEvent(m.transferEvents),
		waitForThroughput(m.throughputEvents),
		tickCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
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
		m.addLog(msg.Time, msg.Message)
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
		m.addLog(msg.Time, fmt.Sprintf("port %d %s (%s)", msg.Port, msg.Action, label))
		m.localForwards = m.scanner.ActivePortInfos()
		return m, waitForPortEvent(m.portEvents)

	case TransferDoneMsg:
		if msg.Err != nil {
			m.addLog(msg.Time, fmt.Sprintf("transfer failed: %s: %v", msg.Filename, msg.Err))
		} else {
			m.addLog(msg.Time, fmt.Sprintf("uploaded %s → %s", msg.Filename, msg.RemotePath))
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
	}

	return m, nil
}

func (m *model) addLog(t time.Time, msg string) {
	m.logEntries = append(m.logEntries, logEntry{Time: t, Message: msg})
	if len(m.logEntries) > 200 {
		m.logEntries = m.logEntries[len(m.logEntries)-200:]
	}
	m.updateViewport()
}

func (m *model) updateViewport() {
	var lines []string
	for _, e := range m.logEntries {
		ts := dimStyle.Render(e.Time.Format("15:04:05"))
		lines = append(lines, fmt.Sprintf("  %s  %s", ts, e.Message))
	}
	content := strings.Join(lines, "\n")
	m.logViewport.SetContent(content)
	m.logViewport.GotoBottom()
}

func (m *model) recalcLayout() {
	logHeight := m.height - 18
	if logHeight < 3 {
		logHeight = 3
	}
	logWidth := m.width - 2
	if logWidth < 20 {
		logWidth = 20
	}
	m.logViewport.Width = logWidth
	m.logViewport.Height = logHeight
	m.updateViewport()
}

func (m model) View() string {
	if m.width == 0 {
		return "initializing..."
	}

	var sections []string

	sections = append(sections, m.renderHeader())
	sections = append(sections, "")
	sections = append(sections, m.renderPorts())
	sections = append(sections, "")
	sections = append(sections, m.renderMiddle())
	sections = append(sections, "")
	sections = append(sections, m.renderActivity())
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

	line := left + " ── " + host + up

	pad := m.width - lipgloss.Width(line) - 10
	if pad < 0 {
		pad = 0
	}

	return line + strings.Repeat("─", pad) + dimStyle.Render(" q quit")
}

func (m model) renderPorts() string {
	colWidth := (m.width - 4) / 2
	if colWidth < 30 {
		colWidth = 30
	}

	// Left: reverse tunnels
	left := sectionTitle.Render("REVERSE TUNNELS") + "\n"
	if len(m.reverseTunnels) == 0 {
		left += portStyle.Render(dimStyle.Render("none"))
	}
	for _, p := range m.reverseTunnels {
		arrow := accentStyle.Render("←")
		port := accentStyle.Render(fmt.Sprintf(":%d", p.Port))
		proc := dimStyle.Render(p.Process)
		left += portStyle.Render(fmt.Sprintf("%s %s  %s", arrow, port, proc)) + "\n"
	}

	// Right: local forwards
	right := sectionTitle.Render("LOCAL FORWARDS (pod → mac)") + "\n"
	if len(m.localForwards) == 0 {
		right += portStyle.Render(dimStyle.Render("scanning..."))
	}
	for _, p := range m.localForwards {
		arrow := greenStyle.Render("→")
		port := greenStyle.Render(fmt.Sprintf(":%d", p.Port))
		proc := p.Process
		if proc == "" {
			proc = "(unknown)"
		}
		proc = dimStyle.Render(proc)
		right += portStyle.Render(fmt.Sprintf("%s %s  %s", arrow, port, proc)) + "\n"
	}

	leftBlock := lipgloss.NewStyle().Width(colWidth).Render(left)
	rightBlock := lipgloss.NewStyle().Width(colWidth).Render(right)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, rightBlock)
}

func (m model) renderMiddle() string {
	colWidth := (m.width - 4) / 2
	if colWidth < 30 {
		colWidth = 30
	}

	sparkWidth := colWidth - 22
	if sparkWidth < 8 {
		sparkWidth = 8
	}

	// Left: network
	left := sectionTitle.Render("NETWORK") + "\n"

	upRate := formatBytes(lastSample(m.uploadSamples))
	downRate := formatBytes(lastSample(m.downloadSamples))

	upSpark := lipgloss.NewStyle().Foreground(sparkUpColor).Render(
		renderSparkline(m.uploadSamples, sparkWidth))
	downSpark := lipgloss.NewStyle().Foreground(sparkDownColor).Render(
		renderSparkline(m.downloadSamples, sparkWidth))

	left += portStyle.Render(fmt.Sprintf("↑ %8s %s", upRate, upSpark)) + "\n"
	left += portStyle.Render(fmt.Sprintf("↓ %8s %s", downRate, downSpark)) + "\n"

	// Right: file transfers
	right := sectionTitle.Render("FILE TRANSFERS") + "\n"
	right += portStyle.Render(dimStyle.Render("inbox: ")+m.inboxPath) + "\n"

	if len(m.recentTransfers) == 0 {
		right += portStyle.Render(dimStyle.Render("no recent transfers")) + "\n"
	} else {
		shown := m.recentTransfers
		if len(shown) > 5 {
			shown = shown[len(shown)-5:]
		}
		for _, t := range shown {
			right += portStyle.Render(fmt.Sprintf("%s → %s",
				t.Filename, t.RemotePath)) + "\n"
		}
	}

	leftBlock := lipgloss.NewStyle().Width(colWidth).Render(left)
	rightBlock := lipgloss.NewStyle().Width(colWidth).Render(right)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, rightBlock)
}

func (m model) renderActivity() string {
	header := sectionTitle.Render("ACTIVITY")
	return header + "\n" + m.logViewport.View()
}

func (m model) renderFooter() string {
	return dimStyle.Render("  ↑↓/jk scroll activity  q quit")
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
