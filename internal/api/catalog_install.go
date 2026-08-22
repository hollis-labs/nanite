package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/plugin/install"
	"github.com/hollis-labs/nanite/internal/store"
)

// This file holds the adapters that let handleCatalogInstall
// (catalog.go) converge onto the shared install.Installer pipeline
// (AD-04, TASKS/audit-remediation/01-plugin-install-convergence/01-unify-
// plugin-catalog-install-pipeline.md) instead of the retired
// download/verify/extract implementation that used to live inline in that
// handler.

// catalogKeyLookup builds an install.KeyLookup over the currently-configured
// catalog sources, keyed on source ID (used as install.Handle.SignerKeyID —
// the API's per-entry catalog model has no separate signer-key-id field the
// way the CLI's single hardcoded catalog does; a source's own trusted
// public key IS its signer identity here).
//
// A source with no configured public key — the out-of-the-box state for
// the default seeded "official" source — makes findSourcePublicKey return
// "", so this returns (nil, false). SignatureVerifier.Verify treats that as
// "unknown signer key id" and rejects the install. This is the fix for
// GO-PLUGIN-001: previously a missing source key silently skipped
// signature verification entirely instead of failing closed.
func catalogKeyLookup(sources []store.CatalogSource) install.KeyLookup {
	return func(keyID string) (ed25519.PublicKey, bool) {
		hexKey := findSourcePublicKey(sources, keyID)
		if hexKey == "" {
			return nil, false
		}
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, false
		}
		return ed25519.PublicKey(raw), true
	}
}

// catalogExtractor is a format-dispatching install.Extractor for
// catalog-sourced archives. The CLI-only install.TarGzExtractor only
// understands .tar.gz; catalog entries can be hosted as either .tar.gz or
// .zip. internal/api/plugins.go's extractZip/extractTarGz (already audited
// as soundly defended against path traversal via pathsafe.ResolveUnder,
// and already backing handleInstallArchive's uploaded-archive path) are
// reused rather than reimplemented.
//
// AD-04 item 2: a naive convergence onto TarGzExtractor alone would
// silently drop zip support and break any externally-hosted .zip catalog
// entry — this was a gap neither the audit nor the task file's original
// draft caught.
type catalogExtractor struct {
	archiveURL string
}

// Extract implements install.Extractor.
//
// The pre-convergence handler (git show main:internal/api/catalog.go:399-
// 410) tolerated an extracted archive whose plugin.yaml wasn't at the
// archive root but inside exactly one subdirectory — the shape a plain
// GitHub "Download ZIP" produces (reponame-branch/). The initial
// convergence pass onto install.Installer dropped that tolerance (logged
// as a deviation in this task's Work Log) because install.Installer's
// state machine calls Validator.Validate directly against targetDir with
// no such fallback hook. Operator decision (recorded in this task's Work
// Log): restore the tolerance entirely within this Extractor, without
// touching install.go's state machine, staging.go, or extract.go's
// TarGzExtractor.
//
// To do that, extraction happens into a scratch subdirectory of targetDir
// first (same filesystem as targetDir, since targetDir is itself the
// staging dir created under DirStaging.StagingRoot — see staging.go), the
// plugin root is resolved against that scratch dir using the old
// handler's exact detection rule, and its contents are flattened
// (renamed) up into targetDir before the scratch dir is removed.
func (e *catalogExtractor) Extract(ctx context.Context, h install.Handle, targetDir string, emit install.EventFunc) error {
	if h.Kind != "archive" {
		return fmt.Errorf("catalog extract: unsupported handle kind %q", h.Kind)
	}

	scratchDir, err := os.MkdirTemp(targetDir, "catalog-extract-scratch-*")
	if err != nil {
		return fmt.Errorf("catalog extract: scratch dir: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	if strings.HasSuffix(e.archiveURL, ".zip") {
		if err := extractZip(h.Path, scratchDir); err != nil {
			return err
		}
	} else if err := extractTarGz(h.Path, scratchDir); err != nil {
		return err
	}

	pluginRoot, err := resolveCatalogPluginRoot(scratchDir)
	if err != nil {
		return err
	}
	return flattenCatalogPluginRoot(pluginRoot, targetDir)
}

// resolveCatalogPluginRoot applies the old handler's exact detection rule
// (git show main:internal/api/catalog.go:399-410): plugin.yaml at the
// extraction root wins outright; otherwise, if the extraction root
// contains exactly one subdirectory, that subdirectory's contents become
// the plugin root. Zero or more than one candidate subdirectory (with no
// root-level plugin.yaml either) is not resolved here — extractDir itself
// is returned unchanged, matching the old code's own default, so the
// caller's later Validator.Validate step is what rejects the install with
// its existing "plugin.yaml missing" error. This function deliberately
// never guesses among multiple ambiguous subdirectory candidates.
func resolveCatalogPluginRoot(extractDir string) (string, error) {
	if fileExists(filepath.Join(extractDir, "plugin.yaml")) {
		return extractDir, nil
	}
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", fmt.Errorf("catalog extract: read extracted dir: %w", err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) == 1 {
		return filepath.Join(extractDir, dirs[0]), nil
	}
	return extractDir, nil
}

// flattenCatalogPluginRoot moves pluginRoot's immediate children up into
// targetDir, matching the old handler's copyDir(pluginRoot, target)
// semantics — except as a same-filesystem rename per child (pluginRoot is
// always inside targetDir's own scratch subdirectory) rather than a full
// second tree copy on top of the one extractZip/extractTarGz already did.
// Any content that isn't part of pluginRoot (e.g. stray files alongside a
// single wrapper subdirectory) is discarded when the caller removes the
// scratch dir afterward, matching the old code's own behavior of only
// ever copying pluginRoot's tree into the target.
func flattenCatalogPluginRoot(pluginRoot, targetDir string) error {
	entries, err := os.ReadDir(pluginRoot)
	if err != nil {
		return fmt.Errorf("catalog extract: read plugin root: %w", err)
	}
	for _, entry := range entries {
		src := filepath.Join(pluginRoot, entry.Name())
		dst := filepath.Join(targetDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("catalog extract: move %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// hostLoader adapts pms.runPluginLoadIntoHost into install.Loader for the
// API's in-process host. The CLI's noopLoader doesn't apply here — unlike
// the CLI (which runs out-of-process and relies on the running service to
// rediscover the plugin on restart), the API handler IS the running
// service and must hot-load the freshly-installed plugin itself so it's
// usable immediately (AD-04 item 3).
//
// Mirrors handleInstall/handleInstallLocal/handleInstallArchive's own
// convention of not failing the HTTP response when hot-load fails — by
// this point the plugin's files are already safely committed to disk, and
// an operator can retry via POST /api/plugins/reload. runPluginLoadIntoHost's
// bool result is intentionally not surfaced as a Loader error, matching
// how the other three install handlers already discard it.
type hostLoader struct {
	pms *pluginManagerState
}

// Load implements install.Loader.
func (l hostLoader) Load(ctx context.Context, pluginID, pluginDir string) error {
	l.pms.runPluginLoadIntoHost(filepath.Join(pluginDir, "plugin.yaml"), pluginDir)
	return nil
}

// catalogInstallEmit returns an install.EventFunc that bridges
// install.Event progress into the existing plugin.install_progress /
// plugin.load_failed event vocabulary the Plugin Manager UI already
// listens for (internal/plugin.Host.EmitPluginInstallProgress /
// EmitPluginLoadFailed). install.State's own string values ("downloading",
// "verifying", "extracting", "validating", "loading", "ready") already
// match the state names this handler emitted before convergence.
func (cs *catalogState) catalogInstallEmit(pluginID string) install.EventFunc {
	return func(e install.Event) {
		if cs.pluginHost == nil {
			return
		}
		cs.pluginHost.EmitPluginInstallProgress(pluginID, string(e.State), e.Message, e.Progress)
		if e.Err != nil {
			cs.pluginHost.EmitPluginLoadFailed(pluginID, fmt.Sprintf("%s: %v", e.Message, e.Err))
		}
	}
}

// stripChecksumPrefix removes catalog.yaml's "sha256:" checksum prefix,
// mirroring cmd/nanite/plugin_install_flow.go's stripSha256Prefix for the
// CLI's own catalog format. install.Handle.ExpectedSHA256 wants the bare
// hex digest, not the "sha256:"-prefixed form catalog entries store it in.
func stripChecksumPrefix(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimPrefix(s, "sha256:")
}

// decodeCatalogSignature hex-decodes a catalog entry's signature field. An
// empty signature decodes to (nil, nil) rather than an error — the
// resulting empty install.Handle.Signature is what makes
// SignatureVerifier.Verify fail closed with "missing signature" in a
// production build, rather than this function pre-emptively rejecting the
// request before the install pipeline's own fail-closed check runs.
func decodeCatalogSignature(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	return b, nil
}

// catalogInstallErrorStatus maps an install.Installer.Install error — always
// wrapped with a "<phase>: " prefix by Installer.Install (e.g. "verify: ...",
// "extract: ...") — to an HTTP status code.
func catalogInstallErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "download:"):
		return http.StatusBadGateway
	case strings.HasPrefix(msg, "verify:"), strings.HasPrefix(msg, "extract:"), strings.HasPrefix(msg, "validate:"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
