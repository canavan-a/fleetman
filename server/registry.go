package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DeviceStatus represents the connection state of a device.
type DeviceStatus string

const (
	StatusActive   DeviceStatus = "active"
	StatusInactive DeviceStatus = "inactive"
)

// DeviceInfo is an API-safe snapshot of a device (no mutex, no conn).
type DeviceInfo struct {
	DeviceID  string            `json:"device_id"`
	Status    DeviceStatus      `json:"status"`
	LastSeen  time.Time         `json:"last_seen"`
	FirstSeen time.Time         `json:"first_seen"`
	Version   string            `json:"version,omitempty"`
	Arch      string            `json:"arch,omitempty"`
	Init      string            `json:"init,omitempty"`
	OS        string            `json:"os,omitempty"`
	Uptime    int64             `json:"uptime,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
}

// liveConn holds a live WebSocket connection and its write mutex.
type liveConn struct {
	conn   *websocket.Conn
	connMu sync.Mutex
}

// Registry manages device state. SQLite is the source of truth for device records;
// in-memory map holds only live WebSocket connections (can't persist sockets).
type Registry struct {
	db    *DB
	mu    sync.RWMutex
	conns map[string]*liveConn // device_id → live connection
}

// NewRegistry creates a registry backed by the given database.
func NewRegistry(db *DB) *Registry {
	return &Registry{
		db:    db,
		conns: make(map[string]*liveConn),
	}
}

// generateID creates a short server-assigned device ID like "dev-a1b2c3d4".
func generateID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("dev-%s", hex.EncodeToString(b))
}

// generateToken creates a cryptographically random device token.
func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// MintToken creates a new device record with an assigned ID and token.
// The device starts as inactive (never connected). Persisted to DB.
func (r *Registry) MintToken() (deviceID, token string, err error) {
	deviceID = generateID()
	for r.db.DeviceExists(deviceID) {
		deviceID = generateID()
	}

	token = generateToken()
	now := time.Now().UTC()

	if err := r.db.InsertDevice(deviceID, token, now); err != nil {
		return "", "", fmt.Errorf("insert device: %w", err)
	}

	return deviceID, token, nil
}

// AuthenticateToken validates a device token and returns the device ID.
func (r *Registry) AuthenticateToken(token string) (string, bool) {
	return r.db.LookupToken(token)
}

// SetActive marks a device as active and stores the WebSocket connection.
func (r *Registry) SetActive(deviceID string, conn *websocket.Conn) {
	now := time.Now().UTC()
	r.db.SetDeviceStatus(deviceID, StatusActive, now)

	r.mu.Lock()
	r.conns[deviceID] = &liveConn{conn: conn}
	r.mu.Unlock()
}

// SetInactive marks a device as inactive and clears the connection.
func (r *Registry) SetInactive(deviceID string) {
	now := time.Now().UTC()
	r.db.SetDeviceStatus(deviceID, StatusInactive, now)

	r.mu.Lock()
	delete(r.conns, deviceID)
	r.mu.Unlock()
}

// UpdateHeartbeat refreshes metadata from a heartbeat message.
func (r *Registry) UpdateHeartbeat(deviceID, version, arch, initType, osName string, uptime int64, labels map[string]string) {
	now := time.Now().UTC()
	r.db.UpdateHeartbeat(deviceID, version, arch, initType, osName, uptime, labels, now)
}

// GetDevice returns an API-safe snapshot of a device record.
func (r *Registry) GetDevice(deviceID string) (DeviceInfo, bool) {
	return r.db.GetDevice(deviceID)
}

// ListDevices returns all device info snapshots from DB.
func (r *Registry) ListDevices() []DeviceInfo {
	return r.db.ListDevices()
}

// DeleteDevice removes a device record, revokes its token, and closes any live connection.
func (r *Registry) DeleteDevice(deviceID string) bool {
	// Close live connection if any.
	r.mu.Lock()
	if lc, ok := r.conns[deviceID]; ok {
		lc.conn.Close()
		delete(r.conns, deviceID)
	}
	r.mu.Unlock()

	return r.db.DeleteDevice(deviceID)
}

// GetConnection returns the live connection for a device (nil if inactive/disconnected).
func (r *Registry) GetConnection(deviceID string) *liveConn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conns[deviceID]
}

// SelectDevices returns active device IDs matching the given targeting criteria.
func (r *Registry) SelectDevices(target TargetSpec) []string {
	devices := r.db.ListActiveDeviceRows()

	var ids []string
	for _, info := range devices {
		if target.matchesInfo(&info) {
			ids = append(ids, info.DeviceID)
		}
	}
	return ids
}

// TargetSpec describes which devices to target.
type TargetSpec struct {
	All      bool              `json:"all,omitempty"`
	IDs      []string          `json:"ids,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	InitType string            `json:"init_type,omitempty"`
}

func (t TargetSpec) matchesInfo(info *DeviceInfo) bool {
	if t.All {
		return true
	}
	for _, id := range t.IDs {
		if info.DeviceID == id {
			return true
		}
	}
	if t.InitType != "" && info.Init == t.InitType {
		return true
	}
	if len(t.Tags) > 0 {
		// Device must have ALL specified tags.
		tagSet := make(map[string]struct{}, len(info.Tags))
		for _, tag := range info.Tags {
			tagSet[tag] = struct{}{}
		}
		allMatch := true
		for _, required := range t.Tags {
			if _, ok := tagSet[required]; !ok {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}
	if len(t.Labels) > 0 {
		for k, v := range t.Labels {
			if info.Labels[k] != v {
				return false
			}
		}
		return true
	}
	return false
}
