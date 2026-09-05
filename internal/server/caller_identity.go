package server

import (
	"net/http"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
)

// HTTP caller-identity header names. Callers that want the service
// layer's messaging authz checks to enforce against a distinct identity
// (rather than trusting body/query-derived caller args) set both
// headers on every request.
//
// G-6.3 design note: nanite is a single-user local app. The HTTP server
// enforces that posture by binding to 127.0.0.1 by default; binding beyond
// the local machine requires an explicit bind-address opt-in. Basic Auth is
// still optional and coarse, but startup always announces whether it is
// enabled and emits a warning when it is not. These headers are the
// *contract* for carrying caller identity end-to-end; the service layer's
// existing authz checks (Inbox caller-match, Thread participant filter,
// Ack/Resolve recipient check, UnreadCount caller-match) then enforce that the
// caller matches the row's intended recipient. When headers are
// absent the service falls back to legacy trust-the-body behavior —
// see messaging.UnreadCount and the HTTP handler comments.
const (
	CallerSessionHeader = "X-Nanite-Caller-Session"
	CallerAgentHeader   = "X-Nanite-Caller-Agent"
)

// callerIdentityMiddleware reads the X-Nanite-Caller-Session and
// X-Nanite-Caller-Agent headers from the incoming request and, when
// BOTH are present and non-empty, stamps a messaging.CallerIdentity on
// the request context. Handlers read the identity via
// messaging.CallerFromCtx and pass it to the messaging.Service as the
// caller arg so authz checks compare against the header-supplied
// identity instead of the body/query-derived target identity.
//
// When either header is absent the middleware is a no-op — the context
// carries no CallerIdentity and handlers fall back to the pre-G-6.3
// behavior of using body/query as both target and caller. This
// preserves compatibility with existing FE/CLI clients while giving
// clients a path to stricter identity enforcement. When Basic Auth is
// unconfigured, the loopback-default trust model means these caller-supplied
// headers are accepted without independent credential verification.
//
// Placement in the chain: sits inside optional basicAuth. When Basic Auth is
// configured, invalid credentials are rejected before the header read; when
// it is disabled, requests pass through and the header values are trusted as
// described above. callerIdentity sits outside bodyLimit because header
// parsing is cheap and body-size caps only matter for request bodies.
func callerIdentityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(CallerSessionHeader)
		agentID := r.Header.Get(CallerAgentHeader)
		if sessionID != "" && agentID != "" {
			ctx := messaging.WithCaller(r.Context(), messaging.CallerIdentity{
				SessionID: sessionID,
				AgentID:   agentID,
			})
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
