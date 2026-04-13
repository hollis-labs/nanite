package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRootKey_Parses ensures the embedded PEM always parses into an
// ed25519.PublicKey of the correct size. This runs in every environment
// (no external deps) and guards against accidental corruption of the
// constant in trustedkeys.go.
func TestRootKey_Parses(t *testing.T) {
	key := RootKey()
	if len(key) != ed25519.PublicKeySize {
		t.Fatalf("RootKey() size = %d, want %d", len(key), ed25519.PublicKeySize)
	}
}

// TestRootKey_VerifiesLiveSignature fetches the catalog signing private key
// from 1Password (op://Nanite/nanite-plugin-catalog-signing-key/private-key),
// signs a known blob in-process, and verifies the signature against the
// embedded RootKey(). This proves the private key in 1Password and the
// public key embedded in nanite are a matching pair — the core invariant
// of the catalog trust model.
//
// The private key is only ever held in memory; no bytes touch disk.
//
// The test skips if:
//   - `op` is not installed
//   - the 1Password CLI is not authenticated (op read returns non-zero)
//
// CI without 1Password integration therefore skips the live-signature half
// but still runs TestRootKey_Parses.
func TestRootKey_VerifiesLiveSignature(t *testing.T) {
	if _, err := exec.LookPath("op"); err != nil {
		t.Skip("op (1Password CLI) not installed; skipping live signature verification")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "op", "read", "op://Nanite/nanite-plugin-catalog-signing-key/private-key").Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Skip("op read timed out — 1Password CLI may not be authenticated; skipping live signature test")
		}
		t.Skipf("op read failed (likely not authenticated): %v", err)
	}
	privPEM := strings.TrimSpace(string(out))
	if privPEM == "" {
		t.Skip("op read returned empty; skipping")
	}

	priv, err := parseEd25519PrivateKeyPEM([]byte(privPEM))
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	// Sanity: derived public key must match the embedded root key.
	derivedPub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("private key does not yield ed25519 public key: %T", priv.Public())
	}
	if want := RootKey(); !equalBytes(derivedPub, want) {
		t.Fatalf("private key's public half does not match embedded RootKey(); 1Password item and trustedkeys.go are out of sync")
	}

	blob := []byte("nanite-catalog-trust-roundtrip-test")
	sig := ed25519.Sign(priv, blob)
	if !ed25519.Verify(RootKey(), blob, sig) {
		t.Fatalf("signature did not verify against embedded RootKey()")
	}

	// Negative check: tampered blob must fail.
	tampered := append([]byte{}, blob...)
	tampered[0] ^= 0xFF
	if ed25519.Verify(RootKey(), tampered, sig) {
		t.Fatalf("tampered blob unexpectedly verified; trust model broken")
	}
}

func parseEd25519PrivateKeyPEM(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errString("no PEM block found")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, errString("unexpected PEM type " + block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	edPriv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errString("private key is not ed25519")
	}
	return edPriv, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type errString string

func (e errString) Error() string { return string(e) }
