// fleetman is the control CLI for the fleet manager system.
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
		cmdMain(cfg, path)
		return
	}

	switch flag.Arg(0) {
	case "login":
		cmdLogin(path, flags)
	case "logout":
		cmdLogout(path)
	case "header":
		cmdHeader(path, flag.Args()[1:])
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", flag.Arg(0))
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `fleetman — fleet manager control CLI

Usage:
  fleetman [flags] <command>

Global flags:
  --config <path>      Config file path (default: ~/.fleetman/config.yaml)
  --server <host>      Server host, e.g. fleetman.example.com (overrides config, skips prompt)
  --insecure            Use plaintext http/ws instead of https/wss (overrides config)
  --master-key <key>   Master API key (overrides config, skips prompt)
  --repo <owner/repo>  GitHub repo for install-script URLs (default: `+defaultRepo+`)

Commands:
  login         Save server URL and master key to config
  logout        Remove saved credentials
  header add    Add a static extra header (--header "Name: Value")
  header list   List configured extra headers
  header clear  Remove all configured extra headers

`)
}

// cmdMain is the main TUI entry point, reached when the binary is run with no args.
// Config is already validated before this is called.
func cmdMain(cfg *Config, configPath string) {
	statePath := defaultStatePath(configPath)
	state, err := LoadTUIState(statePath)
	if err != nil {
		state = &TUIState{}
	}

	p := tea.NewProgram(newMainModel(cfg, state), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		log.Fatalf("FATAL: tui error: %v", err)
	}
	if m, ok := finalModel.(mainModel); ok {
		_ = SaveTUIState(statePath, &TUIState{
			SelectedDeviceIDs: m.selectedIDs(),
			ActiveTag:         m.activeTag,
			Compact:           m.compact,
		})
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

// cmdHeader adds or lists static extra headers.
func cmdHeader(path string, args []string) {
	if len(args) < 1 {
		log.Fatal("FATAL: expected a subcommand: add, list, clear")
	}

	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("header add", flag.ExitOnError)
		header := fs.String("header", "", "header to add, e.g. \"Name: Value\"")
		fs.Parse(args[1:])

		if *header == "" {
			log.Fatal("FATAL: --header is required, e.g. --header \"CF-Access-Client-Id: abc123\"")
		}
		if err := AddHeader(path, *header); err != nil {
			log.Fatalf("FATAL: %v", err)
		}
		fmt.Println("header added")
	case "list":
		cfg, err := LoadConfig(path)
		if err != nil {
			log.Fatalf("FATAL: %v", err)
		}
		if cfg == nil || len(cfg.ExtraHeaders) == 0 {
			fmt.Println("no extra headers configured")
			return
		}
		for k, v := range cfg.ExtraHeaders {
			fmt.Printf("%s: %s\n", k, v)
		}
	case "clear":
		if err := ClearHeaders(path); err != nil {
			log.Fatalf("FATAL: %v", err)
		}
		fmt.Println("headers cleared")
	default:
		log.Fatalf("FATAL: unknown header subcommand: %s", args[0])
	}
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
