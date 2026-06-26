package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/canavan-a/fleetman/wire"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// Default command timeout.
	defaultCommandTimeout = 30 * time.Second

	// WebSocket keepalive settings.
	wsPingInterval = 5 * time.Second
	wsPongTimeout  = 5 * time.Second
)

// Hub ties together the registry, command store, and WebSocket handling.
type Hub struct {
	Registry *Registry
	Commands *CommandStore
}

// --- WebSocket endpoint (device auth) ---

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HandleWebSocket handles the /ws endpoint. Authenticates the device token
// from the handshake, then loops reading messages (heartbeat, result).
func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract token from Authorization header.
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		http.Error(w, "missing or invalid Authorization header", http.StatusUnauthorized)
		return
	}

	deviceID, ok := h.Registry.AuthenticateToken(token)
	if !ok {
		http.Error(w, "invalid device token", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error for %s: %v", deviceID, err)
		return
	}

	log.Printf("device %s connected", deviceID)
	h.Registry.SetActive(deviceID, conn)

	// Cleanup on disconnect.
	defer func() {
		conn.Close()
		h.Registry.SetInactive(deviceID)
		log.Printf("device %s disconnected", deviceID)
	}()

	// Configure keepalive: server sends pings, expects pongs within timeout.
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPingInterval + wsPongTimeout))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(wsPingInterval + wsPongTimeout))

	// Start a goroutine to send periodic pings.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				lc := h.Registry.GetConnection(deviceID)
				if lc == nil {
					return
				}
				lc.connMu.Lock()
				err := lc.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				lc.connMu.Unlock()
				if err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read loop.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws read error for %s: %v", deviceID, err)
			}
			return
		}

		var env wire.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			log.Printf("bad message from %s: %v", deviceID, err)
			continue
		}

		switch env.Type {
		case wire.TypeHeartbeat:
			if env.Heartbeat != nil {
				hb := env.Heartbeat
				h.Registry.UpdateHeartbeat(deviceID, hb.Version, hb.Arch, hb.Init, hb.OS, hb.Uptime, hb.Labels)
			}
		case wire.TypeResult:
			if env.Result != nil {
				res := env.Result
				h.Commands.RecordResult(res.CommandID, deviceID, res.Stdout, res.Stderr, res.Retcode)
			}
		default:
			log.Printf("unknown message type from %s: %s", deviceID, env.Type)
		}
	}
}

// --- HTTP API handlers ---

// HandleMintToken handles POST /tokens.
// Creates a new device record and returns {device_id, token}.
func (h *Hub) HandleMintToken(w http.ResponseWriter, r *http.Request) {
	deviceID, token, err := h.Registry.MintToken()
	if err != nil {
		log.Printf("mint token error: %v", err)
		http.Error(w, `{"error":"failed to mint token"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]string{
		"device_id": deviceID,
		"token":     token,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
	log.Printf("minted token for device %s", deviceID)
}

// HandleListDevices handles GET /devices.
func (h *Hub) HandleListDevices(w http.ResponseWriter, r *http.Request) {
	devices := h.Registry.ListDevices()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"devices": devices,
	})
}

// HandleDeleteDevice handles DELETE /devices/{id}.
func (h *Hub) HandleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"missing device id"}`, http.StatusBadRequest)
		return
	}

	if !h.Registry.DeleteDevice(id) {
		http.Error(w, `{"error":"device not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "device_id": id})
	log.Printf("deleted device %s", id)
}

// postCommandRequest is the JSON body for POST /commands.
type postCommandRequest struct {
	Action  string                 `json:"action"`
	Target  TargetSpec             `json:"target"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// HandlePostCommand handles POST /commands.
// Accepts a command, generates a command_id, routes to target devices.
func (h *Hub) HandlePostCommand(w http.ResponseWriter, r *http.Request) {
	var req postCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Action == "" {
		http.Error(w, `{"error":"action is required"}`, http.StatusBadRequest)
		return
	}

	// Resolve target devices.
	targetIDs := h.Registry.SelectDevices(req.Target)
	if len(targetIDs) == 0 {
		http.Error(w, `{"error":"no matching active devices"}`, http.StatusNotFound)
		return
	}

	commandID := uuid.New().String()
	now := time.Now().UTC()

	// Build the command record.
	results := make(map[string]*DeviceResult, len(targetIDs))
	for _, id := range targetIDs {
		results[id] = &DeviceResult{
			DeviceID: id,
			Status:   "pending",
		}
	}

	cmdRec := &CommandRecord{
		CommandID: commandID,
		Action:    req.Action,
		Payload:   req.Payload,
		TargetIDs: targetIDs,
		CreatedAt: now,
		TimeoutAt: now.Add(defaultCommandTimeout),
		Results:   results,
	}
	if err := h.Commands.Create(cmdRec); err != nil {
		log.Printf("failed to store command: %v", err)
		http.Error(w, `{"error":"failed to store command"}`, http.StatusInternalServerError)
		return
	}

	// Build the wire command.
	cmd := wire.Command{
		CommandID: commandID,
		Action:    req.Action,
		Payload:   req.Payload,
	}
	env := wire.Envelope{
		Type:    wire.TypeCommand,
		Command: &cmd,
	}

	// Send to each target device.
	sent := 0
	for _, id := range targetIDs {
		lc := h.Registry.GetConnection(id)
		if lc == nil {
			continue
		}
		lc.connMu.Lock()
		err := lc.conn.WriteJSON(env)
		lc.connMu.Unlock()
		if err != nil {
			log.Printf("failed to send command %s to %s: %v", commandID, id, err)
			continue
		}
		sent++
	}

	log.Printf("command %s (%s) sent to %d/%d devices", commandID, req.Action, sent, len(targetIDs))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"command_id": commandID,
		"sent":       sent,
		"total":      len(targetIDs),
	})
}

// HandleGetCommand handles GET /commands/{id}.
// Returns collected results so far (poll until complete or timeout).
func (h *Hub) HandleGetCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"missing command id"}`, http.StatusBadRequest)
		return
	}

	rec := h.Commands.Get(id)
	if rec == nil {
		http.Error(w, `{"error":"command not found"}`, http.StatusNotFound)
		return
	}

	// Compute summary.
	pending, completed, failed, timedOut := 0, 0, 0, 0
	for _, dr := range rec.Results {
		switch dr.Status {
		case "pending":
			pending++
		case "ok":
			completed++
		case "error":
			failed++
		case "timeout":
			timedOut++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"command_id": rec.CommandID,
		"action":     rec.Action,
		"created_at": rec.CreatedAt,
		"timeout_at": rec.TimeoutAt,
		"summary": map[string]int{
			"total":     len(rec.Results),
			"pending":   pending,
			"completed": completed,
			"failed":    failed,
			"timeout":   timedOut,
		},
		"results": rec.Results,
	})
}

// releaseInfo holds the current release info per architecture.
type releaseInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// HandleGetRelease handles GET /releases/{arch}.
// Returns the current release URL for the given architecture.
// In v1 this is configured via environment variables.
func (h *Hub) HandleGetRelease(w http.ResponseWriter, r *http.Request) {
	arch := r.PathValue("arch")
	if arch == "" {
		http.Error(w, `{"error":"missing arch"}`, http.StatusBadRequest)
		return
	}

	// Look up release info from environment.
	// Expected env: FLEET_RELEASE_VERSION, FLEET_RELEASE_URL_<ARCH>
	// e.g. FLEET_RELEASE_URL_AMD64, FLEET_RELEASE_URL_ARM64
	version := envOrDefault("FLEET_RELEASE_VERSION", "")
	urlKey := "FLEET_RELEASE_URL_" + strings.ToUpper(arch)
	url := envOrDefault(urlKey, "")

	if version == "" || url == "" {
		http.Error(w, `{"error":"no release configured for this architecture"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(releaseInfo{
		Version: version,
		URL:     url,
	})
}

// --- Tag handlers ---

// HandleListTags handles GET /tags.
func (h *Hub) HandleListTags(w http.ResponseWriter, r *http.Request) {
	tags := h.Registry.db.ListTags()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tags": tags,
	})
}

// HandleCreateTag handles POST /tags.
// Body: {"name": "production"}
func (h *Hub) HandleCreateTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	if err := h.Registry.db.CreateTag(req.Name); err != nil {
		// Likely a UNIQUE constraint violation.
		http.Error(w, `{"error":"tag already exists"}`, http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"tag": req.Name})
	log.Printf("created tag %q", req.Name)
}

// HandleDeleteTag handles DELETE /tags/{name}.
func (h *Hub) HandleDeleteTag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, `{"error":"missing tag name"}`, http.StatusBadRequest)
		return
	}

	if !h.Registry.db.DeleteTag(name) {
		http.Error(w, `{"error":"tag not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "tag": name})
	log.Printf("deleted tag %q", name)
}

// HandleAddDeviceTags handles POST /devices/{id}/tags.
// Body: {"tags": ["production", "web"]}
func (h *Hub) HandleAddDeviceTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"missing device id"}`, http.StatusBadRequest)
		return
	}

	if !h.Registry.db.DeviceExists(id) {
		http.Error(w, `{"error":"device not found"}`, http.StatusNotFound)
		return
	}

	var req struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tags) == 0 {
		http.Error(w, `{"error":"tags array is required"}`, http.StatusBadRequest)
		return
	}

	// Validate all tags exist.
	for _, tag := range req.Tags {
		if !h.Registry.db.TagExists(tag) {
			http.Error(w, fmt.Sprintf(`{"error":"tag %q does not exist, create it first"}`, tag), http.StatusBadRequest)
			return
		}
	}

	if err := h.Registry.db.AddTagToDevices(req.Tags[0], []string{id}); err != nil {
		http.Error(w, `{"error":"failed to add tags"}`, http.StatusInternalServerError)
		return
	}
	// Add remaining tags.
	for _, tag := range req.Tags[1:] {
		h.Registry.db.AddTagToDevices(tag, []string{id})
	}

	tags := h.Registry.db.GetDeviceTags(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id": id,
		"tags":      tags,
	})
	log.Printf("added tags %v to device %s", req.Tags, id)
}

// HandleRemoveDeviceTag handles DELETE /devices/{id}/tags/{tag}.
func (h *Hub) HandleRemoveDeviceTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tag := r.PathValue("tag")
	if id == "" || tag == "" {
		http.Error(w, `{"error":"missing device id or tag"}`, http.StatusBadRequest)
		return
	}

	if !h.Registry.db.RemoveDeviceTag(id, tag) {
		http.Error(w, `{"error":"tag not found on device"}`, http.StatusNotFound)
		return
	}

	tags := h.Registry.db.GetDeviceTags(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id": id,
		"tags":      tags,
	})
	log.Printf("removed tag %q from device %s", tag, id)
}

// HandleBulkTag handles POST /tags/{name}/devices.
// Body: {"device_ids": ["dev-abc", "dev-def"]}
// Adds the tag to multiple devices at once.
func (h *Hub) HandleBulkTag(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("name")
	if tag == "" {
		http.Error(w, `{"error":"missing tag name"}`, http.StatusBadRequest)
		return
	}

	if !h.Registry.db.TagExists(tag) {
		http.Error(w, `{"error":"tag does not exist"}`, http.StatusNotFound)
		return
	}

	var req struct {
		DeviceIDs []string `json:"device_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.DeviceIDs) == 0 {
		http.Error(w, `{"error":"device_ids array is required"}`, http.StatusBadRequest)
		return
	}

	if err := h.Registry.db.AddTagToDevices(tag, req.DeviceIDs); err != nil {
		http.Error(w, `{"error":"failed to tag devices"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tag":        tag,
		"device_ids": req.DeviceIDs,
	})
	log.Printf("bulk-tagged %d devices with %q", len(req.DeviceIDs), tag)
}

// HandleBulkUntag handles DELETE /tags/{name}/devices.
// Body: {"device_ids": ["dev-abc", "dev-def"]}
// Removes the tag from multiple devices at once.
func (h *Hub) HandleBulkUntag(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("name")
	if tag == "" {
		http.Error(w, `{"error":"missing tag name"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		DeviceIDs []string `json:"device_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.DeviceIDs) == 0 {
		http.Error(w, `{"error":"device_ids array is required"}`, http.StatusBadRequest)
		return
	}

	if err := h.Registry.db.RemoveTagFromDevices(tag, req.DeviceIDs); err != nil {
		http.Error(w, `{"error":"failed to untag devices"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tag":        tag,
		"device_ids": req.DeviceIDs,
	})
	log.Printf("bulk-untagged %d devices from %q", len(req.DeviceIDs), tag)
}

func envOrDefault(key, fallback string) string {
	if v, ok := lookupEnv(key); ok {
		return v
	}
	return fallback
}
