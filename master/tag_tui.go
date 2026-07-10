package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- generic single-line text input sub-model ---

type textInputModel struct {
	label       string
	input       textinput.Model
	suggestions []string // existing values to autocomplete/highlight against
	matches     []string // suggestions whose prefix matches the current input
	exists      bool     // current input is an exact (case-insensitive) match
	done        bool
	cancelled   bool
	value       string
}

func newTextInputModel(label, placeholder string) textInputModel {
	return newTextInputModelWithSuggestions(label, placeholder, nil)
}

// newTextInputModelWithSuggestions is a text input that autocompletes
// against known values as you type (Tab completes to the first match) and
// visually flags when the typed value already exists.
func newTextInputModelWithSuggestions(label, placeholder string, suggestions []string) textInputModel {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Width = 40
	ti.CharLimit = 128
	ti.Focus()
	return textInputModel{label: label, input: ti, suggestions: suggestions}
}

func (m textInputModel) Init() tea.Cmd { return textinput.Blink }

func (m textInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.cancelled = true
			m.done = true
			return m, nil
		case tea.KeyEnter:
			m.value = m.input.Value()
			m.done = true
			return m, nil
		case tea.KeyTab:
			if len(m.matches) > 0 {
				m.input.SetValue(m.matches[0])
				m.input.CursorEnd()
				m.refreshMatches()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refreshMatches()
	return m, cmd
}

func (m *textInputModel) refreshMatches() {
	val := strings.ToLower(strings.TrimSpace(m.input.Value()))
	m.matches = nil
	m.exists = false
	if val == "" {
		return
	}
	for _, s := range m.suggestions {
		low := strings.ToLower(s)
		if low == val {
			m.exists = true
		}
		if strings.HasPrefix(low, val) {
			m.matches = append(m.matches, s)
		}
	}
}

func (m textInputModel) View() string {
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render(m.label))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	switch {
	case m.exists:
		b.WriteString(okStyle.Render("✓ existing tag — devices will be added to it"))
	case len(m.matches) > 0:
		b.WriteString(suggestStyle.Render("tab to complete: " + strings.Join(m.matches, ", ")))
	default:
		b.WriteString(" ")
	}
	b.WriteString("\n\n")
	b.WriteString(hintBarStyle.Render("tab · complete    enter · confirm    esc · cancel"))
	return paneFocusedStyle.Width(48).Render(b.String())
}

// --- tag picker sub-model ---

type tagPickerModel struct {
	title     string
	tags      []string
	cursor    int
	done      bool
	cancelled bool
	chosen    string
	emptyMsg  string
}

func newTagPickerModel(tags []string) tagPickerModel {
	return tagPickerModel{
		title:    "Add to tag",
		tags:     tags,
		emptyMsg: "no tags yet — press [n] from browse to create one",
	}
}

// newUntagPickerModel builds a picker scoped to the tags actually present on
// the selected devices, so removing a tag can't be pointed at one that
// doesn't apply to any of them.
func newUntagPickerModel(tags []string) tagPickerModel {
	return tagPickerModel{
		title:    "Remove tag",
		tags:     tags,
		emptyMsg: "selected device(s) have no tags",
	}
}

func (m tagPickerModel) Init() tea.Cmd { return nil }

func (m tagPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "esc", "ctrl+c":
		m.cancelled = true
		m.done = true
	case "j", "down":
		if m.cursor < len(m.tags)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter":
		if len(m.tags) > 0 {
			m.chosen = m.tags[m.cursor]
		}
		m.done = true
	}
	return m, nil
}

func (m tagPickerModel) View() string {
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render(m.title))
	b.WriteString("\n\n")
	if len(m.tags) == 0 {
		b.WriteString(dimStyle.Render(m.emptyMsg))
	}
	for i, t := range m.tags {
		if i == m.cursor {
			b.WriteString(rowCursorStyle.Render("▶ " + t))
		} else {
			b.WriteString("  " + t)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(hintBarStyle.Render("enter · confirm    esc · cancel"))
	return paneFocusedStyle.Width(40).Render(b.String())
}

// --- yes/no confirm sub-model ---

type confirmModel struct {
	prompt    string
	done      bool
	cancelled bool
	confirmed bool
}

func newConfirmModel(prompt string) confirmModel {
	return confirmModel{prompt: prompt}
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch strings.ToLower(keyMsg.String()) {
	case "y":
		m.confirmed = true
		m.done = true
	case "n", "esc", "ctrl+c":
		m.cancelled = true
		m.done = true
	}
	return m, nil
}

func (m confirmModel) View() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(errorColor).
		Padding(1, 2)
	return box.Render(m.prompt + "  " + dimStyle.Render("[y/N]"))
}
