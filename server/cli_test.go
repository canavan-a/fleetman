package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunCLI(t *testing.T) {
	// Create a test admin server
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/info" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"addr":      ":8080",
				"admin_addr": "127.0.0.1:3333",
				"version":   "v1.0.0",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer adminServer.Close()

	// Test info command
	args := []string{"info"}
	result := runCLI(adminServer.URL[7:], args) // Remove "http://"
	
	if !result {
		t.Fatal("Expected runCLI to handle 'info' command")
	}
}

func TestRunCLIUnknownCommand(t *testing.T) {
	// Skip because runCLI calls os.Exit on unknown commands
	t.Skip("Skipping unknown command test due to os.Exit")
}

func TestRunCLIEmptyArgs(t *testing.T) {
	args := []string{}
	result := runCLI("127.0.0.1:3333", args)
	
	if result {
		t.Fatal("Expected runCLI to return false for empty args")
	}
}

func TestRunCLIKeysList(t *testing.T) {
	// Create a test admin server
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/master-keys" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"master_keys": []MasterKeyInfo{
					{ID: "id-1", Name: "test-key", CreatedAt: mustParseTime(time.RFC3339, "2024-01-01T00:00:00Z")},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer adminServer.Close()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	args := []string{"keys", "list"}
	result := runCLI(adminServer.URL[7:], args)
	
	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Fatal("Expected runCLI to handle 'keys list' command")
	}

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "test-key") {
		t.Fatalf("Expected output to contain 'test-key', got: %s", output)
	}
}

func TestRunCLIKeysAdd(t *testing.T) {
	// Create a test admin server
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/master-keys" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "id-1",
				"name":      "my-key",
				"key":       "secret-key-123",
				"created_at": "2024-01-01T00:00:00Z",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer adminServer.Close()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	args := []string{"keys", "add", "my-key"}
	result := runCLI(adminServer.URL[7:], args)
	
	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Fatal("Expected runCLI to handle 'keys add' command")
	}

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "my-key") {
		t.Fatalf("Expected output to contain 'my-key', got: %s", output)
	}
	if !strings.Contains(output, "secret-key-123") {
		t.Fatalf("Expected output to contain key, got: %s", output)
	}
}

func TestRunCLIKeysRevoke(t *testing.T) {
	// Create a test admin server
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/master-keys/id-1" && r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "deleted",
				"id":     "id-1",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer adminServer.Close()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	args := []string{"keys", "revoke", "id-1"}
	result := runCLI(adminServer.URL[7:], args)
	
	w.Close()
	os.Stdout = oldStdout

	if !result {
		t.Fatal("Expected runCLI to handle 'keys revoke' command")
	}

	// Read output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "id-1") {
		t.Fatalf("Expected output to contain 'id-1', got: %s", output)
	}
}

func TestRunCLIVersion(t *testing.T) {
	// Skip because runCLI calls os.Exit on version command
	t.Skip("Skipping version test due to os.Exit")
}

func TestRunCLIHelp(t *testing.T) {
	// Skip because runCLI calls os.Exit on help command
	t.Skip("Skipping help test due to os.Exit")
}

func TestRunCLIKeysAddMissingName(t *testing.T) {
	t.Skip("Skipping due to os.Exit")
}

func TestRunCLIKeysRevokeMissingID(t *testing.T) {
	t.Skip("Skipping due to os.Exit")
}

func TestRunCLIKeysInvalidSubcommand(t *testing.T) {
	t.Skip("Skipping due to os.Exit")
}

func TestAdminRequestSuccess(t *testing.T) {
	// Create a test server
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer adminServer.Close()

	body := adminRequest("GET", adminServer.URL+"/test", nil)

	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Fatalf("Expected status 'ok', got %s", resp["status"])
	}
}

func TestAdminRequestPost(t *testing.T) {
	// Create a test server that echoes the request body
	var receivedBody []byte
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		receivedBody = buf.Bytes()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer adminServer.Close()

	payload := []byte(`{"key": "value"}`)
	body := adminRequest("POST", adminServer.URL+"/test", payload)

	if string(body) != "success" {
		t.Fatalf("Expected 'success', got %s", string(body))
	}

	if !bytes.Equal(receivedBody, payload) {
		t.Fatalf("Expected body %s, got %s", string(payload), string(receivedBody))
	}
}

func TestAdminRequestError(t *testing.T) {
	// This test would verify error handling, but adminRequest calls os.Exit
	// on errors, making it hard to test directly
	t.Skip("Skipping error test due to os.Exit")
}

func TestFatalf(t *testing.T) {
	// fatalf calls os.Exit, so we can't test it directly
	t.Skip("Skipping fatalf test due to os.Exit")
}

func TestCLIUsage(t *testing.T) {
	// Verify that cliUsage constant is not empty
	if cliUsage == "" {
		t.Fatal("cliUsage should not be empty")
	}

	// Should contain key commands
	keyCommands := []string{"info", "keys", "upgrade", "version"}
	for _, cmd := range keyCommands {
		if !strings.Contains(cliUsage, cmd) {
			t.Fatalf("cliUsage should contain '%s'", cmd)
		}
	}
}

func TestRunCLIUpgrade(t *testing.T) {
	// Upgrade command doesn't use the admin server, it runs directly
	// This test would require mocking system calls
	t.Skip("Skipping upgrade test")
}

// Helper function to parse time
func mustParseTime(layout, value string) time.Time {
	t, err := time.Parse(layout, value)
	if err != nil {
		panic(err)
	}
	return t
}
