package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	credentialhost "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability/host"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// This fake is a trusted host port, not a public enrollment implementation.
type trustedFixture struct {
	mu       sync.Mutex
	bindings map[string]VerifiedBinding
}

func (f *trustedFixture) VerifyBinding(ctx context.Context, proof []byte) (VerifiedBinding, error) {
	if err := ctx.Err(); err != nil {
		return VerifiedBinding{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.bindings[string(proof)]
	if !ok {
		return VerifiedBinding{}, ErrUnauthenticated
	}
	return v, nil
}
func fixtureCredentials(t *testing.T, capacity int) (*Credentials, *trustedFixture, *credentialhost.CredentialStore) {
	t.Helper()
	owner := capability.RuntimeIdentity{HostInstance: "candidate-test", OwnerID: "host-owner", OwnerGeneration: 1}
	store, err := credentialhost.NewCredentialStore(credentialhost.CredentialConfig{HostInstance: owner.HostInstance, Audience: "candidate-test", MaxLease: time.Minute, MaxLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.ActivateOwner(owner); err != nil {
		t.Fatal(err)
	}
	fixture := &trustedFixture{bindings: map[string]VerifiedBinding{}}
	for _, id := range []string{"one", "two"} {
		fixture.bindings["proof-"+id] = VerifiedBinding{Caller: VerifiedCaller{ActorID: "verified-" + id, SessionID: "session-" + id}, Claims: credentialhost.CredentialClaims{
			Subject: credentialhost.Subject{Kind: credentialhost.MCPProxyClient, ID: "proxy-" + id}, Owner: owner, Audience: "candidate-test", GrantIDs: []string{"reviewed-mcp"},
			Scopes:          map[string]capability.Scope{"reviewed-mcp": {Allowlists: map[string][]string{"server_tools": {"fixture/reviewed@definition-1"}, "sessions": {"session-" + id}}, Limits: map[string]int64{"concurrency": 1}}},
			CapabilityNames: map[string]string{"reviewed-mcp": capability.MCPReach}, GrantExpiresAt: map[string]time.Time{"reviewed-mcp": time.Now().UTC().Add(time.Minute)},
		}}
	}
	credentials, err := NewCredentials(fixture, store, CredentialLimits{Capacity: capacity, MaxTTL: time.Minute, MaxBindingBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(credentials.Close)
	return credentials, fixture, store
}
func issueFixture(t *testing.T, c *Credentials, id string) string {
	t.Helper()
	issued, err := c.Issue(context.Background(), []byte("proof-"+id), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := issued.Reveal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issued.Reveal(); err == nil {
		t.Fatal("credential revealed twice")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", issued, issued), token) {
		t.Fatal("formatting exposed credential")
	}
	raw, err := json.Marshal(issued)
	if err != nil || strings.Contains(string(raw), token) {
		t.Fatal("JSON exposed credential")
	}
	return token
}

func TestCredentialRequiresActualTrustedPortAndReviewedClaims(t *testing.T) {
	c, f, store := fixtureCredentials(t, 1)
	if _, err := c.Issue(context.Background(), []byte(`{"actor_id":"verified-one","session_id":"session-one"}`), time.Minute); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("claimed actor enrolled: %v", err)
	}
	missing, err := NewCredentials(nil, store, CredentialLimits{Capacity: 1, MaxTTL: time.Minute, MaxBindingBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.Issue(context.Background(), []byte("proof-one"), time.Minute); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("missing trusted issuer did not refuse")
	}
	token := issueFixture(t, c, "one")
	if _, err := c.Issue(context.Background(), []byte("proof-two"), time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatal("capacity bound ignored")
	}
	caller, permit, done, err := c.Acquire(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if caller.ActorID != "verified-one" || caller.SessionID != "session-one" {
		t.Fatal("verified caller lost")
	}
	authority, err := RevalidateAuthority(permit)
	if err != nil || !authority.Claims.Scopes["reviewed-mcp"].Allows("sessions", "session-one") {
		t.Fatal("execution owner lost actual reviewed scopes")
	}
	f.mu.Lock()
	delete(f.bindings, "proof-one")
	f.mu.Unlock()
	if _, _, _, err := c.Acquire(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("revoked host proof authenticated")
	}
	select {
	case <-permit.Done():
	case <-time.After(time.Second):
		t.Fatal("host proof revocation did not cancel dispatch")
	}
}

func TestSDKGenerationFenceCancelsProxyLease(t *testing.T) {
	c, f, store := fixtureCredentials(t, 2)
	token := issueFixture(t, c, "one")
	_, permit, done, err := c.Acquire(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	owner := f.bindings["proof-one"].Claims.Owner
	owner.OwnerGeneration++
	if err := store.ActivateOwner(owner); err != nil {
		t.Fatal(err)
	}
	select {
	case <-permit.Done():
	case <-time.After(time.Second):
		t.Fatal("SDK generation fence did not cancel proxy")
	}
	if _, _, _, err := c.Acquire(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("old-generation credential revived")
	}
}

func TestCredentialExpiryAndCloseRefuseRevival(t *testing.T) {
	c, _, _ := fixtureCredentials(t, 2)
	issued, err := c.Issue(context.Background(), []byte("proof-one"), 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	token, err := issued.Reveal()
	if err != nil {
		t.Fatal(err)
	}
	_, permit, done, err := c.Acquire(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	select {
	case <-permit.Done():
	case <-time.After(time.Second):
		t.Fatal("SDK credential expiry did not cancel proxy")
	}
	if _, _, _, err := c.Acquire(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("expired credential authenticated")
	}
	c.Close()
	if _, err := c.Issue(context.Background(), []byte("proof-two"), time.Minute); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("closed adapter minted a credential")
	}
}

type ownerFixture struct {
	call    func(context.Context, VerifiedCaller, CallRequest) (*mcp.ToolResult, error)
	catalog func(context.Context, VerifiedCaller) (Catalog, error)
}

func (o *ownerFixture) Catalog(ctx context.Context, c VerifiedCaller) (Catalog, error) {
	return o.catalog(ctx, c)
}
func (o *ownerFixture) Call(ctx context.Context, c VerifiedCaller, r CallRequest) (*mcp.ToolResult, error) {
	return o.call(ctx, c, r)
}
func fixtureHandler(t *testing.T, c *Credentials, o ExecutionOwner) *Handler {
	t.Helper()
	h, err := NewHandler(c, o, TransportLimits{MaxRequestBytes: 1024, MaxResponseBytes: 2048, MaxActiveCalls: 2, MaxCallDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func request(h *Handler, token, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestBridgeKeepsVerifiedCallerExpectedBindingAndToolErrors(t *testing.T) {
	c, _, _ := fixtureCredentials(t, 2)
	token := issueFixture(t, c, "one")
	owner := &ownerFixture{call: func(ctx context.Context, caller VerifiedCaller, call CallRequest) (*mcp.ToolResult, error) {
		if caller.ActorID != "verified-one" || caller.SessionID != "session-one" {
			t.Fatal("request claims changed caller")
		}
		if _, err := RevalidateAuthority(ctx); err != nil {
			t.Fatal(err)
		}
		if call.Binding != "reviewed-generation-1" {
			return nil, subprocess.ErrStaleBinding
		}
		if call.OperationKey != "same-input-receipt" {
			t.Fatal("operation key dropped")
		}
		return &mcp.ToolResult{IsError: true, Content: []mcp.ToolContent{{Type: "text", Text: "business refusal"}}}, nil
	}, catalog: func(context.Context, VerifiedCaller) (Catalog, error) {
		return Catalog{Version: 1, Revision: "authoritative-empty"}, nil
	}}
	h := fixtureHandler(t, c, owner)
	body := `{"version":1,"request_id":"call-1","name":"reviewed","tool_binding":"reviewed-generation-1","arguments":{},"operation_key":"same-input-receipt"}`
	w := request(h, token, "/api/mcp/v1/call", body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatalf("tool error became transport failure: %d %s", w.Code, w.Body.String())
	}
	w = request(h, token, "/api/mcp/v1/call", strings.ReplaceAll(body, "reviewed-generation-1", "old-generation"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "stale_binding") {
		t.Fatal("stale binding did not force discovery")
	}
	w = request(h, token, "/api/mcp/v1/list", `{"version":1}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tools":[]`) {
		t.Fatal("authoritative empty catalog lost")
	}
	h.owner = nil
	if w := request(h, token, "/api/mcp/v1/list", `{"version":1}`); w.Code != http.StatusServiceUnavailable {
		t.Fatal("offline owner looked like an empty catalog")
	}
}

func TestBridgeRejectsRawBypassesBeforeExecution(t *testing.T) {
	c, _, _ := fixtureCredentials(t, 1)
	token := issueFixture(t, c, "one")
	owner := &ownerFixture{call: func(context.Context, VerifiedCaller, CallRequest) (*mcp.ToolResult, error) {
		t.Fatal("invalid request reached execution")
		return nil, nil
	}, catalog: func(context.Context, VerifiedCaller) (Catalog, error) {
		t.Fatal("invalid request reached catalog")
		return Catalog{}, nil
	}}
	h := fixtureHandler(t, c, owner)
	for _, body := range []string{`{"version":1,"actor_id":"claimed"}`, `{"version":1}{"version":1}`, `{"version":2}`, `{"version":1,"padding":"` + strings.Repeat("x", 1100) + `"}`} {
		if w := request(h, token, "/api/mcp/v1/list", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid body admitted: %d", w.Code)
		}
	}
	if w := request(h, "wrong-token", "/api/mcp/v1/list", `{"version":1}`); w.Code != http.StatusUnauthorized {
		t.Fatal("wrong credential admitted")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/mcp/v1/list", strings.NewReader(`{"version":1}`))
	r.RemoteAddr = "192.0.2.1:2345"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("forwarded header bypassed loopback boundary")
	}
}

func TestBridgeCancellationCannotReachAnotherCredential(t *testing.T) {
	c, _, _ := fixtureCredentials(t, 2)
	one := issueFixture(t, c, "one")
	two := issueFixture(t, c, "two")
	started := make(chan struct{})
	finished := make(chan *httptest.ResponseRecorder, 1)
	owner := &ownerFixture{call: func(ctx context.Context, _ VerifiedCaller, _ CallRequest) (*mcp.ToolResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ErrUnknownOutcome
	}}
	h := fixtureHandler(t, c, owner)
	go func() {
		finished <- request(h, one, "/api/mcp/v1/call", `{"version":1,"request_id":"same-id","name":"reviewed","tool_binding":"current","arguments":{}}`)
	}()
	<-started
	foreign := request(h, two, "/api/mcp/v1/cancel", `{"version":1,"request_id":"same-id"}`)
	if !strings.Contains(foreign.Body.String(), `"canceled":false`) {
		t.Fatal("foreign credential canceled a call")
	}
	select {
	case <-finished:
		t.Fatal("foreign cancellation reached execution")
	default:
	}
	own := request(h, one, "/api/mcp/v1/cancel", `{"version":1,"request_id":"same-id"}`)
	if !strings.Contains(own.Body.String(), `"canceled":true`) {
		t.Fatal("own cancellation missed its call")
	}
	select {
	case result := <-finished:
		if !strings.Contains(result.Body.String(), "unknown_outcome") {
			t.Fatal("cancellation implied mutation rollback")
		}
	case <-time.After(time.Second):
		t.Fatal("owned call did not cancel")
	}
}
