package install

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/plugin/devmode"
)

// KeyLookup resolves a catalog-declared key id to an Ed25519 public key. The
// boolean reports whether the key is known and currently trusted (expiration
// and revocation checks live in G.4 catalog/trust.go; this package just
// calls the lookup and trusts its answer).
type KeyLookup func(keyID string) (ed25519.PublicKey, bool)

// SignatureVerifier verifies a downloaded archive's sha256 and Ed25519
// signature. A nil KeyLookup is treated as "no trusted keys" and forces
// every archive to fail signature verification, which is the safe default
// for tests; production callers must supply a real lookup.
type SignatureVerifier struct {
	KeyLookup KeyLookup

	// MaxArchiveBytes caps the file we'll read into memory for verification.
	// Defaults to DefaultMaxArchiveBytes. Archives larger than the cap are
	// rejected without being fully read.
	MaxArchiveBytes int64

	// AllowUnsigned, when true AND when the binary was built with the
	// `devmode` build tag, causes Verify to accept archives without a
	// signature / signer key and to skip Ed25519 verification entirely.
	// Shasum verification still runs so archive integrity is preserved.
	//
	// Both production construction sites — cmd/nanite/plugin_install_flow.go's
	// buildInstaller (via resolveAllowUnsignedPlugins) and
	// internal/api/catalog.go's handleCatalogInstall — read
	// user_settings.allow_unsigned_plugins and set this field accordingly,
	// through install.NewInstaller/BuildOptions.AllowUnsigned (AD-25,
	// TASKS/audit-remediation/01-plugin-install-convergence/02-wire-allow-
	// unsigned-plugins-setting.md).
	//
	// In production builds (devmode.HostDevSigningBypass == false) this
	// field's value is still read into the struct, but Verify never consults
	// it — signatures are always enforced regardless of what user_settings
	// holds.
	AllowUnsigned bool
}

// Verify checks that the file at h.Path:
//  1. Exists and is within the configured size cap.
//  2. Has the expected sha256 (case-insensitive hex comparison).
//  3. Carries an Ed25519 signature that verifies under the public key
//     registered under h.SignerKeyID.
//
// Directory handles are rejected — this verifier only runs on archive handles.
func (v *SignatureVerifier) Verify(ctx context.Context, h Handle) error {
	if h.Kind != "archive" {
		return fmt.Errorf("verify: unsupported handle kind %q", h.Kind)
	}
	if h.Path == "" {
		return errors.New("verify: empty path")
	}
	if h.ExpectedSHA256 == "" {
		return errors.New("verify: missing expected sha256")
	}

	// J.2: per-plugin signature bypass. Only active when BOTH the binary
	// was built with `-tags devmode` AND the operator opted in via
	// user_settings.allow_unsigned_plugins (threaded in as v.AllowUnsigned).
	// Production binaries compile devmode.HostDevSigningBypass to false,
	// which folds this whole branch out via dead-code elimination — a
	// compromised user_settings row cannot disable signature verification.
	signatureBypassed := devmode.HostDevSigningBypass && v.AllowUnsigned

	if !signatureBypassed {
		if len(h.Signature) == 0 {
			return errors.New("verify: missing signature")
		}
		if h.SignerKeyID == "" {
			return errors.New("verify: missing signer key id")
		}
		if v.KeyLookup == nil {
			return errors.New("verify: no trusted key lookup configured")
		}
	}

	var pub ed25519.PublicKey
	if !signatureBypassed {
		var ok bool
		pub, ok = v.KeyLookup(h.SignerKeyID)
		if !ok {
			return fmt.Errorf("verify: unknown signer key id %q", h.SignerKeyID)
		}
		if len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("verify: key %q has wrong size %d", h.SignerKeyID, len(pub))
		}
		if len(h.Signature) != ed25519.SignatureSize {
			return fmt.Errorf("verify: signature has wrong size %d", len(h.Signature))
		}
	}

	max := v.MaxArchiveBytes
	if max <= 0 {
		max = DefaultMaxArchiveBytes
	}

	fi, err := os.Stat(h.Path)
	if err != nil {
		return fmt.Errorf("verify: stat: %w", err)
	}
	if fi.Size() > max {
		return fmt.Errorf("verify: archive size %d exceeds cap %d", fi.Size(), max)
	}

	f, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("verify: open: %w", err)
	}
	defer func() {
		_ = f.Close() // Read-only file close is best-effort cleanup; read errors are handled separately.
	}()

	hasher := sha256.New()
	body, err := io.ReadAll(io.TeeReader(io.LimitReader(f, max+1), hasher))
	if err != nil {
		return fmt.Errorf("verify: read: %w", err)
	}
	if int64(len(body)) > max {
		return fmt.Errorf("verify: archive exceeds cap during read")
	}

	gotSum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSum, h.ExpectedSHA256) {
		return fmt.Errorf("verify: sha256 mismatch: got %s want %s", gotSum, h.ExpectedSHA256)
	}

	if !signatureBypassed {
		if !ed25519.Verify(pub, body, h.Signature) {
			return errors.New("verify: signature check failed")
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
