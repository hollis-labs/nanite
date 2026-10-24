package apiauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
)

// CookieName is the UI session cookie.
const CookieName = "nanite_session"

// CSRFHeader carries the CSRF token on unsafe requests authenticated by the
// session cookie.
const CSRFHeader = "X-Nanite-CSRF"

const (
	// agentTTL is an agent credential's sliding lifetime; agentCap bounds the
	// total life of one credential however busy the session is.
	agentTTL = time.Hour
	agentCap = 24 * time.Hour
)

// Guard is the authentication and authorization middleware.
type Guard struct {
	Store *Store
	// Pattern returns the mux pattern a request will be served by ("" when
	// none matches). The server passes mux.Handler.
	Pattern func(*http.Request) string
	// HostAllowed validates the Host header (DNS-rebinding defense). Nil
	// allows loopback names only.
	HostAllowed func(host string) bool
	// SessionLive reports whether an agent credential's bound session is
	// still live. Nil skips the check.
	SessionLive func(ctx context.Context, sessionID string) bool
	// Insecure turns enforcement off for local development (the explicit
	// --insecure-no-auth flag). Requests then run as a synthetic operator.
	Insecure bool

	reg     *registry
	denials atomic.Int64
	window  atomic.Int64
}

// NewGuard builds a Guard and wires revocation to open-request cancellation.
func NewGuard(store *Store) *Guard {
	g := &Guard{Store: store, reg: newRegistry()}
	if store != nil {
		store.OnRevoke(func(id string) { g.reg.cancelKey(id) })
	}
	return g
}

// EndUISession ends a browser session and closes its open requests.
func (g *Guard) EndUISession(ctx context.Context, id string) error {
	err := g.Store.DeleteUISession(ctx, id)
	g.reg.cancelKey("ui:" + id)
	return err
}

// LoopbackHost is the default Host check: localhost names and loopback IPs.
func LoopbackHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Middleware wraps next with enforcement.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostOK := g.HostAllowed
		if hostOK == nil {
			hostOK = LoopbackHost
		}
		if !hostOK(r.Host) {
			writeError(w, r, http.StatusForbidden, "forbidden", "Host not allowed.")
			return
		}
		path := r.URL.Path
		if !needsCredential(path) && path != "/.well-known/agent-card.json" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions || isPublic(r.Method, path) || handlerAuthenticated(r.Method, path) {
			next.ServeHTTP(w, r)
			return
		}
		if g.Insecure {
			synthetic := Principal{CredentialID: "insecure-dev", Kind: KindOperator, Label: "insecure-dev", Scopes: AllScopes()}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), synthetic)))
			return
		}
		p, expires, ok := g.authenticate(w, r)
		if !ok {
			return
		}
		pattern := ""
		if g.Pattern != nil {
			pattern = g.Pattern(r)
		}
		if pattern == "" {
			pattern = r.Method + " " + path
		}
		need, alt, _ := ScopesFor(r.Method, pattern)
		if !p.Has(need) && (alt == "" || !p.Has(alt)) {
			g.audit(r, p, pattern, "forbidden")
			writeForbidden(w, r, need)
			return
		}
		if p.Cookie && unsafeMethod(r.Method) {
			if !originMatchesHost(r) {
				g.audit(r, p, pattern, "bad-origin")
				writeError(w, r, http.StatusForbidden, "forbidden", "Cross-origin request refused.")
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get(CSRFHeader)), []byte(p.CSRF)) != 1 {
				g.audit(r, p, pattern, "bad-csrf")
				writeError(w, r, http.StatusForbidden, "forbidden", "CSRF token required.")
				return
			}
		}
		if need == ScopeAdmin || ((need == ScopeMaintain || need == ScopeAgentsManage) && unsafeMethod(r.Method)) {
			g.audit(r, p, pattern, "allowed")
		}
		base := WithAudit(WithPrincipal(r.Context(), p), func(method, route, outcome string) {
			g.Store.Audit(context.WithoutCancel(r.Context()), p.CredentialID, p.Kind, method, route, outcome, remoteHost(r))
		})
		ctx, cancel := context.WithCancel(base)
		if !expires.IsZero() {
			cancel()
			ctx, cancel = context.WithDeadline(base, expires)
		}
		unregister := g.reg.add(cancel, p.CredentialID, "ui:"+p.UISessionID)
		defer func() { unregister(); cancel() }()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticate resolves the caller or writes the 401. The returned time is
// the credential's expiry (zero when none), used to bound open streams.
func (g *Guard) authenticate(w http.ResponseWriter, r *http.Request) (Principal, time.Time, bool) {
	if g.Store == nil {
		writeUnauthorized(w, r)
		return Principal{}, time.Time{}, false
	}
	ctx := r.Context()
	if auth := r.Header.Get("Authorization"); auth != "" {
		secret, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok {
			g.deny(r, "bad-scheme")
			writeUnauthorized(w, r)
			return Principal{}, time.Time{}, false
		}
		c, err := g.Store.Authenticate(ctx, secret)
		if err != nil {
			g.deny(r, "bad-credential")
			writeUnauthorized(w, r)
			return Principal{}, time.Time{}, false
		}
		expires := c.ExpiresAt
		if c.Kind == KindAgent {
			if g.SessionLive != nil && !g.SessionLive(ctx, c.BoundSessionID) {
				g.deny(r, "session-ended")
				writeUnauthorized(w, r)
				return Principal{}, time.Time{}, false
			}
			if next, err := g.Store.Slide(ctx, c, agentTTL, agentCap); err == nil {
				expires = next
			}
		}
		return Principal{CredentialID: c.ID, Kind: c.Kind, Label: c.Label, Scopes: c.Scopes, BoundSessionID: c.BoundSessionID}, expires, true
	}
	if cookie, err := r.Cookie(CookieName); err == nil {
		u, c, err := g.Store.AuthenticateUISession(ctx, cookie.Value)
		if err != nil {
			g.deny(r, "bad-session")
			writeUnauthorized(w, r)
			return Principal{}, time.Time{}, false
		}
		return Principal{CredentialID: c.ID, Kind: c.Kind, Label: c.Label, Scopes: c.Scopes, Cookie: true, UISessionID: u.ID, CSRF: u.CSRF}, u.ExpiresAt, true
	}
	g.deny(r, "no-credential")
	writeUnauthorized(w, r)
	return Principal{}, time.Time{}, false
}

// deny audits a failed attempt, rate-limited so a flood cannot swamp the
// single-writer database.
func (g *Guard) deny(r *http.Request, reason string) {
	now := time.Now().Unix()
	if g.window.Swap(now) != now {
		g.denials.Store(0)
	}
	if g.denials.Add(1) > 20 {
		return
	}
	g.Store.Audit(r.Context(), "", "", r.Method, r.URL.Path, "denied:"+reason, remoteHost(r))
}

func (g *Guard) audit(r *http.Request, p Principal, route, outcome string) {
	g.Store.Audit(r.Context(), p.CredentialID, p.Kind, r.Method, route, outcome, remoteHost(r))
}

func unsafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func remoteHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// originMatchesHost refuses a cookie-authenticated unsafe request whose
// Origin names another site. A request with no Origin is left to the CSRF
// token.
func originMatchesHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	rest := origin
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	return strings.EqualFold(rest, r.Host)
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="nanite"`)
	writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.")
}

func writeForbidden(w http.ResponseWriter, r *http.Request, need Scope) {
	writeErrorWith(w, r, http.StatusForbidden, "forbidden", "The credential lacks the required scope.", map[string]any{"required_scope": string(need)})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeErrorWith(w, r, status, code, message, nil)
}

func writeErrorWith(w http.ResponseWriter, r *http.Request, status int, code, message string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL.Path == "/api/admin" || strings.HasPrefix(r.URL.Path, "/api/admin/") {
		adminCode := admin.Unauthenticated
		if status == http.StatusForbidden {
			adminCode = admin.Forbidden
		}
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(admin.ErrorResponse{Error: admin.Failure{Code: adminCode, Message: message}}); err != nil {
			slog.Debug("apiauth: write admin error failed", "err", err)
		}
		return
	}
	body := map[string]any{"code": code, "message": message, "retryable": false, "request_id": uuid.NewString()}
	for k, v := range extra {
		body[k] = v
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}
