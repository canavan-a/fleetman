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
	Init    string // "systemd", "openrc", "procd", "runit", "s6", "busybox", "initd"
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
// Detection order matters — more specific checks first.
func detectInit() string {
	// systemd: most common on standard distros.
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return "systemd"
	}

	// procd: OpenWrt's init system.
	if _, err := exec.LookPath("procd"); err == nil {
		return "procd"
	}
	// Also check the ubus way (procd registers itself).
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		if _, err := exec.LookPath("ubus"); err == nil {
			return "procd"
		}
	}

	// OpenRC: Alpine, Gentoo.
	if _, err := exec.LookPath("rc-service"); err == nil {
		return "openrc"
	}

	// runit: Void Linux, some Alpine/container setups.
	if _, err := os.Stat("/run/runit.stopit"); err == nil {
		return "runit"
	}
	if _, err := exec.LookPath("sv"); err == nil {
		// sv exists — check if runit is actually PID 1.
		if data, err := os.ReadFile("/proc/1/comm"); err == nil {
			if strings.TrimSpace(string(data)) == "runit" {
				return "runit"
			}
		}
	}

	// s6: s6-linux-init based systems.
	if _, err := exec.LookPath("s6-svc"); err == nil {
		return "s6"
	}

	// BusyBox init: many embedded/MIPS devices.
	// BusyBox init uses /etc/inittab and PID 1 is typically "init" from BusyBox.
	if data, err := os.ReadFile("/proc/1/comm"); err == nil {
		comm := strings.TrimSpace(string(data))
		if comm == "init" {
			// Could be sysvinit or BusyBox — check if init is BusyBox.
			if isBusyBoxInit() {
				return "busybox"
			}
		}
	}

	// SysV init.d: fallback for traditional Linux.
	return "initd"
}

// isBusyBoxInit checks if /sbin/init is a BusyBox symlink/applet.
func isBusyBoxInit() bool {
	// Check if /sbin/init is a symlink to busybox.
	target, err := os.Readlink("/sbin/init")
	if err == nil && strings.Contains(target, "busybox") {
		return true
	}
	// Some systems bake BusyBox init directly — check version output.
	out, err := exec.Command("/sbin/init", "--version").CombinedOutput()
	if err == nil && strings.Contains(strings.ToLower(string(out)), "busybox") {
		return true
	}
	// Check if busybox is available and provides init.
	if _, err := exec.LookPath("busybox"); err == nil {
		out, err := exec.Command("busybox", "--list").CombinedOutput()
		if err == nil && strings.Contains(string(out), "init") {
			return true
		}
	}
	return false
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
