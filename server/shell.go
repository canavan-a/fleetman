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
	mu          sync.Mutex
	deviceID    string
	chunks      []shellChunk
	closed      bool
	lastUsed    time.Time
	idleTimeout time.Duration // per-session override; 0 means use the store default
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

// Touch records activity on a session (e.g. stdin received), resetting its
// idle clock. No-op if the session doesn't exist.
func (s *ShellStore) Touch(sessionID string) {
	rec := s.Get(sessionID)
	if rec == nil {
		return
	}
	rec.mu.Lock()
	rec.lastUsed = time.Now()
	rec.mu.Unlock()
}

// SetIdleTimeout overrides the idle timeout used for one session, for
// testing expiry behavior without waiting out the default. Returns false if
// the session doesn't exist.
func (s *ShellStore) SetIdleTimeout(sessionID string, d time.Duration) bool {
	rec := s.Get(sessionID)
	if rec == nil {
		return false
	}
	rec.mu.Lock()
	rec.idleTimeout = d
	rec.mu.Unlock()
	return true
}

// expiredShell identifies a session whose agent-side process needs to be
// told to terminate because nothing (input or output) has touched it in a
// while — most commonly a master that crashed or lost its connection
// without ever sending an explicit close.
type expiredShell struct {
	sessionID string
	deviceID  string
}

// ReapIdle scans all sessions and:
//   - returns sessions that are still running but have been idle (no
//     stdin/stdout activity) longer than idleTimeout, so the caller can tell
//     their agent to terminate the process;
//   - prunes sessions that have already closed (process exited) and have
//     sat unread longer than closedGrace, so the store doesn't grow
//     unbounded when a master never polls the final "closed" chunk.
func (s *ShellStore) ReapIdle(idleTimeout, closedGrace time.Duration) []expiredShell {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var expired []expiredShell
	for id, rec := range s.sessions {
		rec.mu.Lock()
		closed := rec.closed
		idle := now.Sub(rec.lastUsed)
		deviceID := rec.deviceID
		timeout := idleTimeout
		if rec.idleTimeout > 0 {
			timeout = rec.idleTimeout
		}
		rec.mu.Unlock()

		switch {
		case closed && idle >= closedGrace:
			delete(s.sessions, id)
		case !closed && idle >= timeout:
			expired = append(expired, expiredShell{sessionID: id, deviceID: deviceID})
		}
	}
	return expired
}
