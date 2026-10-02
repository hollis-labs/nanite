package server

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/go-envelopes/admin"
	"github.com/hollis-labs/nanite/internal/brand"
)

// basicAuthMiddleware returns a middleware that enforces HTTP Basic Auth on /api/ routes
// (except health and routes enforcing their own credentials) when NANITE_AUTH_USER and NANITE_AUTH_PASSWORD env vars are set.
// If neither is set, the middleware is a no-op (local dev mode).
func basicAuthMiddleware(next http.Handler) http.Handler {
	user, pass, enabled := basicAuthCredentials()

	// If no credentials configured, skip auth entirely (local dev mode).
	if !enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exempt /api/health from auth.
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Exempt /api/tools/call: it is the CLI-launch self-tools proxy
		// target, called by a same-host `nanite mcp` subprocess that has no
		// credentials. The handler itself enforces a loopback-only check
		// (see api.handleSelfToolCall), so the loopback gate — not basic
		// auth — is this route's trust boundary.
		if r.URL.Path == "/api/tools/call" {
			next.ServeHTTP(w, r)
			return
		}

		// Plugin read queries authenticate their connection bearer at the core
		// handler. Requiring Basic Auth here would disclose the user's broader
		// credentials to plugins and prevent their scoped grant from working.
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/plugin-host/query/") {
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
			w.Header().Set("WWW-Authenticate", `Basic realm="`+brand.ID+`"`)
			writeBasicAuthUnauthorized(w, r)
			return
		}

		userMatch := subtle.ConstantTimeCompare([]byte(reqUser), []byte(user)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(reqPass), []byte(pass)) == 1

		if !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+brand.ID+`"`)
			writeBasicAuthUnauthorized(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// basicAuthEnabled reports whether basicAuthMiddleware will enforce
// credentials. Its condition intentionally mirrors the middleware's no-op
// branch: either configured value enables the middleware, while both unset
// leaves the server unauthenticated.
func basicAuthEnabled() bool {
	_, _, enabled := basicAuthCredentials()
	return enabled
}

func basicAuthCredentials() (user, pass string, enabled bool) {
	user = os.Getenv(brand.Env("AUTH_USER"))
	pass = os.Getenv(brand.Env("AUTH_PASSWORD"))
	enabled = user != "" || pass != ""
	return user, pass, enabled
}

// writeBasicAuthUnauthorized changes only the denial representation for the
// admin mount. Credential comparisons, exemptions and admission are unchanged.
func writeBasicAuthUnauthorized(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/admin" && !strings.HasPrefix(r.URL.Path, "/api/admin/") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusUnauthorized)
	response := admin.ErrorResponse{Error: admin.Failure{Code: admin.Unauthenticated, Message: "Authentication is required."}}
	if encodeErr := json.NewEncoder(w).Encode(response); encodeErr != nil {
		slog.Debug("server: write admin auth response failed", "err", encodeErr)
	}
}
