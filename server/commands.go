package main

import (
	"time"
)

// CommandRecord holds a pending or completed command and its collected results.
type CommandRecord struct {
	CommandID string                 `json:"command_id"`
	Action    string                 `json:"action"`
	Target    string                 `json:"target,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
	TargetIDs []string               `json:"target_ids"`
	CreatedAt time.Time              `json:"created_at"`
	TimeoutAt time.Time              `json:"timeout_at"`
	Results   map[string]*DeviceResult `json:"results"` // device_id → result
}

// DeviceResult is the per-device outcome within a command.
type DeviceResult struct {
	DeviceID string `json:"device_id"`
	Status   string `json:"status"` // "pending", "ok", "error", "timeout"
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Retcode  int    `json:"retcode,omitempty"`
}

// CommandStore persists commands and results in SQLite.
type CommandStore struct {
	db *DB
}

// NewCommandStore creates a command store backed by the given database.
func NewCommandStore(db *DB) *CommandStore {
	return &CommandStore{db: db}
}

// Create stores a new command record with pending results for each target device.
func (s *CommandStore) Create(rec *CommandRecord) error {
	return s.db.InsertCommand(rec)
}

// RecordResult stores a result from a device for a given command.
func (s *CommandStore) RecordResult(commandID, deviceID, stdout, stderr string, retcode int) bool {
	return s.db.RecordResult(commandID, deviceID, stdout, stderr, retcode)
}

// Get returns a command record with all results. Marks timed-out devices. Returns nil if not found.
func (s *CommandStore) Get(commandID string) *CommandRecord {
	return s.db.GetCommand(commandID)
}
