// Package agentregistry wires Nanite onto the EXTENDED go-agent-launch
// directory-registry engine (S5 platform-reshape, Phase C).
//
// It owns three things:
//
//   - Build: a shared registrar — a FileBackedRegistrar over the
//     boot-profile catalog root, wrapped in a DegradingRegistrar +
//     LastKnownGoodCache. One instance is built at startup and shared by
//     both launch consumers (the standalone launcher and the chat
//     service launch path).
//
//   - RegisterAgentSource: registers Nanite as an `agent-source`
//     resolver HANDLE in the directory (locked decision D2 — the
//     directory holds the handle, never the agent body).
//
//   - ResolveRuntimeBinding: registry-primary runtime-binding
//     resolution with an explicit, observable file/spec fallback
//     (locked decision D1 — the registry is never mandatory on the
//     launch hot path; §4.1 — the fallback is deliberate caller policy).
//
// Locked-decision posture:
//
//   - D1 (local-first): the FileBackedRegistrar is pure local filesystem
//     I/O; the DegradingRegistrar degrades query reads to the
//     last-known-good cache when the inner registrar is unreachable. A
//     launch never hard-fails because a registry is down. A nil/empty
//     catalog root yields an inert registrar — the standalone launcher
//     still launches fully offline.
//
//   - D2 (handles, not content): RegisterAgentSource stores an
//     AgentSourceContract carrying a ResolverHandle ONLY. No
//     system_prompt / tools / modes ever reach the registrar.
package agentregistry

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// inertFileRoot is the placeholder RegistryRegistrar.FileRoot stamped on
// envelopes when no boot-profile catalog root is configured. The
// registrar store is always the in-memory core regardless; this value
// only has to satisfy RegistryRegistrar.Validate's non-empty rule.
const inertFileRoot = "(no-catalog)"

// Registry is Nanite's handle onto the shared directory registry. It
// bundles the degrading registrar with the descriptor every envelope
// needs, so callers do not re-derive the RegistryRegistrar each time.
//
// A Registry is always usable: when no catalog root is configured the
// inner registrar is an empty in-memory store and every resolution
// degrades to the caller's explicit fallback. This is the D1 contract —
// the registry is a side service, never a launch-path dependency.
type Registry struct {
	// reg is the registrar callers issue envelopes against. It is the
	// DegradingRegistrar wrapper, so query reads degrade to the
	// last-known-good cache on inner failure.
	reg agentlaunch.Registrar

	// descriptor is the RegistryRegistrar stamped on every envelope. It
	// describes the file-backed mode + catalog root.
	descriptor agentlaunch.RegistryRegistrar

	// catalogRoot is the boot-profile catalog root the file-backed
	// registrar ingested. Empty when the registry is inert.
	catalogRoot string

	// ingest is the structured result of the startup catalog walk,
	// retained for observability / tests.
	ingest agentlaunch.IngestReport
}

// Build constructs the shared registrar. catalogRoot is the boot-profile
// catalog root (config boot_profile_catalog_path); an empty root yields
// an inert-but-usable registry (D1 — offline-safe). cachePath, when
// non-empty, makes the last-known-good cache durable across restarts.
//
// Build performs local filesystem I/O only (the catalog walk). It never
// reaches a network and never fails the process: a catalog that cannot
// be walked is logged and the registry degrades to inert.
func Build(catalogRoot, cachePath string, log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	// The descriptor's FileRoot is descriptive metadata stamped on every
	// envelope; RegistryRegistrar.Validate requires it to be non-empty
	// for the file-backed mode. With no catalog configured we fall back
	// to a stable local marker so register/query envelopes still
	// validate — the registrar store itself is always the in-memory
	// core, the FileRoot is not a load-bearing path here.
	fileRoot := strings.TrimSpace(catalogRoot)
	if fileRoot == "" {
		fileRoot = inertFileRoot
	}
	descriptor := agentlaunch.RegistryRegistrar{
		Mode:     agentlaunch.RegistrarModeFileBacked,
		FileRoot: fileRoot,
	}

	// The file-backed registrar feeds an in-memory core. With no catalog
	// root we still hand back a wired degrading registrar over an empty
	// core so callers have a uniform surface.
	inner := agentlaunch.NewInMemoryRegistrar()
	r := &Registry{descriptor: descriptor, catalogRoot: catalogRoot}

	if strings.TrimSpace(catalogRoot) != "" {
		fbr := agentlaunch.NewFileBackedRegistrar(catalogRoot, agentlaunch.WithRegistrar(inner))
		report, err := fbr.IngestCatalog()
		if err != nil {
			// D1: a bad catalog root is logged, not fatal. The registry
			// degrades to inert and launches fall back to file-backed.
			log.Warn("agentregistry: catalog ingest failed; registry inert",
				"catalog_root", catalogRoot, "err", err)
		} else {
			r.ingest = report
			log.Info("agentregistry: catalog ingested",
				"catalog_root", catalogRoot,
				"registered", report.Registered(),
				"errors", len(report.Errors))
		}
	}

	cacheOpts := []agentlaunch.CacheOption{}
	if strings.TrimSpace(cachePath) != "" {
		cacheOpts = append(cacheOpts, agentlaunch.WithCachePersistence(cachePath))
	}
	cache := agentlaunch.NewLastKnownGoodCache(cacheOpts...)

	degrading := agentlaunch.NewDegradingRegistrar(inner, cache,
		agentlaunch.WithDegradeHook(func(reason string) {
			// Observable degradation — D1 requires the fallback to be
			// loud, never silent.
			log.Warn("agentregistry: directory registry degraded", "reason", reason)
		}))
	r.reg = degrading
	return r
}

// NewForTest constructs a Registry whose inner registrar is the
// caller-supplied one, wrapped in the same DegradingRegistrar +
// LastKnownGoodCache layer Build uses. It is the seam other packages'
// tests use to inject a down/unreachable inner registrar and exercise
// the D1 degrade-to-fallback path without standing up a full catalog.
// Production code uses Build.
func NewForTest(inner agentlaunch.Registrar, catalogRoot string) *Registry {
	degrading := agentlaunch.NewDegradingRegistrar(inner, agentlaunch.NewLastKnownGoodCache())
	return &Registry{
		reg:         degrading,
		catalogRoot: catalogRoot,
		descriptor: agentlaunch.RegistryRegistrar{
			Mode:     agentlaunch.RegistrarModeFileBacked,
			FileRoot: catalogRoot,
		},
	}
}

// Registrar returns the shared registrar. It is the DegradingRegistrar
// wrapper — query reads degrade to the last-known-good cache when the
// inner registrar is unreachable.
func (r *Registry) Registrar() agentlaunch.Registrar { return r.reg }

// Descriptor returns the RegistryRegistrar descriptor every envelope
// requires.
func (r *Registry) Descriptor() agentlaunch.RegistryRegistrar { return r.descriptor }

// CatalogRoot returns the boot-profile catalog root the file-backed
// registrar ingested. Empty for an inert registry.
func (r *Registry) CatalogRoot() string { return r.catalogRoot }

// IngestReport returns the structured result of the startup catalog
// walk. It is the zero value for an inert registry.
func (r *Registry) IngestReport() agentlaunch.IngestReport { return r.ingest }

// ResolveRuntimeBinding resolves a runner id to a RuntimeBinding,
// registry-primary with an explicit file/spec fallback.
//
// Control flow (locked decisions D1 + §4.1):
//
//  1. Registry-primary: query the shared registrar for a runtime-binding
//     registered under runnerID. Registry-primary is the default NOW.
//  2. On agentlaunch.ErrRuntimeBindingNotFound — the registry is
//     reachable but has no binding for this runner — fall back to the
//     caller-supplied default. The fallback is a DELIBERATE, OBSERVABLE
//     caller choice (§4.1): it is logged, never silent.
//  3. On agentlaunch.ErrRegistryCacheMiss — the registry is DOWN and
//     nothing was ever cached — also fall back to the default (D1: a
//     down registry never blocks a launch).
//  4. Any other error (ambiguous match, malformed source file) is a
//     genuine fault and is returned.
//
// fallback is the spec/profile-default RuntimeBinding the caller
// resolved itself; it is used verbatim when the registry yields nothing.
func (r *Registry) ResolveRuntimeBinding(runnerID string, fallback agentlaunch.RuntimeBinding, log *slog.Logger) (agentlaunch.RuntimeBinding, error) {
	if log == nil {
		log = slog.Default()
	}
	binding, err := agentlaunch.ResolveRuntimeBinding(r.reg, r.descriptor, runnerID)
	if err == nil {
		return binding, nil
	}

	switch {
	case errors.Is(err, agentlaunch.ErrRuntimeBindingNotFound):
		log.Info("agentregistry: no registry runtime-binding; using file/spec fallback",
			"runner", runnerID, "fallback_provider", fallback.Provider)
		return fallback, validateFallback(runnerID, fallback)
	case errors.Is(err, agentlaunch.ErrRegistryCacheMiss):
		// D1: the directory is unreachable AND nothing was cached. A
		// launch must not hard-fail — degrade to the file/spec fallback.
		log.Warn("agentregistry: registry unreachable (cache miss); using file/spec fallback",
			"runner", runnerID, "fallback_provider", fallback.Provider, "err", err)
		return fallback, validateFallback(runnerID, fallback)
	default:
		return agentlaunch.RuntimeBinding{}, fmt.Errorf("agentregistry: resolve runtime binding %q: %w", runnerID, err)
	}
}

// validateFallback checks that a fallback RuntimeBinding the caller
// supplied is itself usable. A fallback that is empty/invalid is a
// caller bug, not a registry condition — surface it explicitly so the
// failure is attributed correctly.
func validateFallback(runnerID string, fallback agentlaunch.RuntimeBinding) error {
	if err := fallback.Validate(); err != nil {
		return fmt.Errorf("agentregistry: runner %q has no registry binding and the file/spec fallback is invalid: %w", runnerID, err)
	}
	return nil
}
