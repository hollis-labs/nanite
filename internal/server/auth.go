package server

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

// basicAuthMiddleware returns a middleware that enforces HTTP Basic Auth on /api/ routes
// (except /api/health) when MENTAT_AUTH_USER and MENTAT_AUTH_PASSWORD env vars are set.
// If neither is set, the middleware is a no-op (local dev mode).
func basicAuthMiddleware(next http.Handler) http.Handler {
	user := os.Getenv("MENTAT_AUTH_USER")
	pass := os.Getenv("MENTAT_AUTH_PASSWORD")

	// If no credentials configured, skip auth entirely (local dev mode).
	if user == "" && pass == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exempt /api/health from auth.
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Only protect /api/ routes.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Check Basic Auth credentials.
		reqUser, reqPass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="mentat"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		userMatch := subtle.ConstantTimeCompare([]byte(reqUser), []byte(user)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(reqPass), []byte(pass)) == 1

		if !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="mentat"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
