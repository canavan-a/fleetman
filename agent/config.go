package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "/etc/fleet-agent/config.yaml"
	configFilePerm    = 0600
)

// Config holds the agent configuration loaded from file + env overrides.
type Config struct {
	Server   string            `yaml:"server"`
	Token    string            `yaml:"token"`
	DeviceID string            `yaml:"device_id"`
	Labels   map[string]string `yaml:"labels"`
}

// LoadConfig reads the config file, applies env overrides, and validates.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath
	}

	// Check file permissions — warn if too open (but don't block; might be testing).
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("config file not found: %s", path)
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		fmt.Fprintf(os.Stderr, "WARNING: config file %s has permissions %o, should be %o (contains secrets)\n", path, perm, configFilePerm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Env overrides — higher priority than file.
	if v := os.Getenv("FLEET_SERVER"); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv("FLEET_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("FLEET_DEVICE_ID"); v != "" {
		cfg.DeviceID = v
	}

	// Validate required fields.
	if cfg.Server == "" {
		return nil, fmt.Errorf("config: 'server' is required (set in config file or FLEET_SERVER env)")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("config: 'token' is required (set in config file or FLEET_TOKEN env)")
	}
	if cfg.DeviceID == "" {
		return nil, fmt.Errorf("config: 'device_id' is required (set in config file or FLEET_DEVICE_ID env)")
	}

	return &cfg, nil
}

// SaveConfig writes the config to file with correct permissions.
func SaveConfig(path string, cfg *Config) error {
	if path == "" {
		path = defaultConfigPath
	}

	// Ensure parent directory exists.
	dir := path[:strings.LastIndex(path, "/")]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, configFilePerm); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Enforce permissions explicitly (WriteFile may not override existing perms).
	if err := os.Chmod(path, configFilePerm); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}

	return nil
}

// SetLabel sets a label key=value in the config and saves.
func SetLabel(path, kv string) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}

	parts := strings.SplitN(kv, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid label format, expected key=value, got %q", kv)
	}

	if cfg.Labels == nil {
		cfg.Labels = make(map[string]string)
	}
	cfg.Labels[parts[0]] = parts[1]

	return SaveConfig(path, cfg)
}
