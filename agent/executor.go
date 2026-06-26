package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/canavan-a/fleetman/wire"
)

// execRunCommand runs an arbitrary command (argv from payload) and captures output.
func (a *Agent) execRunCommand(cmd *wire.Command) wire.Result {
	result := wire.Result{
		CommandID: cmd.CommandID,
		DeviceID:  a.cfg.DeviceID,
	}

	// Extract argv from payload.
	argvRaw, ok := cmd.Payload["argv"]
	if !ok {
		result.Stderr = "missing 'argv' in payload"
		result.Retcode = 1
		return result
	}

	argv, err := toStringSlice(argvRaw)
	if err != nil {
		result.Stderr = fmt.Sprintf("invalid argv: %v", err)
		result.Retcode = 1
		return result
	}
	if len(argv) == 0 {
		result.Stderr = "argv is empty"
		result.Retcode = 1
		return result
	}

	var stdout, stderr bytes.Buffer
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Retcode = exitErr.ExitCode()
		} else {
			result.Stderr = err.Error()
			result.Retcode = 1
		}
	}

	return result
}

// execRestartService restarts a named service using the host's init system.
func (a *Agent) execRestartService(cmd *wire.Command) wire.Result {
	result := wire.Result{
		CommandID: cmd.CommandID,
		DeviceID:  a.cfg.DeviceID,
	}

	// Extract service name from payload.
	serviceRaw, ok := cmd.Payload["service"]
	if !ok {
		result.Stderr = "missing 'service' in payload"
		result.Retcode = 1
		return result
	}
	service, ok := serviceRaw.(string)
	if !ok || service == "" {
		result.Stderr = "invalid service name"
		result.Retcode = 1
		return result
	}

	// Build the restart command based on init type.
	var argv []string
	switch a.host.Init {
	case "systemd":
		argv = []string{"systemctl", "restart", service}
	case "openrc":
		argv = []string{"rc-service", service, "restart"}
	case "procd":
		// OpenWrt uses /etc/init.d/<service> restart
		argv = []string{"/etc/init.d/" + service, "restart"}
	case "runit":
		argv = []string{"sv", "restart", service}
	case "s6":
		// s6 service dirs are typically under /run/service/ or /etc/s6/services/
		argv = []string{"s6-svc", "-r", "/run/service/" + service}
	case "busybox":
		// BusyBox systems typically have /etc/init.d/ scripts.
		argv = []string{"/etc/init.d/" + service, "restart"}
	default: // initd / sysvinit
		argv = []string{"service", service, "restart"}
	}

	var stdout, stderr bytes.Buffer
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Retcode = exitErr.ExitCode()
		} else {
			result.Stderr = err.Error()
			result.Retcode = 1
		}
	}

	return result
}

// toStringSlice converts an interface{} (expected []interface{} from JSON) to []string.
func toStringSlice(v interface{}) ([]string, error) {
	switch val := v.(type) {
	case []interface{}:
		out := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("argv[%d] is not a string", i)
			}
			out[i] = s
		}
		return out, nil
	case []string:
		return val, nil
	case string:
		// Allow a single string, split by spaces for convenience.
		return strings.Fields(val), nil
	default:
		return nil, fmt.Errorf("argv must be an array of strings, got %T", v)
	}
}
