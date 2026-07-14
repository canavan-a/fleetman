package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/canavan-a/fleetman/internal/api"
	"github.com/canavan-a/fleetman/wire"
)

const (
	pollInterval      = 1 * time.Second
	pollTimeout       = 30 * time.Second
	peekStep          = 3 // lines added/removed per peek keypress on the device list
	shellPollInterval = 300 * time.Millisecond
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

// shellOpenedMsg reports the result of opening one device's shell session,
// part of the fan-out ":open" issues across every device in view.
type shellOpenedMsg struct {
	deviceID  string
	sessionID string
	err       error
}

// shellOutputMsg carries a poll result for one device's shell session.
type shellOutputMsg struct {
	deviceID string
	output   api.ShellOutput
	err      error
}

// shellInputSentMsg reports whether a broadcast stdin line reached one device.
type shellInputSentMsg struct {
	deviceID string
	err      error
}

// shellClosedMsg confirms a device's shell session was told to close.
type shellClosedMsg struct {
	deviceID string
}

type shellPollTickMsg struct{}

func shellPollAfter() tea.Cmd {
	return tea.Tick(shellPollInterval, func(t time.Time) tea.Msg { return shellPollTickMsg{} })
}

// fileListMsg carries the result of a ":listf" fetch.
type fileListMsg struct {
	files []api.FileInfo
	err   error
}

// uploadProgressMsg carries one progress update (or the final outcome) of a
// ":uploadf" upload in progress.
type uploadProgressMsg struct {
	sent, total int64
	done        bool
	info        api.FileInfo
	err         error
}

// waitUploadProgress reads the next progress update off ch — the
// channel-listening pattern for driving a bubbletea view off a single
// long-running side effect (here, one multipart HTTP upload) instead of a
// pollable HTTP endpoint like shell output uses.
func waitUploadProgress(ch <-chan uploadProgressMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// --- model ---

type cmdModeModel struct {
	client *api.Client
	repo   string // GitHub "owner/repo", for resolving :upgrade with no version

	width int // terminal width, from the last tea.WindowSizeMsg; 0 until the first one arrives

	growAll int // "e"/"E" — extra output lines shown under every row at once (like peekLines, but all rows instead of just the cursor row)

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
	peekLines   int  // extra output lines shown inline for the row under rowCursor

	// shellOpen is entered via ":open" and left via ":close" (or by exiting
	// command mode, which force-closes any still-live sessions). While
	// true, plain input lines are broadcast as stdin to every live session
	// instead of being dispatched as a one-shot command.
	shellOpen     bool
	shellSessions map[string]string // deviceID -> sessionID
	shellOffsets  map[string]int    // deviceID -> next poll offset
	shellOutput   map[string]string // deviceID -> accumulated transcript
	shellClosed   map[string]bool   // deviceID -> session no longer live
	shellErrs     map[string]string // deviceID -> open/poll/unsupported error

	// fileList/fileListErr hold the last ":listf" result, shown under the
	// command strip until superseded by another ":listf" or a plain command.
	fileList    []api.FileInfo
	fileListErr string

	// uploading tracks an in-flight ":uploadf", driven by uploadCh (see
	// waitUploadProgress) rather than polling, since a multipart upload is
	// one continuous HTTP call.
	uploading   bool
	uploadName  string
	uploadSent  int64
	uploadTotal int64
	uploadErr   string
	uploadDone  string // set to a summary line once the upload completes ok
	uploadCh    chan uploadProgressMsg

	err           string
	helpText      string // set by ":help" to a rendered listing of colon commands
	exitRequested bool
}

func newCmdModeModel(client *api.Client, repo, tag string, devices []api.Device, target map[string]api.Device) cmdModeModel {
	ti := textinput.New()
	ti.Placeholder = "command  (or  :help  for all colon commands)"
	ti.Width = 60
	ti.Focus()
	return cmdModeModel{
		client:        client,
		repo:          repo,
		tag:           tag,
		devices:       devices,
		target:        target,
		input:         ti,
		results:       make(map[string]api.DeviceResult),
		shellSessions: make(map[string]string),
		shellOffsets:  make(map[string]int),
		shellOutput:   make(map[string]string),
		shellClosed:   make(map[string]bool),
		shellErrs:     make(map[string]string),
	}
}

func (m cmdModeModel) Init() tea.Cmd { return textinput.Blink }

func (m cmdModeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		if m.detailOpen {
			rows := m.rowsToShow()
			switch msg.String() {
			case "esc", "enter", "q":
				m.detailOpen = false
			case "left", "h", "p":
				if m.rowCursor > 0 {
					m.rowCursor--
				}
			case "right", "l", "n":
				if m.rowCursor < len(rows)-1 {
					m.rowCursor++
				}
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
			updated, cmd := m.closeShellSessions()
			m = updated.(cmdModeModel)
			m.exitRequested = true
			return m, cmd
		case tea.KeyCtrlC:
			updated, cmd := m.closeShellSessions()
			m = updated.(cmdModeModel)
			m.exitRequested = true
			return m, cmd
		case tea.KeyTab:
			if !m.rowsFocused {
				if ghost := colonCommandGhostSuffix(m.input.Value()); ghost != "" {
					m.input.SetValue(m.input.Value() + ghost)
					m.input.CursorEnd()
					return m, nil
				}
			}
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
					m.peekLines = 0
				}
			case "k", "up":
				if m.rowCursor > 0 {
					m.rowCursor--
					m.peekLines = 0
				}
			case "l", "right", "+":
				m.peekLines += peekStep
			case "h", "left", "-":
				if m.peekLines > 0 {
					m.peekLines -= peekStep
					if m.peekLines < 0 {
						m.peekLines = 0
					}
				}
			case "enter":
				if m.rowCursor < len(rows) {
					id := rows[m.rowCursor].DeviceID
					if m.shellOpen {
						_, hasSession := m.shellSessions[id]
						_, hasErr := m.shellErrs[id]
						if hasSession || hasErr {
							m.detailOpen = true
						}
					} else if _, ok := m.results[id]; ok {
						m.detailOpen = true
					}
				}
			case "e":
				m.growAll++
			case "E":
				if m.growAll > 0 {
					m.growAll--
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

	case shellOpenedMsg:
		if msg.err != nil {
			m.shellErrs[msg.deviceID] = msg.err.Error()
			m.shellClosed[msg.deviceID] = true
			return m, nil
		}
		delete(m.shellErrs, msg.deviceID)
		m.shellSessions[msg.deviceID] = msg.sessionID
		return m, nil

	case shellPollTickMsg:
		if !m.shellOpen || m.liveShellCount() == 0 {
			return m, nil
		}
		var cmds []tea.Cmd
		client := m.client
		for deviceID, sessionID := range m.shellSessions {
			if m.shellClosed[deviceID] {
				continue
			}
			deviceID, sessionID, since := deviceID, sessionID, m.shellOffsets[deviceID]
			cmds = append(cmds, func() tea.Msg {
				out, err := client.PollShellOutput(sessionID, since)
				return shellOutputMsg{deviceID: deviceID, output: out, err: err}
			})
		}
		cmds = append(cmds, shellPollAfter())
		return m, tea.Batch(cmds...)

	case shellOutputMsg:
		if msg.err != nil {
			m.shellErrs[msg.deviceID] = msg.err.Error()
			return m, nil
		}
		for _, c := range msg.output.Chunks {
			m.shellOutput[msg.deviceID] += c.Data
		}
		m.shellOffsets[msg.deviceID] = msg.output.Offset
		if msg.output.Closed {
			m.shellClosed[msg.deviceID] = true
		}
		return m, nil

	case shellInputSentMsg:
		if msg.err != nil {
			m.shellErrs[msg.deviceID] = msg.err.Error()
		}
		return m, nil

	case shellClosedMsg:
		m.shellClosed[msg.deviceID] = true
		return m, nil

	case fileListMsg:
		if msg.err != nil {
			m.fileListErr = msg.err.Error()
			m.fileList = nil
			return m, nil
		}
		m.fileListErr = ""
		m.fileList = msg.files
		return m, nil

	case uploadProgressMsg:
		if !msg.done {
			m.uploadSent = msg.sent
			m.uploadTotal = msg.total
			return m, waitUploadProgress(m.uploadCh)
		}
		m.uploading = false
		if msg.err != nil {
			m.uploadErr = msg.err.Error()
		} else {
			m.uploadDone = fmt.Sprintf("uploaded %s (%d bytes)", msg.info.Name, msg.info.Size)
		}
		return m, nil
	}

	return m, nil
}

// knownColonCommands lists every ":..." command fire() understands, used to
// tell "still typing toward a known command" apart from "typed something
// invalid" and to drive ghost-text autocomplete on the command word itself.
var knownColonCommands = []string{":open", ":close", ":restart", ":upgrade", ":listf", ":uploadf", ":sendf", ":files", ":test-timeout", ":help"}

// colonCommandHelp lists every colon command with a short description, in
// the order shown by ":help".
var colonCommandHelp = []struct{ cmd, desc string }{
	{":open", "open shell on every device in view"},
	{":close", "close open shell session(s)"},
	{":restart <service>", "restart a service"},
	{":upgrade [version]", "upgrade agent to latest release or a given version"},
	{":listf", "list files stored on the server"},
	{":uploadf <local-path>", "upload a file to the server"},
	{":sendf <name> [path]", "send a stored file to target devices"},
	{":files", "list files in the common folder on target devices"},
	{":test-timeout <seconds>", "(open shell only) set the server idle timeout for open session(s), for testing expiry"},
	{":help", "list all colon commands"},
}

type colonCommandState int

const (
	colonNone colonCommandState = iota
	colonRecognized
	colonTyping
	colonUnknown
)

// classifyColonCommand buckets typed input for coloring/hinting: not a
// colon-command at all, a fully recognized one (args and all), still a
// valid prefix of exactly one known command word, or definitely invalid.
func classifyColonCommand(text string) colonCommandState {
	if !strings.HasPrefix(text, ":") {
		return colonNone
	}
	switch {
	case text == ":open", text == ":close":
		return colonRecognized
	case text == ":upgrade" || strings.HasPrefix(text, ":upgrade "):
		return colonRecognized
	case strings.HasPrefix(text, ":restart "):
		return colonRecognized
	case text == ":listf", text == ":files":
		return colonRecognized
	case strings.HasPrefix(text, ":uploadf "):
		return colonRecognized
	case strings.HasPrefix(text, ":sendf "):
		return colonRecognized
	case strings.HasPrefix(text, ":test-timeout "):
		return colonRecognized
	case text == ":help":
		return colonRecognized
	}
	for _, known := range knownColonCommands {
		if strings.HasPrefix(known, text) {
			return colonTyping
		}
	}
	return colonUnknown
}

// colonCommandStyle returns the style the command word itself should be
// rendered in, based on classifyColonCommand.
func colonCommandStyle(text string) lipgloss.Style {
	switch classifyColonCommand(text) {
	case colonRecognized:
		return okStyle
	case colonTyping:
		return dimStyle
	case colonUnknown:
		return errStyle
	default:
		return lipgloss.NewStyle()
	}
}

// colonCommandHint renders a short preview of what a typed ":..." input
// will do if fired, so the effect of a colon-command (and typos in one) are
// visible before Enter is pressed. Returns "" for plain, non-colon input.
func colonCommandHint(text string) string {
	switch classifyColonCommand(text) {
	case colonNone:
		return ""
	case colonTyping:
		return dimStyle.Render("↳ typing …")
	case colonUnknown:
		return errStyle.Render("↳ unknown command")
	}

	switch {
	case text == ":open":
		return okStyle.Render("↳ open shell on every device in view")
	case text == ":close":
		return okStyle.Render("↳ close open shell session(s)")
	case text == ":upgrade" || strings.HasPrefix(text, ":upgrade "):
		v := strings.TrimSpace(strings.TrimPrefix(text, ":upgrade"))
		if v == "" {
			return okStyle.Render("↳ upgrade agent to latest release")
		}
		return okStyle.Render("↳ upgrade agent to " + v)
	case strings.HasPrefix(text, ":restart "):
		svc := strings.TrimSpace(strings.TrimPrefix(text, ":restart "))
		if svc == "" {
			return dimStyle.Render("↳ restart <service> …")
		}
		return okStyle.Render("↳ restart service " + svc)
	case text == ":listf":
		return okStyle.Render("↳ list files stored on the server")
	case text == ":files":
		return okStyle.Render("↳ list files in the common folder on target devices")
	case strings.HasPrefix(text, ":uploadf "):
		p := strings.TrimSpace(strings.TrimPrefix(text, ":uploadf "))
		if p == "" {
			return dimStyle.Render("↳ uploadf <local-path> …")
		}
		return okStyle.Render("↳ upload " + p + " to the server")
	case strings.HasPrefix(text, ":sendf "):
		rest := strings.TrimSpace(strings.TrimPrefix(text, ":sendf "))
		if rest == "" {
			return dimStyle.Render("↳ sendf <name> [path] …")
		}
		fields := strings.Fields(rest)
		if len(fields) == 1 {
			return okStyle.Render("↳ send " + fields[0] + " to common folder on target devices")
		}
		return okStyle.Render("↳ send " + fields[0] + " to " + fields[1] + " on target devices")
	case strings.HasPrefix(text, ":test-timeout "):
		secs := strings.TrimSpace(strings.TrimPrefix(text, ":test-timeout "))
		if secs == "" {
			return dimStyle.Render("↳ test-timeout <seconds> …")
		}
		return okStyle.Render("↳ set open session idle timeout to " + secs + "s (testing)")
	case text == ":help":
		return okStyle.Render("↳ list all colon commands")
	}
	return ""
}

// colonCommandGhostSuffix returns the remainder of the single known command
// word that completes text, e.g. ":op" against knownColonCommands returns
// "en". Empty once the word is complete, args have started (a space was
// typed), or nothing matches.
func colonCommandGhostSuffix(text string) string {
	// len(text) < 2 means there's nothing after the ':' yet — don't
	// suggest until the user has typed at least one letter of a command.
	if len(text) < 2 || !strings.HasPrefix(text, ":") || strings.ContainsRune(text, ' ') {
		return ""
	}
	for _, known := range knownColonCommands {
		if strings.HasPrefix(known, text) && len(known) > len(text) {
			return known[len(text):]
		}
	}
	return ""
}

// renderCommandInput draws the input line, hand-rendering it when the text
// is a ":..." command so the command word can be colored by recognition
// state and trail into ghost-text autocomplete (Tab accepts it) — the same
// pattern provisionModel uses for its tags field. Falls back to the plain
// textinput view for ordinary (non-colon) input.
func (m cmdModeModel) renderCommandInput() string {
	value := m.input.Value()
	if !strings.HasPrefix(value, ":") {
		return m.input.View()
	}
	if m.rowsFocused {
		return colonCommandStyle(value).Render(value)
	}
	var b strings.Builder
	b.WriteString(colonCommandStyle(value).Render(value))
	b.WriteString(suggestStyle.Render(colonCommandGhostSuffix(value)))
	b.WriteString(rowCursorStyle.Render(" "))
	return b.String()
}

// renderFilesBlock renders whichever of ":listf"'s result or ":uploadf"'s
// progress/outcome is currently live, shown just above the command strip
// (the same slot the shell status line / "last: ..." summary occupy).
func (m cmdModeModel) renderFilesBlock() string {
	var b strings.Builder

	if m.uploading || m.uploadErr != "" || m.uploadDone != "" {
		switch {
		case m.uploading:
			pct := 0.0
			if m.uploadTotal > 0 {
				pct = float64(m.uploadSent) / float64(m.uploadTotal) * 100
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf("uploading %s: %s", m.uploadName, renderProgressBar(pct, 24))))
		case m.uploadErr != "":
			b.WriteString(errStyle.Render("✗ upload failed: " + m.uploadErr))
		case m.uploadDone != "":
			b.WriteString(okStyle.Render("✓ " + m.uploadDone))
		}
		return b.String()
	}

	if m.fileListErr != "" {
		return errStyle.Render("✗ " + m.fileListErr)
	}
	if m.fileList != nil {
		if len(m.fileList) == 0 {
			return dimStyle.Render("no files stored on server")
		}
		b.WriteString(dimStyle.Render("files on server:"))
		for _, f := range m.fileList {
			b.WriteString("\n  ")
			b.WriteString(fmt.Sprintf("%-24s %8d bytes   %s", f.Name, f.Size, f.UploadedAt.Local().Format("2006-01-02 15:04")))
		}
		return b.String()
	}

	return ""
}

// renderProgressBar draws a simple "[####----] 42%" bar of the given width.
func renderProgressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(width))
	return fmt.Sprintf("[%s%s] %.0f%%", strings.Repeat("#", filled), strings.Repeat("-", width-filled), pct)
}

// fire dispatches one line of typed input. ":open" and ":close" manage a
// broadcast shell session across every device currently in view; while such
// a session has at least one live device, all other input is forwarded as
// stdin to every live session rather than run as a one-shot command.
func (m cmdModeModel) fire(text string) (tea.Model, tea.Cmd) {
	if text == ":open" {
		return m.openShellSessions()
	}
	if m.shellOpen && m.liveShellCount() > 0 {
		if text == ":close" {
			return m.closeShellSessions()
		}
		if strings.HasPrefix(text, ":test-timeout ") {
			return m.fireTestTimeout(strings.TrimSpace(strings.TrimPrefix(text, ":test-timeout ")))
		}
		if text == ":help" {
			return m.fireHelp()
		}
		return m.fireShell(text)
	}
	if text == ":close" {
		m.history = append(m.history, text)
		m.histIdx = len(m.history)
		m.input.SetValue("")
		return m, nil
	}
	if strings.HasPrefix(text, ":test-timeout ") {
		m.history = append(m.history, text)
		m.histIdx = len(m.history)
		m.input.SetValue("")
		m.err = "test-timeout: no open shell session — :open first"
		return m, nil
	}
	if text == ":help" {
		return m.fireHelp()
	}
	if text == ":listf" {
		return m.fireListFiles()
	}
	if strings.HasPrefix(text, ":uploadf ") {
		return m.fireUploadFile(strings.TrimSpace(strings.TrimPrefix(text, ":uploadf ")))
	}

	m.history = append(m.history, text)
	m.histIdx = len(m.history)
	m.commandTxt = text
	m.results = make(map[string]api.DeviceResult)
	m.summary = nil
	m.rowCursor = 0
	m.detailOpen = false
	m.peekLines = 0
	m.input.SetValue("")
	m.fileList = nil
	m.fileListErr = ""
	m.uploadDone = ""
	m.uploadErr = ""
	m.helpText = ""

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

	if text == ":files" {
		action = wire.ActionListFiles
		payload = nil
	}

	if strings.HasPrefix(text, ":sendf ") {
		fields := strings.Fields(strings.TrimPrefix(text, ":sendf "))
		action = wire.ActionFetchFile
		payload = map[string]interface{}{"name": fields[0]}
		if len(fields) > 1 {
			payload["path"] = fields[1]
		}
	}

	if action == wire.ActionListFiles || action == wire.ActionFetchFile {
		var unsupported []string
		for _, d := range m.rowsToShow() {
			if !d.Online() {
				continue
			}
			if ok, reason := deviceSupportsFeature(d, "files"); !ok {
				unsupported = append(unsupported, fmt.Sprintf("%s (%s)", d.DeviceID, reason))
			}
		}
		if len(unsupported) > 0 {
			m.err = "files unsupported on: " + strings.Join(unsupported, ", ")
			return m, nil
		}
	}

	req := api.PostCommandRequest{Action: action, Target: target, Payload: payload}
	return m, func() tea.Msg {
		resp, err := client.PostCommand(req)
		return cmdPostedMsg{resp: resp, err: err}
	}
}

// fireListFiles handles ":listf" — fetches file metadata from the server's
// file store and displays it under the command strip.
func (m cmdModeModel) fireListFiles() (tea.Model, tea.Cmd) {
	m.history = append(m.history, ":listf")
	m.histIdx = len(m.history)
	m.input.SetValue("")
	m.fileListErr = ""
	m.uploadDone = ""
	m.uploadErr = ""

	client := m.client
	return m, func() tea.Msg {
		files, err := client.ListFiles()
		return fileListMsg{files: files, err: err}
	}
}

// fireUploadFile handles ":uploadf <local-path>" — validates the local
// path, then starts an upload in a background goroutine that reports
// progress back into the TUI over m.uploadCh (see waitUploadProgress).
func (m cmdModeModel) fireUploadFile(path string) (tea.Model, tea.Cmd) {
	m.history = append(m.history, ":uploadf "+path)
	m.histIdx = len(m.history)
	m.input.SetValue("")
	m.fileList = nil
	m.fileListErr = ""

	if path == "" {
		m.err = "usage: :uploadf <local-path>"
		return m, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	if info.IsDir() {
		m.err = path + " is a directory"
		return m, nil
	}

	m.err = ""
	m.uploading = true
	m.uploadName = path
	m.uploadSent = 0
	m.uploadTotal = info.Size()
	m.uploadErr = ""
	m.uploadDone = ""

	ch := make(chan uploadProgressMsg, 1)
	m.uploadCh = ch
	client := m.client

	go func() {
		fi, err := client.UploadFile(path, func(sent, total int64) {
			ch <- uploadProgressMsg{sent: sent, total: total}
		})
		ch <- uploadProgressMsg{done: true, info: fi, err: err}
	}()

	return m, waitUploadProgress(ch)
}

// openShellSessions handles ":open" — it opens one persistent shell per
// device currently in view (skipping offline devices and any that don't
// meet the "shell" feature's minimum agent version) and starts the poll
// loop for their output.
func (m cmdModeModel) openShellSessions() (tea.Model, tea.Cmd) {
	m.history = append(m.history, ":open")
	m.histIdx = len(m.history)
	m.input.SetValue("")

	if m.shellOpen && m.liveShellCount() > 0 {
		m.err = "shell already open — :close first"
		return m, nil
	}

	rows := m.rowsToShow()
	if len(rows) == 0 {
		m.err = "no devices in target"
		return m, nil
	}

	m.shellOpen = true
	m.shellSessions = make(map[string]string)
	m.shellOffsets = make(map[string]int)
	m.shellOutput = make(map[string]string)
	m.shellClosed = make(map[string]bool)
	m.shellErrs = make(map[string]string)
	m.rowCursor = 0
	m.detailOpen = false
	m.peekLines = 0
	m.err = ""

	client := m.client
	cmds := []tea.Cmd{shellPollAfter()}
	for _, d := range rows {
		if !d.Online() {
			m.shellErrs[d.DeviceID] = "device is offline"
			m.shellClosed[d.DeviceID] = true
			continue
		}
		if ok, reason := deviceSupportsFeature(d, "shell"); !ok {
			m.shellErrs[d.DeviceID] = reason
			m.shellClosed[d.DeviceID] = true
			continue
		}
		deviceID := d.DeviceID
		cmds = append(cmds, func() tea.Msg {
			id, err := client.OpenShell(deviceID)
			return shellOpenedMsg{deviceID: deviceID, sessionID: id, err: err}
		})
	}
	return m, tea.Batch(cmds...)
}

// closeShellSessions handles ":close" (and exiting command mode while a
// shell is open) — it tells every still-live session to terminate. Safe to
// call when no shell is open (it's then a no-op).
func (m cmdModeModel) closeShellSessions() (tea.Model, tea.Cmd) {
	client := m.client
	var cmds []tea.Cmd
	for deviceID, sessionID := range m.shellSessions {
		if m.shellClosed[deviceID] {
			continue
		}
		deviceID, sessionID := deviceID, sessionID
		cmds = append(cmds, func() tea.Msg {
			client.CloseShell(sessionID)
			return shellClosedMsg{deviceID: deviceID}
		})
	}
	m.shellOpen = false
	return m, tea.Batch(cmds...)
}

// fireShell broadcasts one line of stdin to every live shell session.
func (m cmdModeModel) fireShell(text string) (tea.Model, tea.Cmd) {
	m.history = append(m.history, text)
	m.histIdx = len(m.history)
	m.input.SetValue("")

	client := m.client
	var cmds []tea.Cmd
	for deviceID, sessionID := range m.shellSessions {
		if m.shellClosed[deviceID] {
			continue
		}
		deviceID, sessionID := deviceID, sessionID
		cmds = append(cmds, func() tea.Msg {
			err := client.SendShellInput(sessionID, text+"\n")
			return shellInputSentMsg{deviceID: deviceID, err: err}
		})
	}
	return m, tea.Batch(cmds...)
}

// fireHelp handles ":help" — renders the full list of colon commands under
// the command strip.
func (m cmdModeModel) fireHelp() (tea.Model, tea.Cmd) {
	m.history = append(m.history, ":help")
	m.histIdx = len(m.history)
	m.input.SetValue("")

	var b strings.Builder
	for i, c := range colonCommandHelp {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.cmd + " — " + c.desc)
	}
	m.helpText = b.String()
	m.err = ""
	return m, nil
}

// fireTestTimeout handles ":test-timeout <seconds>" — overrides the
// server-side idle-reap timeout on every live open shell session, so idle
// expiry can be exercised without waiting out the real default.
func (m cmdModeModel) fireTestTimeout(secsText string) (tea.Model, tea.Cmd) {
	m.history = append(m.history, ":test-timeout "+secsText)
	m.histIdx = len(m.history)
	m.input.SetValue("")

	seconds, err := strconv.Atoi(secsText)
	if err != nil || seconds <= 0 {
		m.err = "test-timeout: seconds must be a positive integer"
		return m, nil
	}

	client := m.client
	var cmds []tea.Cmd
	for deviceID, sessionID := range m.shellSessions {
		if m.shellClosed[deviceID] {
			continue
		}
		deviceID, sessionID := deviceID, sessionID
		cmds = append(cmds, func() tea.Msg {
			err := client.SetShellTimeout(sessionID, seconds)
			return shellInputSentMsg{deviceID: deviceID, err: err}
		})
	}
	return m, tea.Batch(cmds...)
}

// liveShellCount returns how many devices have a shell session that hasn't
// closed or errored out.
func (m cmdModeModel) liveShellCount() int {
	n := 0
	for id := range m.shellSessions {
		if !m.shellClosed[id] {
			n++
		}
	}
	return n
}

// shellRowCols renders the status/output columns for one device's row while
// a shell is open.
func (m cmdModeModel) shellRowCols(deviceID string) (statusCol, outCol string) {
	if errMsg, ok := m.shellErrs[deviceID]; ok {
		return errStyle.Render("✗ " + truncate(errMsg, m.outputWidth())), ""
	}
	tail := lastNonEmptyLine(m.shellOutput[deviceID])
	if m.shellClosed[deviceID] {
		return dimStyle.Render("closed"), tail
	}
	if _, ok := m.shellSessions[deviceID]; ok {
		return okStyle.Render("● live"), tail
	}
	return dimStyle.Render("… opening"), ""
}

// lastNonEmptyLine returns the final trailing line of s, for a one-line
// live tail in the device row list.
func lastNonEmptyLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// tailPreview returns the last n non-empty trailing lines of s, plus a
// count of earlier lines omitted — the shell-mode analog of previewLines,
// which shows leading lines instead.
func tailPreview(s string, n int) (lines []string, more int) {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil, 0
	}
	all := strings.Split(s, "\n")
	if len(all) <= n {
		return all, 0
	}
	return all[len(all)-n:], len(all) - n
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
	if m.shellOpen {
		b.WriteString("  ")
		if m.liveShellCount() > 0 {
			b.WriteString(okStyle.Render(fmt.Sprintf("● SHELL OPEN (%d live)", m.liveShellCount())))
		} else {
			b.WriteString(dimStyle.Render("SHELL CLOSED"))
		}
	}
	b.WriteString("\n\n")

	rows := m.rowsToShow()
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("no devices in target"))
	}
	outW := m.outputWidth()
	for i, d := range rows {
		var statusCol, outCol, peekSrc string
		has := true
		if m.shellOpen {
			statusCol, outCol = m.shellRowCols(d.DeviceID)
			peekSrc = m.shellOutput[d.DeviceID]
		} else {
			res, hasRes := m.results[d.DeviceID]
			has = hasRes
			statusCol = dimStyle.Render("—")
			if hasRes {
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
				peekSrc = res.Stdout
				if res.Status == "error" && strings.TrimSpace(res.Stderr) != "" {
					peekSrc = res.Stderr
				}
			} else if m.dispatched {
				statusCol = dimStyle.Render("… wait")
			}
		}
		marker := "  "
		if m.rowsFocused && i == m.rowCursor {
			marker = "▶ "
		}
		line := fmt.Sprintf("%s%s %-16s %-14s %s", marker, onlineDot(d.Online()), truncate(deviceName(d), 16), statusCol, truncate(outCol, outW))
		if m.rowsFocused && i == m.rowCursor {
			line = rowCursorStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")

		// growAll (e/E) shows extra lines under every row; the cursor row
		// also gets its own peekLines (l/h) on top of that, so it can be
		// expanded further than the rest without growing all of them.
		isCursor := m.rowsFocused && i == m.rowCursor
		extra := m.growAll
		if isCursor && m.peekLines > extra {
			extra = m.peekLines
		}
		if extra > 0 && has {
			var peek []string
			var more int
			if m.shellOpen {
				peek, more = tailPreview(peekSrc, extra)
			} else {
				peek, more = previewLines(peekSrc, extra)
			}
			for _, l := range peek {
				b.WriteString(dimStyle.Render("      " + truncate(l, outW)))
				b.WriteString("\n")
			}
			if more > 0 && isCursor {
				b.WriteString(dimStyle.Render(fmt.Sprintf("      … %d more line(s), 'l' to expand", more)))
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n")
	if m.shellOpen {
		closedCount := len(m.shellSessions) + len(m.shellErrs) - m.liveShellCount()
		b.WriteString(dimStyle.Render(fmt.Sprintf("shell: %d live, %d closed   :close to end", m.liveShellCount(), closedCount)))
		b.WriteString("\n\n")
	} else if m.commandTxt != "" && m.summary != nil {
		b.WriteString(dimStyle.Render(fmt.Sprintf("last: %s   pending=%d ok=%d failed=%d timeout=%d",
			m.commandTxt, m.summary["pending"], m.summary["completed"], m.summary["failed"], m.summary["timeout"])))
		b.WriteString("\n\n")
	}

	if block := m.renderFilesBlock(); block != "" {
		b.WriteString(block)
		b.WriteString("\n\n")
	}

	b.WriteString(m.renderCommandInput())
	b.WriteString("\n")
	if !m.rowsFocused {
		if hint := colonCommandHint(strings.TrimSpace(m.input.Value())); hint != "" {
			b.WriteString(hint)
			b.WriteString("\n")
		}
	}
	if m.err != "" {
		b.WriteString(errStyle.Render("✗ " + m.err))
		b.WriteString("\n")
	}
	if m.helpText != "" {
		b.WriteString(dimStyle.Render(m.helpText))
		b.WriteString("\n")
	}
	switch {
	case m.rowsFocused:
		b.WriteString(hintBarStyle.Render("↑↓ move   ←→ peek this row   e/E · grow/shrink all rows   enter · view full output   tab · back to launcher   esc · back to browse"))
	case m.shellOpen:
		b.WriteString(hintBarStyle.Render("enter · send to all   ↑↓ history   tab · browse output   :close · end session(s)   esc · close & back to browse"))
	default:
		b.WriteString(hintBarStyle.Render("↑↓ history   enter · fire   tab · browse results   esc · back to browse   (:open · shell)"))
	}

	return b.String()
}

func (m cmdModeModel) renderDetail() string {
	rows := m.rowsToShow()
	if m.rowCursor >= len(rows) {
		return ""
	}
	d := rows[m.rowCursor]

	if m.shellOpen {
		return m.renderShellDetail(d)
	}

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
	b.WriteString(hintBarStyle.Render("←/→ · prev/next device   esc / enter · close"))

	return paneFocusedStyle.Render(b.String())
}

// renderShellDetail shows one device's full live shell transcript.
func (m cmdModeModel) renderShellDetail(d api.Device) string {
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render(deviceName(d)))
	b.WriteString("  ")
	switch {
	case m.shellErrs[d.DeviceID] != "":
		b.WriteString(errStyle.Render("✗ " + m.shellErrs[d.DeviceID]))
	case m.shellClosed[d.DeviceID]:
		b.WriteString(dimStyle.Render("closed"))
	default:
		b.WriteString(okStyle.Render("● live"))
	}
	b.WriteString("\n\n")

	out := m.shellOutput[d.DeviceID]
	if strings.TrimSpace(out) != "" {
		b.WriteString(out)
		b.WriteString("\n")
	} else {
		b.WriteString(dimStyle.Render("(no output yet)"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(hintBarStyle.Render("←/→ · prev/next device   esc / enter · close"))

	return paneFocusedStyle.Render(b.String())
}

// cmdRowPrefixWidth is the rendered width everything before the output
// column takes up in a device row: marker(2) + status dot(1) + space(1) +
// name(16) + space(1) + status(14) + space(1), matching the Sprintf layout
// in View(). outputWidth uses it to fit the output column to the actual
// terminal width instead of an arbitrary fixed cap.
const cmdRowPrefixWidth = 36

// outputWidth returns how many characters of live/one-shot output to show
// per device row, sized to the real terminal width once known (falling
// back to a reasonable default before the first WindowSizeMsg arrives).
func (m cmdModeModel) outputWidth() int {
	if m.width <= 0 {
		return 100
	}
	w := m.width - cmdRowPrefixWidth - 2
	if w < 20 {
		w = 20
	}
	return w
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

// previewLines returns up to n non-empty leading lines of s (trimmed of
// trailing whitespace/newline), plus a count of how many lines remain beyond
// that for a "N more lines" hint.
func previewLines(s string, n int) (lines []string, more int) {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil, 0
	}
	all := strings.Split(s, "\n")
	if len(all) <= n {
		return all, 0
	}
	return all[:n], len(all) - n
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
