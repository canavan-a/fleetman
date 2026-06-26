// Package wire defines the message types shared by server, agent, and master.
// All three import this package directly — no codegen.
package wire

// Heartbeat is sent periodically from agent → server.
type Heartbeat struct {
	DeviceID string            `json:"device_id"`
	Version  string            `json:"version"`
	Arch     string            `json:"arch"`
	Init     string            `json:"init"`
	OS       string            `json:"os"`
	Uptime   int64             `json:"uptime"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// Command is sent from server → agent.
type Command struct {
	CommandID string                 `json:"command_id"`
	Action    string                 `json:"action"`
	Target    string                 `json:"target"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// Result is sent from agent → server after executing a command.
type Result struct {
	CommandID string `json:"command_id"`
	DeviceID  string `json:"device_id"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Retcode   int    `json:"retcode"`
}

// Envelope wraps every WebSocket message with a type discriminator.
type Envelope struct {
	Type      string      `json:"type"`               // "heartbeat", "command", "result"
	Heartbeat *Heartbeat  `json:"heartbeat,omitempty"`
	Command   *Command    `json:"command,omitempty"`
	Result    *Result     `json:"result,omitempty"`
}

// Action constants.
const (
	ActionRunCommand     = "run_command"
	ActionRestartService = "restart_service"
	ActionUpgrade        = "upgrade"
)

// Envelope type constants.
const (
	TypeHeartbeat = "heartbeat"
	TypeCommand   = "command"
	TypeResult    = "result"
)
