package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canavan-a/fleetman/internal/headers"
	"gopkg.in/yaml.v3"
)

const (
	configDirName  = ".fleetman"
	configFileName = "config.yaml"
	configFilePerm = 0600
	configDirPerm  = 0700
)

// defaultRepo is the GitHub "owner/repo" used to build install-script URLs
// when none is configured. Most users run this fork, so it's the sane default.
const defaultRepo = "canavan-a/fleetman"

// Config holds the master CLI configuration.
//
// Server is stored as a bare host ("fleetman.example.com" or "host:port"),
// never a scheme-prefixed URL — the scheme is implied secure (https/wss)
// unless Insecure is set. A scheme typed by the user (http://, ws://, ...)
// is treated as an explicit opt-in to plaintext and normalized away into
// Insecure=true so the stored form stays scheme-free everywhere.
type Config struct {
	Server       string            `yaml:"server"`
	Insecure     bool              `yaml:"insecure,omitempty"`
	MasterKey    string            `yaml:"master_key"`
	Repo         string            `yaml:"repo,omitempty"` // GitHub "owner/repo" for install-script URLs
	ExtraHeaders map[string]string `yaml:"extra_headers,omitempty"`
}

// IsComplete reports whether the config has all required fields.
func (c *Config) IsComplete() bool {
	return c != nil && strings.TrimSpace(c.Server) != "" && strings.TrimSpace(c.MasterKey) != ""
}

// EffectiveRepo returns the configured repo, falling back to defaultRepo.
func (c *Config) EffectiveRepo() string {
	if c != nil && strings.TrimSpace(c.Repo) != "" {
		return strings.TrimSpace(c.Repo)
	}
	return defaultRepo
}

// BaseURL returns the full scheme-prefixed URL the master's HTTP API client
// should use: https:// unless Insecure is set.
func (c *Config) BaseURL() string {
	scheme := "https"
	if c.Insecure {
		scheme = "http"
	}
	return scheme + "://" + c.Server
}

// normalizeServer splits a user-entered server value into a bare host and
// an insecure flag. An explicit http:// or ws:// prefix opts into
// plaintext; https://, wss://, or no scheme at all all mean secure.
func normalizeServer(raw string) (host string, insecure bool) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasPrefix(raw, "https://"):
		return strings.TrimPrefix(raw, "https://"), false
	case strings.HasPrefix(raw, "wss://"):
		return strings.TrimPrefix(raw, "wss://"), false
	case strings.HasPrefix(raw, "http://"):
		return strings.TrimPrefix(raw, "http://"), true
	case strings.HasPrefix(raw, "ws://"):
		return strings.TrimPrefix(raw, "ws://"), true
	default:
		return raw, insecure
	}
}

// setServer normalizes and applies a user-entered server value, folding any
// explicit scheme into c.Insecure rather than storing it in c.Server.
func (c *Config) setServer(raw string) {
	host, insecure := normalizeServer(raw)
	c.Server = host
	if insecure {
		c.Insecure = true
	}
}

// defaultConfigPath returns ~/.fleetman/config.yaml.
func defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, configDirName, configFileName), nil
}

// resolveConfigPath returns the config path from flag > env > default.
func resolveConfigPath(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if v := os.Getenv("FLEETMAN_CONFIG"); v != "" {
		return v, nil
	}
	return defaultConfigPath()
}

// LoadConfig reads and parses the config file at path.
// Returns (nil, nil) if the file does not exist — caller should prompt.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Migrate any older scheme-prefixed server value stored on disk.
	cfg.setServer(cfg.Server)

	if v := os.Getenv("FLEET_EXTRA_HEADERS"); v != "" {
		parsed, err := parseHeadersEnv(v)
		if err != nil {
			return nil, fmt.Errorf("parse FLEET_EXTRA_HEADERS: %w", err)
		}
		cfg.ExtraHeaders = parsed
	}

	return &cfg, nil
}

// parseHeadersEnv parses the comma-separated "Name1=Value1,Name2=Value2"
// format used by FLEET_EXTRA_HEADERS.
func parseHeadersEnv(v string) (map[string]string, error) {
	result := make(map[string]string)
	for _, pair := range strings.Split(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header pair %q, expected Name=Value", pair)
		}
		name := strings.TrimSpace(parts[0])
		if strings.EqualFold(name, "Authorization") {
			return nil, fmt.Errorf("cannot set Authorization as an extra header")
		}
		result[name] = strings.TrimSpace(parts[1])
	}
	return result, nil
}

// AddHeader sets an extra static header ("Name: Value") in the config and saves.
func AddHeader(path, kv string) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("no config found at %s; run 'fleetman login' first", path)
	}

	name, value, err := headers.Parse(kv)
	if err != nil {
		return err
	}

	if cfg.ExtraHeaders == nil {
		cfg.ExtraHeaders = make(map[string]string)
	}
	cfg.ExtraHeaders[name] = value

	return SaveConfig(path, cfg)
}

// ClearHeaders removes all extra headers from the config and saves.
func ClearHeaders(path string) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("no config found at %s; run 'fleetman login' first", path)
	}

	cfg.ExtraHeaders = nil

	return SaveConfig(path, cfg)
}

// SaveConfig writes cfg to path with 0600 permissions.
func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), configDirPerm); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, configFilePerm); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Chmod(path, configFilePerm)
}

// DeleteConfig removes the config file (logout). No-op if already gone.
func DeleteConfig(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// LoginFlags holds flag values that can pre-fill or bypass the login prompt.
type LoginFlags struct {
	Server    string // --server
	Insecure  bool   // --insecure
	MasterKey string // --master-key
	Repo      string // --repo
}

// PromptLogin interactively collects credentials, skipping any field already
// supplied via flags. Saves the result and returns the populated Config.
func PromptLogin(path string, flags LoginFlags) (*Config, error) {
	existing, _ := LoadConfig(path)
	if existing == nil {
		existing = &Config{}
	}

	server := flags.Server
	masterKey := flags.MasterKey

	// Only prompt for fields not supplied via flags.
	if server == "" {
		fmt.Println("Fleet manager credentials required.")
		fmt.Println()
		server = promptLine("Server (host, e.g. fleetman.example.com)", existing.Server)
	}
	if masterKey == "" {
		fmt.Printf("  Master API key: ")
		var err error
		masterKey, err = readSecret()
		if err != nil {
			return nil, fmt.Errorf("read master key: %w", err)
		}
	}

	if server == "" || masterKey == "" {
		return nil, fmt.Errorf("server and master key are required")
	}

	cfg := &Config{MasterKey: strings.TrimSpace(masterKey)}
	cfg.setServer(server)
	if flags.Insecure {
		cfg.Insecure = true
	} else if existing.Insecure {
		cfg.Insecure = true
	}
	cfg.Repo = strings.TrimSpace(flags.Repo)
	if cfg.Repo == "" {
		cfg.Repo = strings.TrimSpace(existing.Repo)
	}

	if err := SaveConfig(path, cfg); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Config saved to %s\n\n", path)
	return cfg, nil
}

// LoadOrPrompt loads the config, running the login prompt if it is
// missing or incomplete. Flags can pre-fill or fully bypass prompting.
func LoadOrPrompt(path string, flags LoginFlags) (*Config, error) {
	// If both values supplied via flags, skip the file entirely.
	if flags.Server != "" && flags.MasterKey != "" {
		cfg := &Config{MasterKey: strings.TrimSpace(flags.MasterKey), Repo: strings.TrimSpace(flags.Repo)}
		cfg.setServer(flags.Server)
		if flags.Insecure {
			cfg.Insecure = true
		}
		return cfg, nil
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	if cfg.IsComplete() {
		// Apply flag overrides on top of file values.
		if flags.Server != "" {
			cfg.setServer(flags.Server)
		}
		if flags.MasterKey != "" {
			cfg.MasterKey = strings.TrimSpace(flags.MasterKey)
		}
		if flags.Insecure {
			cfg.Insecure = true
		}
		return cfg, nil
	}

	// File missing or incomplete — prompt for what's still needed.
	return PromptLogin(path, flags)
}

// promptLine prints a prompt with an optional default and reads a line.
func promptLine(label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("  %s: ", label)
	}
	var val string
	fmt.Scanln(&val)
	val = strings.TrimSpace(val)
	if val == "" {
		return defaultVal
	}
	return val
}
