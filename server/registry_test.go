package main

import (
	"os"
	"testing"
	"time"
)

func TestMintToken(t *testing.T) {
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

	reg := NewRegistry(db)

	deviceID, token, err := reg.MintToken()
	if err != nil {
		t.Fatalf("MintToken failed: %v", err)
	}

	if deviceID == "" {
		t.Fatal("MintToken returned empty device_id")
	}
	if token == "" {
		t.Fatal("MintToken returned empty token")
	}
	if len(deviceID) < 8 || deviceID[:4] != "dev-" {
		t.Fatalf("unexpected device_id format: %q", deviceID)
	}
	if len(token) < 60 {
		t.Fatalf("token too short: %d bytes", len(token))
	}
}

func TestMintTokenUniqueIDs(t *testing.T) {
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

	reg := NewRegistry(db)

	// Mint multiple tokens and ensure IDs are unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		deviceID, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken iteration %d failed: %v", i, err)
		}
		if ids[deviceID] {
			t.Fatalf("duplicate device_id generated: %q", deviceID)
		}
		ids[deviceID] = true
	}
}

func TestAuthenticateToken(t *testing.T) {
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

	reg := NewRegistry(db)

	deviceID, token, err := reg.MintToken()
	if err != nil {
		t.Fatalf("MintToken failed: %v", err)
	}

	// Test valid token
	authDeviceID, ok := reg.AuthenticateToken(token)
	if !ok {
		t.Fatal("AuthenticateToken returned false for valid token")
	}
	if authDeviceID != deviceID {
		t.Fatalf("AuthenticateToken returned wrong device_id: got %q, want %q", authDeviceID, deviceID)
	}

	// Test invalid token
	_, ok = reg.AuthenticateToken("invalid-token")
	if ok {
		t.Fatal("AuthenticateToken returned true for invalid token")
	}

	// Test empty token
	_, ok = reg.AuthenticateToken("")
	if ok {
		t.Fatal("AuthenticateToken returned true for empty token")
	}
}

func TestSetActiveAndInactive(t *testing.T) {
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

	reg := NewRegistry(db)

	deviceID, token, err := reg.MintToken()
	if err != nil {
		t.Fatalf("MintToken failed: %v", err)
	}

	// Authenticate to get a valid token
	_, ok := reg.AuthenticateToken(token)
	if !ok {
		t.Fatal("Failed to authenticate token")
	}

	// Create a mock connection (we'll just test the registry state, not actual WS)
	// For this test, we'll test SetActive/SetInactive by checking device status in DB

	info, ok := reg.GetDevice(deviceID)
	if !ok {
		t.Fatal("Device not found after minting")
	}
	if info.Status != StatusInactive {
		t.Fatalf("Expected initial status %q, got %q", StatusInactive, info.Status)
	}

	// SetActive should mark device as active
	now := time.Now().UTC()
	db.SetDeviceStatus(deviceID, StatusActive, now)

	info, ok = reg.GetDevice(deviceID)
	if !ok {
		t.Fatal("Device not found after setting active")
	}
	if info.Status != StatusActive {
		t.Fatalf("Expected status %q after SetActive, got %q", StatusActive, info.Status)
	}

	// SetInactive should mark device as inactive
	db.SetDeviceStatus(deviceID, StatusInactive, now)

	info, ok = reg.GetDevice(deviceID)
	if !ok {
		t.Fatal("Device not found after setting inactive")
	}
	if info.Status != StatusInactive {
		t.Fatalf("Expected status %q after SetInactive, got %q", StatusInactive, info.Status)
	}
}

func TestUpdateHeartbeat(t *testing.T) {
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

	reg := NewRegistry(db)

	deviceID, _, err := reg.MintToken()
	if err != nil {
		t.Fatalf("MintToken failed: %v", err)
	}

	// Update heartbeat with initial info
	labels := map[string]string{"env": "production", "tier": "web"}
	reg.UpdateHeartbeat(deviceID, "v1.0.0", "amd64", "systemd", "linux", 3600, labels)

	info, ok := reg.GetDevice(deviceID)
	if !ok {
		t.Fatal("Device not found after heartbeat")
	}

	if info.Version != "v1.0.0" {
		t.Fatalf("Expected version %q, got %q", "v1.0.0", info.Version)
	}
	if info.Arch != "amd64" {
		t.Fatalf("Expected arch %q, got %q", "amd64", info.Arch)
	}
	if info.Init != "systemd" {
		t.Fatalf("Expected init %q, got %q", "systemd", info.Init)
	}
	if info.OS != "linux" {
		t.Fatalf("Expected OS %q, got %q", "linux", info.OS)
	}
	if info.Uptime != 3600 {
		t.Fatalf("Expected uptime %d, got %d", 3600, info.Uptime)
	}
	if info.Labels["env"] != "production" {
		t.Fatalf("Expected label env=production, got %v", info.Labels)
	}
	if info.Labels["tier"] != "web" {
		t.Fatalf("Expected label tier=web, got %v", info.Labels)
	}
}

func TestDeleteDevice(t *testing.T) {
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

	reg := NewRegistry(db)

	deviceID, token, err := reg.MintToken()
	if err != nil {
		t.Fatalf("MintToken failed: %v", err)
	}

	// Verify device exists
	_, ok := reg.GetDevice(deviceID)
	if !ok {
		t.Fatal("Device not found before deletion")
	}

	// Verify token still works
	_, ok = reg.AuthenticateToken(token)
	if !ok {
		t.Fatal("Token should still be valid before deletion")
	}

	// Delete device
	if !reg.DeleteDevice(deviceID) {
		t.Fatal("DeleteDevice returned false")
	}

	// Verify device is gone
	_, ok = reg.GetDevice(deviceID)
	if ok {
		t.Fatal("Device should not be found after deletion")
	}

	// Verify token is revoked
	_, ok = reg.AuthenticateToken(token)
	if ok {
		t.Fatal("Token should be revoked after device deletion")
	}
}

func TestSelectDevicesAll(t *testing.T) {
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

	reg := NewRegistry(db)

	// Create multiple devices
	var deviceIDs []string
	for i := 0; i < 3; i++ {
		id, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken failed: %v", err)
		}
		deviceIDs = append(deviceIDs, id)
	}

	// Mark some devices as active
	now := time.Now().UTC()
	db.SetDeviceStatus(deviceIDs[0], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[1], StatusActive, now)

	// Select all active devices
	target := TargetSpec{All: true}
	selected := reg.SelectDevices(target)

	if len(selected) != 2 {
		t.Fatalf("Expected 2 active devices, got %d: %v", len(selected), selected)
	}
}

func TestSelectDevicesByID(t *testing.T) {
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

	reg := NewRegistry(db)

	// Create devices
	var deviceIDs []string
	for i := 0; i < 3; i++ {
		id, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken failed: %v", err)
		}
		deviceIDs = append(deviceIDs, id)
	}

	now := time.Now().UTC()
	db.SetDeviceStatus(deviceIDs[0], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[1], StatusActive, now)

	// Select specific devices
	target := TargetSpec{IDs: []string{deviceIDs[0], deviceIDs[2]}}
	selected := reg.SelectDevices(target)

	if len(selected) != 1 {
		t.Fatalf("Expected 1 matching device, got %d: %v", len(selected), selected)
	}
	if selected[0] != deviceIDs[0] {
		t.Fatalf("Expected %s, got %s", deviceIDs[0], selected[0])
	}
}

func TestSelectDevicesByLabels(t *testing.T) {
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

	reg := NewRegistry(db)

	// Create devices with different labels
	var deviceIDs []string
	for i := 0; i < 3; i++ {
		id, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken failed: %v", err)
		}
		deviceIDs = append(deviceIDs, id)
	}

	now := time.Now().UTC()
	db.SetDeviceStatus(deviceIDs[0], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[1], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[2], StatusActive, now)

	db.UpdateHeartbeat(deviceIDs[0], "v1", "amd64", "systemd", "linux", 0, map[string]string{"env": "prod"}, now)
	db.UpdateHeartbeat(deviceIDs[1], "v1", "amd64", "systemd", "linux", 0, map[string]string{"env": "staging"}, now)
	db.UpdateHeartbeat(deviceIDs[2], "v1", "amd64", "systemd", "linux", 0, map[string]string{"env": "prod", "tier": "api"}, now)

	// Select by label
	target := TargetSpec{Labels: map[string]string{"env": "prod"}}
	selected := reg.SelectDevices(target)

	if len(selected) != 2 {
		t.Fatalf("Expected 2 devices with env=prod, got %d: %v", len(selected), selected)
	}
}

func TestSelectDevicesByTags(t *testing.T) {
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

	reg := NewRegistry(db)

	// Create devices
	var deviceIDs []string
	for i := 0; i < 3; i++ {
		id, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken failed: %v", err)
		}
		deviceIDs = append(deviceIDs, id)
	}

	// Create tags
	db.CreateTag("production")
	db.CreateTag("web")

	now := time.Now().UTC()
	db.SetDeviceStatus(deviceIDs[0], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[1], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[2], StatusActive, now)

	// Assign tags
	db.AddTagToDevices("production", []string{deviceIDs[0], deviceIDs[1]})
	db.AddTagToDevices("web", []string{deviceIDs[0]})

	// Select devices with ALL specified tags
	target := TargetSpec{Tags: []string{"production", "web"}}
	selected := reg.SelectDevices(target)

	if len(selected) != 1 {
		t.Fatalf("Expected 1 device with both tags, got %d: %v", len(selected), selected)
	}
	if selected[0] != deviceIDs[0] {
		t.Fatalf("Expected %s, got %s", deviceIDs[0], selected[0])
	}
}

func TestSelectDevicesByInitType(t *testing.T) {
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

	reg := NewRegistry(db)

	// Create devices
	var deviceIDs []string
	for i := 0; i < 3; i++ {
		id, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken failed: %v", err)
		}
		deviceIDs = append(deviceIDs, id)
	}

	now := time.Now().UTC()
	db.SetDeviceStatus(deviceIDs[0], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[1], StatusActive, now)
	db.SetDeviceStatus(deviceIDs[2], StatusActive, now)

	db.UpdateHeartbeat(deviceIDs[0], "v1", "amd64", "systemd", "linux", 0, nil, now)
	db.UpdateHeartbeat(deviceIDs[1], "v1", "amd64", "initd", "debian", 0, nil, now)
	db.UpdateHeartbeat(deviceIDs[2], "v1", "amd64", "systemd", "ubuntu", 0, nil, now)

	// Select by init type
	target := TargetSpec{InitType: "systemd"}
	selected := reg.SelectDevices(target)

	if len(selected) != 2 {
		t.Fatalf("Expected 2 systemd devices, got %d: %v", len(selected), selected)
	}
}

func TestListDevices(t *testing.T) {
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

	reg := NewRegistry(db)

	// Create multiple devices
	for i := 0; i < 5; i++ {
		_, _, err := reg.MintToken()
		if err != nil {
			t.Fatalf("MintToken failed: %v", err)
		}
	}

	devices := reg.ListDevices()

	if len(devices) != 5 {
		t.Fatalf("Expected 5 devices, got %d", len(devices))
	}

	// Verify all devices have required fields
	for _, dev := range devices {
		if dev.DeviceID == "" {
			t.Fatal("Device has empty device_id")
		}
		if dev.Status != StatusActive && dev.Status != StatusInactive {
			t.Fatalf("Invalid status: %q", dev.Status)
		}
	}
}

func TestTargetSpecMatchesInfo(t *testing.T) {
	info := &DeviceInfo{
		DeviceID: "dev-aaa",
		Status:   StatusActive,
		Version:  "v1.0.0",
		Arch:     "amd64",
		Init:     "systemd",
		OS:       "linux",
		Labels:   map[string]string{"env": "prod", "tier": "web"},
		Tags:     []string{"production", "web"},
	}

	// Test All match
	target := TargetSpec{All: true}
	if !target.matchesInfo(info) {
		t.Fatal("All should match everything")
	}

	// Test ID match
	target = TargetSpec{IDs: []string{"dev-aaa", "dev-bbb"}}
	if !target.matchesInfo(info) {
		t.Fatal("ID match should succeed")
	}

	// Test ID no match
	target = TargetSpec{IDs: []string{"dev-bbb", "dev-ccc"}}
	if target.matchesInfo(info) {
		t.Fatal("ID match should fail")
	}

	// Test InitType match
	target = TargetSpec{InitType: "systemd"}
	if !target.matchesInfo(info) {
		t.Fatal("InitType match should succeed")
	}

	// Test InitType no match
	target = TargetSpec{InitType: "initd"}
	if target.matchesInfo(info) {
		t.Fatal("InitType match should fail")
	}

	// Test Tags match (all required)
	target = TargetSpec{Tags: []string{"production", "web"}}
	if !target.matchesInfo(info) {
		t.Fatal("Tags match should succeed")
	}

	// Test Tags no match (missing one)
	target = TargetSpec{Tags: []string{"production", "staging"}}
	if target.matchesInfo(info) {
		t.Fatal("Tags match should fail")
	}

	// Test Labels match
	target = TargetSpec{Labels: map[string]string{"env": "prod"}}
	if !target.matchesInfo(info) {
		t.Fatal("Labels match should succeed")
	}

	// Test Labels no match
	target = TargetSpec{Labels: map[string]string{"env": "staging"}}
	if target.matchesInfo(info) {
		t.Fatal("Labels match should fail")
	}

	// Test Labels multiple match
	target = TargetSpec{Labels: map[string]string{"env": "prod", "tier": "web"}}
	if !target.matchesInfo(info) {
		t.Fatal("Multiple labels match should succeed")
	}

	// Test Labels multiple no match
	target = TargetSpec{Labels: map[string]string{"env": "prod", "tier": "api"}}
	if target.matchesInfo(info) {
		t.Fatal("Multiple labels match should fail")
	}
}
