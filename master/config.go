package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	configDirName  = ".fleetman"
	configFileName = "config.yaml"
	configFilePerm = 0600
	configDirPerm  = 0700
)

// Config holds the master CLI configuration.
type Config struct {
	Server    string `yaml:"server"`
	MasterKey string `yaml:"master_key"`
}

// IsComplete reports whether the config has all required fields.
func (c *Config) IsComplete() bool {
	return c != nil && strings.TrimSpace(c.Server) != "" && strings.TrimSpace(c.MasterKey) != ""
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
	return &cfg, nil
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
	MasterKey string // --master-key
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
		server = promptLine("Server URL", existing.Server)
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

	cfg := &Config{
		Server:    strings.TrimRight(strings.TrimSpace(server), "/"),
		MasterKey: strings.TrimSpace(masterKey),
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
		return &Config{
			Server:    strings.TrimRight(strings.TrimSpace(flags.Server), "/"),
			MasterKey: strings.TrimSpace(flags.MasterKey),
		}, nil
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	if cfg.IsComplete() {
		// Apply flag overrides on top of file values.
		if flags.Server != "" {
			cfg.Server = strings.TrimRight(strings.TrimSpace(flags.Server), "/")
		}
		if flags.MasterKey != "" {
			cfg.MasterKey = strings.TrimSpace(flags.MasterKey)
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
