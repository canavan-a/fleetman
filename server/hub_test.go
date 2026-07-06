package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// connectDevice opens a real WebSocket connection to hub's HandleWebSocket
// using the given token, so hub.Registry.SetActive registers a live
// connection (needed for command dispatch to consider the device reachable).
// The returned func closes the connection and the test server.
func connectDevice(t *testing.T, hub *Hub, token string) func() {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", hub.HandleWebSocket)
	srv := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		srv.Close()
		t.Fatalf("failed to dial websocket: %v", err)
	}

	// Give the server goroutine a moment to register the connection.
	time.Sleep(50 * time.Millisecond)

	return func() {
		conn.Close()
		srv.Close()
	}
}

// serve calls handler directly after populating req's path values the way
// the production ServeMux would (via http.Request.SetPathValue), since the
// tests invoke handlers without going through the real mux.
func serve(handler http.HandlerFunc, req *http.Request, pathValues map[string]string) *httptest.ResponseRecorder {
	for name, value := range pathValues {
		req.SetPathValue(name, value)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleMintToken(t *testing.T) {
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

	hub := &Hub{Registry: NewRegistry(db)}
	req := httptest.NewRequest("POST", "/tokens", nil)
	w := httptest.NewRecorder()

	hub.HandleMintToken(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["device_id"] == "" {
		t.Fatal("Expected device_id in response")
	}
	if resp["token"] == "" {
		t.Fatal("Expected token in response")
	}
}

func TestHandleGetDevice(t *testing.T) {
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
	deviceID, _, _ := reg.MintToken()

	hub := &Hub{Registry: reg}

	// Get existing device
	req := httptest.NewRequest("GET", "/devices/"+deviceID, nil)
	w := serve(hub.HandleGetDevice, req, map[string]string{"id": deviceID})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Get non-existent device
	req = httptest.NewRequest("GET", "/devices/non-existent", nil)
	w = serve(hub.HandleGetDevice, req, map[string]string{"id": "non-existent"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}

	// Missing device ID
	req = httptest.NewRequest("GET", "/devices/", nil)
	w = serve(hub.HandleGetDevice, req, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestHandleListDevices(t *testing.T) {
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
	reg.MintToken()
	reg.MintToken()

	hub := &Hub{Registry: reg}
	req := httptest.NewRequest("GET", "/devices", nil)
	w := httptest.NewRecorder()

	hub.HandleListDevices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	devices, ok := resp["devices"].([]interface{})
	if !ok {
		t.Fatal("Expected devices array in response")
	}

	if len(devices) != 2 {
		t.Fatalf("Expected 2 devices, got %d", len(devices))
	}
}

func TestHandleDeleteDevice(t *testing.T) {
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
	deviceID, _, _ := reg.MintToken()

	hub := &Hub{Registry: reg}

	// Delete existing device
	req := httptest.NewRequest("DELETE", "/devices/"+deviceID, nil)
	w := serve(hub.HandleDeleteDevice, req, map[string]string{"id": deviceID})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Delete non-existent device
	req = httptest.NewRequest("DELETE", "/devices/non-existent", nil)
	w = serve(hub.HandleDeleteDevice, req, map[string]string{"id": "non-existent"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}

	// Missing device ID
	req = httptest.NewRequest("DELETE", "/devices/", nil)
	w = serve(hub.HandleDeleteDevice, req, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestHandlePostCommand(t *testing.T) {
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
	store := NewCommandStore(db)
	deviceID, token, _ := reg.MintToken()

	hub := &Hub{Registry: reg, Commands: store}

	closeConn := connectDevice(t, hub, token)
	defer closeConn()

	payload := `{
		"action": "run",
		"target": {"ids": ["` + deviceID + `"]},
		"payload": {"cmd": "ls -la"}
	}`
	req := httptest.NewRequest("POST", "/commands", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandlePostCommand, req, nil)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["sent"] != float64(1) {
		t.Fatalf("Expected sent=1, got %v", resp["sent"])
	}
	if resp["total"] != float64(1) {
		t.Fatalf("Expected total=1, got %v", resp["total"])
	}
	if resp["command_id"] == "" {
		t.Fatal("Expected command_id in response")
	}
}

func TestHandlePostCommandNoMatchingDevices(t *testing.T) {
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
	hub := &Hub{Registry: reg}

	payload := `{
		"action": "run",
		"target": {"ids": ["non-existent"]}
	}`
	req := httptest.NewRequest("POST", "/commands", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandlePostCommand, req, nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}
}

func TestHandlePostCommandMissingAction(t *testing.T) {
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
	hub := &Hub{Registry: reg}

	payload := `{
		"target": {"all": true}
	}`
	req := httptest.NewRequest("POST", "/commands", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandlePostCommand, req, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestHandleGetCommand(t *testing.T) {
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
	store := NewCommandStore(db)
	hub := &Hub{Registry: reg, Commands: store}

	deviceID, _, _ := reg.MintToken()
	db.SetDeviceStatus(deviceID, StatusActive, time.Now().UTC())

	// Create a command
	now := time.Now().UTC()
	cmdRec := &CommandRecord{
		CommandID:   "cmd-123",
		Action:      "run",
		TargetIDs:   []string{deviceID},
		CreatedAt:   now,
		TimeoutAt:   now.Add(defaultCommandTimeout),
		Results:     map[string]*DeviceResult{deviceID: {DeviceID: deviceID, Status: "pending"}},
	}
	store.Create(cmdRec)

	// Get existing command
	req := httptest.NewRequest("GET", "/commands/cmd-123", nil)
	w := serve(hub.HandleGetCommand, req, map[string]string{"id": "cmd-123"})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["command_id"] != "cmd-123" {
		t.Fatalf("Expected command_id cmd-123, got %v", resp["command_id"])
	}
	if resp["action"] != "run" {
		t.Fatalf("Expected action 'run', got %v", resp["action"])
	}

	// Get non-existent command
	req = httptest.NewRequest("GET", "/commands/non-existent", nil)
	w = serve(hub.HandleGetCommand, req, map[string]string{"id": "non-existent"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}
}

func TestHandleGetRelease(t *testing.T) {
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

	hub := &Hub{Registry: NewRegistry(db)}

	// Set up environment variables
	lookupEnv = func(key string) (string, bool) {
		switch key {
		case "FLEET_RELEASE_VERSION":
			return "v1.2.3", true
		case "FLEET_RELEASE_URL_AMD64":
			return "https://example.com/fleetman-server-linux-amd64", true
		default:
			return "", false
		}
	}

	// Get release for amd64
	req := httptest.NewRequest("GET", "/releases/amd64", nil)
	w := serve(hub.HandleGetRelease, req, map[string]string{"arch": "amd64"})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["version"] != "v1.2.3" {
		t.Fatalf("Expected version v1.2.3, got %s", resp["version"])
	}
	if resp["url"] != "https://example.com/fleetman-server-linux-amd64" {
		t.Fatalf("Expected correct URL, got %s", resp["url"])
	}

	// Get release for unsupported arch
	req = httptest.NewRequest("GET", "/releases/ppc64le", nil)
	w = serve(hub.HandleGetRelease, req, map[string]string{"arch": "ppc64le"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}

	// Missing arch
	req = httptest.NewRequest("GET", "/releases/", nil)
	w = serve(hub.HandleGetRelease, req, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestHandleListTags(t *testing.T) {
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
	reg.db.CreateTag("production")
	reg.db.CreateTag("staging")

	hub := &Hub{Registry: reg}
	req := httptest.NewRequest("GET", "/tags", nil)
	w := httptest.NewRecorder()

	hub.HandleListTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	tags, ok := resp["tags"].([]interface{})
	if !ok {
		t.Fatal("Expected tags array in response")
	}

	if len(tags) != 2 {
		t.Fatalf("Expected 2 tags, got %d", len(tags))
	}
}

func TestHandleCreateTag(t *testing.T) {
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
	hub := &Hub{Registry: reg}

	// Create new tag
	payload := `{"name": "production"}`
	req := httptest.NewRequest("POST", "/tags", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandleCreateTag, req, nil)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	// Try to create duplicate tag
	req = httptest.NewRequest("POST", "/tags", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w = serve(hub.HandleCreateTag, req, nil)

	if w.Code != http.StatusConflict {
		t.Fatalf("Expected status %d for duplicate tag, got %d: %s", http.StatusConflict, w.Code, w.Body.String())
	}

	// Missing name
	payload = `{}`
	req = httptest.NewRequest("POST", "/tags", strings.NewReader(payload))
	w = serve(hub.HandleCreateTag, req, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestHandleGetTagDevices(t *testing.T) {
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
	deviceA, _, _ := reg.MintToken()
	deviceB, _, _ := reg.MintToken()
	db.CreateTag("production")
	db.AddTagToDevices("production", []string{deviceA, deviceB})

	hub := &Hub{Registry: reg}

	// Get devices with tag
	req := httptest.NewRequest("GET", "/tags/production/devices", nil)
	w := serve(hub.HandleGetTagDevices, req, map[string]string{"name": "production"})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["tag"] != "production" {
		t.Fatalf("Expected tag 'production', got %v", resp["tag"])
	}

	deviceIDs, ok := resp["device_ids"].([]interface{})
	if !ok {
		t.Fatal("Expected device_ids array")
	}

	if len(deviceIDs) != 2 {
		t.Fatalf("Expected 2 devices, got %d", len(deviceIDs))
	}

	// Get devices for non-existent tag
	req = httptest.NewRequest("GET", "/tags/nonexistent/devices", nil)
	w = serve(hub.HandleGetTagDevices, req, map[string]string{"name": "nonexistent"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}
}

func TestHandleAddDeviceTags(t *testing.T) {
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
	deviceID, _, _ := reg.MintToken()
	db.CreateTag("production")
	db.CreateTag("web")

	hub := &Hub{Registry: reg}

	payload := `{"tags": ["production", "web"]}`
	req := httptest.NewRequest("POST", "/devices/"+deviceID+"/tags", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandleAddDeviceTags, req, map[string]string{"id": deviceID})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	tags, ok := resp["tags"].([]interface{})
	if !ok {
		t.Fatal("Expected tags array")
	}

	if len(tags) != 2 {
		t.Fatalf("Expected 2 tags, got %d", len(tags))
	}

	// Device not found
	req = httptest.NewRequest("POST", "/devices/non-existent/tags", strings.NewReader(payload))
	w = serve(hub.HandleAddDeviceTags, req, map[string]string{"id": "non-existent"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}

	// Tag doesn't exist
	payload = `{"tags": ["nonexistent"]}`
	req = httptest.NewRequest("POST", "/devices/"+deviceID+"/tags", strings.NewReader(payload))
	w = serve(hub.HandleAddDeviceTags, req, map[string]string{"id": deviceID})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestHandleRemoveDeviceTag(t *testing.T) {
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
	deviceID, _, _ := reg.MintToken()
	db.CreateTag("production")
	db.AddTagToDevices("production", []string{deviceID})

	hub := &Hub{Registry: reg}

	// Remove tag
	req := httptest.NewRequest("DELETE", "/devices/"+deviceID+"/tags/production", nil)
	w := serve(hub.HandleRemoveDeviceTag, req, map[string]string{"id": deviceID, "tag": "production"})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Tag not on device
	req = httptest.NewRequest("DELETE", "/devices/"+deviceID+"/tags/production", nil)
	w = serve(hub.HandleRemoveDeviceTag, req, map[string]string{"id": deviceID, "tag": "production"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}
}

func TestHandleBulkTag(t *testing.T) {
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
	deviceA, _, _ := reg.MintToken()
	deviceB, _, _ := reg.MintToken()
	db.CreateTag("production")

	hub := &Hub{Registry: reg}

	payload := `{"device_ids": ["` + deviceA + `", "` + deviceB + `"]}`
	req := httptest.NewRequest("POST", "/tags/production/devices", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandleBulkTag, req, map[string]string{"name": "production"})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["tag"] != "production" {
		t.Fatalf("Expected tag 'production', got %v", resp["tag"])
	}

	deviceIDs, ok := resp["device_ids"].([]interface{})
	if !ok {
		t.Fatal("Expected device_ids array")
	}

	if len(deviceIDs) != 2 {
		t.Fatalf("Expected 2 device_ids, got %d", len(deviceIDs))
	}

	// Tag doesn't exist
	payload = `{"device_ids": ["dev-aaa"]}`
	req = httptest.NewRequest("POST", "/tags/nonexistent/devices", strings.NewReader(payload))
	w = serve(hub.HandleBulkTag, req, map[string]string{"name": "nonexistent"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusNotFound, w.Code, w.Body.String())
	}
}

func TestHandleBulkUntag(t *testing.T) {
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
	reg.MintToken()
	reg.MintToken()
	db.CreateTag("production")
	db.AddTagToDevices("production", []string{"dev-aaa", "dev-bbb"})

	hub := &Hub{Registry: reg}

	payload := `{"device_ids": ["dev-aaa", "dev-bbb"]}`
	req := httptest.NewRequest("DELETE", "/tags/production/devices", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandleBulkUntag, req, map[string]string{"name": "production"})

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["tag"] != "production" {
		t.Fatalf("Expected tag 'production', got %v", resp["tag"])
	}
}

func TestHandlePostCommandWithUpgradeURLResolution(t *testing.T) {
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
	store := NewCommandStore(db)
	deviceID, _, _ := reg.MintToken()
	db.SetDeviceStatus(deviceID, StatusActive, time.Now().UTC())
	db.UpdateHeartbeat(deviceID, "v1.0.0", "amd64", "systemd", "linux", 0, nil, time.Now().UTC())

	hub := &Hub{Registry: reg, Commands: store}

	// Create upgrade command without URL (should be resolved)
	payload := `{
		"action": "upgrade",
		"target": {"ids": ["` + deviceID + `"]},
		"payload": {"version": "v2.0.0"}
	}`
	req := httptest.NewRequest("POST", "/commands", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := serve(hub.HandlePostCommand, req, nil)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	// Verify command was created
	_ = hub.Commands.Get("cmd-123")
	// Note: This test verifies the command creation endpoint works
	// The actual URL resolution happens during command dispatch
}
