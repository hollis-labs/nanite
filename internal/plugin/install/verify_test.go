package install

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempArchive(t *testing.T, body []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.tar.gz")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sum(body []byte) string {
	s := sha256.Sum256(body)
	return hex.EncodeToString(s[:])
}

func TestVerify_HappyPath(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("archive bytes")
	sig := ed25519.Sign(priv, body)
	archive := writeTempArchive(t, body)

	v := &SignatureVerifier{
		KeyLookup: func(id string) (ed25519.PublicKey, bool) {
			if id == "catalog-root" {
				return pub, true
			}
			return nil, false
		},
	}
	err = v.Verify(context.Background(), Handle{
		Kind: "archive", Path: archive,
		ExpectedSHA256: sum(body), Signature: sig, SignerKeyID: "catalog-root",
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_WrongSHA256(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte("archive")
	sig := ed25519.Sign(priv, body)
	archive := writeTempArchive(t, body)
	v := &SignatureVerifier{
		KeyLookup: func(string) (ed25519.PublicKey, bool) { return pub, true },
	}
	err := v.Verify(context.Background(), Handle{
		Kind: "archive", Path: archive,
		ExpectedSHA256: strings.Repeat("0", 64), Signature: sig, SignerKeyID: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v; want sha256 mismatch", err)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte("archive")
	archive := writeTempArchive(t, body)
	v := &SignatureVerifier{
		KeyLookup: func(string) (ed25519.PublicKey, bool) { return pub, true },
	}
	badSig := make([]byte, ed25519.SignatureSize)
	err := v.Verify(context.Background(), Handle{
		Kind: "archive", Path: archive,
		ExpectedSHA256: sum(body), Signature: badSig, SignerKeyID: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "signature check failed") {
		t.Fatalf("err = %v; want signature check failed", err)
	}
}

func TestVerify_UnknownKeyID(t *testing.T) {
	body := []byte("archive")
	archive := writeTempArchive(t, body)
	v := &SignatureVerifier{
		KeyLookup: func(string) (ed25519.PublicKey, bool) { return nil, false },
	}
	err := v.Verify(context.Background(), Handle{
		Kind: "archive", Path: archive,
		ExpectedSHA256: sum(body),
		Signature:      make([]byte, ed25519.SignatureSize),
		SignerKeyID:    "unknown",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown signer key id") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerify_MissingFields(t *testing.T) {
	v := &SignatureVerifier{
		KeyLookup: func(string) (ed25519.PublicKey, bool) { return make(ed25519.PublicKey, ed25519.PublicKeySize), true },
	}
	cases := []struct {
		name string
		h    Handle
		msg  string
	}{
		{"kind", Handle{Kind: "directory"}, "unsupported handle kind"},
		{"empty path", Handle{Kind: "archive"}, "empty path"},
		{"missing sha", Handle{Kind: "archive", Path: "/x"}, "expected sha256"},
		{"missing sig", Handle{Kind: "archive", Path: "/x", ExpectedSHA256: "a"}, "missing signature"},
		{"missing key id", Handle{Kind: "archive", Path: "/x", ExpectedSHA256: "a", Signature: make([]byte, ed25519.SignatureSize)}, "missing signer key id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Verify(context.Background(), tc.h)
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("err = %v; want contains %q", err, tc.msg)
			}
		})
	}
}

func TestVerify_NoLookup(t *testing.T) {
	body := []byte("archive")
	archive := writeTempArchive(t, body)
	v := &SignatureVerifier{}
	err := v.Verify(context.Background(), Handle{
		Kind: "archive", Path: archive, ExpectedSHA256: sum(body),
		Signature: make([]byte, ed25519.SignatureSize), SignerKeyID: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "no trusted key lookup") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerify_OversizeArchive(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := make([]byte, 2_000)
	sig := ed25519.Sign(priv, body)
	archive := writeTempArchive(t, body)
	v := &SignatureVerifier{
		KeyLookup:       func(string) (ed25519.PublicKey, bool) { return pub, true },
		MaxArchiveBytes: 1_000,
	}
	err := v.Verify(context.Background(), Handle{
		Kind: "archive", Path: archive, ExpectedSHA256: sum(body),
		Signature: sig, SignerKeyID: "k",
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds cap") {
		t.Fatalf("err = %v", err)
	}
}
