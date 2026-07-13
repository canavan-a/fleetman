package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sanitizeFileName rejects names that could escape filesDir (path
// separators, "..", or an empty name after trimming).
func sanitizeFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	return name, nil
}

// HandleUploadFile handles POST /files (master-key authed).
// Multipart form with a "file" field; stores the file under filesDir keyed
// by its filename and records metadata (size, sha256) in the DB.
func (h *Hub) HandleUploadFile(w http.ResponseWriter, r *http.Request) {
	mf, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"missing multipart 'file' field"}`, http.StatusBadRequest)
		return
	}
	defer mf.Close()

	name, err := sanitizeFileName(header.Filename)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	dest := filepath.Join(h.filesDir, name)
	out, err := os.Create(dest)
	if err != nil {
		log.Printf("upload: create %s: %v", dest, err)
		http.Error(w, `{"error":"failed to store file"}`, http.StatusInternalServerError)
		return
	}
	defer out.Close()

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hasher), mf)
	if err != nil {
		log.Printf("upload: write %s: %v", dest, err)
		os.Remove(dest)
		http.Error(w, `{"error":"failed to store file"}`, http.StatusInternalServerError)
		return
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	now := time.Now().UTC()
	if err := h.Registry.db.InsertFile(name, size, sum, now); err != nil {
		log.Printf("upload: record metadata for %s: %v", name, err)
		http.Error(w, `{"error":"failed to record file metadata"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(FileInfo{Name: name, Size: size, SHA256: sum, UploadedAt: now})
	log.Printf("uploaded file %q (%d bytes)", name, size)
}

// HandleListFiles handles GET /files (master-key authed).
func (h *Hub) HandleListFiles(w http.ResponseWriter, r *http.Request) {
	files := h.Registry.db.ListFiles()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
	})
}

// HandleDownloadFile handles GET /files/{name}/download.
// Authenticated with a device bearer token (not the master key) since this
// is the endpoint agents use to fetch a file via ":sendf".
func (h *Hub) HandleDownloadFile(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		http.Error(w, `{"error":"missing or invalid Authorization header"}`, http.StatusUnauthorized)
		return
	}
	if _, ok := h.Registry.AuthenticateToken(token); !ok {
		http.Error(w, `{"error":"invalid device token"}`, http.StatusForbidden)
		return
	}

	name, err := sanitizeFileName(r.PathValue("name"))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}

	if _, ok := h.Registry.db.GetFile(name); !ok {
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, filepath.Join(h.filesDir, name))
}
