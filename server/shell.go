package main

import (
	"sync"
	"time"
)

// shellChunk is one piece of streamed output from an agent's shell session.
type shellChunk struct {
	Stream string `json:"stream"`
	Data   string `json:"data"`
}

// shellSessionRecord tracks a live shell session routed to a specific device.
// Output is buffered here so the master can poll for it, matching the
// existing command-result polling pattern rather than requiring the master
// to hold its own WebSocket.
type shellSessionRecord struct {
	mu       sync.Mutex
	deviceID string
	chunks   []shellChunk
	closed   bool
	lastUsed time.Time
}

// ShellStore tracks open shell sessions in memory (no persistence needed —
// sessions don't survive a server restart any more than the agent's process does).
type ShellStore struct {
	mu       sync.Mutex
	sessions map[string]*shellSessionRecord
}

// NewShellStore creates an empty shell session store.
func NewShellStore() *ShellStore {
	return &ShellStore{sessions: make(map[string]*shellSessionRecord)}
}

// Open registers a new session bound to a device.
func (s *ShellStore) Open(sessionID, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = &shellSessionRecord{deviceID: deviceID, lastUsed: time.Now()}
}

// Get returns the session record, if any.
func (s *ShellStore) Get(sessionID string) *shellSessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

// Close removes a session from the store.
func (s *ShellStore) Close(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// AppendOutput records a chunk of output arriving from the agent.
func (s *ShellStore) AppendOutput(sessionID, stream, data string, closed bool) {
	rec := s.Get(sessionID)
	if rec == nil {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if data != "" {
		rec.chunks = append(rec.chunks, shellChunk{Stream: stream, Data: data})
	}
	if closed {
		rec.closed = true
	}
	rec.lastUsed = time.Now()
}

// ReadSince returns chunks recorded after offset, the new offset, and whether
// the session's shell process has exited.
func (r *shellSessionRecord) ReadSince(offset int) ([]shellChunk, int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if offset >= len(r.chunks) {
		return nil, len(r.chunks), r.closed
	}
	return r.chunks[offset:], len(r.chunks), r.closed
}
