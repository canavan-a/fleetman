package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/canavan-a/fleetman/internal/headers"
	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "/etc/fleet-agent/config.yaml"
	configFilePerm    = 0600
)

// Config holds the agent configuration loaded from file + env overrides.
type Config struct {
	Server       string            `yaml:"server"`
	Token        string            `yaml:"token"`
	DeviceID     string            `yaml:"device_id"`
	Labels       map[string]string `yaml:"labels"`
	ExtraHeaders map[string]string `yaml:"extra_headers,omitempty"`
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
	if v := os.Getenv("FLEET_EXTRA_HEADERS"); v != "" {
		parsed, err := parseHeadersEnv(v)
		if err != nil {
			return nil, fmt.Errorf("parse FLEET_EXTRA_HEADERS: %w", err)
		}
		cfg.ExtraHeaders = parsed
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

// AddHeader sets an extra static header ("Name: Value") in the config and saves.
func AddHeader(path, kv string) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
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

	cfg.ExtraHeaders = nil

	return SaveConfig(path, cfg)
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
