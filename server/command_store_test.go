package main

import (
	"os"
	"testing"
	"time"
)

func TestCommandStoreCreate(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	now := time.Now().UTC()
	cmdRec := &CommandRecord{
		CommandID: "cmd-123",
		Action:    "run",
		Payload:   map[string]interface{}{"cmd": "ls -la"},
		TargetIDs: []string{"dev-aaa", "dev-bbb"},
		CreatedAt: now,
		TimeoutAt: now.Add(defaultCommandTimeout),
		Results: map[string]*DeviceResult{
			"dev-aaa": {DeviceID: "dev-aaa", Status: "pending"},
			"dev-bbb": {DeviceID: "dev-bbb", Status: "pending"},
		},
	}

	if err := store.Create(cmdRec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify command was stored
	retrieved := store.Get("cmd-123")
	if retrieved == nil {
		t.Fatal("Command not found after creation")
	}

	if retrieved.CommandID != "cmd-123" {
		t.Fatalf("Wrong command_id: %q", retrieved.CommandID)
	}
	if retrieved.Action != "run" {
		t.Fatalf("Wrong action: %q", retrieved.Action)
	}
	if len(retrieved.TargetIDs) != 2 {
		t.Fatalf("Wrong target_ids count: %d", len(retrieved.TargetIDs))
	}
	if len(retrieved.Results) != 2 {
		t.Fatalf("Wrong results count: %d", len(retrieved.Results))
	}
}

func TestCommandStoreGetNonExistent(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	cmd := store.Get("non-existent-command")
	if cmd != nil {
		t.Fatal("Expected nil for non-existent command")
	}
}

func TestCommandStoreRecordResult(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	now := time.Now().UTC()
	cmdRec := &CommandRecord{
		CommandID: "cmd-123",
		Action:    "run",
		TargetIDs: []string{"dev-aaa", "dev-bbb"},
		CreatedAt: now,
		TimeoutAt: now.Add(defaultCommandTimeout),
		Results: map[string]*DeviceResult{
			"dev-aaa": {DeviceID: "dev-aaa", Status: "pending"},
			"dev-bbb": {DeviceID: "dev-bbb", Status: "pending"},
		},
	}

	if err := store.Create(cmdRec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Record result for first device
	success := store.RecordResult("cmd-123", "dev-aaa", "output stdout", "output stderr", 0)
	if !success {
		t.Fatal("RecordResult returned false for existing command")
	}

	// Verify result was recorded
	cmd := store.Get("cmd-123")
	if cmd == nil {
		t.Fatal("Command not found after recording result")
	}

	result, ok := cmd.Results["dev-aaa"]
	if !ok {
		t.Fatal("Result for dev-aaa not found")
	}
	if result.Status != "ok" {
		t.Fatalf("Expected status 'ok', got %q", result.Status)
	}
	if result.Stdout != "output stdout" {
		t.Fatalf("Expected stdout 'output stdout', got %q", result.Stdout)
	}
	if result.Stderr != "output stderr" {
		t.Fatalf("Expected stderr 'output stderr', got %q", result.Stderr)
	}
	if result.Retcode != 0 {
		t.Fatalf("Expected retcode 0, got %d", result.Retcode)
	}

	// Record error result for second device
	success = store.RecordResult("cmd-123", "dev-bbb", "", "error message", 1)
	if !success {
		t.Fatal("RecordResult returned false for existing command")
	}

	cmd = store.Get("cmd-123")
	result = cmd.Results["dev-bbb"]
	if result.Status != "error" {
		t.Fatalf("Expected status 'error', got %q", result.Status)
	}
	if result.Retcode != 1 {
		t.Fatalf("Expected retcode 1, got %d", result.Retcode)
	}
}

func TestCommandStoreRecordResultNonExistentCommand(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	success := store.RecordResult("non-existent", "dev-aaa", "stdout", "stderr", 0)
	if success {
		t.Fatal("RecordResult should return false for non-existent command")
	}
}

func TestCommandStoreGetCommandTimeout(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	// Create command with timeout in the past
	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	cmdRec := &CommandRecord{
		CommandID: "cmd-123",
		Action:    "run",
		TargetIDs: []string{"dev-aaa", "dev-bbb"},
		CreatedAt: past,
		TimeoutAt: past.Add(1 * time.Minute),
		Results: map[string]*DeviceResult{
			"dev-aaa": {DeviceID: "dev-aaa", Status: "pending"},
			"dev-bbb": {DeviceID: "dev-bbb", Status: "ok"},
		},
	}

	if err := store.Create(cmdRec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Record ok result for dev-bbb (InsertCommand stores all as pending)
	store.RecordResult("cmd-123", "dev-bbb", "stdout", "", 0)

	// Retrieve command - should mark timed out devices
	cmd := store.Get("cmd-123")
	if cmd == nil {
		t.Fatal("Command not found")
	}

	// dev-aaa should be marked as timeout
	result := cmd.Results["dev-aaa"]
	if result.Status != "timeout" {
		t.Fatalf("Expected status 'timeout' for pending device, got %q", result.Status)
	}

	// dev-bbb should remain ok
	result = cmd.Results["dev-bbb"]
	if result.Status != "ok" {
		t.Fatalf("Expected status 'ok' for completed device, got %q", result.Status)
	}
}

func TestCommandStoreCommandWithEmptyPayload(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	now := time.Now().UTC()
	cmdRec := &CommandRecord{
		CommandID: "cmd-123",
		Action:    "run",
		Payload:   nil,
		TargetIDs: []string{"dev-aaa"},
		CreatedAt: now,
		TimeoutAt: now.Add(defaultCommandTimeout),
		Results: map[string]*DeviceResult{
			"dev-aaa": {DeviceID: "dev-aaa", Status: "pending"},
		},
	}

	if err := store.Create(cmdRec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	cmd := store.Get("cmd-123")
	if cmd == nil {
		t.Fatal("Command not found")
	}
	if cmd.Payload == nil {
		t.Fatal("Payload should not be nil")
	}
}

func TestCommandStoreCommandWithEmptyTargetIDs(t *testing.T) {
	f, err := os.CreateTemp("", "fleetman-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewCommandStore(db)

	now := time.Now().UTC()
	cmdRec := &CommandRecord{
		CommandID:   "cmd-123",
		Action:      "run",
		TargetIDs:   []string{},
		CreatedAt:   now,
		TimeoutAt:   now.Add(defaultCommandTimeout),
		Results:     map[string]*DeviceResult{},
	}

	if err := store.Create(cmdRec); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	cmd := store.Get("cmd-123")
	if cmd == nil {
		t.Fatal("Command not found")
	}
	if len(cmd.TargetIDs) != 0 {
		t.Fatalf("Expected 0 target IDs, got %d", len(cmd.TargetIDs))
	}
}
