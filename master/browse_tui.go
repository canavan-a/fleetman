package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/canavan-a/fleetman/internal/api"
)

// --- modes ---

type pane int

const (
	paneTags pane = iota
	paneDevices
)

type mode int

const (
	modeBrowse mode = iota
	modeTagPicker
	modeTagNew
	modeUntagConfirm
	modeProvision
	modeDeleteConfirm
	modeDeleteTagConfirm
	modeCommand
)

const refreshInterval = 5 * time.Second

// noTagFilter is a synthetic activeTag value (never a real tag name — tags
// can't contain NUL) that filters the device pane to devices with zero tags.
const noTagFilter = "\x00none"

// --- messages ---

type tickMsg struct{}

// cascadeTickMsg drives the "f" key's color-wave animation across the
// banner letters — a lightweight ad-hoc timer separate from the data
// refresh tickMsg above.
type cascadeTickMsg struct{}

const cascadeFrameDelay = 15 * time.Millisecond

func cascadeTickCmd() tea.Cmd {
	return tea.Tick(cascadeFrameDelay, func(t time.Time) tea.Msg { return cascadeTickMsg{} })
}

// cascadeColors is the palette the wave cycles through as it sweeps.
var cascadeColors = []lipgloss.Color{
	lipgloss.Color("196"), // red
	lipgloss.Color("208"), // orange
	lipgloss.Color("226"), // yellow
	lipgloss.Color("46"),  // green
	lipgloss.Color("51"),  // cyan
	lipgloss.Color("33"),  // blue
	lipgloss.Color("201"), // magenta
}

// cascadeWidth is how many columns wide the moving color band is.
const cascadeWidth = 6

// startCascade kicks off the banner color-wave animation. It's a no-op if
// a sweep is already running, so callers never need to check cascadeActive
// themselves — this is the single entry point for triggering the effect,
// whether from a keypress or from noticing new state (a tag/device added).
func (m *mainModel) startCascade() tea.Cmd {
	if m.cascadeActive {
		return nil
	}
	m.cascadeStep = 0
	m.cascadeActive = true
	return cascadeTickCmd()
}

type dataMsg struct {
	tags    []string
	devices []api.Device
	err     error
}

type actionDoneMsg struct {
	err error
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg{} })
}

func fetchData(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		tags, err := client.ListTags()
		if err != nil {
			return dataMsg{err: err}
		}
		devices, err := client.ListDevices()
		if err != nil {
			return dataMsg{err: err}
		}
		sort.Strings(tags)
		sort.Slice(devices, func(i, j int) bool { return devices[i].DeviceID < devices[j].DeviceID })
		return dataMsg{tags: tags, devices: devices}
	}
}

// --- root model ---

type mainModel struct {
	client   *api.Client
	repo     string
	host     string // bare server host, e.g. "fleetman.example.com"
	insecure bool

	focus     pane
	tags      []string
	tagCursor int
	activeTag string // "" = all

	allDevices []api.Device
	devices    []api.Device // filtered to activeTag
	devCursor  int

	selected map[string]api.Device

	width, height int
	err           string
	status        string
	compact       bool

	cascadeActive bool
	cascadeStep   int

	mode             mode
	subModel         tea.Model
	pendingTagDelete string

	quitting bool
}

func newMainModel(cfg *Config) mainModel {
	return mainModel{
		client:   api.New(cfg.BaseURL(), cfg.MasterKey),
		repo:     cfg.EffectiveRepo(),
		host:     cfg.Server,
		insecure: cfg.Insecure,
		selected: make(map[string]api.Device),
	}
}

func (m mainModel) Init() tea.Cmd {
	return tea.Batch(fetchData(m.client), tickEvery(refreshInterval))
}

func (m mainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.mode != modeBrowse && m.subModel != nil {
			updated, cmd := m.subModel.Update(msg)
			m.subModel = updated
			return m, cmd
		}
		return m, nil

	case tickMsg:
		if m.mode == modeBrowse {
			return m, tea.Batch(fetchData(m.client), tickEvery(refreshInterval))
		}
		return m, tickEvery(refreshInterval)

	case dataMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		newTag := len(msg.tags) > len(m.tags)
		newDevice := len(msg.devices) > len(m.allDevices)
		m.err = ""
		m.tags = msg.tags
		m.allDevices = msg.devices
		m.applyFilter()
		if newTag || newDevice {
			return m, m.startCascade()
		}
		return m, nil

	case cascadeTickMsg:
		if !m.cascadeActive {
			return m, nil
		}
		m.cascadeStep++
		if m.cascadeStep > longestBannerLine()+cascadeWidth {
			m.cascadeActive = false
			return m, nil
		}
		return m, cascadeTickCmd()

	case actionDoneMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.err = ""
		}
		m.mode = modeBrowse
		m.subModel = nil
		return m, fetchData(m.client)
	}

	if m.mode != modeBrowse {
		return m.updateSubMode(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleBrowseKey(msg)
	}

	return m, nil
}

func (m *mainModel) applyFilter() {
	switch m.activeTag {
	case "":
		m.devices = m.allDevices
	case noTagFilter:
		var filtered []api.Device
		for _, d := range m.allDevices {
			if len(d.Tags) == 0 {
				filtered = append(filtered, d)
			}
		}
		m.devices = filtered
	default:
		var filtered []api.Device
		for _, d := range m.allDevices {
			for _, t := range d.Tags {
				if t == m.activeTag {
					filtered = append(filtered, d)
					break
				}
			}
		}
		m.devices = filtered
	}
	if m.devCursor >= len(m.devices) {
		m.devCursor = len(m.devices) - 1
	}
	if m.devCursor < 0 {
		m.devCursor = 0
	}
}

func (m mainModel) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit

	case "left", "h":
		m.focus = paneTags
		return m, nil
	case "right", "l":
		m.focus = paneDevices
		return m, nil

	case "j", "down":
		if m.focus == paneTags {
			if m.tagCursor < len(m.tags)+1 {
				m.tagCursor++
			}
		} else {
			if m.devCursor < len(m.devices)-1 {
				m.devCursor++
			}
		}
		return m, nil

	case "k", "up":
		if m.focus == paneTags {
			if m.tagCursor > 0 {
				m.tagCursor--
			}
		} else {
			if m.devCursor > 0 {
				m.devCursor--
			}
		}
		return m, nil

	case "enter":
		if m.focus == paneTags {
			m.activeTag = m.tagName(m.tagCursor)
			m.applyFilter()
			m.focus = paneDevices
		}
		return m, nil

	case " ":
		if m.focus == paneDevices && len(m.devices) > 0 {
			d := m.devices[m.devCursor]
			if _, ok := m.selected[d.DeviceID]; ok {
				delete(m.selected, d.DeviceID)
			} else {
				m.selected[d.DeviceID] = d
			}
		}
		return m, nil

	case "A":
		for _, d := range m.devices {
			m.selected[d.DeviceID] = d
		}
		return m, nil

	case "D":
		m.selected = make(map[string]api.Device)
		return m, nil

	case "a":
		if len(m.selected) == 0 {
			m.err = "select devices first (space)"
			return m, nil
		}
		m.mode = modeTagPicker
		m.subModel = newTagPickerModel(m.tags)
		return m, nil

	case "n":
		m.mode = modeTagNew
		label := "Tag name"
		if len(m.selected) > 0 {
			label = fmt.Sprintf("Tag name (assigns %d selected device(s))", len(m.selected))
		}
		m.subModel = newTextInputModelWithSuggestions(label, "", m.tags)
		return m, nil

	case "d", "delete", "backspace":
		return m.handleDeleteKey()

	case "p":
		m.mode = modeProvision
		m.subModel = newProvisionModel(m.client, m.repo, m.host, m.insecure, m.tags)
		return m, nil

	case "c", "r":
		if len(m.devices) == 0 {
			m.err = "no devices in view"
			return m, nil
		}
		m.mode = modeCommand
		m.subModel = newCmdModeModel(m.client, m.activeTag, m.devices, m.selected)
		return m, m.subModel.Init()

	case "b":
		m.compact = !m.compact
		return m, nil

	case "f":
		return m, m.startCascade()

	case "esc":
		m.err = ""
		return m, nil
	}
	return m, nil
}

// handleDeleteKey is the single contextual "delete" action, bound to d
// (and Del/Backspace as aliases). What it does depends on what's currently
// selected:
//   - tags pane focused, cursor on a real tag → delete that tag
//   - devices selected + a tag is active     → untag them from that tag
//   - devices selected, no active tag        → delete the devices entirely
func (m mainModel) handleDeleteKey() (tea.Model, tea.Cmd) {
	if m.focus == paneTags {
		name := m.tagName(m.tagCursor)
		if name == "" || name == noTagFilter {
			m.err = "cannot delete [all] or [no tag]"
			return m, nil
		}
		if name == m.activeTag {
			m.activeTag = ""
			m.tagCursor = 0
			m.applyFilter()
		}
		m.pendingTagDelete = name
		m.mode = modeDeleteTagConfirm
		m.subModel = newConfirmModel(fmt.Sprintf("Delete tag %q? (devices keep their other tags/records — only the tag itself is removed)", name))
		return m, nil
	}

	if len(m.selected) == 0 {
		m.err = "select devices first (space), or focus the tags pane to delete a tag"
		return m, nil
	}

	if m.activeTag != "" {
		m.mode = modeUntagConfirm
		m.subModel = newConfirmModel(fmt.Sprintf("Remove %d device(s) from %q?", len(m.selected), m.activeTag))
		return m, nil
	}

	m.mode = modeDeleteConfirm
	m.subModel = newConfirmModel(fmt.Sprintf("Delete %d device(s) and revoke tokens?", len(m.selected)))
	return m, nil
}

func (m mainModel) updateSubMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.mode == modeCommand {
			// let command mode handle its own esc (it might be in a nested state)
		} else {
			m.mode = modeBrowse
			m.subModel = nil
			return m, nil
		}
	}

	updated, cmd := m.subModel.Update(msg)
	m.subModel = updated

	switch sm := updated.(type) {
	case tagPickerModel:
		if sm.done {
			if sm.cancelled || sm.chosen == "" {
				m.mode = modeBrowse
				m.subModel = nil
				return m, nil
			}
			return m, m.doTagDevices(sm.chosen)
		}
	case textInputModel:
		if sm.done {
			if sm.cancelled || strings.TrimSpace(sm.value) == "" {
				m.mode = modeBrowse
				m.subModel = nil
				return m, nil
			}
			return m, m.doCreateAndTag(strings.TrimSpace(sm.value))
		}
	case confirmModel:
		if sm.done {
			if sm.cancelled || !sm.confirmed {
				m.mode = modeBrowse
				m.subModel = nil
				return m, nil
			}
			if m.mode == modeUntagConfirm {
				return m, m.doUntag()
			}
			if m.mode == modeDeleteConfirm {
				return m, m.doDelete()
			}
			if m.mode == modeDeleteTagConfirm {
				return m, m.doDeleteTag()
			}
		}
	case provisionModel:
		if sm.done {
			if sm.cancelled {
				m.mode = modeBrowse
				m.subModel = nil
				return m, nil
			}
			// stays in provision mode showing the result until the user closes it
			if sm.closed {
				m.mode = modeBrowse
				m.subModel = nil
				return m, fetchData(m.client)
			}
		}
	case cmdModeModel:
		if sm.exitRequested {
			m.mode = modeBrowse
			m.subModel = nil
			return m, fetchData(m.client)
		}
	}

	return m, cmd
}

func (m mainModel) selectedIDs() []string {
	ids := make([]string, 0, len(m.selected))
	for id := range m.selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m mainModel) doTagDevices(tag string) tea.Cmd {
	ids := m.selectedIDs()
	client := m.client
	return func() tea.Msg {
		err := client.TagDevices(tag, ids)
		return actionDoneMsg{err: err}
	}
}

func (m mainModel) doDeleteTag() tea.Cmd {
	tag := m.pendingTagDelete
	client := m.client
	return func() tea.Msg {
		return actionDoneMsg{err: client.DeleteTag(tag)}
	}
}

func (m mainModel) doCreateAndTag(tag string) tea.Cmd {
	ids := m.selectedIDs()
	client := m.client
	return func() tea.Msg {
		// Reusing an existing tag name is fine — CreateTag conflicting just
		// means we skip straight to (optionally) assigning devices to it.
		if err := client.CreateTag(tag); err != nil && !strings.Contains(err.Error(), "already exists") {
			return actionDoneMsg{err: err}
		}
		if len(ids) == 0 {
			return actionDoneMsg{}
		}
		return actionDoneMsg{err: client.TagDevices(tag, ids)}
	}
}

func (m mainModel) doUntag() tea.Cmd {
	ids := m.selectedIDs()
	tag := m.activeTag
	client := m.client
	return func() tea.Msg {
		return actionDoneMsg{err: client.UntagDevices(tag, ids)}
	}
}

func (m mainModel) doDelete() tea.Cmd {
	ids := m.selectedIDs()
	client := m.client
	return func() tea.Msg {
		for _, id := range ids {
			if err := client.DeleteDevice(id); err != nil {
				return actionDoneMsg{err: err}
			}
			delete(m.selected, id)
		}
		return actionDoneMsg{}
	}
}

// tagName maps a tagCursor position to the filter it represents:
// 0 = [all], 1 = [no tag], 2.. = real tags.
func (m mainModel) tagName(idx int) string {
	switch idx {
	case 0:
		return ""
	case 1:
		return noTagFilter
	default:
		return m.tags[idx-2]
	}
}

// --- view ---

func (m mainModel) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "loading…"
	}

	switch m.mode {
	case modeTagPicker, modeTagNew, modeUntagConfirm, modeDeleteConfirm, modeDeleteTagConfirm, modeProvision:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.subModel.View())
	case modeCommand:
		return m.subModel.View()
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	tagsW := 20
	sideW := 24
	devW := m.width - tagsW - sideW - 8
	if devW < 20 {
		devW = 20
	}

	tagsPane := m.renderTagsPane(tagsW, bodyHeight)
	devPane := m.renderDevicesPane(devW, bodyHeight)
	sidePane := m.renderSidebar(sideW, bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, tagsPane, devPane, sidePane)

	out := header + "\n" + body + "\n" + footer
	if m.err != "" {
		out += "\n" + errStyle.Render("✗ "+m.err)
	}
	return out
}

func (m mainModel) renderHeader() string {
	online, total := 0, len(m.allDevices)
	for _, d := range m.allDevices {
		if d.Online() {
			online++
		}
	}
	status := statusBarStyle.Render(fmt.Sprintf("● %d online  ○ %d offline", online, total-online))

	if m.compact {
		left := "fleetman"
		if m.cascadeActive {
			left = renderCascadeLine(left, m.cascadeStep)
		} else {
			left = paneTitleStyle.Render(left)
		}
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(status) - 2
		if gap < 1 {
			gap = 1
		}
		return left + strings.Repeat(" ", gap) + status
	}

	var banner string
	if m.cascadeActive {
		banner = renderCascadeBanner(m.cascadeStep)
	} else {
		banner = bannerStyle.Render(strings.TrimPrefix(bannerArt, "\n"))
	}
	gap := m.width - lipgloss.Width(status) - 2
	if gap < 0 {
		gap = 0
	}
	statusLine := strings.Repeat(" ", gap) + status
	return banner + "\n" + statusLine
}

func (m mainModel) renderFooter() string {
	return hintBarStyle.Render("[space] select  [a] tag  [n] new tag  [d] delete (tag under cursor, or untag/delete selected)  [p] provision  [c/r] run cmd  [b] compact  [q] quit")
}

func (m mainModel) renderTagsPane(w, h int) string {
	style := paneStyle
	if m.focus == paneTags {
		style = paneFocusedStyle
	}

	var b strings.Builder
	b.WriteString(paneTitleStyle.Render("TAGS"))
	b.WriteString("\n\n")

	renderTagRow := func(cursor int, label string) {
		line := "  " + label
		if cursor == m.tagCursor {
			line = "▶ " + label
			if m.focus == paneTags {
				line = rowCursorStyle.Render(line)
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	renderTagRow(0, "[all]")
	renderTagRow(1, "[no tag]")

	for i, t := range m.tags {
		renderTagRow(i+2, t)
	}

	return style.Width(w).Height(h).Render(b.String())
}

func (m mainModel) renderDevicesPane(w, h int) string {
	style := paneStyle
	if m.focus == paneDevices {
		style = paneFocusedStyle
	}

	title := "all devices"
	switch m.activeTag {
	case "":
	case noTagFilter:
		title = "no tag"
	default:
		title = m.activeTag
	}

	var b strings.Builder
	b.WriteString(paneTitleStyle.Render(fmt.Sprintf("%s (%d devices)", title, len(m.devices))))
	if m.devCursor >= 0 && m.devCursor < len(m.devices) {
		cur := m.devices[m.devCursor]
		if deviceName(cur) != cur.DeviceID {
			b.WriteString("  ")
			b.WriteString(dimStyle.Render(cur.DeviceID))
		}
	}
	b.WriteString("\n\n")

	if len(m.devices) == 0 {
		b.WriteString(dimStyle.Render("no devices"))
	}

	for i, d := range m.devices {
		_, sel := m.selected[d.DeviceID]
		marker := "  "
		if i == m.devCursor && m.focus == paneDevices {
			marker = "▶ "
		}
		checkbox := "[ ]"
		if sel {
			checkbox = "[x]"
		}
		version := d.Version
		if version == "" {
			version = "—"
		}
		line := fmt.Sprintf("%s%s %s %-16s %-8s %-8s", marker, onlineDot(d.Online()), checkbox, truncate(deviceName(d), 16), version, d.Arch)
		if sel {
			line = rowSelectedStyle.Render(line)
		} else if i == m.devCursor && m.focus == paneDevices {
			line = rowCursorStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	return style.Width(w).Height(h).Render(b.String())
}

func (m mainModel) renderSidebar(w, h int) string {
	var b strings.Builder
	b.WriteString(paneTitleStyle.Render(fmt.Sprintf("SELECTED (%d)", len(m.selected))))
	b.WriteString("\n\n")

	for _, id := range m.selectedIDs() {
		b.WriteString(truncate(deviceName(m.selected[id]), w-2))
		b.WriteString("\n")
	}

	if len(m.selected) > 0 {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("[a] add tag\n[n] new tag\n[D] clear"))
	}

	return paneStyle.Width(w).Height(h).Render(b.String())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
