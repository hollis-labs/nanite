# Fix the `models` table's `models.dev` sync target (currently writes to an in-memory overlay, not the DB)

**Phase:** 1
**Status:** not-started
**Depends on:** none, but `02-add-agents-composition-columns.md`'s `model_id` FK is only meaningful once this lands — land this first or in the same change
**Touches:** `internal/service/container.go` (`syncCatalogToRegistry`, the `modelsdev.WithOnRefresh` wiring), `pkg/models/registry.go` (`SyncFromCatalog` — the in-memory overlay this currently feeds), `internal/store/models.go` or equivalent (new DB-write path), `internal/store/seed.go` (the current one-time seed `INSERT`, confirm relationship to the new sync path)

## Context

Architecture doc `01-agent-construction.md`: *"`models` — real schema (`provider_id` FK, `context_window`, pricing, `is_enabled`) — its `models.dev` refresh target needs fixing (currently feeds an in-memory overlay, not this table)."* Decision log §4: *"`models` table already exists with a real schema and a live `models.dev` refresher, but the refresher currently feeds an in-memory overlay (`pkg/models`), not the DB table — needs reconciling before `agents.model_id` can FK against it meaningfully."*

### Exact current gap, verified

`models` table schema (`internal/store/migrations/001_schema.sql:185-198`) is real and complete: `id`, `provider_id FK -> providers(id)`, `model_id`, `display_name`, `context_window`, `max_output`, `supports_tools`, `supports_vision`, `pricing` (JSON), `is_enabled`, `sort_order`. All fields the decision log names are present — the schema isn't the gap.

The sync chain: `internal/service/container.go:885` wires `modelsdev.New(modelsdev.WithOnRefresh(syncCatalogToRegistry))`; `syncCatalogToRegistry` (`container.go:1543`) builds a `modelsdev.CatalogInput` and calls `models.SyncFromCatalog(input)` (`pkg/models/registry.go`), which "atomically replaces the catalog overlay" — an in-memory `map[string]Model` guarded by `sync.RWMutex` (`pkg/models/registry.go:~275`), **not the DB table**. **The only DB write into `models` anywhere in the repo is `internal/store/seed.go:68`'s one-time seed `INSERT`** — confirmed by grep, no `UPDATE models`/ongoing `INSERT INTO models` exists anywhere.

## What to do

1. Decide the target shape: does `syncCatalogToRegistry` get redirected to write the DB table directly (replacing the in-memory overlay), or does it write both (DB as source of truth, in-memory overlay kept as a fast-read cache layered on top)? Per the "database is the source of truth" principle, prefer DB-authoritative — but confirm no other code path depends on `pkg/models`' in-memory overlay's specific read characteristics (latency, no-DB-roundtrip) before removing it outright; if a real reason to keep it as a cache exists, keep it as a cache that's kept in sync with the DB, not as the sole store.
2. Implement the real DB-write sync path: on `models.dev` refresh, upsert `models` rows (matching by `provider_id` + `model_id`), setting `context_window`/`max_output`/`supports_tools`/`supports_vision`/`pricing`/`is_enabled` from the fetched catalog data.
3. Reconcile with `internal/store/seed.go:68`'s one-time seed — confirm the ongoing sync path correctly upserts over the seeded rows rather than conflicting with them (same `id`/`provider_id`+`model_id` matching key).
4. If the in-memory overlay (`pkg/models.Model`) is kept as a cache, confirm every current reader of it (grep `pkg/models` call sites) still gets correct data once the DB is the authoritative write target — don't leave two divergent sources of truth.

## Done means

- `models.dev` refresh cycles write real, verifiable rows into the `models` DB table (confirmed via a real refresh cycle in a dev session, not just a unit test against a fixture).
- `agents.model_id` (added by `02`) can FK against a `models` row and get real, current metadata — verified end to end (an agent composition with a `model_id` set, resolved through the cascade, produces the correct provider/model string for a real turn).
- No divergence between the DB `models` table and any retained in-memory overlay (if kept).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
