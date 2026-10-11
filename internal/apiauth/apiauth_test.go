package apiauth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type harness struct {
	t      *testing.T
	store  *Store
	guard  *Guard
	srv    *httptest.Server
	live   map[string]bool
	hit    map[string]int
	blocks chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := &harness{t: t, store: st, live: map[string]bool{"sess-1": true}, hit: map[string]int{}, blocks: make(chan struct{}, 8)}
	mux := http.NewServeMux()
	mark := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			h.hit[name]++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(name))
		}
	}
	mux.HandleFunc("GET /api/health", mark("health"))
	mux.HandleFunc("GET /api/sessions", mark("list"))
	mux.HandleFunc("POST /api/sessions", mark("create"))
	mux.HandleFunc("POST /api/tools/call", mark("tools"))
	mux.HandleFunc("PUT /api/settings", mark("settings"))
	mux.HandleFunc("POST /api/agents/{id}/retire", mark("retire"))
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", mark("hostquery"))
	mux.HandleFunc("/api/", mark("fallback"))
	mux.HandleFunc("GET /api/stream/hang", func(w http.ResponseWriter, r *http.Request) {
		h.blocks <- struct{}{}
		<-r.Context().Done()
	})
	mux.HandleFunc("/", mark("spa"))
	g := NewGuard(st)
	g.Pattern = func(r *http.Request) string { _, p := mux.Handler(r); return p }
	g.SessionLive = func(_ context.Context, id string) bool { return h.live[id] }
	h.guard = g
	(&Handlers{Guard: g}).Register(mux)
	h.srv = httptest.NewServer(g.Middleware(mux))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) mint(kind Kind, scopes ...Scope) (Credential, string) {
	h.t.Helper()
	p := CreateParams{Kind: kind, Label: string(kind) + "-test", Scopes: scopes}
	if kind == KindAgent {
		p.BoundSessionID = "sess-1"
	}
	c, secret, err := h.store.Create(context.Background(), p)
	if err != nil {
		h.t.Fatal(err)
	}
	return c, secret
}

func (h *harness) do(method, path, bearer string, mod func(*http.Request)) (*http.Response, string) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if mod != nil {
		mod(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.String()
}

func (h *harness) status(method, path, bearer string) int {
	h.t.Helper()
	resp, _ := h.do(method, path, bearer, nil)
	return resp.StatusCode
}

func TestNegative_NoCredentialIsRefusedOnEveryProtectedRoute(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/sessions"}, {"POST", "/api/sessions"}, {"POST", "/api/tools/call"}, {"PUT", "/api/settings"},
		{"POST", "/api/agents/x/retire"}, {"GET", "/api/never-registered"}, {"GET", "/api/auth/credentials"}, {"GET", "/api/stream/hang"},
	} {
		if got := h.status(c.method, c.path, ""); got != http.StatusUnauthorized {
			t.Errorf("%s %s without credential = %d, want 401", c.method, c.path, got)
		}
	}
	if h.hit["list"]+h.hit["create"]+h.hit["tools"]+h.hit["settings"]+h.hit["retire"]+h.hit["fallback"] != 0 {
		t.Fatalf("a handler ran without a credential: %v", h.hit)
	}
}

func TestNegative_BadCredentials(t *testing.T) {
	h := newHarness(t)
	_, op := h.mint(KindOperator)
	for name, secret := range map[string]string{
		"garbage":       "hunter2",
		"wrong shape":   "nan_operator_short",
		"unknown":       "nan_operator_" + strings.Repeat("A", 43),
		"wrong kind":    strings.Replace(op, "nan_operator_", "nan_client_", 1),
		"bearer prefix": "Bearer " + op,
	} {
		if got := h.status("GET", "/api/sessions", secret); got != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, got)
		}
	}
	if got := h.status("GET", "/api/sessions", op); got != http.StatusOK {
		t.Fatalf("control: valid operator = %d", got)
	}
	// Basic auth is not an accepted scheme any more.
	resp, _ := h.do("GET", "/api/sessions", "", func(r *http.Request) { r.SetBasicAuth("admin", "secret") })
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("basic auth = %d, want 401", resp.StatusCode)
	}
}

func TestScopes_ClientAndAgentLimits(t *testing.T) {
	h := newHarness(t)
	_, readOnly := h.mint(KindClient, ScopeRead)
	_, operate := h.mint(KindClient, ScopeRead, ScopeOperate)
	_, agent := h.mint(KindAgent)
	_, op := h.mint(KindOperator)

	cases := []struct {
		name         string
		secret       string
		method, path string
		want         int
	}{
		{"read client reads", readOnly, "GET", "/api/sessions", 200},
		{"read client cannot create", readOnly, "POST", "/api/sessions", 403},
		{"operate client creates", operate, "POST", "/api/sessions", 200},
		{"operate client cannot change settings", operate, "PUT", "/api/settings", 403},
		{"operate client cannot retire", operate, "POST", "/api/agents/x/retire", 403},
		{"agent calls tools", agent, "POST", "/api/tools/call", 200},
		{"agent cannot list sessions", agent, "GET", "/api/sessions", 403},
		{"agent cannot create", agent, "POST", "/api/sessions", 403},
		{"agent cannot reach admin", agent, "GET", "/api/auth/credentials", 403},
		{"client cannot reach admin", operate, "GET", "/api/auth/credentials", 403},
		{"operator does everything", op, "POST", "/api/agents/x/retire", 200},
		{"unknown write is admin: client refused", operate, "POST", "/api/never-heard-of-it", 403},
		{"unknown write is admin: operator", op, "POST", "/api/never-heard-of-it", 200},
	}
	for _, c := range cases {
		if got := h.status(c.method, c.path, c.secret); got != c.want {
			t.Errorf("%s: %s %s = %d, want %d", c.name, c.method, c.path, got, c.want)
		}
	}
}

func TestCreateRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, _, err := h.store.Create(ctx, CreateParams{Kind: KindClient, Label: "x", Scopes: []Scope{ScopeRead, ScopeAdmin}}); err == nil {
		t.Error("a client credential with admin was created")
	}
	if _, _, err := h.store.Create(ctx, CreateParams{Kind: KindClient, Label: "x"}); err == nil {
		t.Error("a client credential with no scopes was created")
	}
	if _, _, err := h.store.Create(ctx, CreateParams{Kind: KindAgent, Label: "x"}); err == nil {
		t.Error("an unbound agent credential was created")
	}
	c, _, err := h.store.Create(ctx, CreateParams{Kind: KindAgent, Label: "x", BoundSessionID: "s", Scopes: AllScopes()})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Scopes) != 1 || c.Scopes[0] != ScopeTools {
		t.Errorf("agent scopes = %v, want only tools", c.Scopes)
	}
}

func TestPublicAndExemptRoutes(t *testing.T) {
	h := newHarness(t)
	if got := h.status("GET", "/api/health", ""); got != 200 {
		t.Errorf("health = %d, want 200", got)
	}
	if got := h.status("GET", "/", ""); got != 200 {
		t.Errorf("SPA shell = %d, want 200 (it carries no data)", got)
	}
	// Plugin host queries carry their own bearer; the guard leaves them alone.
	if got := h.status("GET", "/api/plugin-host/query/x", ""); got != 200 {
		t.Errorf("plugin-host query = %d, want 200 (own bearer)", got)
	}
}

func TestHostValidation(t *testing.T) {
	h := newHarness(t)
	_, op := h.mint(KindOperator)
	resp, _ := h.do("GET", "/api/sessions", op, func(r *http.Request) { r.Host = "evil.example.com" })
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host = %d, want 403 (DNS rebinding)", resp.StatusCode)
	}
	resp, _ = h.do("GET", "/api/health", "", func(r *http.Request) { r.Host = "rebind.attacker.test:8090" })
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host on a public route = %d, want 403", resp.StatusCode)
	}
}

func TestRevokeIsImmediateAndRotateReplaces(t *testing.T) {
	h := newHarness(t)
	c, secret := h.mint(KindClient, ScopeRead)
	if got := h.status("GET", "/api/sessions", secret); got != 200 {
		t.Fatalf("control = %d", got)
	}
	if ok, err := h.store.Revoke(context.Background(), c.ID); err != nil || !ok {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if got := h.status("GET", "/api/sessions", secret); got != 401 {
		t.Errorf("revoked credential = %d, want 401", got)
	}
	c2, s2 := h.mint(KindClient, ScopeRead)
	fresh, s3, err := h.store.Rotate(context.Background(), c2.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if h.status("GET", "/api/sessions", s2) != 401 || h.status("GET", "/api/sessions", s3) != 200 || fresh.ID == c2.ID {
		t.Error("rotate did not replace the credential")
	}
}

func TestExpiredCredentialIsRefused(t *testing.T) {
	h := newHarness(t)
	c, secret, err := h.store.Create(context.Background(), CreateParams{Kind: KindClient, Label: "short", Scopes: []Scope{ScopeRead}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	h.store.now = func() time.Time { return c.CreatedAt.Add(2 * time.Hour) }
	if got := h.status("GET", "/api/sessions", secret); got != 401 {
		t.Errorf("expired credential = %d, want 401", got)
	}
}

func TestAgentCredentialDiesWithItsSession(t *testing.T) {
	h := newHarness(t)
	_, agent := h.mint(KindAgent)
	if got := h.status("POST", "/api/tools/call", agent); got != 200 {
		t.Fatalf("control = %d", got)
	}
	h.live["sess-1"] = false
	if got := h.status("POST", "/api/tools/call", agent); got != 401 {
		t.Errorf("agent credential after its session ended = %d, want 401", got)
	}
}

func TestOpenStreamClosesOnRevoke(t *testing.T) {
	h := newHarness(t)
	c, secret := h.mint(KindClient, ScopeRead)
	done := make(chan struct{})
	go func() {
		_, _ = h.do("GET", "/api/stream/hang", secret, nil)
		close(done)
	}()
	select {
	case <-h.blocks:
	case <-time.After(5 * time.Second):
		t.Fatal("stream handler never started")
	}
	if _, err := h.store.Revoke(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("open stream was not closed after the credential was revoked")
	}
}

func TestInsecureModeIsExplicit(t *testing.T) {
	h := newHarness(t)
	if got := h.status("GET", "/api/sessions", ""); got != 401 {
		t.Fatalf("default = %d, want 401", got)
	}
	h.guard.Insecure = true
	if got := h.status("GET", "/api/sessions", ""); got != 200 {
		t.Errorf("insecure flag = %d, want 200", got)
	}
	resp, _ := h.do("GET", "/api/sessions", "", func(r *http.Request) { r.Host = "evil.example.com" })
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("insecure mode still validates Host: %d, want 403", resp.StatusCode)
	}
}

func postJSON(h *harness, path, bearer string, body any, mod func(*http.Request)) (*http.Response, string) {
	raw, _ := json.Marshal(body)
	return h.do("POST", path, bearer, func(r *http.Request) {
		r.Body = http.NoBody
		r.Body = readCloser(raw)
		r.ContentLength = int64(len(raw))
		r.Header.Set("Content-Type", "application/json")
		if mod != nil {
			mod(r)
		}
	})
}

type rc struct{ *bytes.Reader }

func (rc) Close() error { return nil }

func readCloser(b []byte) rc { return rc{bytes.NewReader(b)} }

func TestUISessionLoginCSRFAndLogout(t *testing.T) {
	h := newHarness(t)
	_, op := h.mint(KindOperator)

	// A bad token does not log in.
	if resp, _ := postJSON(h, "/api/auth/login", "", map[string]string{"token": "nope"}, nil); resp.StatusCode != 401 {
		t.Fatalf("bad login = %d, want 401", resp.StatusCode)
	}
	// An agent credential cannot log in.
	_, agent := h.mint(KindAgent)
	if resp, _ := postJSON(h, "/api/auth/login", "", map[string]string{"token": agent}, nil); resp.StatusCode != 401 {
		t.Fatalf("agent login = %d, want 401", resp.StatusCode)
	}
	resp, body := postJSON(h, "/api/auth/login", "", map[string]string{"token": op}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie missing or weak: %+v", cookie)
	}
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	withCookie := func(r *http.Request) { r.AddCookie(cookie) }

	if resp, _ := h.do("GET", "/api/sessions", "", withCookie); resp.StatusCode != 200 {
		t.Errorf("cookie read = %d, want 200", resp.StatusCode)
	}
	// Unsafe request: no CSRF token is refused.
	if resp, _ := h.do("POST", "/api/sessions", "", withCookie); resp.StatusCode != 403 {
		t.Errorf("cookie write without CSRF = %d, want 403", resp.StatusCode)
	}
	// Wrong CSRF token.
	if resp, _ := h.do("POST", "/api/sessions", "", func(r *http.Request) { r.AddCookie(cookie); r.Header.Set(CSRFHeader, "wrong") }); resp.StatusCode != 403 {
		t.Errorf("cookie write with wrong CSRF = %d, want 403", resp.StatusCode)
	}
	// Right CSRF, foreign Origin.
	if resp, _ := h.do("POST", "/api/sessions", "", func(r *http.Request) {
		r.AddCookie(cookie)
		r.Header.Set(CSRFHeader, out.CSRF)
		r.Header.Set("Origin", "https://evil.example.com")
	}); resp.StatusCode != 403 {
		t.Errorf("cookie write from a foreign Origin = %d, want 403", resp.StatusCode)
	}
	// Right CSRF.
	if resp, _ := h.do("POST", "/api/sessions", "", func(r *http.Request) { r.AddCookie(cookie); r.Header.Set(CSRFHeader, out.CSRF) }); resp.StatusCode != 200 {
		t.Errorf("cookie write with CSRF = %d, want 200", resp.StatusCode)
	}
	// Logout ends the session.
	if resp, _ := h.do("POST", "/api/auth/logout", "", func(r *http.Request) { r.AddCookie(cookie); r.Header.Set(CSRFHeader, out.CSRF) }); resp.StatusCode != 204 {
		t.Fatalf("logout = %d, want 204", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/api/sessions", "", withCookie); resp.StatusCode != 401 {
		t.Errorf("cookie after logout = %d, want 401", resp.StatusCode)
	}
}

func TestLoginLinkIsSingleUse(t *testing.T) {
	h := newHarness(t)
	op, secret := h.mint(KindOperator)
	_, client := h.mint(KindClient, ScopeRead)
	if resp, _ := postJSON(h, "/api/auth/login-links", client, map[string]string{}, nil); resp.StatusCode != 403 {
		t.Fatalf("client minting a link = %d, want 403", resp.StatusCode)
	}
	resp, body := postJSON(h, "/api/auth/login-links", secret, map[string]string{}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("mint link = %d %s", resp.StatusCode, body)
	}
	var out struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	noRedirect := func() *http.Client {
		return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	get := func() *http.Response {
		req, _ := http.NewRequest("GET", h.srv.URL+out.Path, nil)
		r, err := noRedirect().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		return r
	}
	first := get()
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("first use = %d, want 303", first.StatusCode)
	}
	var got bool
	for _, c := range first.Cookies() {
		got = got || c.Name == CookieName
	}
	if !got {
		t.Error("login link did not set the session cookie")
	}
	if second := get(); second.StatusCode != http.StatusUnauthorized {
		t.Errorf("second use = %d, want 401 (single use)", second.StatusCode)
	}
	_ = op
}

func TestCredentialAPI_SecretShownOnceAndNeverListed(t *testing.T) {
	h := newHarness(t)
	_, op := h.mint(KindOperator)
	resp, body := postJSON(h, "/api/auth/credentials", op, map[string]any{"kind": "client", "label": "tangent", "scopes": []string{"read", "operate"}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	var created struct {
		ID, Secret string
	}
	_ = json.Unmarshal([]byte(strings.ReplaceAll(strings.ReplaceAll(body, `"id"`, `"ID"`), `"secret"`, `"Secret"`)), &created)
	if !strings.HasPrefix(created.Secret, "nan_client_") {
		t.Fatalf("no secret returned: %s", body)
	}
	if resp, _ := postJSON(h, "/api/auth/credentials", op, map[string]any{"kind": "client", "label": "x", "scopes": []string{"admin"}}, nil); resp.StatusCode != 400 {
		t.Errorf("client with admin = %d, want 400", resp.StatusCode)
	}
	if resp, _ := postJSON(h, "/api/auth/credentials", op, map[string]any{"kind": "operator", "label": "x"}, nil); resp.StatusCode != 400 {
		t.Errorf("creating an operator through the API = %d, want 400", resp.StatusCode)
	}
	_, list := h.do("GET", "/api/auth/credentials", op, nil)
	if strings.Contains(list, created.Secret) || strings.Contains(list, "secret_hash") || strings.Contains(list, op) {
		t.Fatalf("the credential list leaks a secret or hash: %s", list)
	}
	// The new credential works within its scopes.
	if h.status("GET", "/api/sessions", created.Secret) != 200 || h.status("PUT", "/api/settings", created.Secret) != 403 {
		t.Error("created client has the wrong scopes")
	}
	// The last operator credential cannot be revoked.
	creds, _ := h.store.List(context.Background(), false)
	for _, c := range creds {
		if c.Kind == KindOperator {
			if got := h.status("DELETE", "/api/auth/credentials/"+c.ID, op); got != http.StatusConflict {
				t.Errorf("revoking the last operator = %d, want 409", got)
			}
		}
	}
}

func TestBootstrapOperatorFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(ctx, filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	token := filepath.Join(dir, "sub", "operator.token")
	created, err := EnsureOperator(ctx, st, token, "")
	if err != nil || !created {
		t.Fatalf("first EnsureOperator = %v %v", created, err)
	}
	info, err := os.Stat(token)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v mode %v, want 0600", err, info)
	}
	secret, _ := ReadTokenFile(token)
	if _, err := st.Authenticate(ctx, secret); err != nil {
		t.Fatalf("token file secret does not authenticate: %v", err)
	}
	if created, err := EnsureOperator(ctx, st, token, ""); err != nil || created {
		t.Fatalf("second EnsureOperator = %v %v, want a no-op", created, err)
	}
	// The database holds a hash, never the secret.
	raw, _ := os.ReadFile(filepath.Join(dir, "auth.db")) // #nosec G304 -- a temp path this test created
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the plaintext secret is in the auth database file")
	}
	// Reset revokes the old operator and writes a new file.
	if err := ResetOperator(ctx, st, token); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Authenticate(ctx, secret); err == nil {
		t.Error("the old operator secret still works after reset")
	}
	// The env override imports an operator-shaped secret and writes no file.
	other := filepath.Join(dir, "other.token")
	st2, _ := Open(ctx, filepath.Join(dir, "auth2.db"))
	defer func() { _ = st2.Close() }()
	sec, _, _ := newSecret(KindOperator)
	if created, err := EnsureOperator(ctx, st2, other, sec); err != nil || created {
		t.Fatalf("override EnsureOperator = %v %v", created, err)
	}
	if _, err := st2.Authenticate(ctx, sec); err != nil {
		t.Errorf("override secret does not authenticate: %v", err)
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("the override wrote a token file")
	}
	if _, err := EnsureOperator(ctx, st2, other, "not-a-valid-secret"); err == nil {
		t.Error("a malformed override was accepted")
	}
}

func TestSecretShape(t *testing.T) {
	secret, hash, err := newSecret(KindClient)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "nan_client_") || len(secret) != len("nan_client_")+43 {
		t.Errorf("unexpected secret shape %q", secret)
	}
	if hash == secret || len(hash) != 64 {
		t.Error("hash looks wrong")
	}
	if k, err := kindOfSecret(secret); err != nil || k != KindClient {
		t.Errorf("kindOfSecret = %v %v", k, err)
	}
}

func TestAdminAuthenticator(t *testing.T) {
	auth := AdminAuthenticator(ScopeOperate)
	mk := func(p *Principal) *http.Request {
		r := httptest.NewRequest("POST", "/api/workflows/runs/r/approvals/s", nil)
		if p != nil {
			r = r.WithContext(WithPrincipal(r.Context(), *p))
		}
		return r
	}
	if _, ok := auth(mk(nil)); ok {
		t.Error("an unauthenticated request was accepted")
	}
	if _, ok := auth(mk(&Principal{Kind: KindClient, Label: "x", Scopes: []Scope{ScopeRead}})); ok {
		t.Error("a read-only principal was accepted for an operate action")
	}
	if got, ok := auth(mk(&Principal{Kind: KindClient, Label: "tangent", Scopes: []Scope{ScopeOperate}})); !ok || got != "tangent" {
		t.Errorf("operate client: %q %v, want its label", got, ok)
	}
	if got, ok := auth(mk(&Principal{Kind: KindOperator, Label: "operator"})); !ok || got != "operator" {
		t.Errorf("operator: %q %v", got, ok)
	}
}
