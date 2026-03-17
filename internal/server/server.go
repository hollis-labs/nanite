package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/hollis-labs/conduit/internal/api"
	conduitplugin "github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/conduit/internal/store"
)

// Server is the HTTP server for Conduit Chat.
type Server struct {
	store      *store.Store
	port       int
	dev        bool
	mux        *http.ServeMux
	api        *api.API
	pluginHost *conduitplugin.Host
}

// New creates a new Server wired to the given store and API.
func New(s *store.Store, a *api.API, port int, dev bool, pluginHost *conduitplugin.Host) *Server {
	mux := http.NewServeMux()
	srv := &Server{
		store:      s,
		port:       port,
		dev:        dev,
		mux:        mux,
		api:        a,
		pluginHost: pluginHost,
	}

	// Set the router on the plugin host if it exists
	if pluginHost != nil {
		pluginHost.SetRouter(mux)
	}

	srv.routes()
	return srv
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	handler := s.recoverMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux))))
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("conduit listening on %s (dev=%v)", addr, s.dev)
	return http.ListenAndServe(addr, handler)
}

func (s *Server) routes() {
	// Health
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	// Plugin management routes
	if s.pluginHost != nil {
		s.mux.HandleFunc("GET /api/plugins", s.handleListPlugins)
		s.mux.HandleFunc("GET /api/plugins/ui-components", s.handleGetUIComponents)
		s.mux.HandleFunc("POST /api/plugins/events", s.handleEmitEvent)
	}

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

// Plugin management handlers

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	if s.pluginHost == nil {
		http.Error(w, "Plugin system not initialized", http.StatusServiceUnavailable)
		return
	}

	plugins := s.pluginHost.ListPlugins()
	pluginInfos := make([]map[string]interface{}, len(plugins))

	for i, p := range plugins {
		pluginInfos[i] = map[string]interface{}{
			"id":           p.ID(),
			"name":         p.Name(),
			"version":      p.Version(),
			"description":  p.Description(),
			"dependencies": p.Dependencies(),
			"status":       p.Status(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"plugins": pluginInfos,
		"count":   len(pluginInfos),
	})
}

func (s *Server) handleGetUIComponents(w http.ResponseWriter, r *http.Request) {
	if s.pluginHost == nil {
		http.Error(w, "Plugin system not initialized", http.StatusServiceUnavailable)
		return
	}

	components := s.pluginHost.GetUIComponents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"components": components,
		"count":      len(components),
	})
}

func (s *Server) handleEmitEvent(w http.ResponseWriter, r *http.Request) {
	if s.pluginHost == nil {
		http.Error(w, "Plugin system not initialized", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		EventType string                 `json:"event_type"`
		Source    string                 `json:"source"`
		Data      map[string]interface{} `json:"data"`
		SessionID string                 `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	event := conduitplugin.NewEvent(req.EventType, req.Source, conduitplugin.EventData{
		SessionID: req.SessionID,
	})

	// Override data with the provided data
	event.Data = req.Data
	if req.SessionID != "" {
		event.SessionID = req.SessionID
	}

	s.pluginHost.EmitEvent(event)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
