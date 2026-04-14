package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/plugin/devmode"
)

// DefaultCatalogFetchTimeout caps a single catalog.yaml fetch.
const DefaultCatalogFetchTimeout = 15 * time.Second

// MaxCatalogBytes caps catalog.yaml size. A few thousand plugins × ~1KB each
// fits comfortably under 1MiB; giving 4MiB leaves headroom.
const MaxCatalogBytes = int64(4 * 1024 * 1024)

// SignedCatalog wraps the raw YAML bytes of a fetched, signature-verified
// catalog.
type SignedCatalog struct {
	YAML        []byte
	SignerKeyID string
}

// SignedFetcher fetches catalog.yaml + catalog.yaml.sig, verifies the
// signature against a KeyRing, and caches both files on disk keyed on a
// sha256 of the catalog URL. On fetch failure, cached catalogs are
// returned (best-effort offline operation) as long as their sig still
// verifies.
type SignedFetcher struct {
	Client   *http.Client
	Ring     *KeyRing
	CacheDir string
	Timeout  time.Duration

	// SignerKeyID identifies which entry in Ring signs this catalog. For
	// a single-root deployment this is typically "catalog-root".
	SignerKeyID string
}

// Fetch pulls catalog.yaml + .sig from catalogURL, verifies the signature,
// caches both on disk, and returns the YAML bytes alongside the signer key
// id. Stale caches whose signature no longer verifies are removed.
func (f *SignedFetcher) Fetch(ctx context.Context, catalogURL string) (*SignedCatalog, error) {
	// J.2 build-tag bypass. In devmode builds HostDevSigningBypass is true,
	// and the catalog signature is skipped entirely — including skipping the
	// KeyRing lookup so a dev build without any trusted keys configured can
	// still browse a local catalog. In production this branch is dead code
	// (the constant is a compile-time false) so the verifier below is
	// unconditionally reached.
	if devmode.HostDevSigningBypass {
		return f.fetchUnverified(ctx, catalogURL)
	}

	if f.Ring == nil {
		return nil, errors.New("catalog: KeyRing is nil")
	}
	keyID := f.SignerKeyID
	if keyID == "" {
		keyID = "catalog-root"
	}
	pub, ok := f.Ring.Lookup(keyID)
	if !ok {
		return nil, fmt.Errorf("catalog: signer key %q not trusted", keyID)
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultCatalogFetchTimeout
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	// Try network first.
	yamlBytes, sigBytes, netErr := f.downloadPair(ctx, client, catalogURL)
	if netErr == nil {
		if err := verify(pub, yamlBytes, sigBytes); err != nil {
			return nil, fmt.Errorf("catalog: signature on fetched catalog invalid: %w", err)
		}
		f.writeCache(catalogURL, yamlBytes, sigBytes)
		return &SignedCatalog{YAML: yamlBytes, SignerKeyID: keyID}, nil
	}

	// Fall back to cache.
	cy, cs, cerr := f.readCache(catalogURL)
	if cerr != nil {
		return nil, fmt.Errorf("catalog: fetch failed (%v) and no usable cache: %w", netErr, cerr)
	}
	if err := verify(pub, cy, cs); err != nil {
		f.removeCache(catalogURL)
		return nil, fmt.Errorf("catalog: cached catalog signature invalid (removed): %w", err)
	}
	return &SignedCatalog{YAML: cy, SignerKeyID: keyID}, nil
}

// fetchUnverified is the devmode-only shortcut: fetch catalog.yaml without
// requiring (or verifying) a signature. Cache reads/writes are still
// best-effort so the dev flow stays offline-friendly. Only reachable when
// devmode.HostDevSigningBypass is true — the production build folds this
// whole branch out of the binary via dead-code elimination of the const.
func (f *SignedFetcher) fetchUnverified(ctx context.Context, catalogURL string) (*SignedCatalog, error) {
	keyID := f.SignerKeyID
	if keyID == "" {
		keyID = "catalog-root"
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultCatalogFetchTimeout
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	yamlBytes, err := fetchBytes(ctx, client, catalogURL, MaxCatalogBytes)
	if err == nil {
		// Cache the YAML for offline reuse; no .sig written in this path.
		if yp, _, ok := f.cachePaths(catalogURL); ok {
			_ = os.MkdirAll(f.CacheDir, 0o755)
			_ = os.WriteFile(yp, yamlBytes, 0o600)
		}
		return &SignedCatalog{YAML: yamlBytes, SignerKeyID: keyID}, nil
	}
	// Fall back to the cached YAML if any; signature state is ignored.
	if yp, _, ok := f.cachePaths(catalogURL); ok {
		if cy, cerr := os.ReadFile(yp); cerr == nil {
			return &SignedCatalog{YAML: cy, SignerKeyID: keyID}, nil
		}
	}
	return nil, fmt.Errorf("catalog (devmode): fetch failed and no usable cache: %w", err)
}

func (f *SignedFetcher) downloadPair(ctx context.Context, client *http.Client, catalogURL string) ([]byte, []byte, error) {
	yamlBytes, err := fetchBytes(ctx, client, catalogURL, MaxCatalogBytes)
	if err != nil {
		return nil, nil, err
	}
	sigBytes, err := fetchBytes(ctx, client, catalogURL+".sig", ed25519.SignatureSize*2)
	if err != nil {
		return nil, nil, err
	}
	// Sig may be hex-encoded or raw. Detect.
	if decoded, derr := hex.DecodeString(strings.TrimSpace(string(sigBytes))); derr == nil && len(decoded) == ed25519.SignatureSize {
		sigBytes = decoded
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return nil, nil, fmt.Errorf("catalog: signature file wrong size (%d bytes)", len(sigBytes))
	}
	return yamlBytes, sigBytes, nil
}

func fetchBytes(ctx context.Context, client *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("http %s", resp.Status)
	}
	if resp.ContentLength > max {
		return nil, fmt.Errorf("content-length %d > cap %d", resp.ContentLength, max)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("body exceeds cap %d", max)
	}
	return body, nil
}

func verify(pub ed25519.PublicKey, yamlBytes, sigBytes []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("pubkey size")
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return errors.New("signature size")
	}
	if !ed25519.Verify(pub, yamlBytes, sigBytes) {
		return errors.New("signature mismatch")
	}
	return nil
}

func (f *SignedFetcher) cachePaths(catalogURL string) (string, string, bool) {
	if f.CacheDir == "" {
		return "", "", false
	}
	sum := sha256.Sum256([]byte(catalogURL))
	base := filepath.Join(f.CacheDir, fmt.Sprintf("catalog-%s", hex.EncodeToString(sum[:8])))
	return base + ".yaml", base + ".yaml.sig", true
}

func (f *SignedFetcher) writeCache(catalogURL string, yamlBytes, sigBytes []byte) {
	yp, sp, ok := f.cachePaths(catalogURL)
	if !ok {
		return
	}
	_ = os.MkdirAll(f.CacheDir, 0o755)
	_ = os.WriteFile(yp, yamlBytes, 0o600)
	_ = os.WriteFile(sp, sigBytes, 0o600)
}

func (f *SignedFetcher) readCache(catalogURL string) ([]byte, []byte, error) {
	yp, sp, ok := f.cachePaths(catalogURL)
	if !ok {
		return nil, nil, errors.New("no cache dir")
	}
	y, err := os.ReadFile(yp)
	if err != nil {
		return nil, nil, err
	}
	s, err := os.ReadFile(sp)
	if err != nil {
		return nil, nil, err
	}
	return y, s, nil
}

func (f *SignedFetcher) removeCache(catalogURL string) {
	yp, sp, ok := f.cachePaths(catalogURL)
	if !ok {
		return
	}
	_ = os.Remove(yp)
	_ = os.Remove(sp)
}
