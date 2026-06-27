package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Version and Repo are injected at build time via ldflags.
var (
	Version = "dev"
	Repo    = "canavan-a/fleetman"
)

var archSuffixUpgrade = map[string]string{
	"amd64": "linux-amd64",
	"arm64": "linux-arm64",
}

func runUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	versionFlag := fs.String("version", "", "version to upgrade to (default: latest)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: fleetman-server upgrade [--version v1.2.3]\n\n")
		fmt.Fprintf(os.Stderr, "Upgrades the fleetman-server binary in-place and restarts the systemd service.\n")
		fmt.Fprintf(os.Stderr, "Must be run as root on the server.\n\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	// Nix detection — binary living in the store means we don't own the install path.
	exe, err := os.Executable()
	if err == nil && strings.HasPrefix(exe, "/nix/store/") {
		fmt.Fprintln(os.Stderr, "ERROR: upgrade is disabled when running under Nix.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Update your flake input and rebuild your NixOS configuration:")
		fmt.Fprintln(os.Stderr, "  nix flake update fleetman")
		fmt.Fprintln(os.Stderr, "  nixos-rebuild switch")
		os.Exit(1)
	}

	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "ERROR: upgrade must be run as root (needed to replace the binary and restart the service).")
		fmt.Fprintln(os.Stderr, "  sudo fleetman-server upgrade")
		os.Exit(1)
	}

	suffix, ok := archSuffixUpgrade[runtime.GOARCH]
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: unsupported architecture %q for self-upgrade.\n", runtime.GOARCH)
		os.Exit(1)
	}

	version := *versionFlag
	if version == "" {
		fmt.Println("Fetching latest release version...")
		version, err = fetchLatestVersion()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: could not fetch latest version: %v\n", err)
			os.Exit(1)
		}
	}

	if version == Version {
		fmt.Printf("Already running %s — nothing to do.\n", Version)
		os.Exit(0)
	}

	binURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/fleetman-server-%s", Repo, version, suffix)
	fmt.Printf("Upgrading fleetman-server %s → %s (%s)\n", Version, version, suffix)
	fmt.Printf("Downloading %s ...\n", binURL)

	// Download to a temp file next to the current binary so rename is atomic.
	tmpFile, err := os.CreateTemp("/usr/local/bin", ".fleetman-server-upgrade-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: could not create temp file: %v\n", err)
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadTo(binURL, tmpFile); err != nil {
		tmpFile.Close()
		fmt.Fprintf(os.Stderr, "ERROR: download failed: %v\n", err)
		os.Exit(1)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: chmod failed: %v\n", err)
		os.Exit(1)
	}

	// Quick sanity check — new binary should report a version.
	out, err := exec.Command(tmpPath, "--version").Output()
	if err != nil || !strings.Contains(string(out), version) {
		fmt.Fprintf(os.Stderr, "ERROR: downloaded binary failed version check (got %q).\n", strings.TrimSpace(string(out)))
		fmt.Fprintf(os.Stderr, "Aborting — original binary is unchanged.\n")
		os.Exit(1)
	}

	fmt.Println("Stopping fleetman-server service...")
	if err := systemctl("stop", "fleetman-server"); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: systemctl stop failed: %v — attempting upgrade anyway.\n", err)
	}

	// Atomic rename — replaces the running binary on disk.
	dest := exe
	if dest == "" {
		dest = "/usr/local/bin/fleetman-server"
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: could not replace binary: %v\n", err)
		fmt.Fprintf(os.Stderr, "Attempting to restart original service...\n")
		systemctl("start", "fleetman-server")
		os.Exit(1)
	}

	fmt.Println("Starting fleetman-server service...")
	if err := systemctl("start", "fleetman-server"); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: service failed to start: %v\n", err)
		fmt.Fprintf(os.Stderr, "Check logs with: journalctl -u fleetman-server -n 50\n")
		os.Exit(1)
	}

	fmt.Printf("\nfleetman-server upgraded to %s successfully.\n", version)
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("no releases found")
	}
	return payload.TagName, nil
}

func downloadTo(url string, dst *os.File) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

func systemctl(action, unit string) error {
	cmd := exec.Command("systemctl", action, unit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
