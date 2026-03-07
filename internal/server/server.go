package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/hollis-labs/mentat-chat/internal/api"
	"github.com/hollis-labs/mentat-chat/internal/store"
)

// Server is the HTTP server for Mentat Chat.
type Server struct {
	store  *store.Store
	port   int
	dev    bool
	mux    *http.ServeMux
	api    *api.API
}

// New creates a new Server wired to the given store and API.
func New(s *store.Store, a *api.API, port int, dev bool) *Server {
	srv := &Server{
		store: s,
		port:  port,
		dev:   dev,
		mux:   http.NewServeMux(),
		api:   a,
	}
	srv.routes()
	return srv
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	handler := s.recoverMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux))))
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("mentat-chat listening on %s (dev=%v)", addr, s.dev)
	return http.ListenAndServe(addr, handler)
}

func (s *Server) routes() {
	// Health
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	// Register all API routes.
	if s.api != nil {
		s.api.RegisterRoutes(s.mux)
	}

	// SPA fallback — must be last
	s.mux.HandleFunc("/", s.handleSPA)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": "0.1.0",
	})
}

// --- Middleware ---

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Allow localhost origins for development.
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
