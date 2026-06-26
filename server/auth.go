package main

import (
	"net/http"
	"strings"
)

// MasterAuth returns middleware that validates the master API key
// on every HTTP request. Keys are checked against the provided set.
func MasterAuth(validKeys map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, `{"error":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth {
				// No "Bearer " prefix found.
				http.Error(w, `{"error":"invalid Authorization format, expected Bearer <key>"}`, http.StatusUnauthorized)
				return
			}
			if _, ok := validKeys[token]; !ok {
				http.Error(w, `{"error":"invalid master API key"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
