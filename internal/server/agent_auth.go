package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/brand"
)

type agentBearerPrincipalKey struct{}

func agentAuthToken() string { return os.Getenv(brand.Env("AUTH_TOKEN")) }
func validateAgentBind(bind, token string) error {
	ip := net.ParseIP(bind)
	if ip == nil || !ip.IsLoopback() {
		if token == "" {
			return errors.New("non-loopback binding requires NANITE_AUTH_TOKEN")
		}
	}
	return nil
}
func requestIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// agentAuthMiddleware authenticates the single operator at the HTTP host.
// Forwarded headers and request metadata never become trusted actor identities.
func agentAuthMiddleware(next http.Handler) http.Handler {
	token := agentAuthToken()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		agentRoute := path == "/api/agent/v1" || strings.HasPrefix(path, "/api/agent/v1/")
		toolRoute := path == "/api/tools/call"
		remoteAPI := strings.HasPrefix(path, "/api/") && !requestIsLoopback(r)
		handlerAuthenticated := path == "/api/health" || strings.HasPrefix(path, "/api/plugin-host/query/") || path == "/api/plugin-host/durable-wake"
		if handlerAuthenticated || (!agentRoute && !toolRoute && !remoteAPI) {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" && !toolRoute && requestIsLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		authorization := r.Header.Get("Authorization")
		supplied, hasBearer := strings.CutPrefix(authorization, "Bearer ")
		if token == "" || !hasBearer || subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+brand.ID+`"`)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "unauthenticated", "message": "Bearer authentication is required.", "retryable": false, "request_id": uuid.NewString()}})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentBearerPrincipalKey{}, true)))
	})
}
