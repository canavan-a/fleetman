// fleet-master is the control CLI for the fleet manager system.
// It talks to the server's HTTP API to provision devices, send commands,
// and display results.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	configPath := flag.String("config", "", "path to config file (default: ~/.fleetman/config.yaml)")
	server := flag.String("server", "", "server URL (overrides config)")
	masterKey := flag.String("master-key", "", "master API key (overrides config)")
	flag.Parse()

	flags := LoginFlags{
		Server:    *server,
		MasterKey: *masterKey,
	}

	path, err := resolveConfigPath(*configPath)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	// No subcommand — check auth and launch the main TUI.
	if flag.NArg() == 0 {
		cfg := requireConfig(path, flags)
		cmdMain(cfg)
		return
	}

	switch flag.Arg(0) {
	case "login":
		cmdLogin(path, flags)
	case "logout":
		cmdLogout(path)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", flag.Arg(0))
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `fleet-master — fleet manager control CLI

Usage:
  fleet-master [flags] <command>

Global flags:
  --config <path>      Config file path (default: ~/.fleetman/config.yaml)
  --server <url>       Server URL (overrides config, skips prompt)
  --master-key <key>   Master API key (overrides config, skips prompt)

Commands:
  login     Save server URL and master key to config
  logout    Remove saved credentials

`)
}

// cmdMain is the main TUI entry point, reached when the binary is run with no args.
// Config is already validated before this is called.
func cmdMain(cfg *Config) {
	// Placeholder — main TUI goes here.
	fmt.Printf("Connected to %s\n", cfg.Server)
}

// cmdLogin saves credentials, using the TUI form when running interactively.
func cmdLogin(path string, flags LoginFlags) {
	existing, _ := LoadConfig(path)
	if existing == nil {
		existing = &Config{}
	}

	// Merge flags onto existing config so the TUI/prompts show current values.
	if flags.Server != "" {
		existing.Server = flags.Server
	}
	if flags.MasterKey != "" {
		existing.MasterKey = flags.MasterKey
	}

	var cfg Config

	if IsTTY() {
		result, err := RunLoginTUI(*existing)
		if err != nil {
			// User cancelled — exit cleanly.
			os.Exit(0)
		}
		cfg = result
	} else {
		result, err := PromptLogin(path, flags)
		if err != nil {
			log.Fatalf("FATAL: %v", err)
		}
		cfg = *result
	}

	if err := SaveConfig(path, &cfg); err != nil {
		log.Fatalf("FATAL: save config: %v", err)
	}
	fmt.Printf("Config saved to %s\n", path)
}

func cmdLogout(path string) {
	if err := DeleteConfig(path); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	fmt.Println("Logged out.")
}

// requireConfig loads the config, auto-launching the login TUI/prompt if
// it is missing or incomplete. Used by commands that need credentials.
func requireConfig(path string, flags LoginFlags) *Config {
	// Both flags supplied — skip file entirely.
	if flags.Server != "" && flags.MasterKey != "" {
		return &Config{Server: flags.Server, MasterKey: flags.MasterKey}
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	if cfg.IsComplete() {
		if flags.Server != "" {
			cfg.Server = flags.Server
		}
		if flags.MasterKey != "" {
			cfg.MasterKey = flags.MasterKey
		}
		return cfg
	}

	// Config missing or incomplete — run login flow then retry.
	cmdLogin(path, flags)

	cfg, err = LoadConfig(path)
	if err != nil || !cfg.IsComplete() {
		log.Fatalf("FATAL: credentials required")
	}
	return cfg
}
