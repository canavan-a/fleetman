package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/canavan-a/fleetman/internal/api"
)

const (
	shellPollInterval = 300 * time.Millisecond
	shellOutputLines  = 20
)

// --- messages ---

type shellOpenedMsg struct {
	sessionID string
	err       error
}

type shellOutputMsg struct {
	output api.ShellOutput
	err    error
}

type shellInputSentMsg struct {
	err error
}

type shellPollTickMsg struct{}

func shellPollAfter() tea.Cmd {
	return tea.Tick(shellPollInterval, func(t time.Time) tea.Msg { return shellPollTickMsg{} })
}

func pollShellOutput(client *api.Client, sessionID string, since int) tea.Cmd {
	return func() tea.Msg {
		out, err := client.PollShellOutput(sessionID, since)
		return shellOutputMsg{output: out, err: err}
	}
}

// --- model ---

// shellModeModel drives a single persistent, interactive shell session
// against one device: it opens a session, streams output back via polling,
// and forwards typed lines as stdin.
type shellModeModel struct {
	client *api.Client
	device api.Device

	sessionID string
	offset    int
	closed    bool
	output    string

	input   textinput.Model
	history []string
	histIdx int

	err           string
	exitRequested bool
}

func newShellModeModel(client *api.Client, device api.Device) shellModeModel {
	ti := textinput.New()
	ti.Placeholder = "shell command"
	ti.Width = 76
	ti.Focus()
	return shellModeModel{
		client: client,
		device: device,
		input:  ti,
	}
}

func (m shellModeModel) Init() tea.Cmd {
	client := m.client
	deviceID := m.device.DeviceID
	return tea.Batch(textinput.Blink, func() tea.Msg {
		id, err := client.OpenShell(deviceID)
		return shellOpenedMsg{sessionID: id, err: err}
	})
}

func (m shellModeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.exitRequested = true
			if m.sessionID != "" && !m.closed {
				client, sessionID := m.client, m.sessionID
				go client.CloseShell(sessionID)
			}
			return m, nil
		case tea.KeyUp:
			if len(m.history) > 0 && m.histIdx > 0 {
				m.histIdx--
				m.input.SetValue(m.history[m.histIdx])
				m.input.CursorEnd()
			}
			return m, nil
		case tea.KeyDown:
			if m.histIdx < len(m.history)-1 {
				m.histIdx++
				m.input.SetValue(m.history[m.histIdx])
				m.input.CursorEnd()
			} else {
				m.histIdx = len(m.history)
				m.input.SetValue("")
			}
			return m, nil
		case tea.KeyEnter:
			text := m.input.Value()
			if text == "" || m.sessionID == "" || m.closed {
				return m, nil
			}
			m.history = append(m.history, text)
			m.histIdx = len(m.history)
			m.input.SetValue("")
			client, sessionID := m.client, m.sessionID
			return m, func() tea.Msg {
				err := client.SendShellInput(sessionID, text+"\n")
				return shellInputSentMsg{err: err}
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case shellOpenedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.sessionID = msg.sessionID
		return m, tea.Batch(pollShellOutput(m.client, m.sessionID, m.offset), shellPollAfter())

	case shellPollTickMsg:
		if m.sessionID == "" || m.closed {
			return m, nil
		}
		return m, tea.Batch(pollShellOutput(m.client, m.sessionID, m.offset), shellPollAfter())

	case shellOutputMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		for _, c := range msg.output.Chunks {
			m.output += c.Data
		}
		m.offset = msg.output.Offset
		if msg.output.Closed {
			m.closed = true
		}
		return m, nil

	case shellInputSentMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		return m, nil
	}

	return m, nil
}

func (m shellModeModel) View() string {
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("SHELL — " + deviceName(m.device)))
	b.WriteString("  ")
	switch {
	case m.closed:
		b.WriteString(dimStyle.Render("session closed"))
	case m.sessionID == "":
		b.WriteString(dimStyle.Render("connecting…"))
	default:
		b.WriteString(okStyle.Render("● live"))
	}
	b.WriteString("\n\n")

	lines := tailLines(m.output, shellOutputLines)
	if len(lines) == 0 {
		b.WriteString(dimStyle.Render("(no output yet)"))
		b.WriteString("\n")
	}
	for _, l := range lines {
		b.WriteString(truncate(l, 100))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	if m.err != "" {
		b.WriteString(errStyle.Render("✗ " + m.err))
		b.WriteString("\n")
	}
	b.WriteString(hintBarStyle.Render("enter · send   ↑↓ history   esc · close session & back to browse"))

	return b.String()
}

// tailLines returns the last n non-empty trailing lines of s.
func tailLines(s string, n int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	all := strings.Split(s, "\n")
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}
