// Package main is the entry point for the fleet-manager server (hub).
// It boots the HTTP control plane + WebSocket data plane on a single port,
// and a localhost-only admin server for managing master API keys.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/canavan-a/fleetman/internal/banner"
)

func main() {
	addr        := flag.String("addr", ":8080", "public listen address")
	adminAddr   := flag.String("admin-addr", "127.0.0.1:3333", "admin listen address (localhost only); set empty to disable")
	dbPath      := flag.String("db", "fleetman.db", "path to SQLite database file (created if missing)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() { fmt.Print(cliUsage) }
	flag.Parse()

	if len(os.Args) == 1 {
		banner.Print(Version)
		fmt.Print(cliUsage)
		return
	}

	if *showVersion {
		fmt.Printf("fleetman-server %s\n", Version)
		return
	}

	if runCLI(*adminAddr, flag.Args()) {
		return
	}

	db, err := OpenDB(*dbPath)
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	defer db.Close()

	registry := NewRegistry(db)
	cmdStore  := NewCommandStore(db)
	shellStore := NewShellStore()
	hub := &Hub{
		Registry: registry,
		Commands: cmdStore,
		Shells:   shellStore,
	}

	// --- Public API ---
	mux  := http.NewServeMux()
	auth := MasterAuth(db)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /{$}", HandleHome())

	mux.HandleFunc("GET /ws", hub.HandleWebSocket)

	mux.Handle("POST /tokens",                    auth(http.HandlerFunc(hub.HandleMintToken)))
	mux.Handle("GET /devices",                    auth(http.HandlerFunc(hub.HandleListDevices)))
	mux.Handle("GET /devices/{id}",               auth(http.HandlerFunc(hub.HandleGetDevice)))
	mux.Handle("DELETE /devices/{id}",            auth(http.HandlerFunc(hub.HandleDeleteDevice)))
	mux.Handle("POST /devices/{id}/tags",         auth(http.HandlerFunc(hub.HandleAddDeviceTags)))
	mux.Handle("DELETE /devices/{id}/tags/{tag}", auth(http.HandlerFunc(hub.HandleRemoveDeviceTag)))
	mux.Handle("POST /commands",                  auth(http.HandlerFunc(hub.HandlePostCommand)))
	mux.Handle("GET /commands/{id}",              auth(http.HandlerFunc(hub.HandleGetCommand)))
	mux.Handle("GET /releases/{arch}",            auth(http.HandlerFunc(hub.HandleGetRelease)))
	mux.Handle("GET /tags",                       auth(http.HandlerFunc(hub.HandleListTags)))
	mux.Handle("POST /tags",                      auth(http.HandlerFunc(hub.HandleCreateTag)))
	mux.Handle("DELETE /tags/{name}",             auth(http.HandlerFunc(hub.HandleDeleteTag)))
	mux.Handle("GET /tags/{name}/devices",        auth(http.HandlerFunc(hub.HandleGetTagDevices)))
	mux.Handle("POST /tags/{name}/devices",       auth(http.HandlerFunc(hub.HandleBulkTag)))
	mux.Handle("DELETE /tags/{name}/devices",     auth(http.HandlerFunc(hub.HandleBulkUntag)))
	mux.Handle("POST /shell",                     auth(http.HandlerFunc(hub.HandleOpenShell)))
	mux.Handle("POST /shell/{id}/input",          auth(http.HandlerFunc(hub.HandleShellInput)))
	mux.Handle("GET /shell/{id}/output",          auth(http.HandlerFunc(hub.HandleShellOutput)))
	mux.Handle("DELETE /shell/{id}",              auth(http.HandlerFunc(hub.HandleCloseShell)))

	// --- Admin server (localhost only, no auth) ---
	if *adminAddr != "" {
		adminMux := http.NewServeMux()
		admin := &AdminHub{db: db, Addr: *addr, AdminAddr: *adminAddr}
		admin.RegisterAdminRoutes(adminMux)

		go func() {
			log.Printf("fleet-manager admin listening on %s", *adminAddr)
			if err := http.ListenAndServe(*adminAddr, adminMux); err != nil {
				log.Fatalf("FATAL: admin server: %v", err)
			}
		}()
	}

	banner.Print(Version)
	log.Printf("fleet-manager server listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("FATAL: %v", err)
	}
}
