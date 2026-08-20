# Provenance tier — per-kind declare allow-list, enforced at write time

**Phase:** 2 — Provenance & Telemetry (`TASKS/reflex-taxonomy`)
**Status:** not-started
**Depends on:** `01-taxonomy-schema-foundation.md` (needs `reflex_provenance_tiers`, `reflex_action_kinds`, `agent_reflexes.provenance_tier`). Parallel-safe with `02`/`03`/`04` — this task's file surface (`internal/api/reflexes.go`, a new join table, store insert/update paths) barely overlaps the engine-internal files those three touch; confirm no overlap on `internal/store/agent_reflexes.go`'s specific functions before running truly concurrently.
**Touches:** `internal/store/migrations/` (new migration for the allow-list join table), `internal/store/agent_reflexes.go` (`InsertAgentReflex`, `UpdateAgentReflex`, `ApprovePendingReflex`), `internal/api/reflexes.go` (`validateReflexDefinition`).

## Context

`docs/engineering/architecture/10-reflex-action-taxonomy.md`, "Facet 3 — Provenance / authority tier": *"used first for a security purpose: which tiers may even declare a given action kind."* Live tiers: `system` (seeded, may be marked required/opt-out-immune — the three `halt_session` seeds already are, via the existing `opt_out_allowed` column from `internal/store/migrations/115_agent_reflex_opt_out.sql`), `operator` (authored through the CRUD API), `plugin` (plugin-contributed — no live caller exists yet). `halt_session` and `dispatch_to_agent` are named as "the obvious candidates to restrict away from `plugin`-tier" — **not locked values**, the mechanism is decided, the specific policy isn't. The operator's explicit call on this (recorded in `TASKS/phase-4/10-reflex-architecture-review.md`'s Work Log, point 7): *"acceptable to leave open and let real usage over the next couple weeks inform the answer, rather than force a decision now."* Seed the doc's own named candidates as a sane, documented-as-adjustable default — do not treat this as final security policy requiring further sign-off before landing.

**`agent_proposed` is confirmed not a fourth live tier.** `store.ApprovePendingReflex` (`internal/store/agent_reflexes.go:555-577`) already collapses an approved pending reflex's `created_by` to `"operator:" + reviewedBy` on approval — "active in `agent_reflexes` ⇒ operator-approved" is already true today by construction. This task's job is to make sure the new `provenance_tier` column preserves that same collapse (→ `'operator'`), not to invent new approval-time behavior.

## What to do

1. **New table `reflex_action_kind_provenance_allow`**:
   ```sql
   CREATE TABLE reflex_action_kind_provenance_allow (
       kind_name TEXT NOT NULL REFERENCES reflex_action_kinds(name),
       tier_name TEXT NOT NULL REFERENCES reflex_provenance_tiers(name),
       PRIMARY KEY (kind_name, tier_name)
   );
   ```
   Presence of a `(kind, tier)` row means that tier may declare that action kind. Seed: every one of the 6 kinds × 3 tiers = 18 combinations, **except** `(halt_session, plugin)` and `(dispatch_to_agent, plugin)` — 16 allowed rows, 2 denied by omission. Document in this file's Work Log that this default is explicitly adjustable per the operator's call above, not a locked policy — a future operator decision to loosen or tighten it is a data change (`INSERT`/`DELETE` on this table), not a code change.

2. **Enforce at write time in `internal/api/reflexes.go`'s `validateReflexDefinition`** (and any other real insert path into `agent_reflexes` you find — grep for callers of `InsertAgentReflex` and `ApprovePendingReflex` first; as of this task's authoring, `internal/api/reflexes.go:108` hardcodes `CreatedBy: "operator"` for the one live CRUD create path, and no plugin-registration insert path exists). Given the reflex's `action_kind` and the resolved provenance tier for this write (see step 3), reject the write if no `reflex_action_kind_provenance_allow` row exists for that pair — clear, specific error message naming both the kind and the tier.

3. **Resolve "the caller's provenance tier"** the same way `created_by` is resolved today at each real call site — this is mostly already fixed by construction (the one live CRUD path is always `'operator'`; seeded rows in `seeds.go`/`loom_pilot_seeds.go` are always `'system'`). If you find a call site this reasoning doesn't cover, treat it as a genuine unknown per `EXECUTION-PROCESS.md`'s escalation rule (stop, document, don't guess) rather than inventing a tier-resolution heuristic.

4. **`ApprovePendingReflex`** (`internal/store/agent_reflexes.go:555-577`): the resulting live row's `provenance_tier` must be `'operator'` regardless of the pending reflex's original proposer, mirroring the existing `created_by` → `"operator:" + reviewedBy` collapse. Add this explicitly — don't let it fall through to whatever default the column happens to have.

5. Do **not** build an actual `plugin`-tier insert path in this task. No concrete plugin caller exists today (design doc: "no concrete plugin need exists today" for the plugin-registered-action-kind seam more broadly). This task's job is to make sure the *gate* is real and load-bearing wherever a write does happen — not to invent a caller for it to gate.

## Done means

- `reflex_action_kind_provenance_allow` seeded with exactly 16 allowed rows (18 combinations minus the two named exceptions), migration applies cleanly against a real backup DB copy.
- A test: attempting to declare a `halt_session` reflex at `plugin` tier is rejected with a clear error; the same attempt at `system` or `operator` tier succeeds.
- A test: every one of the real pre-existing seeded/operator-created `agent_reflexes` rows still validates successfully post-migration (no false-positive rejection of already-live data — spot check all 14+ real rows from a backup, not just a synthetic fixture).
- A test: `ApprovePendingReflex` on a pending reflex produces a live row with `provenance_tier='operator'`, independent of whatever tier (if any) the pending row itself carried.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

<!-- Worker fills in as it goes. -->

## Review notes

<!-- Reviewer fills in. -->
