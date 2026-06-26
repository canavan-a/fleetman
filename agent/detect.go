package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HostInfo holds runtime-detected host information (never from config).
type HostInfo struct {
	ExePath string // binary location, for self-swap
	Arch    string // runtime.GOARCH: "amd64", "arm64", "arm"
	OS      string // runtime.GOOS: "linux"
	Init    string // "systemd", "openrc", "initd"
	Uptime  int64  // seconds
}

// DetectHost gathers runtime host information.
func DetectHost() HostInfo {
	exePath, _ := os.Executable()

	return HostInfo{
		ExePath: exePath,
		Arch:    runtime.GOARCH,
		OS:      runtime.GOOS,
		Init:    detectInit(),
		Uptime:  detectUptime(),
	}
}

// detectInit determines the init system on the host.
func detectInit() string {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return "systemd"
	}
	if _, err := exec.LookPath("rc-service"); err == nil {
		return "openrc"
	}
	return "initd"
}

// detectUptime reads system uptime from /proc/uptime (Linux).
func detectUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	// Format: "12345.67 12345.67" — we want the first field (seconds).
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	// Parse the float, truncate to int.
	parts := strings.Split(fields[0], ".")
	secs, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0
	}
	return secs
}
