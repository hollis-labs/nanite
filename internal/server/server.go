package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/config"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/version"
)

// Timeout + body-cap defaults. Used when the injected HTTPConfig leaves a
// field at its zero value. Chosen conservatively:
//   - ReadHeaderTimeout defends against Slowloris — clients have 10s to finish
//     sending request headers.
//   - ReadTimeout bounds the entire request read (headers + body) at 30s.
//   - WriteTimeout bounds a response write at 60s. Handlers that legitimately
//     need to stream longer (SSE, long downloads) must reset the deadline
//     explicitly via http.ResponseController — do not raise this default
//     globally or the Slowloris/slow-body mitigations weaken.
//   - IdleTimeout bounds keep-alive idle time between requests.
const (
	defaultReadTimeout       = 30 * time.Second
	defaultReadHeaderTimeout = 10 * time.Second
	defaultWriteTimeout      = 60 * time.Second
	defaultIdleTimeout       = 120 * time.Second

	defaultMaxRequestBodyBytes int64 = 10 << 20 // 10 MiB
	defaultMaxUploadBodyBytes  int64 = 32 << 20 // 32 MiB
)

// uploadPathPrefixes lists route prefixes that legitimately accept multipart
// uploads and are subject to MaxUploadBodyBytes instead of MaxRequestBodyBytes.
var uploadPathPrefixes = []string{
	"/api/artifacts/upload",
	"/api/plugins/install",
}

// Server is the HTTP server for Nanite Chat.
type Server struct {
	store      *store.Store
	port       int
	dev        bool
	mux        *http.ServeMux
	api        *api.API
	pluginHost *naniteplugin.Host
	pluginsDir string
	httpCfg    config.HTTPConfig
}

// New creates a new Server wired to the given store and API.
//
// httpCfg is consulted for timeouts and body-size caps. Fields that are zero
// are replaced with conservative defaults — see the default* constants above.
func New(s *store.Store, a *api.API, port int, dev bool, pluginHost *naniteplugin.Host, httpCfg config.HTTPConfig) *Server {
	mux := http.NewServeMux()
	srv := &Server{
		store:      s,
		port:       port,
		dev:        dev,
		mux:        mux,
		api:        a,
		pluginHost: pluginHost,
		httpCfg:    resolveHTTPConfig(httpCfg),
	}

	// Set the router on the plugin host if it exists
	if pluginHost != nil {
		pluginHost.SetRouter(mux)
	}

	srv.routes()
	return srv
}

// resolveHTTPConfig returns a copy of cfg with zero-valued fields filled in
// from the conservative defaults. Callers may pass a zero value to opt into
// defaults entirely.
func resolveHTTPConfig(cfg config.HTTPConfig) config.HTTPConfig {
	if cfg.ReadTimeoutSeconds <= 0 {
		cfg.ReadTimeoutSeconds = int(defaultReadTimeout / time.Second)
	}
	if cfg.ReadHeaderTimeoutSeconds <= 0 {
		cfg.ReadHeaderTimeoutSeconds = int(defaultReadHeaderTimeout / time.Second)
	}
	if cfg.WriteTimeoutSeconds <= 0 {
		cfg.WriteTimeoutSeconds = int(defaultWriteTimeout / time.Second)
	}
	if cfg.IdleTimeoutSeconds <= 0 {
		cfg.IdleTimeoutSeconds = int(defaultIdleTimeout / time.Second)
	}
	if cfg.MaxRequestBodyBytes <= 0 {
		cfg.MaxRequestBodyBytes = defaultMaxRequestBodyBytes
	}
	if cfg.MaxUploadBodyBytes <= 0 {
		cfg.MaxUploadBodyBytes = defaultMaxUploadBodyBytes
	}
	return cfg
}

// SetPluginsDir sets the plugins directory path for the management API routes.
// Must be called before ListenAndServe if plugin management is desired.
func (s *Server) SetPluginsDir(dir string) {
	s.pluginsDir = dir
	// Register plugin management API routes now that we have the directory.
	api.RegisterPluginManagementRoutes(s.mux, dir, s.store, s.pluginHost)
	// Register plugin catalog API routes.
	api.RegisterCatalogRoutes(s.mux, s.store, dir, s.pluginHost)
}

// ListenAndServe starts the HTTP server.
//
// Middleware chain (outer -> inner):
//   recover -> logging -> CORS -> basicAuth -> bodyLimit -> mux
//
// CORS is outside basicAuth so that preflight (OPTIONS) requests succeed for
// allowed origins even when the caller has not yet sent credentials — auth
// UAs cannot attach credentials to a preflight. The body-limit middleware
// sits inside auth because unauthenticated traffic is already rejected by
// auth; caps only matter for requests that reach a handler.
func (s *Server) ListenAndServe() error {
	handler := s.recoverMiddleware(
		s.loggingMiddleware(
			s.corsMiddleware(
				basicAuthMiddleware(
					s.bodyLimitMiddleware(s.mux),
				),
			),
		),
	)
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("nanite listening on %s (dev=%v)", addr, s.dev)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       time.Duration(s.httpCfg.ReadTimeoutSeconds) * time.Second,
		ReadHeaderTimeout: time.Duration(s.httpCfg.ReadHeaderTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(s.httpCfg.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(s.httpCfg.IdleTimeoutSeconds) * time.Second,
	}
	return httpSrv.ListenAndServe()
}

// newHTTPServer is exposed to tests so they can spin up an httptest server
// configured with the same timeouts / body caps as production.
func (s *Server) newHTTPServer(addr string) *http.Server {
	handler := s.recoverMiddleware(
		s.loggingMiddleware(
			s.corsMiddleware(
				basicAuthMiddleware(
					s.bodyLimitMiddleware(s.mux),
				),
			),
		),
	)
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       time.Duration(s.httpCfg.ReadTimeoutSeconds) * time.Second,
		ReadHeaderTimeout: time.Duration(s.httpCfg.ReadHeaderTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(s.httpCfg.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(s.httpCfg.IdleTimeoutSeconds) * time.Second,
	}
}

func (s *Server) routes() {
	// Health
	s.mux.HandleFunc("GET /api/health", s.handleHealth)

	// Plugin management routes
	if s.pluginHost != nil {
		s.mux.HandleFunc("GET /api/plugins", s.handleListPlugins)
		s.mux.HandleFunc("GET /api/plugins/ui-components", s.handleGetUIComponents)
		s.mux.HandleFunc("GET /api/plugins/ui-slots", s.handleGetUISlots)
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
		"version": version.Version,
	})
}

// --- Middleware ---

// corsMiddleware applies CORS response headers and short-circuits preflight
// requests with 204. It is placed *outside* basicAuthMiddleware so that
// preflight requests (which cannot carry credentials) succeed for allowed
// origins.
//
// Policy surface for TASK-016: the origin-allow decision lives in
// isOriginAllowed. Task 016 replaces that stub with an explicit allowlist
// (pulled from config); the middleware skeleton — header set, preflight
// short-circuit, placement in the chain — is stable.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
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

// isOriginAllowed decides whether to reflect an Origin header. Current policy
// (pre-TASK-016) is reflect-any — matches the prior server behaviour and
// preserves compatibility for the post-audit phase-1 cut. TASK-016 replaces
// this with a config-driven allowlist.
func (s *Server) isOriginAllowed(origin string) bool {
	_ = origin
	return true
}

// bodyLimitMiddleware wraps the request Body in http.MaxBytesReader for
// mutating methods (POST/PUT/PATCH/DELETE). Non-mutating requests pass
// through untouched.
//
// Limit selection: upload endpoints (prefixes listed in uploadPathPrefixes)
// use the higher MaxUploadBodyBytes cap; all other mutating requests use
// MaxRequestBodyBytes.
//
// Note: MaxBytesReader arms the limit; the 413 response happens lazily when
// a handler reads past the cap. Handlers that decode JSON via (*API).decode
// or json.NewDecoder surface the MaxBytesError as a decode error. For the
// explicit 413 path, downstream handlers should detect *http.MaxBytesError
// and return http.StatusRequestEntityTooLarge — json.NewDecoder errors today
// already bubble to 400, which the user sees; the 413 guarantee here is the
// server-side protection, not the status code. For the test harness we
// therefore assert on either 400 (decode-error propagation) or 413 (direct
// read). See server_test.go::TestBodyLimitMiddleware.
func (s *Server) bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingMethod(r.Method) || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		limit := s.httpCfg.MaxRequestBodyBytes
		if isUploadPath(r.URL.Path) {
			limit = s.httpCfg.MaxUploadBodyBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func isMutatingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isUploadPath(p string) bool {
	for _, prefix := range uploadPathPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
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

	components := s.pluginHost.GetUIComponentsWithOwners()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"components": components,
		"count":      len(components),
	})
}

func (s *Server) handleGetUISlots(w http.ResponseWriter, r *http.Request) {
	if s.pluginHost == nil {
		http.Error(w, "Plugin system not initialized", http.StatusServiceUnavailable)
		return
	}

	slots := s.pluginHost.GetAllSlots()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(slots)
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

	event := naniteplugin.NewEvent(req.EventType, req.Source, naniteplugin.EventData{
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
