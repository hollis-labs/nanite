package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRing(t *testing.T) (*KeyRing, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := NewKeyRing()
	if err := r.Add(TrustedKey{ID: "test-signer", Key: pub}); err != nil {
		t.Fatal(err)
	}
	return r, priv
}

func signedCatalogServer(t *testing.T, yamlBody []byte, priv ed25519.PrivateKey, hexSig bool) *httptest.Server {
	t.Helper()
	sig := ed25519.Sign(priv, yamlBody)
	var sigPayload []byte
	if hexSig {
		sigPayload = []byte(hex.EncodeToString(sig))
	} else {
		sigPayload = sig
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "")
		_, _ = w.Write(yamlBody)
	})
	mux.HandleFunc("/catalog.yaml.sig", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(sigPayload)
	})
	return httptest.NewServer(mux)
}

func TestSignedFetcher_HappyPath_RawSig(t *testing.T) {
	ring, priv := testRing(t)
	body := []byte("version: 1\nplugins: []\n")
	srv := signedCatalogServer(t, body, priv, false)
	defer srv.Close()

	f := &SignedFetcher{
		Ring: ring, SignerKeyID: "test-signer",
		CacheDir: t.TempDir(),
	}
	got, err := f.Fetch(context.Background(), srv.URL+"/catalog.yaml")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got.YAML) != string(body) {
		t.Errorf("yaml = %q", got.YAML)
	}
}

func TestSignedFetcher_HappyPath_HexSig(t *testing.T) {
	ring, priv := testRing(t)
	body := []byte("version: 1\n")
	srv := signedCatalogServer(t, body, priv, true)
	defer srv.Close()

	f := &SignedFetcher{Ring: ring, SignerKeyID: "test-signer", CacheDir: t.TempDir()}
	if _, err := f.Fetch(context.Background(), srv.URL+"/catalog.yaml"); err != nil {
		t.Fatalf("Fetch (hex): %v", err)
	}
}

func TestSignedFetcher_BadSig_Rejected(t *testing.T) {
	if devmodeBypassActive() {
		t.Skip("devmode build bypasses catalog sig verification")
	}
	ring, _ := testRing(t)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte("version: 1\n")
	srv := signedCatalogServer(t, body, otherPriv, false)
	defer srv.Close()

	f := &SignedFetcher{Ring: ring, SignerKeyID: "test-signer", CacheDir: t.TempDir()}
	_, err := f.Fetch(context.Background(), srv.URL+"/catalog.yaml")
	if err == nil || !strings.Contains(err.Error(), "signature on fetched catalog invalid") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignedFetcher_UnknownSigner_Rejected(t *testing.T) {
	if devmodeBypassActive() {
		t.Skip("devmode build bypasses catalog sig verification")
	}
	ring, priv := testRing(t)
	body := []byte("version: 1\n")
	srv := signedCatalogServer(t, body, priv, false)
	defer srv.Close()
	f := &SignedFetcher{Ring: ring, SignerKeyID: "missing"}
	_, err := f.Fetch(context.Background(), srv.URL+"/catalog.yaml")
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignedFetcher_RevokedKey_Rejected(t *testing.T) {
	if devmodeBypassActive() {
		t.Skip("devmode build bypasses catalog sig verification")
	}
	ring, priv := testRing(t)
	if err := ring.Revoke("test-signer"); err != nil {
		t.Fatal(err)
	}
	body := []byte("version: 1\n")
	srv := signedCatalogServer(t, body, priv, false)
	defer srv.Close()
	f := &SignedFetcher{Ring: ring, SignerKeyID: "test-signer"}
	_, err := f.Fetch(context.Background(), srv.URL+"/catalog.yaml")
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignedFetcher_CacheFallback(t *testing.T) {
	ring, priv := testRing(t)
	body := []byte("version: 1\nplugins: []\n")
	srv := signedCatalogServer(t, body, priv, false)

	cacheDir := t.TempDir()
	f := &SignedFetcher{Ring: ring, SignerKeyID: "test-signer", CacheDir: cacheDir}
	// First fetch populates cache.
	url := srv.URL + "/catalog.yaml"
	if _, err := f.Fetch(context.Background(), url); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	srv.Close()

	// Second fetch — network gone, must fall back.
	got, err := f.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("fallback fetch: %v", err)
	}
	if string(got.YAML) != string(body) {
		t.Error("cache returned wrong content")
	}
}

func TestSignedFetcher_CorruptCache_Removed(t *testing.T) {
	if devmodeBypassActive() {
		t.Skip("devmode build bypasses catalog sig verification")
	}
	ring, priv := testRing(t)
	body := []byte("version: 1\n")
	srv := signedCatalogServer(t, body, priv, false)

	cacheDir := t.TempDir()
	f := &SignedFetcher{Ring: ring, SignerKeyID: "test-signer", CacheDir: cacheDir}
	url := srv.URL + "/catalog.yaml"
	if _, err := f.Fetch(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	srv.Close()

	// Corrupt the cached YAML so the sig no longer verifies.
	ents, _ := os.ReadDir(cacheDir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".yaml") {
			_ = os.WriteFile(filepath.Join(cacheDir, e.Name()), []byte("tampered"), 0o600)
		}
	}
	_, err := f.Fetch(context.Background(), url)
	if err == nil || !strings.Contains(err.Error(), "cached catalog signature invalid") {
		t.Fatalf("err = %v", err)
	}
	// Cache removed.
	ents2, _ := os.ReadDir(cacheDir)
	for _, e := range ents2 {
		if !strings.Contains(e.Name(), ".lock") && !e.IsDir() {
			// Only lockfiles acceptable; every real cache file should be gone.
			if strings.Contains(e.Name(), ".yaml") {
				t.Errorf("cache file %q still present after corruption", e.Name())
			}
		}
	}
}

func TestSignedFetcher_NilRing(t *testing.T) {
	if devmodeBypassActive() {
		t.Skip("devmode build bypasses KeyRing; covered by TestSignedFetcher_Devmode_*")
	}
	f := &SignedFetcher{}
	_, err := f.Fetch(context.Background(), "http://x")
	if err == nil || !strings.Contains(err.Error(), "KeyRing is nil") {
		t.Fatalf("err = %v", err)
	}
}
