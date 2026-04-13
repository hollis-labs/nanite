// Package catalog holds the embedded trust roots and parsing helpers for the
// nanite plugin catalog.
//
// The catalog is a signed YAML document hosted at
// https://plugins.nanite.hollislabs.dev/catalog.yaml, accompanied by a raw
// Ed25519 detached signature at catalog.yaml.sig. Nanite ships with the
// catalog root's public key baked into the binary (CatalogRootKeyPEM below)
// and will only trust catalogs whose signature validates against it.
//
// The corresponding private key is held in 1Password
// (op://Nanite/nanite-plugin-catalog-signing-key/private-key) and is NEVER
// stored in this repository or on disk outside that vault. See
// docs/architecture/plugin-execution-plan-2026-04-10.md §F.2 and the Track
// F.2 Engine artifact for key custody and rotation details.
package catalog

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sync"
)

// CatalogRootKeyPEM is the PEM-encoded Ed25519 public key of the Nanite
// plugin catalog root. Signatures over catalog.yaml are verified against this
// key. Rotation procedure: introduce a new key alongside this one (dual-trust
// window), publish a catalog signed by both, then retire the old key.
const CatalogRootKeyPEM = `-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAzWSPGfuVmcYwb/OZhm7TeGDj0ixyHa8k5g1bLOhVmLg=
-----END PUBLIC KEY-----
`

var (
	rootKey     ed25519.PublicKey
	rootKeyOnce sync.Once
)

// RootKey returns the parsed ed25519.PublicKey for the catalog root.
// It panics if the embedded PEM fails to parse, since an unbuildable trust
// root is a compile-time-level defect and there is no sensible fallback.
// The parse is performed once and cached for the lifetime of the process.
func RootKey() ed25519.PublicKey {
	rootKeyOnce.Do(func() {
		key, err := parseEd25519PublicKeyPEM([]byte(CatalogRootKeyPEM))
		if err != nil {
			panic(fmt.Sprintf("catalog: embedded root key is invalid: %v", err))
		}
		rootKey = key
	})
	return rootKey
}

func parseEd25519PublicKeyPEM(pemBytes []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("unexpected PEM type %q (want %q)", block.Type, "PUBLIC KEY")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX: %w", err)
	}
	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is %T, want ed25519.PublicKey", pub)
	}
	return edPub, nil
}
