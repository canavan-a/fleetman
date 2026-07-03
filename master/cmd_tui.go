package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/canavan-a/fleetman/internal/api"
	"github.com/canavan-a/fleetman/wire"
)

const (
	pollInterval = 1 * time.Second
	pollTimeout  = 30 * time.Second
)

// --- messages ---

type cmdPostedMsg struct {
	resp api.PostCommandResponse
	err  error
}

type cmdPolledMsg struct {
	status api.CommandStatus
	err    error
}

type pollTickMsg struct{}

func pollAfter() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg { return pollTickMsg{} })
}

// --- model ---

type cmdModeModel struct {
	client *api.Client
	repo   string // GitHub "owner/repo", for resolving :upgrade with no version

	tag     string
	devices []api.Device          // devices in current view
	target  map[string]api.Device // pre-selected sidebar devices, if any

	input   textinput.Model
	history []string
	histIdx int

	commandID  string
	commandTxt string
	dispatched bool
	startedAt  time.Time
	summary    map[string]int
	results    map[string]api.DeviceResult

	rowsFocused bool // Tab toggles focus between the launcher and the result rows
	rowCursor   int
	detailOpen  bool // full stdout/stderr overlay for the row under rowCursor

	err           string
	exitRequested bool
}

func newCmdModeModel(client *api.Client, repo, tag string, devices []api.Device, target map[string]api.Device) cmdModeModel {
	ti := textinput.New()
	ti.Placeholder = "command  (or  :restart <service>, :upgrade [version])"
	ti.Width = 60
	ti.Focus()
	return cmdModeModel{
		client:  client,
		repo:    repo,
		tag:     tag,
		devices: devices,
		target:  target,
		input:   ti,
		results: make(map[string]api.DeviceResult),
	}
}

func (m cmdModeModel) Init() tea.Cmd { return textinput.Blink }

func (m cmdModeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.detailOpen {
			switch msg.String() {
			case "esc", "enter", "q":
				m.detailOpen = false
			}
			return m, nil
		}

		switch msg.Type {
		case tea.KeyEsc:
			if m.rowsFocused {
				m.rowsFocused = false
				m.input.Focus()
				return m, textinput.Blink
			}
			m.exitRequested = true
			return m, nil
		case tea.KeyCtrlC:
			m.exitRequested = true
			return m, nil
		case tea.KeyTab:
			m.rowsFocused = !m.rowsFocused
			if m.rowsFocused {
				m.input.Blur()
			} else {
				m.input.Focus()
			}
			return m, textinput.Blink
		}

		if m.rowsFocused {
			rows := m.rowsToShow()
			switch msg.String() {
			case "j", "down":
				if m.rowCursor < len(rows)-1 {
					m.rowCursor++
				}
			case "k", "up":
				if m.rowCursor > 0 {
					m.rowCursor--
				}
			case "enter":
				if m.rowCursor < len(rows) {
					if _, ok := m.results[rows[m.rowCursor].DeviceID]; ok {
						m.detailOpen = true
					}
				}
			}
			return m, nil
		}

		switch msg.Type {
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
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			return m.fire(text)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case cmdPostedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.dispatched = false
			return m, nil
		}
		m.err = ""
		m.commandID = msg.resp.CommandID
		m.dispatched = true
		m.startedAt = time.Now()
		return m, tea.Batch(pollCommand(m.client, m.commandID), pollAfter())

	case pollTickMsg:
		if !m.dispatched {
			return m, nil
		}
		if time.Since(m.startedAt) > pollTimeout {
			m.dispatched = false
			return m, nil
		}
		return m, tea.Batch(pollCommand(m.client, m.commandID), pollAfter())

	case cmdPolledMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.summary = msg.status.Summary
		for id, r := range msg.status.Results {
			m.results[id] = *r
		}
		if m.summary["pending"] == 0 {
			m.dispatched = false
		}
		return m, nil
	}

	return m, nil
}

func (m cmdModeModel) fire(text string) (tea.Model, tea.Cmd) {
	m.history = append(m.history, text)
	m.histIdx = len(m.history)
	m.commandTxt = text
	m.results = make(map[string]api.DeviceResult)
	m.summary = nil
	m.rowCursor = 0
	m.detailOpen = false
	m.input.SetValue("")

	target := api.Target{}
	switch {
	case len(m.target) > 0:
		ids := make([]string, 0, len(m.target))
		for id := range m.target {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		target.IDs = ids
	case m.tag == noTagFilter:
		// The server has no "no tag" filter — target exactly the
		// (already client-side-filtered) devices shown in this view.
		ids := make([]string, 0, len(m.devices))
		for _, d := range m.devices {
			ids = append(ids, d.DeviceID)
		}
		target.IDs = ids
	case m.tag != "":
		target.Tags = []string{m.tag}
	default:
		target.All = true
	}

	client := m.client

	if text == ":upgrade" || strings.HasPrefix(text, ":upgrade ") {
		version := strings.TrimSpace(strings.TrimPrefix(text, ":upgrade"))
		repo := m.repo
		return m, func() tea.Msg {
			if version == "" {
				v, err := fetchLatestGithubTag(repo)
				if err != nil {
					return cmdPostedMsg{err: fmt.Errorf("resolve latest version: %w", err)}
				}
				version = v
			}
			req := api.PostCommandRequest{
				Action: wire.ActionUpgrade,
				Target: target,
				Payload: map[string]interface{}{
					"version": version,
				},
			}
			resp, err := client.PostCommand(req)
			return cmdPostedMsg{resp: resp, err: err}
		}
	}

	action := wire.ActionRunCommand
	payload := map[string]interface{}{"argv": []string{"sh", "-c", text}}

	if strings.HasPrefix(text, ":restart ") {
		action = wire.ActionRestartService
		payload = map[string]interface{}{"service": strings.TrimSpace(strings.TrimPrefix(text, ":restart "))}
	}

	req := api.PostCommandRequest{Action: action, Target: target, Payload: payload}
	return m, func() tea.Msg {
		resp, err := client.PostCommand(req)
		return cmdPostedMsg{resp: resp, err: err}
	}
}

// fetchLatestGithubTag resolves the latest release tag for repo ("owner/name")
// via GitHub's public API, for ":upgrade" with no explicit version.
func fetchLatestGithubTag(repo string) (string, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("no releases found for %s", repo)
	}
	return payload.TagName, nil
}

func pollCommand(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		status, err := client.GetCommand(id)
		return cmdPolledMsg{status: status, err: err}
	}
}

func (m cmdModeModel) View() string {
	if m.detailOpen {
		return m.renderDetail()
	}

	var b strings.Builder

	targetDesc := "all devices"
	switch {
	case len(m.target) > 0:
		targetDesc = fmt.Sprintf("%d selected", len(m.target))
	case m.tag == noTagFilter:
		targetDesc = "no tag"
	case m.tag != "":
		targetDesc = m.tag
	}
	b.WriteString(paneTitleStyle.Render("COMMAND MODE"))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render("target: " + targetDesc))
	b.WriteString("\n\n")

	rows := m.rowsToShow()
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("no devices in target"))
	}
	for i, d := range rows {
		res, has := m.results[d.DeviceID]
		statusCol := dimStyle.Render("—")
		outCol := ""
		if has {
			switch {
			case res.Status == "ok":
				statusCol = okStyle.Render("✓ ok")
			case res.Status == "error":
				statusCol = errStyle.Render(fmt.Sprintf("✗ err(%d)", res.Retcode))
			case res.Status == "timeout":
				statusCol = dimStyle.Render("⏱ timeout")
			case !m.dispatched:
				// Still "pending" per the last poll, but we've stopped
				// polling (client-side timeout hit) — the server will
				// eventually flip this to "timeout" itself, we just won't
				// hear about it since we're not asking anymore. Render it
				// as such rather than leaving a misleading "… wait"
				// forever implying it's still in flight.
				statusCol = dimStyle.Render("⏱ timeout")
			default:
				statusCol = dimStyle.Render("… wait")
			}
			outCol = firstLine(res.Stdout)
			if res.Status == "error" && outCol == "" {
				outCol = firstLine(res.Stderr)
			}
		} else if m.dispatched {
			statusCol = dimStyle.Render("… wait")
		}
		marker := "  "
		if m.rowsFocused && i == m.rowCursor {
			marker = "▶ "
		}
		line := fmt.Sprintf("%s%s %-16s %-14s %s", marker, onlineDot(d.Online()), truncate(deviceName(d), 16), statusCol, truncate(outCol, 60))
		if m.rowsFocused && i == m.rowCursor {
			line = rowCursorStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.commandTxt != "" && m.summary != nil {
		b.WriteString(dimStyle.Render(fmt.Sprintf("last: %s   pending=%d ok=%d failed=%d timeout=%d",
			m.commandTxt, m.summary["pending"], m.summary["completed"], m.summary["failed"], m.summary["timeout"])))
		b.WriteString("\n\n")
	}

	b.WriteString(m.input.View())
	b.WriteString("\n")
	if m.err != "" {
		b.WriteString(errStyle.Render("✗ " + m.err))
		b.WriteString("\n")
	}
	if m.rowsFocused {
		b.WriteString(hintBarStyle.Render("↑↓ move   enter · view full output   tab · back to launcher   esc · back to browse"))
	} else {
		b.WriteString(hintBarStyle.Render("↑↓ history   enter · fire   tab · browse results   esc · back to browse"))
	}

	return b.String()
}

func (m cmdModeModel) renderDetail() string {
	rows := m.rowsToShow()
	if m.rowCursor >= len(rows) {
		return ""
	}
	d := rows[m.rowCursor]
	res, ok := m.results[d.DeviceID]

	var b strings.Builder
	b.WriteString(paneTitleStyle.Render(deviceName(d)))
	if ok {
		b.WriteString("  ")
		switch res.Status {
		case "ok":
			b.WriteString(okStyle.Render(fmt.Sprintf("✓ ok (exit %d)", res.Retcode)))
		case "error":
			b.WriteString(errStyle.Render(fmt.Sprintf("✗ error (exit %d)", res.Retcode)))
		case "timeout":
			b.WriteString(dimStyle.Render("⏱ timeout"))
		default:
			b.WriteString(dimStyle.Render("… wait"))
		}
	}
	b.WriteString("\n\n")

	if ok && strings.TrimSpace(res.Stdout) != "" {
		b.WriteString(dimStyle.Render("stdout:"))
		b.WriteString("\n")
		b.WriteString(res.Stdout)
		b.WriteString("\n")
	}
	if ok && strings.TrimSpace(res.Stderr) != "" {
		b.WriteString("\n")
		b.WriteString(errStyle.Render("stderr:"))
		b.WriteString("\n")
		b.WriteString(res.Stderr)
		b.WriteString("\n")
	}
	if !ok || (strings.TrimSpace(res.Stdout) == "" && strings.TrimSpace(res.Stderr) == "") {
		b.WriteString(dimStyle.Render("(no output)"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(hintBarStyle.Render("esc / enter · close"))

	return paneFocusedStyle.Render(b.String())
}

func (m cmdModeModel) rowsToShow() []api.Device {
	if len(m.target) > 0 {
		ids := make([]string, 0, len(m.target))
		for id := range m.target {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out := make([]api.Device, 0, len(ids))
		for _, id := range ids {
			out = append(out, m.target[id])
		}
		return out
	}
	return m.devices
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
