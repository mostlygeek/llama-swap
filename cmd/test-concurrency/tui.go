package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type panelStatus int

const (
	statusWaiting panelStatus = iota
	statusStreaming
	statusDone
	statusError
)

func (s panelStatus) String() string {
	switch s {
	case statusStreaming:
		return "streaming"
	case statusDone:
		return "done"
	case statusError:
		return "error"
	default:
		return "waiting"
	}
}

// deltaMsg appends streamed text to a panel.
type deltaMsg struct {
	idx  int
	text string
}

// statusMsg updates a panel's lifecycle state.
type statusMsg struct {
	idx     int
	status  panelStatus
	elapsed time.Duration
	err     error
}

type panel struct {
	idx     int
	model   string
	color   lipgloss.Color
	status  panelStatus
	buf     strings.Builder
	elapsed time.Duration
	err     error
}

const (
	minPanelWidth = 28
	maxCols       = 3
	panelHeight   = 9 // total box height including border + header
)

type model struct {
	panels  []*panel
	focused int
	vp      viewport.Model
	width   int
	height  int
	cols    int
	pw      int // inner panel content width
	ready   bool
}

func newModel(models []string) *model {
	// Assign a stable color per unique model name (by first appearance).
	colorOf := map[string]lipgloss.Color{}
	panels := make([]*panel, len(models))
	for i, m := range models {
		c, ok := colorOf[m]
		if !ok {
			c = modelPalette[len(colorOf)%len(modelPalette)]
			colorOf[m] = c
		}
		panels[i] = &panel{idx: i, model: m, color: c, status: statusWaiting}
	}
	return &model{panels: panels, focused: 0}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		m.refreshViewport(true)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "tab", "right", "l":
			m.setFocus(m.focused + 1)
			return m, nil
		case "shift+tab", "left", "h":
			m.setFocus(m.focused - 1)
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if idx, ok := m.panelAt(msg.X, msg.Y); ok {
				m.setFocus(idx)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case deltaMsg:
		p := m.panels[msg.idx]
		p.buf.WriteString(msg.text)
		if msg.idx == m.focused {
			atBottom := m.vp.AtBottom()
			m.refreshViewport(false)
			if atBottom {
				m.vp.GotoBottom()
			}
		}
		return m, nil

	case statusMsg:
		p := m.panels[msg.idx]
		p.status = msg.status
		p.elapsed = msg.elapsed
		p.err = msg.err
		if msg.err != nil {
			errTxt := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("\n" + msg.err.Error())
			p.buf.WriteString(errTxt)
			if msg.idx == m.focused {
				m.refreshViewport(false)
				m.vp.GotoBottom()
			}
		}
		return m, nil
	}

	return m, nil
}

func (m *model) setFocus(idx int) {
	if len(m.panels) == 0 {
		return
	}
	if idx < 0 {
		idx = len(m.panels) - 1
	}
	if idx >= len(m.panels) {
		idx = 0
	}
	if idx == m.focused {
		return
	}
	m.focused = idx
	m.refreshViewport(true)
}

// relayout recomputes grid columns and panel/viewport dimensions.
func (m *model) relayout() {
	if m.width < minPanelWidth+4 {
		m.cols = 1
	} else {
		m.cols = m.width / (minPanelWidth + 2)
		if m.cols > maxCols {
			m.cols = maxCols
		}
		if m.cols > len(m.panels) {
			m.cols = len(m.panels)
		}
		if m.cols < 1 {
			m.cols = 1
		}
	}

	// inner content width: total width / cols, minus borders+padding (4) and gap.
	boxOuter := m.width/m.cols - 1
	m.pw = boxOuter - 4
	if m.pw < 8 {
		m.pw = 8
	}

	m.vp = viewport.New(m.pw, panelHeight-2)
	m.ready = true
}

func (m *model) refreshViewport(reset bool) {
	if !m.ready || len(m.panels) == 0 {
		return
	}
	content := lipgloss.NewStyle().Width(m.pw).Render(m.panels[m.focused].buf.String())
	m.vp.SetContent(content)
	if reset {
		m.vp.GotoBottom()
	}
}

// panelAt maps screen coordinates to a panel index based on the grid layout.
func (m *model) panelAt(x, y int) (int, bool) {
	if m.cols == 0 {
		return 0, false
	}
	boxOuterW := m.width/m.cols + 1
	col := x / boxOuterW
	row := y / panelHeight
	idx := row*m.cols + col
	if col < m.cols && idx >= 0 && idx < len(m.panels) {
		return idx, true
	}
	return 0, false
}

func (m *model) View() string {
	if !m.ready {
		return "loading..."
	}

	rows := []string{}
	var current []string
	for i, p := range m.panels {
		current = append(current, m.renderPanel(p, i == m.focused))
		if len(current) == m.cols {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, current...))
			current = nil
		}
	}
	if len(current) > 0 {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, current...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rows...)
	footer := lipgloss.NewStyle().Faint(true).Render(
		"tab/click: focus panel  •  wheel/↑↓/pgup/pgdn: scroll focused  •  q: quit")
	return grid + "\n" + footer
}

// modelPalette gives each panel a distinct, readable color for its name.
var modelPalette = []lipgloss.Color{
	"39",  // blue
	"213", // magenta
	"214", // orange
	"45",  // cyan
	"141", // purple
	"203", // salmon
	"82",  // lime
	"227", // light yellow
}

func statusColor(s panelStatus) lipgloss.Color {
	switch s {
	case statusStreaming:
		return lipgloss.Color("220") // yellow - active
	case statusDone:
		return lipgloss.Color("42") // green - success
	case statusError:
		return lipgloss.Color("196") // red - error
	default:
		return lipgloss.Color("244") // gray - waiting
	}
}

func (m *model) renderPanel(p *panel, focused bool) string {
	border := lipgloss.RoundedBorder()
	if focused {
		border = lipgloss.DoubleBorder()
	}
	style := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color("240"))

	statusTxt := p.status.String()
	if p.elapsed > 0 {
		statusTxt += " " + p.elapsed.Round(time.Millisecond).String()
	}

	// Header: model name (left, model color) + status/timer (right, status color).
	name := fmt.Sprintf("[%d] %s", p.idx, p.model)
	gap := m.pw - lipgloss.Width(name) - lipgloss.Width(statusTxt)
	if gap < 1 {
		name = truncate(name, m.pw-lipgloss.Width(statusTxt)-1)
		gap = m.pw - lipgloss.Width(name) - lipgloss.Width(statusTxt)
	}
	if gap < 1 {
		gap = 1
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(p.color).Render(name) +
		strings.Repeat(" ", gap) +
		lipgloss.NewStyle().Foreground(statusColor(p.status)).Render(statusTxt)

	var bodyLines string
	if focused {
		bodyLines = m.vp.View()
	} else {
		bodyLines = tailLines(p.buf.String(), m.pw, panelHeight-2)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, bodyLines)
	return style.Width(m.pw).Height(panelHeight - 2).Render(content)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if len(r) > w {
		r = r[:w]
	}
	return string(r)
}

// tailLines wraps text to width w and returns the last n lines.
func tailLines(s string, w, n int) string {
	wrapped := lipgloss.NewStyle().Width(w).Render(s)
	lines := strings.Split(wrapped, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
