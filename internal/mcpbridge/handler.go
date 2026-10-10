package mcpbridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// CandidateVersion describes this internal candidate only. No public routes or
// bootstrap policy are registered by this package.
const CandidateVersion = 1

var (
	ErrTargetUnavailable = errors.New("MCP execution owner is unavailable")
	ErrForbidden         = errors.New("MCP operation is forbidden")
	ErrConflict          = errors.New("MCP operation conflicts with an existing call")
	ErrUnknownOutcome    = errors.New("MCP mutation outcome is unknown")
)

type ToolDefinition struct {
	mcp.Tool
	Effect  string `json:"effect"`
	Binding string `json:"tool_binding"`
}

type Catalog struct {
	Version  int              `json:"version"`
	Revision string           `json:"catalog_revision"`
	Tools    []ToolDefinition `json:"tools"`
}

type ListRequest struct {
	Version int `json:"version"`
}

// CallRequest carries no initiating actor or session claim. The host backend
// receives the caller verified from the scoped proxy credential separately.
type CallRequest struct {
	Version      int            `json:"version"`
	RequestID    string         `json:"request_id"`
	Name         string         `json:"name"`
	Binding      string         `json:"tool_binding"`
	Arguments    map[string]any `json:"arguments"`
	OperationKey string         `json:"operation_key,omitempty"`
}

type CancelRequest struct {
	Version   int    `json:"version"`
	RequestID string `json:"request_id"`
}

// ExecutionOwner must be the running host's policy/execution service. It owns
// current caller authorization, reviewed definitions, expected catalog binding
// admission, actual plugin incarnation fencing, approvals, commit and receipts.
// RevalidateAuthority(ctx) exposes the current credential scope to that owner;
// effects/grants and commit checks must use it, not just caller identity.
// Implementing this interface must not create another plugin host or datastore.
type ExecutionOwner interface {
	Catalog(context.Context, VerifiedCaller) (Catalog, error)
	Call(context.Context, VerifiedCaller, CallRequest) (*mcp.ToolResult, error)
}

type TransportLimits struct {
	MaxRequestBytes  int64
	MaxResponseBytes int
	MaxActiveCalls   int
	MaxCallDuration  time.Duration
}

type activeKey struct {
	credential [32]byte
	requestID  string
}

type Handler struct {
	credentials *Credentials
	owner       ExecutionOwner
	limits      TransportLimits
	mu          sync.Mutex
	active      map[activeKey]context.CancelFunc
	slots       chan struct{}
}

func NewHandler(credentials *Credentials, owner ExecutionOwner, limits TransportLimits) (*Handler, error) {
	if limits.MaxRequestBytes <= 0 || limits.MaxResponseBytes <= 0 || limits.MaxActiveCalls <= 0 || limits.MaxCallDuration <= 0 {
		return nil, errors.New("explicit positive MCP proxy transport limits are required")
	}
	return &Handler{credentials: credentials, owner: owner, limits: limits, active: make(map[activeKey]context.CancelFunc), slots: make(chan struct{}, limits.MaxActiveCalls)}, nil
}

// ServeHTTP is an unregistered candidate fixed list/call/cancel handler. A
// claimed identity in headers or JSON never changes its verified principal.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopbackPeer(r.RemoteAddr) {
		h.writeError(w, ErrForbidden)
		return
	}
	if r.Method != http.MethodPost {
		h.write(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
		h.write(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer {
		h.writeError(w, ErrUnauthenticated)
		return
	}
	caller, permit, release, err := h.credentials.Acquire(r.Context(), token)
	if err != nil {
		h.writeError(w, err)
		return
	}
	defer release()
	if h.owner == nil {
		h.writeError(w, ErrTargetUnavailable)
		return
	}
	key := activeKey{credential: sha256.Sum256([]byte(token))}
	if r.URL.Path == "/api/mcp/v1/list" || r.URL.Path == "/api/mcp/v1/call" {
		select {
		case h.slots <- struct{}{}:
			defer func() { <-h.slots }()
		default:
			h.writeError(w, ErrCapacity)
			return
		}
	}
	switch r.URL.Path {
	case "/api/mcp/v1/list":
		var request ListRequest
		if err := h.decode(w, r, &request); err != nil || request.Version != CandidateVersion {
			h.write(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		bounded, cancel := context.WithTimeout(permit, h.limits.MaxCallDuration)
		defer cancel()
		catalog, err := h.owner.Catalog(bounded, caller)
		if err != nil {
			h.writeError(w, err)
			return
		}
		if catalog.Version != CandidateVersion || catalog.Revision == "" {
			h.writeError(w, ErrTargetUnavailable)
			return
		}
		if catalog.Tools == nil {
			catalog.Tools = []ToolDefinition{}
		}
		h.write(w, http.StatusOK, catalog)
	case "/api/mcp/v1/call":
		var request CallRequest
		if err := h.decode(w, r, &request); err != nil || request.Version != CandidateVersion || !validRequestID(request.RequestID) || request.Name == "" || len(request.Name) > 64 || request.Binding == "" || len(request.Binding) > 1024 || len(request.OperationKey) > 256 {
			h.write(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		key.requestID = request.RequestID
		bounded, cancel := context.WithTimeout(permit, h.limits.MaxCallDuration)
		defer cancel()
		h.mu.Lock()
		_, duplicate := h.active[key]
		if duplicate || len(h.active) >= h.limits.MaxActiveCalls {
			h.mu.Unlock()
			if duplicate {
				h.writeError(w, ErrConflict)
			} else {
				h.writeError(w, ErrCapacity)
			}
			return
		}
		h.active[key] = cancel
		h.mu.Unlock()
		defer func() { h.mu.Lock(); delete(h.active, key); h.mu.Unlock() }()
		result, err := h.owner.Call(bounded, caller, request)
		if err != nil {
			h.writeError(w, err)
			return
		}
		if result == nil {
			h.writeError(w, ErrTargetUnavailable)
			return
		}
		h.write(w, http.StatusOK, result)
	case "/api/mcp/v1/cancel":
		var request CancelRequest
		if err := h.decode(w, r, &request); err != nil || request.Version != CandidateVersion || !validRequestID(request.RequestID) {
			h.write(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		key.requestID = request.RequestID
		h.mu.Lock()
		cancel := h.active[key]
		if cancel != nil {
			cancel()
		}
		h.mu.Unlock()
		h.write(w, http.StatusOK, map[string]any{"version": CandidateVersion, "canceled": cancel != nil})
	default:
		h.write(w, http.StatusNotFound, map[string]any{"error": "unknown_method"})
	}
}

func loopbackPeer(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validRequestID(id string) bool { return len(id) > 0 && len(id) <= 128 }

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, h.limits.MaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("exactly one JSON request is required")
	}
	return nil
}

func (h *Handler) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	body, err := json.Marshal(value)
	if err != nil || len(body) > h.limits.MaxResponseBytes {
		status = http.StatusBadGateway
		body = []byte(`{"error":"response_limit"}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadGateway, "target_unavailable"
	switch {
	case errors.Is(err, ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, ErrAuthorityUnavailable):
		status, code = http.StatusServiceUnavailable, "authority_unavailable"
	case errors.Is(err, ErrTargetUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, subprocess.ErrStaleBinding):
		status, code = http.StatusConflict, "stale_binding"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, ErrCapacity):
		status, code = http.StatusTooManyRequests, "overloaded"
	case errors.Is(err, ErrUnknownOutcome):
		code = "unknown_outcome"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	}
	h.write(w, status, map[string]any{"error": code})
}
