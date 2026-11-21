package providers_test

import (
	"fmt"
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
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

// newFakeWarden is the shared fake Warden (wardentest) with grants for
// llm-gateway and warden, and grant requests filed pending at the elevated
// tier.
func newFakeWarden(t *testing.T, parent string) (*wardentest.Fake, *httptest.Server) {
	t.Helper()
	f := wardentest.NewFake(t, parent).Grant("llm-gateway").Grant("warden").FileAs("pending", "elevated").
		Tokens(func(n int, aud string) string { return fixtureValue(fmt.Sprintf("access-%s-%d", aud, n)) }).
		Parents(func(n int) string { return fixtureValue(fmt.Sprintf("parent-%d", n)) })
	return f, f.Server
}

// identity returns an agent directory and its identity, whose credential
// file is outside the directory.
func identity(t *testing.T, wardenURL string) (string, authored.Identity, string) {
	t.Helper()
	cred := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(cred, fixtureValue("parent-0")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	id, err := authored.ParseIdentity([]byte("principal: agent:workbench-test\nwarden:\n  url: "+wardenURL+"\n  credential_file: "+cred+"\n"), dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, id, cred
}

// TestNewNamedWithWarden: a named provider with auth warden sends a minted
// token as its bearer, rotates the parent credential, puts the token in the
// redaction scope, and shares one token cache with every provider built
// with the same Warden.
func TestNewNamedWithWarden(t *testing.T) {
	fw, ws := newFakeWarden(t, fixtureValue("parent-0"))
	srv, last := server(t)
	dir, id, cred := identity(t, ws.URL)
	scope := redact.NewScope()
	w, err := providers.NewWarden(dir, id, providers.WardenOptions{Redactor: scope})
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
	mints := fw.Mints()
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
	dir := t.TempDir()
	if _, err := providers.NewWarden(dir, authored.Identity{Principal: "agent:workbench-test"}, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden from an identity without a warden section")
	}
	cred := filepath.Join(t.TempDir(), "agent.cred")
	bad := authored.Identity{Principal: "nobody", Warden: &authored.WardenIdentity{URL: "https://warden.example", CredentialFile: cred}}
	if _, err := providers.NewWarden(dir, bad, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden from an identity with an invalid principal")
	}
	insecure := authored.Identity{Principal: "agent:workbench-test", Warden: &authored.WardenIdentity{URL: "http://warden.example", CredentialFile: cred}}
	if _, err := providers.NewWarden(dir, insecure, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden for a plain-http, non-loopback URL")
	}
	good := authored.Identity{Principal: "agent:workbench-test", Warden: &authored.WardenIdentity{URL: "https://warden.example", CredentialFile: cred}}
	if _, err := providers.NewWarden("", good, providers.WardenOptions{}); err == nil {
		t.Fatal("built a Warden without the agent directory")
	}
	inside := authored.Identity{Principal: "agent:workbench-test", Warden: &authored.WardenIdentity{URL: "https://warden.example", CredentialFile: filepath.Join(dir, "agent.cred")}}
	if _, err := providers.NewWarden(dir, inside, providers.WardenOptions{}); err == nil || !strings.Contains(err.Error(), "outside the agent directory") {
		t.Fatalf("credential file inside the agent directory: err = %v", err)
	}
	if _, err := providers.NewWarden(dir, good, providers.WardenOptions{}); err != nil {
		t.Fatalf("valid identity refused: %v", err)
	}
}

// TestNewNamedWardenNeedsTheWardensScope: a provider whose Options.Redactor
// is not the Warden's own scope is refused, so a minted token can never miss
// the scope the caller's logs use.
func TestNewNamedWardenNeedsTheWardensScope(t *testing.T) {
	_, ws := newFakeWarden(t, fixtureValue("parent-0"))
	dir, id, _ := identity(t, ws.URL)
	spec := authored.ProviderSpec{Name: "gateway", Wire: authored.WireOpenAIChat, BaseURL: "https://gateway.example/v1", Auth: authored.AuthWarden,
		Audience: "llm-gateway", Scopes: []string{"llm-gateway.chat"}}
	scope := redact.NewScope()
	w, err := providers.NewWarden(dir, id, providers.WardenOptions{Redactor: scope})
	if err != nil {
		t.Fatal(err)
	}
	for name, other := range map[string]*redact.Scope{"another scope": redact.NewScope(), "no scope": nil} {
		if _, err := providers.NewNamed(spec, providers.Options{Redactor: other, Warden: w}); err == nil || !strings.Contains(err.Error(), "WardenOptions.Redactor") {
			t.Fatalf("%s: err = %v; want a refusal naming WardenOptions.Redactor", name, err)
		}
	}
	if _, err := providers.NewNamed(spec, providers.Options{Redactor: scope, Warden: w}); err != nil {
		t.Fatalf("same scope refused: %v", err)
	}
}

// TestNewNamedWardenReportsAWaitingGrant: with no grant for the provider's
// audience, the request fails, a grant request is filed, and OnAuthWaiting
// sees it as an agent.AuthWaiting.
func TestNewNamedWardenReportsAWaitingGrant(t *testing.T) {
	fw, ws := newFakeWarden(t, fixtureValue("parent-0"))
	srv, _ := server(t)
	dir, id, _ := identity(t, ws.URL)
	var mu sync.Mutex
	var seen []agent.AuthWaiting
	w, err := providers.NewWarden(dir, id, providers.WardenOptions{OnAuthWaiting: func(a agent.AuthWaiting) {
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
	if len(fw.Requests()) != 1 {
		t.Fatalf("%d grant requests filed; want 1", len(fw.Requests()))
	}
}
