package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/canavan-a/fleetman/wire"
	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval = 5 * time.Second
	baseReconnDelay   = 1 * time.Second
	maxReconnDelay    = 60 * time.Second
	jitterFraction    = 0.20 // ±20%
)

// Agent holds runtime state for the agent process.
type Agent struct {
	cfg     *Config
	host    HostInfo
	version string

	conn   *websocket.Conn
	connMu sync.Mutex // protects writes to conn

	done chan struct{} // signals shutdown
}

// NewAgent creates a new agent with the given config and version string.
func NewAgent(cfg *Config, version string) *Agent {
	return &Agent{
		cfg:     cfg,
		host:    DetectHost(),
		version: version,
		done:    make(chan struct{}),
	}
}

// Run is the main loop: connect, send heartbeats, handle commands, reconnect on failure.
func (a *Agent) Run() {
	for {
		err := a.connectAndServe()
		select {
		case <-a.done:
			return
		default:
		}
		if err != nil {
			log.Printf("connection lost: %v", err)
		}
		a.reconnectWait()
	}
}

// connectAndServe dials the server, starts the heartbeat, and reads commands.
func (a *Agent) connectAndServe() error {
	if err := a.dial(); err != nil {
		return err
	}
	defer func() {
		a.connMu.Lock()
		if a.conn != nil {
			a.conn.Close()
			a.conn = nil
		}
		a.connMu.Unlock()
	}()

	// Start heartbeat goroutine.
	hbDone := make(chan struct{})
	go a.heartbeatLoop(hbDone)
	defer close(hbDone)

	// Read loop — receive commands from the server.
	for {
		_, msg, err := a.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var env wire.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			log.Printf("bad message from server: %v", err)
			continue
		}

		if env.Type == wire.TypeCommand && env.Command != nil {
			go a.handleCommand(env.Command)
		}
	}
}

// dial connects to the server WebSocket with the device token.
func (a *Agent) dial() error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.cfg.Token)
	for k, v := range a.cfg.ExtraHeaders {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		header.Set(k, v)
	}

	url := normalizeServerURL(a.cfg.Server) + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		return fmt.Errorf("dial %s: %w", url, err)
	}

	a.connMu.Lock()
	a.conn = conn
	a.connMu.Unlock()

	log.Printf("connected to %s as %s", a.cfg.Server, a.cfg.DeviceID)
	// Reset backoff state on successful connect.
	reconnAttempt = 0
	return nil
}

// sendEnvelope sends a wire.Envelope over the WebSocket (thread-safe).
func (a *Agent) sendEnvelope(env wire.Envelope) error {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.conn == nil {
		return fmt.Errorf("not connected")
	}
	return a.conn.WriteJSON(env)
}

// heartbeatLoop sends periodic heartbeats until done is closed.
func (a *Agent) heartbeatLoop(done chan struct{}) {
	// Send one immediately on connect.
	a.sendHeartbeat()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.sendHeartbeat()
		case <-done:
			return
		case <-a.done:
			return
		}
	}
}

// sendHeartbeat sends a single heartbeat to the server.
func (a *Agent) sendHeartbeat() {
	// Refresh uptime each time.
	a.host.Uptime = detectUptime()

	hb := wire.Heartbeat{
		DeviceID: a.cfg.DeviceID,
		Version:  a.version,
		Arch:     a.host.Arch,
		Init:     a.host.Init,
		OS:       a.host.OS,
		Uptime:   a.host.Uptime,
		Labels:   a.cfg.Labels,
	}

	env := wire.Envelope{
		Type:      wire.TypeHeartbeat,
		Heartbeat: &hb,
	}

	if err := a.sendEnvelope(env); err != nil {
		log.Printf("heartbeat send failed: %v", err)
	}
}

// --- Reconnect with exponential backoff + jitter ---

var reconnAttempt int

// reconnectWait waits with exponential backoff capped at 60s with ±20% jitter.
func (a *Agent) reconnectWait() {
	delay := backoffDelay(reconnAttempt)
	reconnAttempt++

	log.Printf("reconnecting in %s (attempt %d)", delay.Round(time.Millisecond), reconnAttempt)

	select {
	case <-time.After(delay):
	case <-a.done:
	}
}

// backoffDelay calculates: min(base * 2^attempt, 60s) ± 20% jitter.
func backoffDelay(attempt int) time.Duration {
	delay := float64(baseReconnDelay) * math.Pow(2, float64(attempt))
	if delay > float64(maxReconnDelay) {
		delay = float64(maxReconnDelay)
	}
	// Apply ±20% jitter.
	jitter := delay * jitterFraction * (2*rand.Float64() - 1) // range: -20% to +20%
	delay += jitter
	if delay < 0 {
		delay = float64(baseReconnDelay)
	}
	return time.Duration(delay)
}

// handleCommand dispatches a command by action.
func (a *Agent) handleCommand(cmd *wire.Command) {
	log.Printf("received command %s: %s", cmd.CommandID, cmd.Action)

	var result wire.Result
	result.CommandID = cmd.CommandID
	result.DeviceID = a.cfg.DeviceID

	switch cmd.Action {
	case wire.ActionRunCommand:
		result = a.execRunCommand(cmd)
	case wire.ActionRestartService:
		result = a.execRestartService(cmd)
	case wire.ActionUpgrade:
		a.execUpgrade(cmd)
		// Upgrade either exits the process or sends its own result on failure.
		return
	default:
		result.Stderr = fmt.Sprintf("unknown action: %s", cmd.Action)
		result.Retcode = 1
	}

	env := wire.Envelope{
		Type:   wire.TypeResult,
		Result: &result,
	}
	if err := a.sendEnvelope(env); err != nil {
		log.Printf("failed to send result for %s: %v", cmd.CommandID, err)
	}
}

// normalizeServerURL ensures the server address has a ws:// or wss:// scheme.
// Accepts: "host:port", "http://host:port", "https://host:port", "ws://...", "wss://...".
// A bare host (no scheme) defaults to secure (wss://) — pass an explicit
// ws:// or http:// prefix to opt into a plaintext connection.
func normalizeServerURL(addr string) string {
	switch {
	case strings.HasPrefix(addr, "ws://") || strings.HasPrefix(addr, "wss://"):
		return strings.TrimRight(addr, "/")
	case strings.HasPrefix(addr, "https://"):
		return "wss://" + strings.TrimRight(addr[len("https://"):], "/")
	case strings.HasPrefix(addr, "http://"):
		return "ws://" + strings.TrimRight(addr[len("http://"):], "/")
	default:
		return "wss://" + strings.TrimRight(addr, "/")
	}
}
