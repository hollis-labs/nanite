package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/config"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/version"
)

// maxRecoveredStackBytes caps the stack trace emitted by recoverMiddleware.
// 8 KiB holds a deep goroutine stack while bounding log volume on repeat
// panics. debug.Stack() is truncated to this length before logging and
// before being copied into the OTel span event.
const maxRecoveredStackBytes = 8 * 1024

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
// httpCfg is consulted for the bind address, timeouts, and body-size caps.
// Empty/zero fields are replaced with conservative defaults — see
// resolveHTTPConfig and the default* constants above.
func New(s *store.Store, a *api.API, port int, dev bool, pluginHost *naniteplugin.Host, httpCfg config.HTTPConfig) (*Server, error) {
	resolvedHTTPConfig, err := resolveHTTPConfig(httpCfg)
	if err != nil {
		return nil, fmt.Errorf("resolve HTTP config: %w", err)
	}
	mux := http.NewServeMux()
	srv := &Server{
		store:      s,
		port:       port,
		dev:        dev,
		mux:        mux,
		api:        a,
		pluginHost: pluginHost,
		httpCfg:    resolvedHTTPConfig,
	}

	// Set the router on the plugin host if it exists
	if pluginHost != nil {
		pluginHost.SetRouter(mux)
	}

	srv.routes()
	return srv, nil
}

// resolveHTTPConfig returns a copy of cfg with zero-valued fields filled in
// from the conservative defaults. Callers may pass a zero value to opt into
// defaults entirely.
func resolveHTTPConfig(cfg config.HTTPConfig) (config.HTTPConfig, error) {
	bindAddress, err := config.ResolveHTTPBindAddress(cfg.BindAddress)
	if err != nil {
		return config.HTTPConfig{}, err
	}
	cfg.BindAddress = bindAddress
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
	if len(cfg.CORSAllowedOrigins) == 0 {
		// Dev-oriented default. Deliberately narrow — replaces the prior
		// reflect-any policy (audit finding: Critical). Production deployments
		// are expected to set cors_allowed_origins in nanite.yaml.
		cfg.CORSAllowedOrigins = []string{
			"http://localhost:5173",
			"http://127.0.0.1:5173",
		}
	}
	return cfg, nil
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
//
//	recover -> logging -> CORS -> basicAuth -> callerIdentity -> bodyLimit -> mux
//
// CORS is outside basicAuth so that preflight (OPTIONS) requests succeed for
// allowed origins even when the caller has not yet sent credentials — auth
// UAs cannot attach credentials to a preflight. When Basic Auth is configured,
// its placement avoids reading bodies from rejected requests; when Basic Auth
// is disabled, it passes through and bodyLimit still caps requests before the
// mux. callerIdentity accepts both headers after that optional auth check. With
// Basic Auth disabled, those header values are trusted without credential
// verification as part of the loopback-default tradeoff; see
// caller_identity.go for the G-6.3 header contract.
func (s *Server) ListenAndServe() error {
	handler := s.recoverMiddleware(
		s.loggingMiddleware(
			s.corsMiddleware(
				basicAuthMiddleware(
					callerIdentityMiddleware(
						s.bodyLimitMiddleware(s.mux),
					),
				),
			),
		),
	)
	addr := s.listenAddress()
	s.logStartupPosture(addr)

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

func (s *Server) listenAddress() string {
	return net.JoinHostPort(s.httpCfg.BindAddress, fmt.Sprintf("%d", s.port))
}

func (s *Server) logStartupPosture(addr string) {
	if basicAuthEnabled() {
		slog.Info("nanite listening", "addr", addr, "dev", s.dev, "auth", "enabled")
		return
	}

	slog.Warn(
		"nanite listening without authentication",
		"addr", addr,
		"dev", s.dev,
		"auth", "disabled",
		"warning", "configure NANITE_AUTH_USER and NANITE_AUTH_PASSWORD before exposing Nanite beyond a trusted host",
	)
}

// newHTTPServer is exposed to tests so they can spin up an httptest server
// configured with the same timeouts / body caps as production.
func (s *Server) newHTTPServer(addr string) *http.Server {
	handler := s.recoverMiddleware(
		s.loggingMiddleware(
			s.corsMiddleware(
				basicAuthMiddleware(
					callerIdentityMiddleware(
						s.bodyLimitMiddleware(s.mux),
					),
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
		// Vary: Origin must be set on all responses whose content could vary
		// with Origin, even when the caller is disallowed — otherwise shared
		// caches can serve a cross-origin hit to a same-origin client.
		if origin != "" {
			w.Header().Add("Vary", "Origin")
		}
		if origin != "" && s.isOriginAllowed(origin) {
			// Special-case wildcard: per spec, Access-Control-Allow-Origin: *
			// cannot be combined with Access-Control-Allow-Credentials: true.
			// When operators opt into "*" we reflect any origin but drop
			// credentials rather than silently violating the spec.
			if s.hasWildcardCORS() {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			// Custom headers must appear here or browser preflight rejects them:
			//   - X-Nanite-Caller-Session / X-Nanite-Caller-Agent: caller identity
			//     plumbed by callerIdentityMiddleware (see caller_identity.go).
			//   - X-Nanite-Agent-Kind: CLI-provenance flag consumed by the send
			//     handler (see internal/api/messaging.go).
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Nanite-Caller-Session, X-Nanite-Caller-Agent, X-Nanite-Agent-Kind")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOriginAllowed decides whether to reflect the Origin header based on the
// configured HTTPConfig.CORSAllowedOrigins allowlist.
//
// Semantics (see appconfig.go for the full policy doc):
//   - Exact string match against the Origin header. No substring/suffix/regex.
//   - The special value "*" reflects any origin (see hasWildcardCORS); the
//     middleware then drops Access-Control-Allow-Credentials per spec.
//   - An empty Origin is rejected (handled by the middleware before calling).
func (s *Server) isOriginAllowed(origin string) bool {
	for _, allowed := range s.httpCfg.CORSAllowedOrigins {
		if allowed == "*" {
			return true
		}
		if allowed == origin {
			return true
		}
	}
	return false
}

// hasWildcardCORS reports whether the configured allowlist contains the "*"
// sentinel. Used by the middleware to switch to wildcard ACAO + no credentials.
func (s *Server) hasWildcardCORS() bool {
	for _, allowed := range s.httpCfg.CORSAllowedOrigins {
		if allowed == "*" {
			return true
		}
	}
	return false
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
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}

// recoverMiddleware recovers from panics in downstream handlers, logs the
// panic value + a capped stack, records an OTel span event on the request
// span (if one is present in r.Context()), and returns HTTP 500.
//
// Shape is modelled on internal/safego.recoverAndReport but kept inline
// because the middleware must write an HTTP response in addition to the
// log + span event. The parallel slog-migration session will convert the
// slog.Error emission below carries panic + stack + method + path as
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			err := recover()
			if err == nil {
				return
			}
			stack := debug.Stack()
			if len(stack) > maxRecoveredStackBytes {
				stack = stack[:maxRecoveredStackBytes]
			}
			slog.Error("http handler panic",
				"panic", fmt.Sprintf("%v", err),
				"stack", string(stack),
				"method", r.Method,
				"path", r.URL.Path,
			)

			// OTel span event: only recorded when the request already has
			// a span in context. When OTel is disabled (no-op provider)
			// or no tracing middleware runs above this one, SpanFromContext
			// returns a non-recording span and AddEvent is a cheap no-op.
			span := trace.SpanFromContext(r.Context())
			if span.SpanContext().IsValid() {
				span.AddEvent("http.panic",
					trace.WithAttributes(
						attribute.String("panic", fmt.Sprintf("%v", err)),
						attribute.String("stack", string(stack)),
						attribute.String("http.method", r.Method),
						attribute.String("http.target", r.URL.Path),
					),
				)
			}

			http.Error(w, "internal server error", http.StatusInternalServerError)
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

	all := s.pluginHost.GetUIComponentsWithOwners()
	// Exclude components that are pure server-side handlers (Handler != nil).
	// These are backend-only API routes with no corresponding React component
	// and should not appear in the right rail widget list.
	components := make([]naniteplugin.UIComponentWithOwner, 0, len(all))
	for _, c := range all {
		if c.Handler == nil {
			components = append(components, c)
		}
	}
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

// knownEmitEventTypes lists the event names the HTTP /api/plugins/events
// endpoint will accept from external callers. Sourced from the plugin event
// catalog in internal/plugin/events.go plus the Claude Code hook aliases that
// NormalizeEventType resolves to canonical names.
//
// TODO: replace this hardcoded list once Host exposes a richer registered-
// event-types lookup (plugins will then contribute event types via manifest
// metadata). Until then the allowlist is curated in source.
var knownEmitEventTypes = map[string]struct{}{
	naniteplugin.EventSessionStart:        {},
	naniteplugin.EventSessionEnd:          {},
	naniteplugin.EventSessionArchived:     {},
	naniteplugin.EventAgentSwitched:       {},
	naniteplugin.EventAgentLoaded:         {},
	naniteplugin.EventMessageSent:         {},
	naniteplugin.EventMessageReceived:     {},
	naniteplugin.EventMessageDeleted:      {},
	naniteplugin.EventMessageBookmarked:   {},
	naniteplugin.EventMessageUnbookmarked: {},
	naniteplugin.EventScopeChanged:        {},
	naniteplugin.EventToolCalled:          {},
	naniteplugin.EventToolFailed:          {},
	naniteplugin.EventToolComplete:        {},
	naniteplugin.EventToolExecuting:       {},
	naniteplugin.EventMessageSending:      {},
	naniteplugin.EventEnvelopeRendered:    {},
	naniteplugin.EventWidgetLoaded:        {},
	naniteplugin.EventActionTriggered:     {},
	naniteplugin.EventWorkflowStarted:     {},
	naniteplugin.EventWorkflowComplete:    {},
	naniteplugin.EventWorkflowFailed:      {},
	naniteplugin.EventConfigChanged:       {},
	naniteplugin.EventPluginInstalled:     {},
	naniteplugin.EventPluginUninstalled:   {},
	naniteplugin.EventProviderError:       {},
	naniteplugin.EventProviderFallback:    {},
	naniteplugin.EventShellExec:           {},
	naniteplugin.EventShellError:          {},
	naniteplugin.EventShellBlocked:        {},
	naniteplugin.EventContextCompacted:    {},
	naniteplugin.EventContextAssembled:    {},
	naniteplugin.EventArtifactCreated:     {},
	naniteplugin.EventArtifactDeleted:     {},
}

// isKnownEmitEventType reports whether name is an allowlisted event type the
// HTTP emit endpoint will forward. The input is normalized via
// NormalizeEventType first so Claude Code aliases (e.g. "PreToolUse") resolve
// to the canonical Nanite name before the lookup.
func isKnownEmitEventType(name string) bool {
	canonical := naniteplugin.NormalizeEventType(name)
	_, ok := knownEmitEventTypes[canonical]
	return ok
}

func (s *Server) handleEmitEvent(w http.ResponseWriter, r *http.Request) {
	if s.pluginHost == nil {
		http.Error(w, "Plugin system not initialized", http.StatusServiceUnavailable)
		return
	}

	// Decode as json.RawMessage for the data field so we can enforce
	// "must be a JSON object" explicitly — decoding directly into a
	// map[string]interface{} would reject raw scalars but also reject a
	// missing field the same way, which we want to distinguish.
	var req struct {
		EventType string          `json:"event_type"`
		Source    string          `json:"source"`
		Data      json.RawMessage `json:"data"`
		SessionID string          `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.EventType == "" {
		http.Error(w, "event_type is required", http.StatusBadRequest)
		return
	}
	if !isKnownEmitEventType(req.EventType) {
		http.Error(w, fmt.Sprintf("unknown event_type %q", req.EventType), http.StatusBadRequest)
		return
	}

	// Payload must be a JSON object (or omitted). Raw strings, arrays, numbers,
	// booleans, and explicit nulls are rejected — the plugin event contract
	// assumes keyed data.
	var data map[string]interface{}
	if len(req.Data) > 0 && string(req.Data) != "null" {
		trimmed := bytes.TrimSpace(req.Data)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			http.Error(w, "data must be a JSON object", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(req.Data, &data); err != nil {
			http.Error(w, "data must be a JSON object", http.StatusBadRequest)
			return
		}
	}
	if data == nil {
		data = map[string]interface{}{}
	}

	event := naniteplugin.NewEvent(req.EventType, req.Source, naniteplugin.EventData{
		SessionID: req.SessionID,
	})

	// Override data with the provided data
	event.Data = data
	if req.SessionID != "" {
		event.SessionID = req.SessionID
	}

	s.pluginHost.EmitEvent(event)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
