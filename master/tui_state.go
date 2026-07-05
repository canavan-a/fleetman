package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const stateFileName = "tui_state.yaml"

// TUIState holds master TUI state that persists across runs: the device
// selection sidebar, active tag filter, and compact/banner display mode.
type TUIState struct {
	SelectedDeviceIDs []string `yaml:"selected_device_ids,omitempty"`
	ActiveTag         string   `yaml:"active_tag,omitempty"`
	Compact           bool     `yaml:"compact,omitempty"`
}

// defaultStatePath returns the state file path as a sibling of the config
// file, e.g. ~/.fleetman/tui_state.yaml given ~/.fleetman/config.yaml.
func defaultStatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), stateFileName)
}

// LoadTUIState reads and parses the state file at path.
// Returns an empty (non-nil) state if the file does not exist.
func LoadTUIState(path string) (*TUIState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &TUIState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tui state: %w", err)
	}

	var s TUIState
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse tui state: %w", err)
	}
	return &s, nil
}

// SaveTUIState writes s to path with the same permissions as the config file.
func SaveTUIState(path string, s *TUIState) error {
	if err := os.MkdirAll(filepath.Dir(path), configDirPerm); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal tui state: %w", err)
	}
	if err := os.WriteFile(path, data, configFilePerm); err != nil {
		return fmt.Errorf("write tui state: %w", err)
	}
	return os.Chmod(path, configFilePerm)
}
