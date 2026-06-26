package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection with all persistence operations.
type DB struct {
	conn *sqlx.DB
}

// OpenDB opens (or creates) the SQLite database at path and runs migrations.
func OpenDB(path string) (*DB, error) {
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON", path)
	conn, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite is single-writer; limit to 1 open connection to avoid SQLITE_BUSY.
	conn.SetMaxOpenConns(1)

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Printf("database opened: %s", path)
	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// migrate creates the schema if it doesn't exist.
func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS devices (
		device_id   TEXT PRIMARY KEY,
		token       TEXT NOT NULL UNIQUE,
		status      TEXT NOT NULL DEFAULT 'inactive',
		first_seen  TEXT NOT NULL,
		last_seen   TEXT NOT NULL,
		version     TEXT NOT NULL DEFAULT '',
		arch        TEXT NOT NULL DEFAULT '',
		init_type   TEXT NOT NULL DEFAULT '',
		os          TEXT NOT NULL DEFAULT '',
		uptime      INTEGER NOT NULL DEFAULT 0,
		labels      TEXT NOT NULL DEFAULT '{}'
	);

	CREATE INDEX IF NOT EXISTS idx_devices_token ON devices(token);

	CREATE TABLE IF NOT EXISTS commands (
		command_id  TEXT PRIMARY KEY,
		action      TEXT NOT NULL,
		payload     TEXT NOT NULL DEFAULT '{}',
		target_ids  TEXT NOT NULL DEFAULT '[]',
		created_at  TEXT NOT NULL,
		timeout_at  TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS command_results (
		command_id  TEXT NOT NULL,
		device_id   TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending',
		stdout      TEXT NOT NULL DEFAULT '',
		stderr      TEXT NOT NULL DEFAULT '',
		retcode     INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (command_id, device_id),
		FOREIGN KEY (command_id) REFERENCES commands(command_id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS tags (
		name        TEXT PRIMARY KEY
	);

	CREATE TABLE IF NOT EXISTS device_tags (
		device_id   TEXT NOT NULL,
		tag         TEXT NOT NULL,
		PRIMARY KEY (device_id, tag),
		FOREIGN KEY (device_id) REFERENCES devices(device_id) ON DELETE CASCADE,
		FOREIGN KEY (tag) REFERENCES tags(name) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_device_tags_tag ON device_tags(tag);
	`
	_, err := db.conn.Exec(schema)
	return err
}

// --- Device operations ---

// deviceRow is the DB representation of a device.
type deviceRow struct {
	DeviceID  string `db:"device_id"`
	Token     string `db:"token"`
	Status    string `db:"status"`
	FirstSeen string `db:"first_seen"`
	LastSeen  string `db:"last_seen"`
	Version   string `db:"version"`
	Arch      string `db:"arch"`
	InitType  string `db:"init_type"`
	OS        string `db:"os"`
	Uptime    int64  `db:"uptime"`
	Labels    string `db:"labels"` // JSON
}

func (row *deviceRow) toInfo() DeviceInfo {
	firstSeen, _ := time.Parse(time.RFC3339, row.FirstSeen)
	lastSeen, _ := time.Parse(time.RFC3339, row.LastSeen)
	var labels map[string]string
	json.Unmarshal([]byte(row.Labels), &labels)

	return DeviceInfo{
		DeviceID:  row.DeviceID,
		Status:    DeviceStatus(row.Status),
		FirstSeen: firstSeen,
		LastSeen:  lastSeen,
		Version:   row.Version,
		Arch:      row.Arch,
		Init:      row.InitType,
		OS:        row.OS,
		Uptime:    row.Uptime,
		Labels:    labels,
	}
}

// InsertDevice inserts a new device record.
func (db *DB) InsertDevice(deviceID, token string, now time.Time) error {
	_, err := db.conn.Exec(
		`INSERT INTO devices (device_id, token, status, first_seen, last_seen) VALUES (?, ?, 'inactive', ?, ?)`,
		deviceID, token, now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	return err
}

// LookupToken returns the device_id for a token, or ("", false) if not found.
func (db *DB) LookupToken(token string) (string, bool) {
	var deviceID string
	err := db.conn.Get(&deviceID, `SELECT device_id FROM devices WHERE token = ?`, token)
	if err != nil {
		return "", false
	}
	return deviceID, true
}

// DeviceExists checks if a device_id already exists.
func (db *DB) DeviceExists(deviceID string) bool {
	var n int
	db.conn.Get(&n, `SELECT COUNT(*) FROM devices WHERE device_id = ?`, deviceID)
	return n > 0
}

// SetDeviceStatus updates the status and last_seen for a device.
func (db *DB) SetDeviceStatus(deviceID string, status DeviceStatus, now time.Time) error {
	_, err := db.conn.Exec(
		`UPDATE devices SET status = ?, last_seen = ? WHERE device_id = ?`,
		string(status), now.Format(time.RFC3339), deviceID,
	)
	return err
}

// UpdateHeartbeat updates device metadata from a heartbeat.
func (db *DB) UpdateHeartbeat(deviceID, version, arch, initType, osName string, uptime int64, labels map[string]string, now time.Time) error {
	labelsJSON, _ := json.Marshal(labels)
	if labels == nil {
		labelsJSON = []byte("{}")
	}
	_, err := db.conn.Exec(
		`UPDATE devices SET version = ?, arch = ?, init_type = ?, os = ?, uptime = ?, labels = ?, last_seen = ? WHERE device_id = ?`,
		version, arch, initType, osName, uptime, string(labelsJSON), now.Format(time.RFC3339), deviceID,
	)
	return err
}

// DeleteDevice removes a device and revokes its token. Returns false if not found.
func (db *DB) DeleteDevice(deviceID string) bool {
	res, err := db.conn.Exec(`DELETE FROM devices WHERE device_id = ?`, deviceID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// GetDevice returns a device info snapshot (nil-like if not found).
func (db *DB) GetDevice(deviceID string) (DeviceInfo, bool) {
	var row deviceRow
	err := db.conn.Get(&row, `SELECT * FROM devices WHERE device_id = ?`, deviceID)
	if err != nil {
		return DeviceInfo{}, false
	}
	info := row.toInfo()
	info.Tags = db.GetDeviceTags(deviceID)
	return info, true
}

// ListDevices returns all device info snapshots.
func (db *DB) ListDevices() []DeviceInfo {
	var rows []deviceRow
	err := db.conn.Select(&rows, `SELECT * FROM devices ORDER BY device_id`)
	if err != nil {
		log.Printf("ListDevices error: %v", err)
		return nil
	}

	// Batch-load all device-tag mappings.
	tagMap := db.getAllDeviceTags()

	out := make([]DeviceInfo, len(rows))
	for i, row := range rows {
		out[i] = row.toInfo()
		out[i].Tags = tagMap[row.DeviceID]
	}
	return out
}

// ListActiveDeviceRows returns active devices for targeting.
// Also attaches tags to the returned DeviceInfo.
func (db *DB) ListActiveDeviceRows() []DeviceInfo {
	var rows []deviceRow
	err := db.conn.Select(&rows, `SELECT * FROM devices WHERE status = 'active'`)
	if err != nil {
		log.Printf("ListActiveDeviceRows error: %v", err)
		return nil
	}

	tagMap := db.getAllDeviceTags()

	out := make([]DeviceInfo, len(rows))
	for i, row := range rows {
		out[i] = row.toInfo()
		out[i].Tags = tagMap[row.DeviceID]
	}
	return out
}

// --- Tag operations ---

// CreateTag creates a new tag. Returns an error if it already exists.
func (db *DB) CreateTag(name string) error {
	_, err := db.conn.Exec(`INSERT INTO tags (name) VALUES (?)`, name)
	return err
}

// DeleteTag removes a tag and all its device associations.
func (db *DB) DeleteTag(name string) bool {
	res, err := db.conn.Exec(`DELETE FROM tags WHERE name = ?`, name)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// ListTags returns all tag names.
func (db *DB) ListTags() []string {
	var tags []string
	err := db.conn.Select(&tags, `SELECT name FROM tags ORDER BY name`)
	if err != nil {
		log.Printf("ListTags error: %v", err)
		return nil
	}
	return tags
}

// TagExists checks if a tag exists.
func (db *DB) TagExists(name string) bool {
	var n int
	db.conn.Get(&n, `SELECT COUNT(*) FROM tags WHERE name = ?`, name)
	return n > 0
}

// AddDeviceTag assigns a tag to a device. The tag must already exist.
func (db *DB) AddDeviceTag(deviceID, tag string) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO device_tags (device_id, tag) VALUES (?, ?)`,
		deviceID, tag,
	)
	return err
}

// RemoveDeviceTag removes a tag from a device.
func (db *DB) RemoveDeviceTag(deviceID, tag string) bool {
	res, err := db.conn.Exec(
		`DELETE FROM device_tags WHERE device_id = ? AND tag = ?`,
		deviceID, tag,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// GetDeviceTags returns all tags for a device.
func (db *DB) GetDeviceTags(deviceID string) []string {
	var tags []string
	err := db.conn.Select(&tags, `SELECT tag FROM device_tags WHERE device_id = ? ORDER BY tag`, deviceID)
	if err != nil {
		return nil
	}
	return tags
}

// GetDevicesByTag returns device IDs that have the given tag.
func (db *DB) GetDevicesByTag(tag string) []string {
	var ids []string
	err := db.conn.Select(&ids, `SELECT device_id FROM device_tags WHERE tag = ? ORDER BY device_id`, tag)
	if err != nil {
		return nil
	}
	return ids
}

// getAllDeviceTags returns a map of device_id → []tag for all devices.
func (db *DB) getAllDeviceTags() map[string][]string {
	var rows []struct {
		DeviceID string `db:"device_id"`
		Tag      string `db:"tag"`
	}
	err := db.conn.Select(&rows, `SELECT device_id, tag FROM device_tags ORDER BY device_id, tag`)
	if err != nil {
		log.Printf("getAllDeviceTags error: %v", err)
		return nil
	}
	m := make(map[string][]string)
	for _, row := range rows {
		m[row.DeviceID] = append(m[row.DeviceID], row.Tag)
	}
	return m
}

// AddTagToDevices assigns a tag to multiple devices in one transaction.
func (db *DB) AddTagToDevices(tag string, deviceIDs []string) error {
	tx, err := db.conn.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, id := range deviceIDs {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO device_tags (device_id, tag) VALUES (?, ?)`,
			id, tag,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RemoveTagFromDevices removes a tag from multiple devices in one transaction.
func (db *DB) RemoveTagFromDevices(tag string, deviceIDs []string) error {
	tx, err := db.conn.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, id := range deviceIDs {
		_, err := tx.Exec(
			`DELETE FROM device_tags WHERE device_id = ? AND tag = ?`,
			id, tag,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- Command operations ---

// InsertCommand inserts a command and its pending results.
func (db *DB) InsertCommand(rec *CommandRecord) error {
	payloadJSON, _ := json.Marshal(rec.Payload)
	if rec.Payload == nil {
		payloadJSON = []byte("{}")
	}
	targetJSON, _ := json.Marshal(rec.TargetIDs)

	tx, err := db.conn.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO commands (command_id, action, payload, target_ids, created_at, timeout_at) VALUES (?, ?, ?, ?, ?, ?)`,
		rec.CommandID, rec.Action, string(payloadJSON), string(targetJSON),
		rec.CreatedAt.Format(time.RFC3339), rec.TimeoutAt.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

	for _, id := range rec.TargetIDs {
		_, err = tx.Exec(
			`INSERT INTO command_results (command_id, device_id, status) VALUES (?, ?, 'pending')`,
			rec.CommandID, id,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RecordResult stores a device result for a command. Returns false if not found.
func (db *DB) RecordResult(commandID, deviceID, stdout, stderr string, retcode int) bool {
	status := "ok"
	if retcode != 0 {
		status = "error"
	}
	res, err := db.conn.Exec(
		`UPDATE command_results SET status = ?, stdout = ?, stderr = ?, retcode = ? WHERE command_id = ? AND device_id = ?`,
		status, stdout, stderr, retcode, commandID, deviceID,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// GetCommand returns a full command record with results. Marks timeouts. Returns nil if not found.
func (db *DB) GetCommand(commandID string) *CommandRecord {
	// Fetch command row.
	var cmd struct {
		CommandID string `db:"command_id"`
		Action    string `db:"action"`
		Payload   string `db:"payload"`
		TargetIDs string `db:"target_ids"`
		CreatedAt string `db:"created_at"`
		TimeoutAt string `db:"timeout_at"`
	}
	err := db.conn.Get(&cmd, `SELECT * FROM commands WHERE command_id = ?`, commandID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		log.Printf("GetCommand error: %v", err)
		return nil
	}

	createdAt, _ := time.Parse(time.RFC3339, cmd.CreatedAt)
	timeoutAt, _ := time.Parse(time.RFC3339, cmd.TimeoutAt)

	var payload map[string]interface{}
	json.Unmarshal([]byte(cmd.Payload), &payload)
	var targetIDs []string
	json.Unmarshal([]byte(cmd.TargetIDs), &targetIDs)

	// Fetch results.
	var resultRows []struct {
		CommandID string `db:"command_id"`
		DeviceID  string `db:"device_id"`
		Status    string `db:"status"`
		Stdout    string `db:"stdout"`
		Stderr    string `db:"stderr"`
		Retcode   int    `db:"retcode"`
	}
	err = db.conn.Select(&resultRows, `SELECT * FROM command_results WHERE command_id = ?`, commandID)
	if err != nil {
		log.Printf("GetCommand results error: %v", err)
		return nil
	}

	// Mark timed-out devices.
	now := time.Now().UTC()
	timedOut := now.After(timeoutAt)

	results := make(map[string]*DeviceResult, len(resultRows))
	for _, row := range resultRows {
		status := row.Status
		if timedOut && status == "pending" {
			status = "timeout"
			// Persist the timeout status.
			db.conn.Exec(
				`UPDATE command_results SET status = 'timeout' WHERE command_id = ? AND device_id = ? AND status = 'pending'`,
				commandID, row.DeviceID,
			)
		}
		results[row.DeviceID] = &DeviceResult{
			DeviceID: row.DeviceID,
			Status:   status,
			Stdout:   row.Stdout,
			Stderr:   row.Stderr,
			Retcode:  row.Retcode,
		}
	}

	return &CommandRecord{
		CommandID: commandID,
		Action:    cmd.Action,
		Payload:   payload,
		TargetIDs: targetIDs,
		CreatedAt: createdAt,
		TimeoutAt: timeoutAt,
		Results:   results,
	}
}
