// fleet-agent is the on-device daemon for the fleet manager system.
// It dials out to the server over WebSocket, sends heartbeats,
// receives and executes commands, and handles OTA self-updates.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:])
	case "configure":
		cmdConfigure(os.Args[2:])
	case "show-config":
		cmdShowConfig(os.Args[2:])
	case "uninstall":
		cmdUninstall(os.Args[2:])
	case "version":
		fmt.Println(Version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `fleet-agent — on-device fleet management daemon

Usage:
  fleet-agent run            Start the agent daemon
  fleet-agent configure      Write or update config
  fleet-agent show-config    Print current config
  fleet-agent uninstall      Stop service, remove binary and config
  fleet-agent version        Print version

`)
}

// cmdRun starts the agent daemon.
func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config file")
	fs.Parse(args)

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	host := DetectHost()
	log.Printf("fleet-agent %s starting (device=%s arch=%s os=%s init=%s)", Version, cfg.DeviceID, host.Arch, host.OS, host.Init)

	// Check for rollback condition (post-upgrade safety).
	RollbackIfNeeded(cfg, host)

	agent := NewAgent(cfg, Version)
	agent.Run()
}

// cmdConfigure writes or updates the config file.
func cmdConfigure(args []string) {
	fs := flag.NewFlagSet("configure", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config file")
	server := fs.String("server", "", "server WebSocket URL (e.g. ws://host:8080)")
	token := fs.String("token", "", "device token")
	deviceID := fs.String("device-id", "", "server-assigned device ID")
	setLabel := fs.String("set", "", "set a label (key=value)")
	fs.Parse(args)

	// Handle --set separately (requires existing config).
	if *setLabel != "" {
		if err := SetLabel(*configPath, *setLabel); err != nil {
			log.Fatalf("FATAL: %v", err)
		}
		fmt.Println("label updated")
		return
	}

	// Build or update config.
	cfg := &Config{}

	// Try to load existing config (may not exist yet).
	existing, err := LoadConfig(*configPath)
	if err == nil {
		cfg = existing
	}

	if *server != "" {
		cfg.Server = *server
	}
	if *token != "" {
		cfg.Token = *token
	}
	if *deviceID != "" {
		cfg.DeviceID = *deviceID
	}

	if cfg.Server == "" || cfg.Token == "" || cfg.DeviceID == "" {
		log.Fatal("FATAL: --server, --token, and --device-id are all required for initial configuration")
	}

	if err := SaveConfig(*configPath, cfg); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	fmt.Printf("config written to %s\n", *configPath)
}

// cmdShowConfig prints the current config.
func cmdShowConfig(args []string) {
	fs := flag.NewFlagSet("show-config", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config file")
	fs.Parse(args)

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	fmt.Printf("server:    %s\n", cfg.Server)
	fmt.Printf("token:     %s...%s\n", cfg.Token[:4], cfg.Token[len(cfg.Token)-4:])
	fmt.Printf("device_id: %s\n", cfg.DeviceID)
	if len(cfg.Labels) > 0 {
		fmt.Println("labels:")
		for k, v := range cfg.Labels {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
}
