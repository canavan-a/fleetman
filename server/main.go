// Package main is the entry point for the fleet-manager server (hub).
// It boots the HTTP control plane + WebSocket data plane on a single port.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	masterKeys := flag.String("master-keys", "", "comma-separated master API keys (or FLEET_MASTER_KEYS env)")
	dbPath := flag.String("db", "fleetman.db", "path to SQLite database file (created if missing)")
	flag.Parse()

	// Master keys from flag or env.
	keys := *masterKeys
	if keys == "" {
		keys = os.Getenv("FLEET_MASTER_KEYS")
	}
	if keys == "" {
		log.Fatal("FATAL: no master API keys configured. Set --master-keys or FLEET_MASTER_KEYS")
	}
	keySet := make(map[string]struct{})
	for _, k := range strings.Split(keys, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keySet[k] = struct{}{}
		}
	}

	// Open (or create) the database.
	db, err := OpenDB(*dbPath)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	defer db.Close()

	registry := NewRegistry(db)
	cmdStore := NewCommandStore(db)
	hub := &Hub{
		Registry: registry,
		Commands: cmdStore,
	}

	mux := http.NewServeMux()

	// Health check — no auth required.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	// WebSocket endpoint — device auth happens inside the handler.
	mux.HandleFunc("GET /ws", hub.HandleWebSocket)

	// Master API endpoints — all require master key auth.
	auth := MasterAuth(keySet)

	mux.Handle("POST /tokens", auth(http.HandlerFunc(hub.HandleMintToken)))
	mux.Handle("GET /devices", auth(http.HandlerFunc(hub.HandleListDevices)))
	mux.Handle("GET /devices/{id}", auth(http.HandlerFunc(hub.HandleGetDevice)))
	mux.Handle("DELETE /devices/{id}", auth(http.HandlerFunc(hub.HandleDeleteDevice)))
	mux.Handle("POST /devices/{id}/tags", auth(http.HandlerFunc(hub.HandleAddDeviceTags)))
	mux.Handle("DELETE /devices/{id}/tags/{tag}", auth(http.HandlerFunc(hub.HandleRemoveDeviceTag)))
	mux.Handle("POST /commands", auth(http.HandlerFunc(hub.HandlePostCommand)))
	mux.Handle("GET /commands/{id}", auth(http.HandlerFunc(hub.HandleGetCommand)))
	mux.Handle("GET /releases/{arch}", auth(http.HandlerFunc(hub.HandleGetRelease)))
	mux.Handle("GET /tags", auth(http.HandlerFunc(hub.HandleListTags)))
	mux.Handle("POST /tags", auth(http.HandlerFunc(hub.HandleCreateTag)))
	mux.Handle("DELETE /tags/{name}", auth(http.HandlerFunc(hub.HandleDeleteTag)))
	mux.Handle("GET /tags/{name}/devices", auth(http.HandlerFunc(hub.HandleGetTagDevices)))
	mux.Handle("POST /tags/{name}/devices", auth(http.HandlerFunc(hub.HandleBulkTag)))
	mux.Handle("DELETE /tags/{name}/devices", auth(http.HandlerFunc(hub.HandleBulkUntag)))

	log.Printf("fleet-manager server listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
}
