package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// cmdUninstall stops the service, deregisters it, and removes all fleet-agent files.
func cmdUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	fs.Parse(args)

	if !*yes {
		fmt.Print("This will stop the fleet-agent service and remove all files. Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("aborted")
			return
		}
	}

	host := DetectHost()
	fmt.Printf("Uninstalling fleet-agent (init: %s)\n", host.Init)

	// Stop and deregister service.
	deregisterService(host.Init)

	// Remove config.
	if err := os.RemoveAll("/etc/fleet-agent"); err != nil {
		log.Printf("warning: failed to remove /etc/fleet-agent: %v", err)
	} else {
		fmt.Println("  removed /etc/fleet-agent/")
	}

	// Remove pidfile.
	os.Remove("/var/run/fleet-agent.pid")

	// Remove .old rollback binary if present.
	oldPath := host.ExePath + ".old"
	os.Remove(oldPath)

	// Remove the binary itself — do this last since we're running from it.
	// On Linux, unlinking a running binary is fine; the inode stays until the process exits.
	exePath := host.ExePath
	if err := os.Remove(exePath); err != nil {
		log.Printf("warning: failed to remove %s: %v", exePath, err)
	} else {
		fmt.Printf("  removed %s\n", exePath)
	}

	fmt.Println("\nfleet-agent uninstalled.")
}

// deregisterService stops and removes the service registration for the detected init system.
func deregisterService(initType string) {
	switch initType {
	case "systemd":
		run("systemctl", "stop", "fleet-agent")
		run("systemctl", "disable", "fleet-agent")
		os.Remove("/etc/systemd/system/fleet-agent.service")
		run("systemctl", "daemon-reload")
		fmt.Println("  removed systemd service")

	case "openrc":
		run("rc-service", "fleet-agent", "stop")
		run("rc-update", "del", "fleet-agent", "default")
		os.Remove("/etc/init.d/fleet-agent")
		fmt.Println("  removed openrc service")

	case "procd":
		run("/etc/init.d/fleet-agent", "stop")
		run("/etc/init.d/fleet-agent", "disable")
		os.Remove("/etc/init.d/fleet-agent")
		fmt.Println("  removed procd service")

	case "runit":
		run("sv", "stop", "fleet-agent")
		// Remove symlinks from active service dirs.
		os.Remove("/run/service/fleet-agent")
		os.Remove("/var/service/fleet-agent")
		os.Remove("/service/fleet-agent")
		os.RemoveAll("/etc/sv/fleet-agent")
		os.RemoveAll("/var/log/fleet-agent")
		fmt.Println("  removed runit service")

	case "s6":
		run("s6-svc", "-d", "/run/service/fleet-agent")
		os.Remove("/run/service/fleet-agent")
		os.RemoveAll("/etc/s6/services/fleet-agent")
		run("s6-svscanctl", "-a", "/run/service")
		fmt.Println("  removed s6 service")

	case "busybox":
		run("/etc/init.d/fleet-agent", "stop")
		os.Remove("/etc/init.d/fleet-agent")
		// Remove inittab entry.
		removeInittabEntry()
		fmt.Println("  removed busybox service")

	default: // initd
		run("service", "fleet-agent", "stop")
		run("update-rc.d", "fleet-agent", "remove")
		os.Remove("/etc/init.d/fleet-agent")
		fmt.Println("  removed init.d service")
	}
}

// run executes a command, ignoring errors (service may already be stopped, etc.).
func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Run() // errors are intentionally ignored
}

// removeInittabEntry removes the fleet-agent line from /etc/inittab.
func removeInittabEntry() {
	data, err := os.ReadFile("/etc/inittab")
	if err != nil {
		return
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "fleet-agent") {
			lines = append(lines, line)
		}
	}
	os.WriteFile("/etc/inittab", []byte(strings.Join(lines, "\n")), 0644)
	// Signal init to re-read.
	exec.Command("kill", "-HUP", "1").Run()
}
