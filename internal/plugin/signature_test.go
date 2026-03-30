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

func TestSignAndVerify(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Create a test file.
	f, _ := os.CreateTemp(t.TempDir(), "test-archive-*.tar.gz")
	f.Write([]byte("this is a fake plugin archive with some content"))
	f.Close()

	// Sign.
	sig, err := SignFile(f.Name(), priv)
	if err != nil {
		t.Fatalf("SignFile: %v", err)
	}
	if len(sig) != 128 { // 64 bytes = 128 hex chars
		t.Errorf("signature hex length: expected 128, got %d", len(sig))
	}

	// Verify with correct key.
	if err := VerifySignature(f.Name(), pub, sig); err != nil {
		t.Errorf("VerifySignature should pass: %v", err)
	}
}

func TestVerifySignature_WrongKey(t *testing.T) {
	_, priv, _ := GenerateKeyPair()
	otherPub, _, _ := GenerateKeyPair()

	f, _ := os.CreateTemp(t.TempDir(), "test-*")
	f.Write([]byte("plugin archive data"))
	f.Close()

	sig, _ := SignFile(f.Name(), priv)

	// Verify with wrong key — should fail.
	err := VerifySignature(f.Name(), otherPub, sig)
	if err == nil {
		t.Error("expected verification failure with wrong key")
	}
}

func TestVerifySignature_TamperedFile(t *testing.T) {
	pub, priv, _ := GenerateKeyPair()

	f, _ := os.CreateTemp(t.TempDir(), "test-*")
	f.Write([]byte("original content"))
	f.Close()

	sig, _ := SignFile(f.Name(), priv)

	// Tamper with the file.
	os.WriteFile(f.Name(), []byte("tampered content"), 0644)

	err := VerifySignature(f.Name(), pub, sig)
	if err == nil {
		t.Error("expected verification failure after tampering")
	}
}

func TestVerifySignature_InvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		pub    string
		sig    string
		errMsg string
	}{
		{"bad public key hex", "zzzz", "aa", "decode public key"},
		{"short public key", "aabb", "aa", "invalid public key size"},
		{"bad signature hex", "0000000000000000000000000000000000000000000000000000000000000000", "zz", "decode signature"},
		{"short signature", "0000000000000000000000000000000000000000000000000000000000000000", "aabb", "invalid signature size"},
	}

	f, _ := os.CreateTemp(t.TempDir(), "test-*")
	f.Write([]byte("data"))
	f.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifySignature(f.Name(), tt.pub, tt.sig)
			if err == nil {
				t.Error("expected error")
			}
		})
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
