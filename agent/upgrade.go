package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/canavan-a/fleetman/wire"
)

const (
	// How long the new binary gets to reach the server before we rollback.
	rollbackTimeout = 60 * time.Second
)

// execUpgrade handles the OTA self-update flow:
// 1. Download binary from URL in payload
// 2. chmod +x, keep .old rollback copy, atomic rename swap
// 3. Restart per init type
// On failure, sends an error result back to the server.
func (a *Agent) execUpgrade(cmd *wire.Command) {
	result := wire.Result{
		CommandID: cmd.CommandID,
		DeviceID:  a.cfg.DeviceID,
	}

	urlRaw, ok := cmd.Payload["url"]
	if !ok {
		result.Stderr = "missing 'url' in upgrade payload"
		result.Retcode = 1
		a.sendResult(result)
		return
	}
	url, ok := urlRaw.(string)
	if !ok || url == "" {
		result.Stderr = "invalid url in upgrade payload"
		result.Retcode = 1
		a.sendResult(result)
		return
	}

	exePath := a.host.ExePath
	exeDir := filepath.Dir(exePath)
	tmpPath := filepath.Join(exeDir, "fleet-agent.new")
	oldPath := exePath + ".old"

	log.Printf("upgrade: downloading %s", url)

	// 1. Download to temp file in same dir (same filesystem for atomic rename).
	if err := downloadFile(tmpPath, url); err != nil {
		result.Stderr = fmt.Sprintf("download failed: %v", err)
		result.Retcode = 1
		a.sendResult(result)
		return
	}

	// 2. chmod +x.
	if err := os.Chmod(tmpPath, 0755); err != nil {
		result.Stderr = fmt.Sprintf("chmod failed: %v", err)
		result.Retcode = 1
		os.Remove(tmpPath)
		a.sendResult(result)
		return
	}

	// 3. Keep rollback copy.
	if err := os.Rename(exePath, oldPath); err != nil {
		result.Stderr = fmt.Sprintf("backup failed: %v", err)
		result.Retcode = 1
		os.Remove(tmpPath)
		a.sendResult(result)
		return
	}

	// 4. Atomic swap.
	if err := os.Rename(tmpPath, exePath); err != nil {
		// Try to restore old binary.
		os.Rename(oldPath, exePath)
		result.Stderr = fmt.Sprintf("swap failed: %v", err)
		result.Retcode = 1
		a.sendResult(result)
		return
	}

	log.Printf("upgrade: binary swapped, restarting")

	// 5. Restart based on init type.
	switch a.host.Init {
	case "systemd":
		// systemd's Restart=always brings up the new binary.
		os.Exit(0)
	default:
		// init.d / openrc — re-exec into the new binary directly.
		argv0 := exePath
		err := syscall.Exec(argv0, os.Args, os.Environ())
		if err != nil {
			// re-exec failed — rollback.
			log.Printf("upgrade: re-exec failed: %v, rolling back", err)
			os.Rename(oldPath, exePath)
			result.Stderr = fmt.Sprintf("re-exec failed: %v", err)
			result.Retcode = 1
			a.sendResult(result)
		}
	}
}

// RollbackIfNeeded checks on startup if a .old binary exists and the server
// is unreachable. If so, restores the old binary and exits.
func RollbackIfNeeded(cfg *Config, host HostInfo) {
	oldPath := host.ExePath + ".old"
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return // no rollback binary, nothing to do
	}

	log.Printf("rollback check: .old binary exists, verifying server connectivity")

	// Try to reach the server within the timeout.
	deadline := time.Now().Add(rollbackTimeout)
	for time.Now().Before(deadline) {
		// Simple HTTP GET to healthz to check connectivity.
		url := httpURL(cfg.Server) + "/healthz"
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				// Server reachable — new binary is good, remove .old.
				log.Printf("rollback check: server reachable, keeping new binary")
				os.Remove(oldPath)
				return
			}
		}
		time.Sleep(5 * time.Second)
	}

	// Server unreachable — rollback.
	log.Printf("rollback: server unreachable after %s, restoring old binary", rollbackTimeout)
	exePath := host.ExePath
	if err := os.Rename(oldPath, exePath); err != nil {
		log.Fatalf("rollback: failed to restore old binary: %v", err)
	}
	os.Exit(1)
}

// sendResult is a helper to send a result envelope.
func (a *Agent) sendResult(result wire.Result) {
	env := wire.Envelope{
		Type:   wire.TypeResult,
		Result: &result,
	}
	if err := a.sendEnvelope(env); err != nil {
		log.Printf("failed to send result for %s: %v", result.CommandID, err)
	}
}

// downloadFile downloads a URL to a local file path.
func downloadFile(path, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// httpURL converts a server address to an http(s) URL for health checks.
func httpURL(addr string) string {
	switch {
	case len(addr) >= 6 && addr[:6] == "wss://":
		return "https://" + addr[6:]
	case len(addr) >= 5 && addr[:5] == "ws://":
		return "http://" + addr[5:]
	case len(addr) >= 8 && addr[:8] == "https://":
		return addr
	case len(addr) >= 7 && addr[:7] == "http://":
		return addr
	default:
		return "http://" + addr
	}
}
