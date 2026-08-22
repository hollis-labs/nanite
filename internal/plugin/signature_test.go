package plugin

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Public key: 32 bytes = 64 hex chars.
	if len(pub) != 64 {
		t.Errorf("public key hex length: expected 64, got %d", len(pub))
	}
	// Private key: 64 bytes = 128 hex chars.
	if len(priv) != 128 {
		t.Errorf("private key hex length: expected 128, got %d", len(priv))
	}

	// Verify they decode to valid lengths.
	pubBytes, _ := hex.DecodeString(pub)
	if len(pubBytes) != ed25519.PublicKeySize {
		t.Errorf("public key size: expected %d, got %d", ed25519.PublicKeySize, len(pubBytes))
	}
	privBytes, _ := hex.DecodeString(priv)
	if len(privBytes) != ed25519.PrivateKeySize {
		t.Errorf("private key size: expected %d, got %d", ed25519.PrivateKeySize, len(privBytes))
	}
}

// TestSignFile_ProducesValidSignature checks SignFile's output directly
// against crypto/ed25519.Verify rather than against this package's own
// (now-retired) VerifySignature — see signature.go's doc comment: the
// GUI/API and CLI install paths both verify catalog-signed archives via
// internal/plugin/install.SignatureVerifier now, not this package.
func TestSignFile_ProducesValidSignature(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Create a test file.
	f, _ := os.CreateTemp(t.TempDir(), "test-archive-*.tar.gz")
	body := []byte("this is a fake plugin archive with some content")
	f.Write(body)
	f.Close()

	// Sign.
	sig, err := SignFile(f.Name(), priv)
	if err != nil {
		t.Fatalf("SignFile: %v", err)
	}
	if len(sig) != 128 { // 64 bytes = 128 hex chars
		t.Errorf("signature hex length: expected 128, got %d", len(sig))
	}

	pubBytes, err := hex.DecodeString(pub)
	if err != nil {
		t.Fatalf("decode pub: %v", err)
	}
	sigBytes, err := hex.DecodeString(sig)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), body, sigBytes) {
		t.Error("expected signature to verify against the signed file's bytes")
	}

	// A signature produced by a different key must not verify.
	otherPub, _, _ := GenerateKeyPair()
	otherPubBytes, _ := hex.DecodeString(otherPub)
	if ed25519.Verify(ed25519.PublicKey(otherPubBytes), body, sigBytes) {
		t.Error("expected signature to fail verification against an unrelated public key")
	}

	// A signature over tampered content must not verify.
	if ed25519.Verify(ed25519.PublicKey(pubBytes), []byte("tampered content"), sigBytes) {
		t.Error("expected signature to fail verification against tampered content")
	}
}

func TestSignFile_InvalidKey(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "test-*")
	f.Close()

	_, err := SignFile(f.Name(), "tooshort")
	if err == nil {
		t.Error("expected error for short key")
	}
}
