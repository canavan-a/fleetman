package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/canavan-a/fleetman/internal/banner"
)

const cliUsage = `Usage: fleetman-server [flags] [command]

Service flags (daemon mode — no command given):
  --addr <addr>        Public listen address (default: :8080)
  --admin-addr <addr>  Admin API address (default: 127.0.0.1:3333)
  --db <path>          SQLite database path (default: fleetman.db)
  --version            Print version and exit

Commands (CLI mode — run on the server directly or via SSH):
  info                 Show server addresses and version
  keys list            List all master API keys
  keys add <name>      Create a new master API key (key shown once)
  keys revoke <id>     Revoke a master API key by ID
  upgrade              Upgrade the server binary to the latest release
  upgrade --version v1.2.3  Upgrade to a specific version

The keys commands talk to the admin port (default 127.0.0.1:3333, localhost only).
If you changed --admin-addr at install time, pass it explicitly:
  fleetman-server --admin-addr 127.0.0.1:4444 keys list
`

// runCLI dispatches CLI subcommands against the admin API.
// Returns true if a CLI command was handled (caller should not start the service).
func runCLI(adminAddr string, args []string) bool {
	if len(args) == 0 {
		return false
	}

	base := "http://" + adminAddr

	switch args[0] {
	case "info":
		cliInfo(base)
		return true

	case "upgrade":
		runUpgrade(args[1:])
		return true

	case "version", "--version", "-v":
		fmt.Printf("fleetman-server %s\n", Version)
		os.Exit(0)
		return true

	case "keys":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: fleetman-server keys <list|add|revoke>\n")
			os.Exit(1)
		}
		switch args[1] {
		case "list":
			cliKeysList(base)
		case "add":
			if len(args) < 3 {
				fmt.Fprintf(os.Stderr, "Usage: fleetman-server keys add <name>\n")
				os.Exit(1)
			}
			cliKeysAdd(base, strings.Join(args[2:], " "))
		case "revoke":
			if len(args) < 3 {
				fmt.Fprintf(os.Stderr, "Usage: fleetman-server keys revoke <id>\n")
				os.Exit(1)
			}
			cliKeysRevoke(base, args[2])
		default:
			fmt.Fprintf(os.Stderr, "Unknown keys subcommand: %s\n\n%s", args[1], cliUsage)
			os.Exit(1)
		}
		return true

	case "help", "--help", "-h":
		banner.Print(Version)
		fmt.Print(cliUsage)
		os.Exit(0)
		return true

	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown command %q\n\n", args[0])
		banner.Print(Version)
		fmt.Print(cliUsage)
		os.Exit(1)
		return false
	}
}

func cliInfo(base string) {
	body := adminRequest("GET", base+"/admin/info", nil)

	var resp struct {
		Addr      string `json:"addr"`
		AdminAddr string `json:"admin_addr"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		fatalf("failed to parse response: %v", err)
	}

	fmt.Printf("Version:     %s\n", resp.Version)
	fmt.Printf("Server addr: %s\n", resp.Addr)
	fmt.Printf("Admin addr:  %s\n", resp.AdminAddr)
}

func cliKeysList(base string) {
	body := adminRequest("GET", base+"/admin/master-keys", nil)

	var resp struct {
		MasterKeys []MasterKeyInfo `json:"master_keys"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		fatalf("failed to parse response: %v", err)
	}

	if len(resp.MasterKeys) == 0 {
		fmt.Println("No master keys. Add one with: fleetman-server keys add <name>")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tCREATED")
	for _, k := range resp.MasterKeys {
		fmt.Fprintf(w, "%s\t%s\t%s\n", k.ID, k.Name, k.CreatedAt.Format(time.RFC3339))
	}
	w.Flush()
}

func cliKeysAdd(base, name string) {
	payload, _ := json.Marshal(map[string]string{"name": name})
	body := adminRequest("POST", base+"/admin/master-keys", payload)

	var resp struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Key       string    `json:"key"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		fatalf("failed to parse response: %v", err)
	}

	fmt.Printf("Created master key %q\n\n", resp.Name)
	fmt.Printf("  ID:  %s\n", resp.ID)
	fmt.Printf("  Key: %s\n\n", resp.Key)
	fmt.Println("Save this key — it will not be shown again.")
}

func cliKeysRevoke(base, id string) {
	adminRequest("DELETE", base+"/admin/master-keys/"+url.PathEscape(id), nil)
	fmt.Printf("Revoked key %s\n", id)
}

// adminRequest makes a request to the admin API and returns the response body.
// Exits with a helpful message on connection failure or non-2xx status.
func adminRequest(method, rawURL string, body []byte) []byte {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, rawURL, reqBody)
	if err != nil {
		fatalf("failed to build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Parse the admin addr out of the URL for the hint
		u, _ := url.Parse(rawURL)
		addr := u.Host
		fmt.Fprintf(os.Stderr, "ERROR: could not reach admin API at %s\n\n", addr)
		fmt.Fprintf(os.Stderr, "  Is fleetman-server running?\n")
		fmt.Fprintf(os.Stderr, "  If you changed the admin port at install time, pass it explicitly:\n")
		fmt.Fprintf(os.Stderr, "    fleetman-server --admin-addr <addr> %s\n\n", strings.Join(os.Args[1:], " "))
		fmt.Fprintf(os.Stderr, "  Check the current admin address:\n")
		fmt.Fprintf(os.Stderr, "    systemctl cat fleetman-server | grep admin-addr\n")
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(respBody))
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		fmt.Fprintf(os.Stderr, "ERROR: %s (HTTP %d)\n", msg, resp.StatusCode)
		os.Exit(1)
	}

	return respBody
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
