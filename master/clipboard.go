package main

import (
	"os/exec"
	"runtime"
)

// copyToClipboard writes text to the system clipboard by shelling out to
// whatever tool is available. No extra dependency: tries the common CLI
// clipboard tools per platform and reports whether one succeeded.
func copyToClipboard(text string) error {
	candidates := clipboardCommands()

	var lastErr error
	for _, args := range candidates {
		path, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, args[1:]...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			lastErr = err
			continue
		}
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		stdin.Write([]byte(text))
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errNoClipboardTool
	}
	return lastErr
}

var errNoClipboardTool = clipboardErr("no clipboard tool found (xclip, xsel, wl-copy, or pbcopy) — copy the command manually")

type clipboardErr string

func (e clipboardErr) Error() string { return string(e) }

func clipboardCommands() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	default:
		return [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	}
}
