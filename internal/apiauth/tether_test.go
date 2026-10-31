package apiauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mintJWT(t *testing.T, priv ed25519.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": kid})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	return in + "." + b64(ed25519.Sign(priv, []byte(in)))
}

func tetherFixture(t *testing.T) (*TetherVerifier, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "kid": "k1", "alg": "EdDSA", "use": "sig", "x": b64(pub)}}})
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "jwks.json")
	if err := os.WriteFile(keyFile, jwks, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	v, err := NewTetherVerifier(ctx, TetherConfig{BootstrapKeyFile: keyFile})
	if err != nil || v == nil {
		t.Fatalf("verifier: %v %v", v, err)
	}
	return v, priv, keyFile
}

func claims(aud string, exp time.Time) map[string]any {
	return map[string]any{"iss": "tether", "sub": "svc/tachyon", "aud": aud, "scope": "nanite.read", "exp": exp.Unix(), "jti": "j1", "kind": "service"}
}

func TestTetherVerifierDisabledByDefault(t *testing.T) {
	v, err := NewTetherVerifier(context.Background(), TetherConfig{})
	if v != nil || err != nil {
		t.Fatalf("want nil,nil got %v %v", v, err)
	}
}

func TestTetherVerifierRejectsBadIssuerURL(t *testing.T) {
	if _, err := NewTetherVerifier(context.Background(), TetherConfig{Issuer: "unix:/x"}); err == nil {
		t.Fatal("want error")
	}
}

func TestTetherVerifierAcceptsAndScopes(t *testing.T) {
	v, priv, _ := tetherFixture(t)
	p, err := v.VerifyServiceToken(context.Background(), mintJWT(t, priv, "k1", claims("nanite", time.Now().Add(time.Hour))))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has(ScopeRead) || !p.Has(ScopeOperate) || !p.Has(ScopeTools) || p.Has(ScopeAdmin) || p.Has(ScopeMaintain) {
		t.Fatalf("scopes %v", p.Scopes)
	}
}

func TestTetherVerifierRejects(t *testing.T) {
	v, priv, _ := tetherFixture(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for name, tok := range map[string]string{
		"wrong audience": mintJWT(t, priv, "k1", claims("tesseract", time.Now().Add(time.Hour))),
		"expired":        mintJWT(t, priv, "k1", claims("nanite", time.Now().Add(-time.Hour))),
		"wrong key":      mintJWT(t, other, "k1", claims("nanite", time.Now().Add(time.Hour))),
		"garbage":        "not-a-jwt",
	} {
		if _, err := v.VerifyServiceToken(context.Background(), tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTetherVerifierDenyList(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "OKP", "crv": "Ed25519", "kid": "k1", "alg": "EdDSA", "use": "sig", "x": b64(pub)}}})
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(jwks) })
	mux.HandleFunc("/auth/denylist", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jti":["revoked"],"sub":[],"sid":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v, err := NewTetherVerifier(ctx, TetherConfig{Issuer: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := claims("nanite", time.Now().Add(time.Hour))
	c["iss"] = srv.URL
	if _, err := v.VerifyServiceToken(ctx, mintJWT(t, priv, "k1", c)); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	c["jti"] = "revoked"
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := v.VerifyServiceToken(ctx, mintJWT(t, priv, "k1", c)); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("revoked token still accepted")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestGuardServiceTokens(t *testing.T) {
	h := newHarness(t)
	_, secret := h.mint(KindClient, ScopeRead)
	v, priv, _ := tetherFixture(t)
	good := mintJWT(t, priv, "k1", claims("nanite", time.Now().Add(time.Hour)))
	bad := mintJWT(t, priv, "k1", claims("tesseract", time.Now().Add(time.Hour)))

	// Verifier off (default): a service token is just an invalid credential.
	if got := h.status("GET", "/api/sessions", good); got != http.StatusUnauthorized {
		t.Fatalf("off: %d", got)
	}
	h.guard.ServiceTokens = v
	if got := h.status("GET", "/api/sessions", good); got != http.StatusOK {
		t.Fatalf("read: %d", got)
	}
	if got := h.status("POST", "/api/sessions", good); got != http.StatusOK {
		t.Fatalf("operate: %d", got)
	}
	if got := h.status("POST", "/api/tools/call", good); got != http.StatusOK {
		t.Fatalf("tools: %d", got)
	}
	if got := h.status("PUT", "/api/settings", good); got != http.StatusForbidden {
		t.Fatalf("maintain must be refused: %d", got)
	}
	if got := h.status("GET", "/api/sessions", bad); got != http.StatusUnauthorized {
		t.Fatalf("wrong audience: %d", got)
	}
	// Existing credentials are unaffected.
	if got := h.status("GET", "/api/sessions", secret); got != http.StatusOK {
		t.Fatalf("client credential: %d", got)
	}
	if got := h.status("GET", "/api/sessions", ""); got != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", got)
	}
}
