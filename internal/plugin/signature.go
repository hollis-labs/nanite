package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Signing utilities for catalog-distributed plugins.
//
// - Catalog entries carry a "signature" field: hex-encoded Ed25519 signature
//   over the archive file bytes, produced by SignFile below.
// - Each catalog source can have a trusted public key (stored in catalog_sources.public_key).
// - User-uploaded plugins (install-local, install-archive) skip verification entirely.
//
// Verification itself no longer lives here — internal/api/catalog.go's
// handleCatalogInstall (the GUI/API catalog-install path) and
// cmd/nanite's CLI catalog-install flow both converge on
// internal/plugin/install.SignatureVerifier, which fails closed on a
// missing/invalid signature in production builds (AD-04,
// TASKS/audit-remediation/01-plugin-install-convergence/01-unify-plugin-
// catalog-install-pipeline.md). The VerifySignature function that used to
// live here was retired as part of that convergence — it had exactly one
// caller, and that caller is gone.

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
