package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
	messaging "github.com/hollis-labs/substrate/mesh/messaging/mailbox"
)

// run sends one request through the middleware with an optional principal and
// reports the stamped identity.
func runCallerIdentity(t *testing.T, p *apiauth.Principal, session, agent string) (messaging.CallerIdentity, bool) {
	t.Helper()
	var got messaging.CallerIdentity
	var present bool
	handler := callerIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, present = messaging.CallerFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/ping", nil)
	if session != "" {
		req.Header.Set(CallerSessionHeader, session)
	}
	if agent != "" {
		req.Header.Set(CallerAgentHeader, agent)
	}
	if p != nil {
		req = req.WithContext(apiauth.WithPrincipal(req.Context(), *p))
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return got, present
}

// Identity comes from the credential, never from a header
// (CW-20261011-0036): only an agent credential bound to exactly the session
// the header names may stamp a caller identity.
func TestCallerIdentity_OnlyABoundAgentCredentialStampsIt(t *testing.T) {
	agent := &apiauth.Principal{Kind: apiauth.KindAgent, BoundSessionID: "sess-1"}
	got, present := runCallerIdentity(t, agent, "sess-1", "file-backend")
	if !present || got != (messaging.CallerIdentity{SessionID: "sess-1", AgentID: "file-backend"}) {
		t.Fatalf("bound agent credential: present=%v got=%+v", present, got)
	}
}

func TestCallerIdentity_HeadersAreIgnoredOtherwise(t *testing.T) {
	cases := []struct {
		name    string
		p       *apiauth.Principal
		session string
		agent   string
	}{
		{"no principal", nil, "sess-1", "file-backend"},
		{"operator asserting headers", &apiauth.Principal{Kind: apiauth.KindOperator}, "sess-1", "file-backend"},
		{"client asserting headers", &apiauth.Principal{Kind: apiauth.KindClient}, "sess-1", "file-backend"},
		{"agent bound to another session", &apiauth.Principal{Kind: apiauth.KindAgent, BoundSessionID: "sess-2"}, "sess-1", "file-backend"},
		{"agent with no bound session", &apiauth.Principal{Kind: apiauth.KindAgent}, "sess-1", "file-backend"},
		{"agent, session header only", &apiauth.Principal{Kind: apiauth.KindAgent, BoundSessionID: "sess-1"}, "sess-1", ""},
		{"agent, agent header only", &apiauth.Principal{Kind: apiauth.KindAgent, BoundSessionID: "sess-1"}, "", "file-backend"},
		{"no headers", &apiauth.Principal{Kind: apiauth.KindAgent, BoundSessionID: "sess-1"}, "", ""},
	}
	for _, c := range cases {
		if got, present := runCallerIdentity(t, c.p, c.session, c.agent); present {
			t.Errorf("%s: a caller identity was stamped: %+v", c.name, got)
		}
	}
}
