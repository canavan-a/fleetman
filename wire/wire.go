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

// ShellOpen is sent from server → agent to start a persistent shell session.
type ShellOpen struct {
	SessionID string `json:"session_id"`
}

// ShellInput is sent from server → agent, carrying a chunk of stdin to write
// into an already-open shell session.
type ShellInput struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

// ShellOutput is sent from agent → server, carrying a chunk of stdout/stderr
// read from an open shell session as it arrives. Closed is set once the
// session's shell process has exited — Data may still hold trailing output.
type ShellOutput struct {
	SessionID string `json:"session_id"`
	Stream    string `json:"stream"` // "stdout" or "stderr"
	Data      string `json:"data"`
	Closed    bool   `json:"closed,omitempty"`
}

// ShellClose is sent from server → agent to terminate a shell session.
type ShellClose struct {
	SessionID string `json:"session_id"`
}

// Envelope wraps every WebSocket message with a type discriminator.
type Envelope struct {
	Type        string       `json:"type"` // "heartbeat", "command", "result", "shell_open", "shell_input", "shell_output", "shell_close"
	Heartbeat   *Heartbeat   `json:"heartbeat,omitempty"`
	Command     *Command     `json:"command,omitempty"`
	Result      *Result      `json:"result,omitempty"`
	ShellOpen   *ShellOpen   `json:"shell_open,omitempty"`
	ShellInput  *ShellInput  `json:"shell_input,omitempty"`
	ShellOutput *ShellOutput `json:"shell_output,omitempty"`
	ShellClose  *ShellClose  `json:"shell_close,omitempty"`
}

// Action constants.
const (
	ActionRunCommand     = "run_command"
	ActionRestartService = "restart_service"
	ActionUpgrade        = "upgrade"
	ActionFetchFile      = "fetch_file"
	ActionListFiles      = "list_files"
)

// Envelope type constants.
const (
	TypeHeartbeat   = "heartbeat"
	TypeCommand     = "command"
	TypeResult      = "result"
	TypeShellOpen   = "shell_open"
	TypeShellInput  = "shell_input"
	TypeShellOutput = "shell_output"
	TypeShellClose  = "shell_close"
)
