package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/canavan-a/fleetman/internal/api"
)

// deviceName returns the device's role label if it has one, falling back to
// its server-assigned device_id. This is the single place that decides what
// a device is "called" throughout the TUI.
func deviceName(d api.Device) string {
	if v := d.Labels["role"]; v != "" {
		return v
	}
	return d.DeviceID
}

// Shared style constants — "amber ember" warm palette.
var (
	accentColor  = lipgloss.Color("214") // #ffaf00 amber
	mutedColor   = lipgloss.Color("137") // #af8a5a warm tan
	hintColor    = lipgloss.Color("58")  // #5a4a2a dark amber-brown
	errorColor   = lipgloss.Color("203") // #ff5f5f warm red
	onlineColor  = lipgloss.Color("220") // #ffd700 gold
	offlineColor = lipgloss.Color("238") // #4a3a2a-ish dark neutral
)

var (
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(hintColor).
			Padding(0, 1)

	paneFocusedStyle = paneStyle.
				BorderForeground(accentColor)

	paneTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	rowSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accentColor)

	rowCursorStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236"))

	dimStyle = lipgloss.NewStyle().Foreground(hintColor)

	// suggestStyle is for actionable secondary info (e.g. autocomplete
	// matches) that needs to actually be legible, unlike footer hints.
	suggestStyle = lipgloss.NewStyle().Foreground(mutedColor)

	errStyle = lipgloss.NewStyle().Foreground(errorColor)
	okStyle  = lipgloss.NewStyle().Foreground(onlineColor)

	hintBarStyle = lipgloss.NewStyle().Foreground(hintColor)
)

// bannerArt is the full-size fleetman wordmark shown in the browse header.
const bannerArt = `
  __ _           _
 / _| | ___  ___| |_ _ __ ___   __ _ _ __
| |_| |/ _ \/ _ \ __| '_ ` + "`" + ` _ \ / _` + "`" + ` | '_ \
|  _| |  __/  __/ |_| | | | | | (_| | | | |
|_| |_|\___|\___|\__|_| |_| |_|\__,_|_| |_|`

var bannerStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)

func longestBannerLine() int {
	longest := 0
	for _, line := range strings.Split(strings.TrimPrefix(bannerArt, "\n"), "\n") {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}

// renderCascadeLine colors a single line of text with a wave of color
// sweeping across its columns, centered at cascadeStep.
func renderCascadeLine(line string, step int) string {
	var b strings.Builder
	for col, r := range line {
		if d := col - step; d >= -cascadeWidth/2 && d <= cascadeWidth/2 {
			c := cascadeColors[((col+step)%len(cascadeColors)+len(cascadeColors))%len(cascadeColors)]
			b.WriteString(lipgloss.NewStyle().Foreground(c).Bold(true).Render(string(r)))
		} else {
			b.WriteString(bannerStyle.Render(string(r)))
		}
	}
	return b.String()
}

// renderCascadeBanner colors the multi-line banner art with a color wave
// sweeping across the same column on every line, so it reads as a single
// band moving across the letters.
func renderCascadeBanner(step int) string {
	lines := strings.Split(strings.TrimPrefix(bannerArt, "\n"), "\n")
	for i, line := range lines {
		lines[i] = renderCascadeLine(line, step)
	}
	return strings.Join(lines, "\n")
}

func onlineDot(online bool) string {
	if online {
		return lipgloss.NewStyle().Foreground(onlineColor).Render("●")
	}
	return lipgloss.NewStyle().Foreground(offlineColor).Render("○")
}
