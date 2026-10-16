package server

import (
	"net/http"

	messaging "github.com/hollis-labs/libs/mesh/core/messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/apiauth"
)

// HTTP caller-identity header names. Callers that want the service
// layer's messaging authz checks to enforce against a distinct identity
// (rather than trusting body/query-derived caller args) set both
// headers on every request.
//
// These headers are the contract for carrying caller identity end-to-end; the
// service layer's authz checks (Inbox caller-match, Thread participant filter,
// Ack/Resolve recipient check, UnreadCount caller-match) then enforce that the
// caller matches the row's intended recipient. When no identity is stamped the
// service falls back to legacy trust-the-body behavior — see
// messaging.UnreadCount and the HTTP handler comments. Since CW-20261011-0036
// the headers are only honored for an agent credential bound to that session;
// every other caller's identity is its credential.
const (
	CallerSessionHeader = "X-Nanite-Caller-Session"
	CallerAgentHeader   = "X-Nanite-Caller-Agent"
)

// callerIdentityMiddleware reads the X-Nanite-Caller-Session and
// X-Nanite-Caller-Agent headers from the incoming request and, when
// BOTH are present and non-empty and the authenticated principal is an agent
// credential bound to exactly that session, stamps a messaging.CallerIdentity
// on the request context. Handlers read the identity via
// messaging.CallerFromCtx and pass it to the messaging.Service as the
// caller arg so authz checks compare against the header-supplied
// identity instead of the body/query-derived target identity.
//
// When either header is absent the middleware is a no-op — the context
// carries no CallerIdentity and handlers fall back to the pre-G-6.3
// behavior of using body/query as both target and caller. This
// preserves compatibility with existing FE/CLI clients while giving
// clients a path to stricter identity enforcement.
//
// Placement in the chain: sits inside the auth guard, so the principal is
// already authenticated when the headers are read. callerIdentity sits outside
// bodyLimit because header parsing is cheap and body-size caps only matter for
// request bodies.
func callerIdentityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(CallerSessionHeader)
		agentID := r.Header.Get(CallerAgentHeader)
		// Identity comes from the credential, never from a header
		// (CW-20261011-0036): only an agent credential bound to exactly this
		// session may assert a caller identity. Operator and client
		// credentials, and every unauthenticated-development request, cannot.
		if p, ok := apiauth.FromContext(r.Context()); ok && p.Kind == apiauth.KindAgent && p.BoundSessionID != "" && p.BoundSessionID == sessionID && agentID != "" {
			ctx := messaging.WithCaller(r.Context(), messaging.CallerIdentity{
				SessionID: sessionID,
				AgentID:   agentID,
			})
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
