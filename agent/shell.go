package main

import (
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/canavan-a/fleetman/wire"
)

// shellSession holds a live bash process backing a persistent shell session.
type shellSession struct {
	id    string
	cmd   *exec.Cmd
	stdin io.WriteCloser

	closeOnce sync.Once
}

// openShell spawns a new bash process for the given session ID and starts
// streaming its stdout/stderr back to the server as shell_output envelopes.
func (a *Agent) openShell(open *wire.ShellOpen) {
	sessionID := open.SessionID

	cmd := exec.Command("bash")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.sendShellOutput(sessionID, "stderr", err.Error(), true)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.sendShellOutput(sessionID, "stderr", err.Error(), true)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.sendShellOutput(sessionID, "stderr", err.Error(), true)
		return
	}

	if err := cmd.Start(); err != nil {
		a.sendShellOutput(sessionID, "stderr", err.Error(), true)
		return
	}

	sess := &shellSession{id: sessionID, cmd: cmd, stdin: stdin}

	a.shellsMu.Lock()
	a.shells[sessionID] = sess
	a.shellsMu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go a.pumpShellOutput(sess, stdout, "stdout", &wg)
	go a.pumpShellOutput(sess, stderr, "stderr", &wg)

	go func() {
		wg.Wait()
		cmd.Wait()
		stdin.Close()
		a.shellsMu.Lock()
		delete(a.shells, sessionID)
		a.shellsMu.Unlock()
		a.sendShellOutput(sessionID, "stdout", "", true)
	}()
}

// pumpShellOutput reads chunks from r and streams them back as shell_output
// envelopes until the pipe is closed (process exited).
func (a *Agent) pumpShellOutput(sess *shellSession, r io.Reader, stream string, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			a.sendShellOutput(sess.id, stream, string(buf[:n]), false)
		}
		if err != nil {
			return
		}
	}
}

// writeShellInput writes a chunk of data to an open session's stdin.
func (a *Agent) writeShellInput(in *wire.ShellInput) {
	a.shellsMu.Lock()
	sess := a.shells[in.SessionID]
	a.shellsMu.Unlock()

	if sess == nil {
		return
	}
	if _, err := sess.stdin.Write([]byte(in.Data)); err != nil {
		log.Printf("shell %s: write stdin: %v", in.SessionID, err)
	}
}

// closeShell terminates a session's process and cleans up.
func (a *Agent) closeShell(req *wire.ShellClose) {
	a.shellsMu.Lock()
	sess := a.shells[req.SessionID]
	delete(a.shells, req.SessionID)
	a.shellsMu.Unlock()

	if sess == nil {
		return
	}
	sess.closeOnce.Do(func() {
		sess.stdin.Close()
		if sess.cmd.Process != nil {
			sess.cmd.Process.Kill()
		}
	})
}

// sendShellOutput sends a shell_output envelope back to the server.
func (a *Agent) sendShellOutput(sessionID, stream, data string, closed bool) {
	env := wire.Envelope{
		Type: wire.TypeShellOutput,
		ShellOutput: &wire.ShellOutput{
			SessionID: sessionID,
			Stream:    stream,
			Data:      data,
			Closed:    closed,
		},
	}
	if err := a.sendEnvelope(env); err != nil {
		log.Printf("failed to send shell output for %s: %v", sessionID, err)
	}
}
