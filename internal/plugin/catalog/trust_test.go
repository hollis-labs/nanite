package catalog

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestKeyRing_RootKeySeeded(t *testing.T) {
	r := NewKeyRing()
	pub, ok := r.Lookup("catalog-root")
	if !ok {
		t.Fatal("catalog-root not found")
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("root key size = %d", len(pub))
	}
}

func TestKeyRing_AddRevoke(t *testing.T) {
	r := NewKeyRing()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := r.Add(TrustedKey{ID: "alice", Key: pub}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("alice"); !ok {
		t.Fatal("alice should resolve")
	}
	if err := r.Revoke("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("alice"); ok {
		t.Error("revoked key should not resolve")
	}
}

func TestKeyRing_Expiration(t *testing.T) {
	r := NewKeyRing()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := r.Add(TrustedKey{ID: "stale", Key: pub, ExpiresAt: past}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("stale"); ok {
		t.Error("expired key should not resolve")
	}

	future := time.Now().Add(time.Hour)
	if err := r.Add(TrustedKey{ID: "fresh", Key: pub, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("fresh"); !ok {
		t.Error("future-expiration key should resolve")
	}
}

func TestKeyRing_AddRejectsBadKey(t *testing.T) {
	r := NewKeyRing()
	if err := r.Add(TrustedKey{ID: "", Key: make(ed25519.PublicKey, ed25519.PublicKeySize)}); err == nil {
		t.Error("empty id should fail")
	}
	if err := r.Add(TrustedKey{ID: "bad", Key: []byte{1, 2, 3}}); err == nil {
		t.Error("short key should fail")
	}
}

func TestKeyRing_Revoke_Unknown(t *testing.T) {
	r := NewKeyRing()
	if err := r.Revoke("nope"); err == nil {
		t.Error("revoking unknown id should fail")
	}
}

func TestKeyRing_LookupFunc(t *testing.T) {
	r := NewKeyRing()
	fn := r.LookupFunc()
	pub, ok := fn("catalog-root")
	if !ok || len(pub) != ed25519.PublicKeySize {
		t.Errorf("LookupFunc wrong result: ok=%v len=%d", ok, len(pub))
	}
}
