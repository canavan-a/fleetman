// Package api is a thin HTTP client for the fleetman server's control-plane API.
// Used by the master CLI/TUI.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the fleetman server's HTTP API using a master key.
type Client struct {
	base         string
	key          string
	extraHeaders map[string]string
	http         *http.Client
}

// New creates a Client for the given server base URL and master API key.
// extraHeaders are static headers (e.g. for a Cloudflare Access-style WAF in
// front of the server) merged into every request; a nil map is fine.
func New(base, key string, extraHeaders map[string]string) *Client {
	return &Client{
		base:         base,
		key:          key,
		extraHeaders: extraHeaders,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
}

// Base returns the server's HTTP base URL, as configured.
func (c *Client) Base() string { return c.base }

// Device mirrors the server's DeviceInfo JSON.
type Device struct {
	DeviceID  string            `json:"device_id"`
	Status    string            `json:"status"`
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

// Online reports whether the device is currently connected.
func (d Device) Online() bool { return d.Status == "active" }

// TokenResponse is returned by MintToken.
type TokenResponse struct {
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
}

// DeviceResult is a single device's outcome within a CommandStatus.
type DeviceResult struct {
	DeviceID string  `json:"device_id"`
	Status   string  `json:"status"` // "pending", "ok", "error", "timeout"
	Stdout   string  `json:"stdout,omitempty"`
	Stderr   string  `json:"stderr,omitempty"`
	Retcode  int     `json:"retcode,omitempty"`
	Device   *Device `json:"device,omitempty"`
}

// CommandStatus is returned by GetCommand.
type CommandStatus struct {
	CommandID string                   `json:"command_id"`
	Action    string                   `json:"action"`
	CreatedAt time.Time                `json:"created_at"`
	TimeoutAt time.Time                `json:"timeout_at"`
	Summary   map[string]int           `json:"summary"`
	Results   map[string]*DeviceResult `json:"results"`
}

// Target describes which devices a command applies to.
type Target struct {
	All  bool     `json:"all,omitempty"`
	IDs  []string `json:"ids,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

// apiError is returned by the server on non-2xx responses.
type apiError struct {
	Error string `json:"error"`
}

func (c *Client) do(method, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.extraHeaders {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		var ae apiError
		if json.Unmarshal(data, &ae) == nil && ae.Error != "" {
			return fmt.Errorf("%s", ae.Error)
		}
		return fmt.Errorf("server returned %s", resp.Status)
	}

	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// ListTags returns all tag names.
func (c *Client) ListTags() ([]string, error) {
	var resp struct {
		Tags []string `json:"tags"`
	}
	if err := c.do(http.MethodGet, "/tags", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tags, nil
}

// CreateTag creates a new tag.
func (c *Client) CreateTag(name string) error {
	return c.do(http.MethodPost, "/tags", map[string]string{"name": name}, nil)
}

// DeleteTag deletes a tag.
func (c *Client) DeleteTag(name string) error {
	return c.do(http.MethodDelete, "/tags/"+name, nil, nil)
}

// ListDevices returns every known device.
func (c *Client) ListDevices() ([]Device, error) {
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(http.MethodGet, "/devices", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// DeleteDevice removes a device and revokes its token.
func (c *Client) DeleteDevice(id string) error {
	return c.do(http.MethodDelete, "/devices/"+id, nil, nil)
}

// TagDevices adds tag to the given devices (bulk).
func (c *Client) TagDevices(tag string, ids []string) error {
	return c.do(http.MethodPost, "/tags/"+tag+"/devices", map[string][]string{"device_ids": ids}, nil)
}

// UntagDevices removes tag from the given devices (bulk).
func (c *Client) UntagDevices(tag string, ids []string) error {
	return c.do(http.MethodDelete, "/tags/"+tag+"/devices", map[string][]string{"device_ids": ids}, nil)
}

// MintToken registers a new device and returns its id + token.
func (c *Client) MintToken() (TokenResponse, error) {
	var resp TokenResponse
	err := c.do(http.MethodPost, "/tokens", nil, &resp)
	return resp, err
}

// PostCommandRequest is the body for PostCommand.
type PostCommandRequest struct {
	Action  string                 `json:"action"`
	Target  Target                 `json:"target"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// PostCommandResponse is returned when a command is accepted.
type PostCommandResponse struct {
	CommandID string `json:"command_id"`
	Sent      int    `json:"sent"`
	Total     int    `json:"total"`
}

// PostCommand dispatches a command to target devices.
func (c *Client) PostCommand(req PostCommandRequest) (PostCommandResponse, error) {
	var resp PostCommandResponse
	err := c.do(http.MethodPost, "/commands", req, &resp)
	return resp, err
}

// GetCommand fetches the current status/results of a command.
func (c *Client) GetCommand(id string) (CommandStatus, error) {
	var resp CommandStatus
	err := c.do(http.MethodGet, "/commands/"+id, nil, &resp)
	return resp, err
}

// ShellChunk is one piece of streamed output from a shell session.
type ShellChunk struct {
	Stream string `json:"stream"`
	Data   string `json:"data"`
}

// ShellOutput is the response from PollShellOutput.
type ShellOutput struct {
	Chunks []ShellChunk `json:"chunks"`
	Offset int          `json:"offset"`
	Closed bool         `json:"closed"`
}

// OpenShell starts a persistent shell session on the given device and
// returns its session ID.
func (c *Client) OpenShell(deviceID string) (string, error) {
	var resp struct {
		SessionID string `json:"session_id"`
	}
	err := c.do(http.MethodPost, "/shell", map[string]string{"device_id": deviceID}, &resp)
	return resp.SessionID, err
}

// SendShellInput writes a chunk of stdin to an open shell session.
func (c *Client) SendShellInput(sessionID, data string) error {
	return c.do(http.MethodPost, "/shell/"+sessionID+"/input", map[string]string{"data": data}, nil)
}

// PollShellOutput fetches output chunks recorded after offset "since".
func (c *Client) PollShellOutput(sessionID string, since int) (ShellOutput, error) {
	var resp ShellOutput
	err := c.do(http.MethodGet, fmt.Sprintf("/shell/%s/output?since=%d", sessionID, since), nil, &resp)
	return resp, err
}

// CloseShell terminates a shell session.
func (c *Client) CloseShell(sessionID string) error {
	return c.do(http.MethodDelete, "/shell/"+sessionID, nil, nil)
}
