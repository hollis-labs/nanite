package server

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/api"
)

// adminCORSMiddleware uses the command Origin policy for the admin prefix only.
// Preflight is body/store-free; the later command still requires operator auth
// and an approved Origin before decoding. Legacy CORS remains independent.
func (s *Server) adminCORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if api.AdminOriginAllowed(r, s.adminAllowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, If-Match")
			w.Header().Set("Access-Control-Expose-Headers", "ETag")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
