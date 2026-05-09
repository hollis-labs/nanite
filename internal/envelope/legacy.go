package envelope

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/hollis-labs/go-envelopes"
)

// SetupForTesting builds a registry with core types + orphan schemas and
// installs it via SetEnvelopeRegistry. Call from TestMain in any package
// whose tests exercise ValidateData / DefaultRenderTarget /
// IsPassiveRenderable so they don't fall through to the
// "envelope registry not configured" guard. Mirrors the runtime
// composition root in cmd/nanite/main.go: LoadCore is required (panic on
// failure — without core schemas no test can validate anything), but
// RegisterOrphans is best-effort. A partial orphan failure logs a warning
// with the count of registered + the first error, keeps any successfully
// registered orphans, and returns the registry so tests can still run
// against the rest of the catalog.
func SetupForTesting() *envelopes.Registry {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		panic(fmt.Sprintf("envelope.SetupForTesting: LoadCore: %v", err))
	}
	if registered, err := RegisterOrphans(reg); err != nil {
		// Best-effort: log the partial outcome and continue. Mirrors the
		// production composition root (cmd/nanite/main.go) which logs and
		// proceeds rather than panicking on a missing/changed orphan
		// schema.
		slog.Warn("envelope.SetupForTesting: partial orphan registration",
			"registered", registered, "want", len(OrphanTypes), "err", err)
	}
	SetEnvelopeRegistry(reg)
	return reg
}

// LegacyPluginID is the synthetic plugin ID used to register Nanite's
// orphan envelope schemas with the shared go-envelopes Registry. The
// schemas were extracted into the lib's manifest dir (manifest/schemas/)
// but were never carried into the YAML manifest because they predate the
// catalog tightening. Until the catalog cleanup task lands and decides
// promote-vs-delete for each, Nanite registers them at startup so
// callers like nanite_show_card and chat.BuildKBEnvelope continue to
// resolve schemas.
const LegacyPluginID = "nanite-legacy"

// OrphanTypes is the verbatim list of envelope types that ship a JSON
// Schema in go-envelopes manifest/schemas/ but are absent from the
// canonical YAML manifest. Order is preserved from the seed extraction.
var OrphanTypes = []string{
	"kb-result",
	"giphy-modal",
	"resolution-capture",
	"ticket-form",
	"ticket-confirmation",
}

// LegacyTypeName returns the namespaced registry name a bare orphan
// resolves to under nanite-legacy.* (e.g. "kb-result" →
// "nanite-legacy.kb-result"). Used by callers that still address the
// schema by its historical bare name.
func LegacyTypeName(bare string) string {
	return LegacyPluginID + "." + bare
}

// RegisterOrphans registers every entry in OrphanTypes against reg using
// the lib's RegisterTypeFromManifest path with pluginID = nanite-legacy.
// Schemas are read from envelopes.EmbeddedFS() at the canonical
// manifest/schemas/<type>.schema.json path. Returns the count of types
// successfully registered and the first error encountered (if any).
//
// Best-effort by design: if a single orphan fails to load (e.g. the lib
// drops a schema in a future release) the rest still register and the
// caller logs the partial outcome rather than gating startup.
func RegisterOrphans(reg *envelopes.Registry) (registered int, err error) {
	if reg == nil {
		return 0, fmt.Errorf("envelope: nil registry")
	}
	libFS := envelopes.EmbeddedFS()
	for _, bare := range OrphanTypes {
		schemaPath := "manifest/schemas/" + bare + ".schema.json"
		schemaBytes, readErr := fs.ReadFile(libFS, schemaPath)
		if readErr != nil {
			if err == nil {
				err = fmt.Errorf("read orphan %q schema: %w", bare, readErr)
			}
			continue
		}
		manifestBytes := []byte(`{"type":"` + bare + `"}`)
		if regErr := reg.RegisterTypeFromManifest(manifestBytes, schemaBytes, LegacyPluginID); regErr != nil {
			if err == nil {
				err = fmt.Errorf("register orphan %q: %w", bare, regErr)
			}
			continue
		}
		registered++
	}
	return registered, err
}
