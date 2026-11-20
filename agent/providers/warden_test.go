package providers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/agent/authored"
	"github.com/hollis-labs/nanite/agent/providers"
)

// fakeWarden is Warden's token endpoint with a rotating parent, grants for
// llm-gateway and warden, and the grant-request API.
type fakeWarden struct {
	mu       sync.Mutex
	parent   string
	n        int
	mints    int
	requests int
}

func newFakeWarden(t *testing.T, parent string) (*fakeWarden, *httptest.Server) {
	t.Helper()
	f := &fakeWarden{parent: parent}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeWarden) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/token":
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != f.parent {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		aud := r.Form.Get("audience")
		if aud != "llm-gateway" && aud != "warden" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_scope"})
			return
		}
		f.mints++
		f.n++
		f.parent = fixtureValue(fmt.Sprintf("parent-%d", f.n))
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fixtureValue(fmt.Sprintf("access-%s-%d", aud, f.n)), "expires_in": 900, "refresh_token": f.parent})
	case "/api/v1/requests":
		f.requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "req-1", "status": "pending", "tier": "elevated"})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func identity(t *testing.T, wardenURL string) (authored.Identity, string) {
	t.Helper()
	cred := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(cred, fixtureValue("parent-0")); err != nil {
		t.Fatal(err)
	}
	id, err := authored.ParseIdentity([]byte("principal: agent:workbench-test\nwarden:\n  url: "+wardenURL+"\n  credential_file: "+cred+"\n"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id, cred
}

// TestNewNamedWithWarden: a named provider with auth warden sends a minted
// token as its bearer, rotates the parent credential, puts the token in the
// redaction scope, and shares one token cache with every provider built
// with the same Warden.
func TestNewNamedWithWarden(t *testing.T) {
	fw, ws := newFakeWarden(t, fixtureValue("parent-0"))
	srv, last := server(t)
	id, cred := identity(t, ws.URL)
	scope := redact.NewScope()
	w, err := providers.NewWarden(id, providers.WardenOptions{Redactor: scope})
	if err != nil {
		t.Fatal(err)
	}
	spec := authored.ProviderSpec{Name: "gateway", Wire: authored.WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: authored.AuthWarden,
		Audience: "llm-gateway", Scopes: []string{"llm-gateway.chat"}}
	opts := providers.Options{Redactor: scope, Warden: w}
	first, err := providers.NewNamed(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := providers.NewNamed(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	minted := fixtureValue("access-llm-gateway-1")
	for i, p := range []llmcontracts.Provider{first, second} {
		if got, err := p.Complete(t.Context(), request); err != nil || got != "answer" {
			t.Fatalf("provider %d: Complete = %q, %v", i, got, err)
		}
		if s := last(); s.auth != "Bearer "+minted || s.apiKey != "" {
			t.Fatalf("provider %d: server saw %+v", i, s)
		}
	}
	fw.mu.Lock()
	mints := fw.mints
	fw.mu.Unlock()
	if mints != 1 {
		t.Fatalf("%d mints for two providers sharing a Warden; want 1", mints)
	}
	if got, err := tokenfile.Read(cred); err != nil || got != fixtureValue("parent-1") {
		t.Fatalf("parent after the mint = %q, %v", got, err)
	}
	if !scrubs(t, scope, minted) {
		t.Fatal("the minted token is not in the caller's redaction scope")
	}
}

// TestNewNamedWardenNeedsAWarden: auth warden without Options.Warden is
// refused when the provider is built, naming what is missing.
func TestNewNamedWardenNeedsAWarden(t *testing.T) {
	_, err := providers.NewNamed(authored.ProviderSpec{Name: "gateway", Wire: authored.WireOpenAIChat, BaseURL: "https://gateway.example/v1",
		Auth: authored.AuthWarden, Audience: "llm-gateway"}, providers.Options{})
	if err == nil || !strings.Contains(err.Error(), "Options.Warden") {
		t.Fatalf("err = %v; want one naming Options.Warden", err)
	}
}

// TestNewWardenRefusesAnIdentityWithoutWarden: an identity with no warden
// section, or an invalid one, builds no Warden.
func TestNewWardenRefusesAnIdentityWithoutWarden(t *testing.T) {
	if _, err := providers.NewWarden(authored.Identity{Principal: "agent:workbench-test"}, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden from an identity without a warden section")
	}
	cred := filepath.Join(t.TempDir(), "agent.cred")
	bad := authored.Identity{Principal: "nobody", Warden: &authored.WardenIdentity{URL: "https://warden.example", CredentialFile: cred}}
	if _, err := providers.NewWarden(bad, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden from an identity with an invalid principal")
	}
	insecure := authored.Identity{Principal: "agent:workbench-test", Warden: &authored.WardenIdentity{URL: "http://warden.example", CredentialFile: cred}}
	if _, err := providers.NewWarden(insecure, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden for a plain-http, non-loopback URL")
	}
}

// TestNewNamedWardenReportsAWaitingGrant: with no grant for the provider's
// audience, the request fails, a grant request is filed, and OnAuthWaiting
// sees it as an agent.AuthWaiting.
func TestNewNamedWardenReportsAWaitingGrant(t *testing.T) {
	fw, ws := newFakeWarden(t, fixtureValue("parent-0"))
	srv, _ := server(t)
	id, _ := identity(t, ws.URL)
	var mu sync.Mutex
	var seen []agent.AuthWaiting
	w, err := providers.NewWarden(id, providers.WardenOptions{OnAuthWaiting: func(a agent.AuthWaiting) {
		mu.Lock()
		seen = append(seen, a)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := providers.NewNamed(authored.ProviderSpec{Name: "gateway", Wire: authored.WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: authored.AuthWarden,
		Audience: "other-gateway", Scopes: []string{"other.chat"}}, providers.Options{Warden: w})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Complete(t.Context(), request); err == nil || !strings.Contains(err.Error(), "req-1") {
		t.Fatalf("Complete err = %v; want one naming the grant request", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0].RequestID != "req-1" || seen[0].Audience != "other-gateway" || seen[0].Status != "pending" {
		t.Fatalf("OnAuthWaiting saw %+v", seen)
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.requests != 1 {
		t.Fatalf("%d grant requests filed; want 1", fw.requests)
	}
}
