package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Signature verification for catalog-distributed plugins.
//
// - Catalog entries include a "signature" field: hex-encoded Ed25519 signature
//   over the archive file bytes.
// - Each catalog source can have a trusted public key (stored in catalog_sources.public_key).
// - User-uploaded plugins (install-local, install-archive) skip verification entirely.
// - If a catalog entry has no signature, a warning is logged but install proceeds.

// GenerateKeyPair creates a new Ed25519 key pair for signing plugin archives.
// Returns (publicKeyHex, privateKeyHex). This is a utility for catalog maintainers,
// not called at runtime.
func GenerateKeyPair() (publicKeyHex, privateKeyHex string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 key: %w", err)
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), nil
}

// SignFile signs a file with the given Ed25519 private key (hex-encoded).
// Returns the hex-encoded signature.
func SignFile(filePath, privateKeyHex string) (string, error) {
	privBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid private key size: expected %d bytes, got %d", ed25519.PrivateKeySize, len(privBytes))
	}

	data, err := readFileBytes(filePath)
	if err != nil {
		return "", err
	}

	sig := ed25519.Sign(ed25519.PrivateKey(privBytes), data)
	return hex.EncodeToString(sig), nil
}

// VerifySignature verifies an Ed25519 signature on a file.
// publicKeyHex is the hex-encoded 32-byte public key.
// signatureHex is the hex-encoded 64-byte signature.
// Returns nil if valid, error if invalid or verification fails.
func VerifySignature(filePath, publicKeyHex, signatureHex string) error {
	pubBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: expected %d bytes, got %d", ed25519.PublicKeySize, len(pubBytes))
	}

	sigBytes, err := hex.DecodeString(signatureHex)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature size: expected %d bytes, got %d", ed25519.SignatureSize, len(sigBytes))
	}

	data, err := readFileBytes(filePath)
	if err != nil {
		return err
	}

	if !ed25519.Verify(ed25519.PublicKey(pubBytes), data, sigBytes) {
		return fmt.Errorf("signature verification failed: signature does not match file contents")
	}
	return nil
}

// readFileBytes reads an entire file into memory. Plugin archives are bounded
// at 100MB by the download handler, so this is safe.
func readFileBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}
