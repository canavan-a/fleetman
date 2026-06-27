package main

import (
	"net/http"
	"strings"
)

// MasterAuth returns middleware that validates the master API key on every
// HTTP request by looking it up in the database.
func MasterAuth(db *DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, `{"error":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth {
				http.Error(w, `{"error":"invalid Authorization format, expected Bearer <key>"}`, http.StatusUnauthorized)
				return
			}
			if !db.LookupMasterKey(token) {
				http.Error(w, `{"error":"invalid master API key"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
