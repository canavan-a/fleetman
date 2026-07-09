package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	provNumTextFields
)

type provisionModel struct {
	client         *api.Client
	repo           string
	host           string // bare server host — implies wss unless insecure
	insecure       bool
	tags           []string
	extraHeaders   map[string]string
	includeHeaders bool // space toggles (when focused on the header-toggle field); whether to append master's --header flags

	inputs  [provNumTextFields]textinput.Model
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

func newProvisionModel(client *api.Client, repo, host string, insecure bool, tags []string, extraHeaders map[string]string) provisionModel {
	name := textinput.New()
	name.Placeholder = "e.g. web1"
	name.Width = 40
	name.CharLimit = 64
	name.Focus()

	// Rendered by hand via renderTagsLine (for ghost-text autocomplete and
	// per-tag coloring), so no Placeholder/Width needed here — only used
	// for keystroke handling and value storage.
	tagsInput := textinput.New()
	tagsInput.CharLimit = 128

	return provisionModel{
		client:         client,
		repo:           repo,
		host:           host,
		insecure:       insecure,
		tags:           tags,
		extraHeaders:   extraHeaders,
		includeHeaders: len(extraHeaders) > 0,
		inputs:         [provNumTextFields]textinput.Model{name, tagsInput},
	}
}

func (m provisionModel) Init() tea.Cmd { return textinput.Blink }

// headerToggleField is the pseudo-field index for the "include master
// headers" toggle — it comes after the real text inputs and only
// participates in the tab order when there are headers to offer.
const headerToggleField = provNumTextFields

// fieldCount returns how many fields (text inputs plus the header toggle,
// if applicable) participate in tab/enter navigation.
func (m provisionModel) fieldCount() int {
	if len(m.extraHeaders) > 0 {
		return provNumTextFields + 1
	}
	return provNumTextFields
}

// setFocus updates m.focused and blurs/focuses the text inputs accordingly.
// No-op on text-input focus state when landing on the header-toggle field.
func (m *provisionModel) setFocus(i int) {
	m.focused = i
	for idx := range m.inputs {
		if idx == m.focused {
			m.inputs[idx].Focus()
		} else {
			m.inputs[idx].Blur()
		}
	}
}

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
			case tea.KeyTab:
				// On the tags field, Tab first accepts the ghost-text
				// completion; only moves focus once there's nothing left
				// to complete.
				if m.focused == provFieldTags && m.acceptTagGhost() {
					return m, nil
				}
				m.setFocus((m.focused + 1) % m.fieldCount())
				return m, textinput.Blink
			case tea.KeyShiftTab:
				m.setFocus((m.focused - 1 + m.fieldCount()) % m.fieldCount())
				return m, textinput.Blink
			case tea.KeyEnter:
				if m.focused < m.fieldCount()-1 {
					m.setFocus(m.focused + 1)
					return m, textinput.Blink
				}
				m.stage = provisionSubmitting
				return m, m.mint()
			case tea.KeySpace:
				if m.focused == headerToggleField {
					m.includeHeaders = !m.includeHeaders
					return m, nil
				}
			}
			if m.focused == headerToggleField {
				// Toggle field has no text input to forward keys to.
				return m, nil
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

// splitTagToken splits a comma-separated tags value into the already-typed
// tags before the last comma and the in-progress token being typed.
func splitTagToken(value string) (completed []string, current string) {
	i := strings.LastIndexByte(value, ',')
	if i < 0 {
		return nil, value
	}
	return parseTagList(value[:i]), value[i+1:]
}

// tagGhostSuffix returns the remainder of the first existing tag that
// completes the in-progress token, e.g. typed "pro" against ["prod"]
// returns "d". Empty if there's no match or the token is already complete.
func (m *provisionModel) tagGhostSuffix() string {
	_, current := splitTagToken(m.inputs[provFieldTags].Value())
	trimmed := strings.TrimSpace(current)
	if trimmed == "" {
		return ""
	}
	low := strings.ToLower(trimmed)
	for _, t := range m.tags {
		if strings.HasPrefix(strings.ToLower(t), low) && len(t) > len(trimmed) {
			return t[len(trimmed):]
		}
	}
	return ""
}

// acceptTagGhost completes the in-progress token with the current ghost
// suggestion (if any), keeping any already-completed tags before it.
func (m *provisionModel) acceptTagGhost() bool {
	ghost := m.tagGhostSuffix()
	if ghost == "" {
		return false
	}
	value := m.inputs[provFieldTags].Value()
	m.inputs[provFieldTags].SetValue(value + ghost)
	m.inputs[provFieldTags].CursorEnd()
	return true
}

// tagExists reports whether name is (case-insensitively) an already-known tag.
func (m *provisionModel) tagKnown(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	for _, t := range m.tags {
		if strings.ToLower(t) == low {
			return true
		}
	}
	return false
}

// renderTagsLine draws the tags field by hand: completed tokens are colored
// by whether they match an existing tag, the in-progress token is plain,
// and any autocomplete match trails off as faint ghost text.
func (m provisionModel) renderTagsLine() string {
	value := m.inputs[provFieldTags].Value()
	completed, current := splitTagToken(value)

	var b strings.Builder
	for i, tag := range completed {
		if i > 0 {
			b.WriteString(", ")
		}
		if m.tagKnown(tag) {
			b.WriteString(okStyle.Render(tag))
		} else {
			b.WriteString(tag)
		}
	}
	if len(completed) > 0 {
		b.WriteString(", ")
	}

	b.WriteString(current)
	if m.focused == provFieldTags {
		b.WriteString(suggestStyle.Render(m.tagGhostSuffix()))
		b.WriteString(rowCursorStyle.Render(" "))
	}
	if value == "" {
		b.WriteString(dimStyle.Render("prod, edge"))
	}

	return b.String()
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

// installArgs returns the pieces installCmd is built from, so both the
// single-line and multi-line renderings stay in sync.
func (m provisionModel) installArgs() (installURL string, flags []string) {
	installURL = "https://github.com/" + m.repo + "/releases/latest/download/agent-install.sh"
	// The agent's install script defaults a bare host to a secure (wss://)
	// connection — only prepend ws:// when this master was explicitly set
	// up as insecure, so we don't silently downgrade otherwise.
	serverFlag := m.host
	if m.insecure {
		serverFlag = "ws://" + m.host
	}
	flags = []string{
		"--server " + serverFlag,
		"--token " + m.resultToken,
		"--device-id " + m.resultDevice,
	}
	if m.resultName != "" {
		flags = append(flags, "--role "+m.resultName)
	}
	if m.includeHeaders && len(m.extraHeaders) > 0 {
		keys := make([]string, 0, len(m.extraHeaders))
		for k := range m.extraHeaders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flags = append(flags, fmt.Sprintf("--header %q", k+": "+m.extraHeaders[k]))
		}
	}
	return installURL, flags
}

// installCmd builds the single-line install command, used for the
// clipboard copy — one unbroken line is the safest thing to paste at a
// shell prompt.
func (m provisionModel) installCmd() string {
	installURL, flags := m.installArgs()
	return fmt.Sprintf("curl -fsSL %s | sudo sh -s -- %s", installURL, strings.Join(flags, " "))
}

// installCmdMultiline formats the same command with a shell line
// continuation (`\` + newline) after each flag, for on-screen readability.
// These are real shell line-continuations, not a terminal-wrap artifact —
// safe to copy-paste as multiple lines, unlike letting lipgloss wrap a
// single long line inside a bordered box.
func (m provisionModel) installCmdMultiline() string {
	installURL, flags := m.installArgs()
	var b strings.Builder
	fmt.Fprintf(&b, "curl -fsSL %s | sudo sh -s -- \\\n", installURL)
	for i, f := range flags {
		b.WriteString("  " + f)
		if i < len(flags)-1 {
			b.WriteString(" \\")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
		b.WriteString(dimStyle.Render("Name (optional)"))
		b.WriteString("\n")
		b.WriteString(m.inputs[provFieldName].View())
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("Tags (optional, comma-separated)"))
		b.WriteString("\n")
		b.WriteString(m.renderTagsLine())
		b.WriteString("\n\n")
		if len(m.extraHeaders) > 0 {
			prefix := "  "
			if m.focused == headerToggleField {
				prefix = "▸ "
			}
			if m.includeHeaders {
				b.WriteString(prefix + okStyle.Render(fmt.Sprintf("[x] include %d master header(s) (space to toggle)", len(m.extraHeaders))))
			} else {
				b.WriteString(prefix + suggestStyle.Render("[ ] include master headers (space to toggle)"))
			}
			b.WriteString("\n\n")
		}
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
		b.WriteString(m.installCmdMultiline())
		b.WriteString("\n\n")
		b.WriteString(hintBarStyle.Render("v / enter / esc · back"))
		// Deliberately no border here: a bordered box puts a "│" on both
		// sides of every line, and dragging a selection across multiple
		// lines in a terminal captures those characters right along with
		// the command text — exactly the copy-paste corruption we're
		// trying to avoid, just via the border instead of text wrapping.
		return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
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
