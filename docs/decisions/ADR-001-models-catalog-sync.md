# ADR-001: Model Catalog Sync via Overlay Pattern

**Date:** 2026-04-25  
**Status:** Accepted  
**Deciders:** Chrispian, Claude (nanite-backend)

---

## Context

Nanite hardcodes model metadata — pricing, context window sizes, and max output tokens — in `pkg/models/registry.go`. This data goes stale whenever providers change pricing or release new model tiers, requiring a code deploy to pick up the change.

A new shared module (`github.com/hollis-labs/go-modelsdev`) fetches live model metadata from `https://models.dev/api.json` (public, no auth, community-updated daily) and caches it locally with a 24-hour TTL and stale-fallback semantics.

### Alternatives considered

**A. Thread the catalog through every call stack.**  
Each caller of `models.Pricing()`, `models.MaxOutputFor()`, `models.ContextWindowFor()` would need to accept a `*modelsdev.Client`. Requires changes to `internal/store` (usage recording), `go-providers` adapters, and `internal/toolclient`. High blast radius, no clean boundary.

**B. Wrapper API service.**  
A dedicated HTTP service sits between Nanite and models.dev. Clean source-of-truth boundary, supports non-Go consumers. Adds infra to run and a network hop to fail. Overkill for current scale; revisit if Clockwork and other consumers need a stable shared endpoint.

**C. Catalog overlay on `pkg/models` (chosen).**  
Keep `pkg/models` as the single in-process source of truth. Add a `catalogOverlay map[string]Model` guarded by its own `sync.RWMutex`. `SyncFromCatalog(CatalogInput)` atomically replaces the overlay. All existing lookup functions (`ByModelID`, `Pricing`, `MaxOutputFor`, `ContextWindowFor`) check the overlay before the static registry. `pkg/models` has no direct dependency on `go-modelsdev` — the bridge lives in `internal/service/container.go`.

---

## Decision

Use option C.

`pkg/models.SyncFromCatalog(CatalogInput)` accepts a provider-agnostic struct of maps (context windows, max output tokens, input/output pricing). The caller — `internal/service/container.go` — builds the `CatalogInput` from the `modelsdev.Client` and registers an `OnRefresh` callback so the overlay is updated after every successful fetch.

```
modelsdev.Client  ──OnRefresh──►  syncCatalogToRegistry()  ──►  models.SyncFromCatalog()
                                                                        │
                                                                        ▼
                                                              catalogOverlay (RWMutex)
                                                                        │
                              ┌─────────────────────────────────────────┘
                              ▼
               models.ByModelID / Pricing / MaxOutputFor / ContextWindowFor
                              │
                 (overlay wins; static registry is fallback)
```

### Boot sequence

1. `NewContainer` creates `modelsdev.New(WithOnRefresh(syncCatalogToRegistry))`.
2. `syncCatalogToRegistry(catalog)` is called immediately — syncs from warm disk cache if present, no-op if cache is empty.
3. `catalog.StartRefresher(ctx)` starts the background loop: stale check on boot, then 24h TTL-drift scheduling. Each successful fetch fires `OnRefresh` again.

---

## Consequences

### What gets better immediately

- **Pricing accuracy.** `internal/store.RecordUsage` calls `models.Pricing(model)` — it now gets live prices after the first catalog fetch.
- **Max output tokens.** Provider adapters call `models.MaxOutputFor(model)` — new model tiers with larger output windows are picked up without a deploy.
- **Context windows for all callers.** `models.ContextWindowFor(modelID)` is used by `internal/toolclient` (tool budget allocation) and `internal/chat` (legacy enforcement path). Both now get catalog data.

### What stays hardcoded (by design)

- `pkg/models.DefaultContextWindowTokens = 200000` — ultimate fallback when catalog is empty and no registry entry exists. Safe floor, not a ceiling.
- `internal/context/window.go:BudgetFraction = 0.80` — a policy decision, not model data.
- Per-slot budget ceilings (`DefaultBudgets` in `context/slot.go`) — proportional allocations, not model-specific.
- `internal/contextbroker/broker.go:MaxTokens = 50000` — context retrieval budget; a separate tuning concern.

### Limitations

- The overlay is process-local. A catalog update in one Nanite instance is not seen by others until they fetch.
- `pkg/models.SyncFromCatalog` acquires a write lock for the duration of the overlay build. At ~1000 models this is microseconds; no concern at current catalog size.
- models.dev does not expose per-tier rate limits. Rate limiting remains provider-managed.
- **Correction (Phase 1 #06, 2026-08-18):** "provider-agnostic" turned out to hide a real correctness gap, not just a simplification. `CatalogInput`'s maps are keyed by bare model ID with no provider dimension, and models.dev's catalog carries ~190 providers, several of which are resellers/gateways that mirror a vendor's model ID string verbatim (confirmed against a real disk cache: `qihang-ai`, `302ai`, `jiekou`, `nano-gpt`, `helicone`, `llmgateway`, `abacus` all list `claude-sonnet-4-5-20250929`, at different — usually discounted — pricing and context limits than Anthropic's own listing). Whichever provider sorts last alphabetically for a given model ID silently wins the merge, for every caller of `Pricing`/`MaxOutputFor`/`ContextWindowFor` (including real cost accounting in `internal/store.RecordUsage`) — not just a theoretical edge case. `internal/service/container.go`'s `syncCatalogToRegistry` now filters `modelsdev.Client.List()` to a `modelsDevProviderAllowlist` (currently `anthropic`, `openai` — the providers Nanite's own adapters call) before building `CatalogInput`, rather than changing `CatalogInput`'s shape. Option C (this ADR's core decision) is unchanged; this narrows which of models.dev's providers may contribute to the one shared merge.

### Future path to option B

If a wrapper API service becomes warranted (multi-instance shared cache, non-Go consumers, custom enrichment), the `CatalogInput` struct is the natural API contract. The service translates its response into `CatalogInput`; consumers call `SyncFromCatalog` exactly as today.
