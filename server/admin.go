package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// AdminHub exposes the admin-only HTTP handlers (local port, no auth).
type AdminHub struct {
	db *DB
}

// RegisterAdminRoutes wires admin endpoints onto mux.
func (a *AdminHub) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/master-keys", a.HandleListMasterKeys)
	mux.HandleFunc("POST /admin/master-keys", a.HandleCreateMasterKey)
	mux.HandleFunc("DELETE /admin/master-keys/{id}", a.HandleDeleteMasterKey)
}

// HandleListMasterKeys handles GET /admin/master-keys.
// Returns all master keys (id + name + created_at, never the key value).
func (a *AdminHub) HandleListMasterKeys(w http.ResponseWriter, r *http.Request) {
	keys := a.db.ListMasterKeys()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"master_keys": keys,
	})
}

// HandleCreateMasterKey handles POST /admin/master-keys.
// Body: {"name": "my-laptop"}
// Returns the key value once — it cannot be retrieved again.
func (a *AdminHub) HandleCreateMasterKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	id := uuid.New().String()
	key := generateToken()
	now := time.Now().UTC()

	if err := a.db.InsertMasterKey(id, req.Name, key, now); err != nil {
		log.Printf("create master key error: %v", err)
		http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         id,
		"name":       req.Name,
		"key":        key,
		"created_at": now,
	})
	log.Printf("created master key %q (%s)", req.Name, id)
}

// HandleDeleteMasterKey handles DELETE /admin/master-keys/{id}.
func (a *AdminHub) HandleDeleteMasterKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"missing key id"}`, http.StatusBadRequest)
		return
	}

	if !a.db.DeleteMasterKey(id) {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "id": id})
	log.Printf("deleted master key %s", id)
}
