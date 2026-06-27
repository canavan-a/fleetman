package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- styles ---

var (
	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 3).
			Width(52)

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("62")).
			MarginBottom(1)

	styleLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			MarginTop(1)

	styleHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			MarginTop(1)
)

// --- model ---

const (
	fieldServer = iota
	fieldKey
	fieldCount
)

type loginModel struct {
	inputs    [fieldCount]textinput.Model
	focused   int
	err       string
	done      bool
	cancelled bool
	result    Config // populated on success
}

func newLoginModel(prefill Config) loginModel {
	server := textinput.New()
	server.Placeholder = "http://localhost:8080"
	server.CharLimit = 256
	server.Width = 44
	server.SetValue(prefill.Server)

	key := textinput.New()
	key.Placeholder = "master API key"
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'
	key.CharLimit = 256
	key.Width = 44
	key.SetValue(prefill.MasterKey)

	m := loginModel{
		inputs:  [fieldCount]textinput.Model{server, key},
		focused: fieldServer,
	}
	m.inputs[fieldServer].Focus()
	return m
}

func (m loginModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m loginModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.cancelled = true
			m.done = true
			return m, tea.Quit

		case tea.KeyTab, tea.KeyShiftTab:
			m.err = ""
			if msg.Type == tea.KeyTab {
				m.focused = (m.focused + 1) % fieldCount
			} else {
				m.focused = (m.focused - 1 + fieldCount) % fieldCount
			}
			for i := range m.inputs {
				if i == m.focused {
					m.inputs[i].Focus()
				} else {
					m.inputs[i].Blur()
				}
			}
			return m, textinput.Blink

		case tea.KeyEnter:
			if m.focused < fieldCount-1 {
				// Move to next field.
				m.err = ""
				m.inputs[m.focused].Blur()
				m.focused++
				m.inputs[m.focused].Focus()
				return m, textinput.Blink
			}
			// Last field — attempt submit.
			return m.submit()
		}
	}

	// Forward key events to the focused input.
	var cmd tea.Cmd
	m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
	return m, cmd
}

func (m loginModel) submit() (tea.Model, tea.Cmd) {
	server := strings.TrimRight(strings.TrimSpace(m.inputs[fieldServer].Value()), "/")
	key := strings.TrimSpace(m.inputs[fieldKey].Value())

	if server == "" {
		m.err = "Server URL is required"
		m.focused = fieldServer
		m.inputs[fieldServer].Focus()
		m.inputs[fieldKey].Blur()
		return m, textinput.Blink
	}
	if key == "" {
		m.err = "Master API key is required"
		return m, nil
	}

	m.result = Config{Server: server, MasterKey: key}
	m.done = true
	return m, tea.Quit
}

func (m loginModel) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Fleet Master — Login"))
	b.WriteString("\n")

	b.WriteString(styleLabel.Render("Server URL"))
	b.WriteString("\n")
	b.WriteString(m.inputs[fieldServer].View())
	b.WriteString("\n\n")

	b.WriteString(styleLabel.Render("Master API Key"))
	b.WriteString("\n")
	b.WriteString(m.inputs[fieldKey].View())

	if m.err != "" {
		b.WriteString(styleError.Render("✗ " + m.err))
	} else {
		b.WriteString(styleHint.Render("tab · next field    enter · confirm"))
	}

	return styleBox.Render(b.String())
}

// --- entry point ---

// RunLoginTUI launches the Bubble Tea login form and returns the submitted
// Config on success, or an error if the user cancelled.
func RunLoginTUI(prefill Config) (Config, error) {
	m := newLoginModel(prefill)
	p := tea.NewProgram(m)

	final, err := p.Run()
	if err != nil {
		return Config{}, fmt.Errorf("tui error: %w", err)
	}

	result := final.(loginModel)
	if result.cancelled || !result.done {
		return Config{}, fmt.Errorf("login cancelled")
	}
	return result.result, nil
}
