package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/canavan-a/fleetman/internal/api"
)

type provisionStage int

const (
	provisionInput provisionStage = iota
	provisionSubmitting
	provisionResult
)

type tokenMintedMsg struct {
	resp api.TokenResponse
	err  error
}

const (
	provFieldName = iota
	provFieldTags
	provFieldCount
)

type provisionModel struct {
	client   *api.Client
	repo     string
	host     string // bare server host — implies wss unless insecure
	insecure bool
	tags     []string

	inputs  [provFieldCount]textinput.Model
	focused int
	stage   provisionStage

	resultDevice string
	resultToken  string
	resultName   string
	err          string
	clipMsg      string
	showFull     bool

	done      bool
	cancelled bool
	closed    bool
}

func newProvisionModel(client *api.Client, repo, host string, insecure bool, tags []string) provisionModel {
	name := textinput.New()
	name.Placeholder = "e.g. web1 (optional, defaults to the device id)"
	name.Width = 40
	name.CharLimit = 64
	name.Focus()

	tagsInput := textinput.New()
	tagsInput.Placeholder = "prod, edge"
	tagsInput.Width = 40
	tagsInput.CharLimit = 128

	return provisionModel{
		client:   client,
		repo:     repo,
		host:     host,
		insecure: insecure,
		tags:     tags,
		inputs:   [provFieldCount]textinput.Model{name, tagsInput},
	}
}

func (m provisionModel) Init() tea.Cmd { return textinput.Blink }

func (m provisionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.stage {
		case provisionInput:
			switch msg.Type {
			case tea.KeyEsc, tea.KeyCtrlC:
				m.cancelled = true
				m.done = true
				return m, nil
			case tea.KeyTab, tea.KeyShiftTab:
				if msg.Type == tea.KeyTab {
					m.focused = (m.focused + 1) % provFieldCount
				} else {
					m.focused = (m.focused - 1 + provFieldCount) % provFieldCount
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
				if m.focused < provFieldCount-1 {
					m.inputs[m.focused].Blur()
					m.focused++
					m.inputs[m.focused].Focus()
					return m, textinput.Blink
				}
				m.stage = provisionSubmitting
				return m, m.mint()
			}
			var cmd tea.Cmd
			m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
			return m, cmd
		case provisionResult:
			switch msg.String() {
			case "c":
				if err := copyToClipboard(m.installCmd()); err != nil {
					m.clipMsg = err.Error()
				} else {
					m.clipMsg = "copied to clipboard"
				}
				return m, nil
			case "v":
				m.showFull = !m.showFull
				return m, nil
			case "enter", "esc":
				if m.showFull {
					m.showFull = false
					return m, nil
				}
				m.closed = true
				m.done = true
				return m, nil
			}
			return m, nil
		}

	case tokenMintedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.stage = provisionInput
			return m, nil
		}
		m.resultDevice = msg.resp.DeviceID
		m.resultToken = msg.resp.Token
		m.resultName = strings.TrimSpace(m.inputs[provFieldName].Value())
		m.stage = provisionResult
		tags := parseTagList(m.inputs[provFieldTags].Value())
		if len(tags) > 0 {
			return m, m.tagNewDevice(tags)
		}
		return m, nil
	}
	return m, nil
}

func parseTagList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (m provisionModel) mint() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		resp, err := client.MintToken()
		return tokenMintedMsg{resp: resp, err: err}
	}
}

// installCmd builds the single-line install command for this device, used
// both by the clipboard copy and (if ever needed) direct display.
func (m provisionModel) installCmd() string {
	installURL := "https://github.com/" + m.repo + "/releases/latest/download/agent-install.sh"
	// The agent's install script defaults a bare host to a secure (wss://)
	// connection — only prepend ws:// when this master was explicitly set
	// up as insecure, so we don't silently downgrade otherwise.
	serverFlag := m.host
	if m.insecure {
		serverFlag = "ws://" + m.host
	}
	cmd := fmt.Sprintf("curl -fsSL %s | sudo sh -s -- --server %s --token %s --device-id %s",
		installURL, serverFlag, m.resultToken, m.resultDevice)
	if m.resultName != "" {
		cmd += " --role " + m.resultName
	}
	return cmd
}

func (m provisionModel) tagNewDevice(tags []string) tea.Cmd {
	client := m.client
	deviceID := m.resultDevice
	return func() tea.Msg {
		for _, t := range tags {
			client.CreateTag(t)
		}
		for _, t := range tags {
			client.TagDevices(t, []string{deviceID})
		}
		return nil
	}
}

func (m provisionModel) View() string {
	var b strings.Builder

	if m.stage != provisionResult {
		b.WriteString(paneTitleStyle.Render("Register new device"))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("Name (optional — sets the install script's --role label)"))
		b.WriteString("\n")
		b.WriteString(m.inputs[provFieldName].View())
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("Tags (optional, comma-separated)"))
		b.WriteString("\n")
		b.WriteString(m.inputs[provFieldTags].View())
		b.WriteString("\n\n")
		if m.err != "" {
			b.WriteString(errStyle.Render("✗ " + m.err))
			b.WriteString("\n")
		}
		if m.stage == provisionSubmitting {
			b.WriteString(dimStyle.Render("minting token…"))
		} else {
			b.WriteString(hintBarStyle.Render("tab · next field    enter · create    esc · cancel"))
		}
		return paneFocusedStyle.Width(56).Render(b.String())
	}

	if m.showFull {
		b.WriteString(paneTitleStyle.Render("Install command"))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("Select and copy manually if the clipboard didn't work:"))
		b.WriteString("\n\n")
		b.WriteString(m.installCmd())
		b.WriteString("\n\n")
		b.WriteString(hintBarStyle.Render("v / enter / esc · back"))
		// No fixed Width here either, for the same copy-paste-safety reason
		// as the command itself: a wrapped line inside a border mixes
		// border characters into anything selected from the terminal.
		return paneFocusedStyle.Render(b.String())
	}

	labelStyle := dimStyle.Width(11)

	b.WriteString(paneTitleStyle.Render("Device registered"))
	b.WriteString("\n\n")
	if m.resultName != "" {
		b.WriteString(labelStyle.Render("name") + rowSelectedStyle.Render(m.resultName))
		b.WriteString("\n")
	}
	b.WriteString(labelStyle.Render("device") + rowSelectedStyle.Render(m.resultDevice))
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("server") + m.host)
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("token") + truncate(m.resultToken, 24))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Full install command is one press away:"))
	b.WriteString("\n\n")
	if m.clipMsg != "" {
		b.WriteString(okStyle.Render(m.clipMsg))
	} else {
		b.WriteString(hintBarStyle.Render("[c] copy install command"))
	}
	b.WriteString("\n")
	b.WriteString(hintBarStyle.Render("[v] view full command    enter / esc · close"))
	return paneFocusedStyle.Width(48).Render(b.String())
}
