package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/canavan-a/fleetman/wire"
)

// commonFilesDir returns (and creates) the directory ":sendf" writes fetched
// files into by default, alongside the agent binary.
func commonFilesDir(a *Agent) (string, error) {
	dir := filepath.Join(filepath.Dir(a.host.ExePath), "files")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// execFetchFile handles ActionFetchFile (":sendf"): downloads a
// server-stored file (by name) and writes it to the common files dir, or an
// explicit path override from the payload.
func (a *Agent) execFetchFile(cmd *wire.Command) wire.Result {
	result := wire.Result{CommandID: cmd.CommandID, DeviceID: a.cfg.DeviceID}

	nameRaw, ok := cmd.Payload["name"]
	if !ok {
		result.Stderr = "missing 'name' in fetch_file payload"
		result.Retcode = 1
		return result
	}
	name, ok := nameRaw.(string)
	if !ok || name == "" {
		result.Stderr = "invalid name in fetch_file payload"
		result.Retcode = 1
		return result
	}

	dest := ""
	if pathRaw, ok := cmd.Payload["path"]; ok {
		if p, ok := pathRaw.(string); ok && p != "" {
			dest = p
		}
	}
	if dest == "" {
		dir, err := commonFilesDir(a)
		if err != nil {
			result.Stderr = fmt.Sprintf("failed to prepare files dir: %v", err)
			result.Retcode = 1
			return result
		}
		dest = filepath.Join(dir, name)
	} else if dir := filepath.Dir(dest); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			result.Stderr = fmt.Sprintf("failed to prepare destination dir: %v", err)
			result.Retcode = 1
			return result
		}
	}

	url := httpURL(a.cfg.Server) + "/files/" + name + "/download"
	if err := downloadAuthedFile(dest, url, a.cfg.Token); err != nil {
		result.Stderr = fmt.Sprintf("download failed: %v", err)
		result.Retcode = 1
		return result
	}

	abs, err := filepath.Abs(dest)
	if err != nil {
		abs = dest
	}
	result.Stdout = abs
	return result
}

// execListFiles handles ActionListFiles (":files"): lists the contents of
// the common files dir on this device.
func (a *Agent) execListFiles(cmd *wire.Command) wire.Result {
	result := wire.Result{CommandID: cmd.CommandID, DeviceID: a.cfg.DeviceID}

	dir, err := commonFilesDir(a)
	if err != nil {
		result.Stderr = fmt.Sprintf("failed to prepare files dir: %v", err)
		result.Retcode = 1
		return result
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		result.Stderr = fmt.Sprintf("failed to list %s: %v", dir, err)
		result.Retcode = 1
		return result
	}

	if len(entries) == 0 {
		result.Stdout = "(empty)"
		return result
	}

	out := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}
		out += fmt.Sprintf("%s\t%d\n", e.Name(), size)
	}
	result.Stdout = out
	return result
}

// downloadAuthedFile downloads url to path using a device bearer token,
// unlike downloadFile (agent/upgrade.go) which is used only for the
// unauthenticated OTA release URLs.
func downloadAuthedFile(path, url, token string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
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
