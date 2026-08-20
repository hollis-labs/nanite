package server

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/brand"
)

// basicAuthMiddleware returns a middleware that enforces HTTP Basic Auth on /api/ routes
// (except /api/health) when NANITE_AUTH_USER and NANITE_AUTH_PASSWORD env vars are set.
// If neither is set, the middleware is a no-op (local dev mode).
func basicAuthMiddleware(next http.Handler) http.Handler {
	user := os.Getenv(brand.Env("AUTH_USER"))
	pass := os.Getenv(brand.Env("AUTH_PASSWORD"))

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

		// Exempt /api/tools/call: it is the CLI-launch self-tools proxy
		// target, called by a same-host `nanite mcp` subprocess that has no
		// credentials. The handler itself enforces a loopback-only check
		// (see api.handleSelfToolCall), so the loopback gate — not basic
		// auth — is this route's trust boundary.
		if r.URL.Path == "/api/tools/call" {
			next.ServeHTTP(w, r)
			return
		}

		// Exempt /api/example/task-updates: the harness-reactive
		// self-tools worked example's internal_api_call reaction
		// (internal/selftools/reactions/internal_api_call.go) issues this
		// same-host, same-process call with no credentials, exactly the
		// reasoning /api/tools/call above already documents — it's a
		// trivial demo/test fixture, not a real feature, so no gate
		// beyond "reachable from this process" is warranted.
		if r.URL.Path == "/api/example/task-updates" {
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
			w.Header().Set("WWW-Authenticate", `Basic realm="` + brand.ID + `"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		userMatch := subtle.ConstantTimeCompare([]byte(reqUser), []byte(user)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(reqPass), []byte(pass)) == 1

		if !userMatch || !passMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="` + brand.ID + `"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
