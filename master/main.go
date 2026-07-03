// fleet-master is the control CLI for the fleet manager system.
// It talks to the server's HTTP API to provision devices, send commands,
// and display results.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	configPath := flag.String("config", "", "path to config file (default: ~/.fleetman/config.yaml)")
	server := flag.String("server", "", "server host, e.g. fleetman.example.com (overrides config)")
	insecure := flag.Bool("insecure", false, "use plaintext http/ws instead of https/wss (overrides config)")
	masterKey := flag.String("master-key", "", "master API key (overrides config)")
	repo := flag.String("repo", "", "GitHub \"owner/repo\" for install-script URLs (overrides config, default: "+defaultRepo+")")
	flag.Parse()

	if *repo == "" {
		*repo = os.Getenv("FLEETMAN_REPO")
	}

	flags := LoginFlags{
		Server:    *server,
		Insecure:  *insecure,
		MasterKey: *masterKey,
		Repo:      *repo,
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
  --server <host>      Server host, e.g. fleetman.example.com (overrides config, skips prompt)
  --insecure            Use plaintext http/ws instead of https/wss (overrides config)
  --master-key <key>   Master API key (overrides config, skips prompt)
  --repo <owner/repo>  GitHub repo for install-script URLs (default: `+defaultRepo+`)

Commands:
  login     Save server URL and master key to config
  logout    Remove saved credentials

`)
}

// cmdMain is the main TUI entry point, reached when the binary is run with no args.
// Config is already validated before this is called.
func cmdMain(cfg *Config) {
	p := tea.NewProgram(newMainModel(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("FATAL: tui error: %v", err)
	}
}

// cmdLogin saves credentials, using the TUI form when running interactively.
func cmdLogin(path string, flags LoginFlags) {
	existing, _ := LoadConfig(path)
	if existing == nil {
		existing = &Config{}
	}

	// Merge flags onto existing config so the TUI/prompts show current values.
	if flags.Server != "" {
		existing.setServer(flags.Server)
	}
	if flags.Insecure {
		existing.Insecure = true
	}
	if flags.MasterKey != "" {
		existing.MasterKey = flags.MasterKey
	}
	if flags.Repo != "" {
		existing.Repo = flags.Repo
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
		cfg := &Config{MasterKey: flags.MasterKey, Repo: flags.Repo, Insecure: flags.Insecure}
		cfg.setServer(flags.Server)
		return cfg
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	if cfg.IsComplete() {
		if flags.Server != "" {
			cfg.setServer(flags.Server)
		}
		if flags.Insecure {
			cfg.Insecure = true
		}
		if flags.MasterKey != "" {
			cfg.MasterKey = flags.MasterKey
		}
		if flags.Repo != "" {
			cfg.Repo = flags.Repo
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
