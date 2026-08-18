# Convert builtin/seed agent profiles to one-time seed data

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/service/ingest.go` (`AutoIngestAgents`, `upsertAgentDef`), `internal/agent/builtin/profiles.go` (`SourceInternal`, `InternalProfiles()` — read for context, no change expected), `internal/store/agents.go` (`UpdateAgent`, `CreateAgent` — read for what fields get overwritten), `agent_profiles` table (no schema change expected — task is explicitly bounded to NOT require the Phase 1 `roles`/`agents` schema split)

## Context

TASKS.md item 10 + decision log `docs/architecture-decision-log-2026-08-17.md` §4 ("Agent-definition spec — early requirements"):

> Builtin/seed profiles become one-time seed data, not re-overwritten from the compiled file on every boot — closes a real live bug where a GUI customization to a builtin agent is silently reverted on next restart.

Architecture doc `docs/engineering/architecture/01-agent-construction.md`, "What's cut":

> **Files as agent storage**, except builtin/seed content. Builtin/seed profiles become one-time seed data — inserted once, never re-overwritten from the compiled file on every boot (this closed a real bug where a GUI customization to a builtin agent was silently reverted on restart).

TASKS.md's own framing is explicit that this is intentionally narrow: "Bounded to that one behavior; doesn't need the full `roles`/`agents` schema split." Don't let this task grow into Phase 1 work.

### The overwrite mechanism — verified, real, and exactly as described

`internal/service/container.go`'s boot sequence: `agent.Discover(...)` finds file-based agent definitions, then `builtin.InternalProfiles()` (`internal/agent/builtin/profiles.go`) parses the **embedded** `internal/agent/builtin/profiles/*.md` files and appends them — every one stamped `Source = "internal"` (`profiles.go`'s doc comment: "every internal profile is stamped with `Source=\"internal\"`... the upsert preserves the existing row's ID and rewrites the system_prompt, description, and other content fields, editing a file under `profiles/` followed by a Nanite restart is equivalent to 'replace the row'"). `container.go` then calls `AutoIngestAgents(cfg.Store, agentDefs, knownTools)` on **every boot**, unconditionally, for the full combined set of file-based + embedded-internal definitions.

`AutoIngestAgents` → `upsertAgentDef` (`internal/service/ingest.go:152-255`): for each definition, it looks up an existing `agent_profiles` row (by ID for stamped managed files, falling back to slug — internal profiles use the deterministic `file-<slug>` unstamped path, so they resolve by slug). If a row already exists, `upsertAgentDef` preserves only `ID`/`AgentHash`/`Version`/`Kind`/`CapabilitiesJSON`/`LimitsJSON` from the existing row (lines 204-218) and then calls **`st.UpdateAgent(profile)`** (line 219) unconditionally — which overwrites `system_prompt`, `description`, `tools`, `tool_permissions`, `mcp_servers`, `settings`, and every other content column with whatever the embedded file currently contains (`internal/store/agents.go:531-546`'s `UPDATE agent_profiles SET name = ?, ... system_prompt = ?, description = ?, ...`). There is no check anywhere in this path for "has this row already been seeded once" or "has the DB content diverged from the file since the last sync" — it just re-syncs from the file every time, every boot. This part of the decision-log claim is fully verified and real.

### Reality check on the specific "GUI customization silently reverted" scenario — verify before assuming the obvious fix path is reachable

**This part needs the worker's own verification before implementation, not blind trust in the decision-log's framing** — the specific mechanism described (edit a builtin agent in the GUI, restart, watch the edit vanish) may not currently be reachable through the ordinary API surface, and you should confirm exactly how it manifests before finalizing the fix shape.

`internal/agent/source_class.go` (added by commit `9898f13`, "Managed agent editability..." — **2026-05-25, three months before the decision log was written**) defines `ManageClass.Classify(source, sourceRef)`, which returns `ManageClassInternal` for `source == "internal"` (line 81-82). `ManageClassInternal.Editable()` is `false` (only `ManageClassManaged` is editable, line 39), and `ManageClassInternal.CopyToManagedAllowed()` is also explicitly `false` (line 45-47: "Internal harness primitives are deliberately excluded — they are not user content and stay hidden from the management surface").

Every mutating agent endpoint routes through this gate:
- `internal/api/agents.go:179` (`handleUpdateAgent`) and `:343` (`handleDeleteAgent`) both check `class.Editable()` directly and reject with `writeNotManaged` if false.
- `internal/api/agent_capabilities.go`'s `requireMutableAgent` (line 678, used by every known-tool/known-skill/procedure/knowledge-seed create/update/delete handler — verified via grep, all ~14 mutation handlers route through it) applies the same `class.Editable()` check.

**So as of today, every direct-edit and every mutation-of-a-related-table path for a `source='internal'` agent appears to be blocked at the API layer already** — which raises a real question about how "a GUI customization to a builtin agent is silently reverted" is supposed to happen at all right now. Possible explanations, in rough order of plausibility — **investigate, don't guess**:
1. The bug as originally observed predates commit `9898f13` (2026-05-25) and the editability gate already closed the direct-edit vector; the decision log's phrasing may be describing a historical bug whose primary trigger is already fixed, while the underlying "boot-time overwrite" behavior (verified above) is still real and still worth fixing on its own merits regardless of exactly how it's triggered today.
2. A **slug collision** path: if a user creates a *separate*, fully-editable (`source='user'`) agent profile whose slug happens to match a builtin internal profile's slug, `AutoIngestAgents` processes both defs in the same boot pass (file-discovered defs first, then embedded internal defs appended after — see `container.go`), and since `upsertAgentDef` resolves the existing row by slug and unconditionally sets `profile.Source = def.Source` before writing, the internal def processing *after* the user's would flip that row's `source` back to `'internal'` and overwrite its content — a real mechanism, but a slug-collision scenario, not literally "edit the same builtin agent in place."
3. Some other write path not covered by `AgentConfigService`/`requireMutableAgent` that this investigation didn't find.

**Do this verification first**: try to reproduce the described bug against current `main` (edit a `source='internal'` agent through the real GUI/API, restart, see if the edit survives or reverts) before finalizing the fix. If you can't find a reachable path for the literal scenario, say so explicitly in the Work log and implement the fix anyway (see below — the underlying "don't re-overwrite on every boot" behavior is independently correct and matches the architecture doc's stated target state regardless of exactly which historical bug triggered the decision), rather than blocking on fully resolving the discrepancy. If you find something that contradicts this analysis (e.g. a write path this investigation missed), that's worth a quick escalation note in `TASKS/ESCALATIONS.md` since it would mean the editability gate has a real gap of its own.

## What to do

Implement the fix regardless of how the historical bug manifested — the target behavior decision log §4 and the architecture doc both state plainly ("one-time seed data... never re-overwritten from the compiled file on every boot") is correct on its own terms.

**No schema change** (per the explicit scope bound — this must not require the Phase 1 `roles`/`agents` split, and doesn't need one): the minimal fix is behavioral, inside `upsertAgentDef` (`internal/service/ingest.go`). When `def.Source == builtin.SourceInternal` (i.e. `"internal"`) **and** an existing `agent_profiles` row is already found for that slug, skip the content-overwriting `st.UpdateAgent(profile)` call — treat the first successful `CreateAgent` as the one-time seed, and no-op the content sync on every subsequent boot for that row.

Two known-good shapes for this (pick one, or propose a better one if you find it during implementation — this isn't meant to be prescriptive about the exact mechanism, just the outcome):
- **Simplest**: an early return inside `upsertAgentDef`'s `existing != nil` branch, gated on `def.Source == builtin.SourceInternal`, before the `UpdateAgent` call — literally "if this is an internal profile and the row already exists, do nothing to its content."
- **More explicit**: track a lightweight "already seeded" signal (could be as simple as checking `existing.Source == builtin.SourceInternal` — i.e. it doesn't matter whether the *incoming* def or the *existing row* drives the check, but be precise about which, since the slug-collision scenario in the Context section above means these could theoretically differ) rather than relying purely on the incoming def's `Source`.

**Check the secondary seed paths too** — `upsertAgentDef` also unconditionally calls `seedProcedures`/`seedRoleSkills`/`seedRoleToolsFromIngest` (lines 236-253) whenever `def.Procedures`/`def.RoleSkills`/`def.RoleTools` are non-empty, regardless of new-vs-existing row. `seedRoleToolsFromIngest` uses `INSERT OR REPLACE` (`internal/store/agent_known_tools.go` — verified, this is idempotent by design, not accidentally overwrite-prone) — check whether `seedProcedures`/`seedRoleSkills`'s underlying store calls (`InsertAgentProcedure`/`InsertAgentKnownSkill`) have the same `INSERT OR REPLACE` idempotency or would create duplicates/silently reset a GUI customization to those tables on every boot too. If they'd also re-stomp customizations for internal profiles on every boot, gate them the same way as the main content sync; if they're genuinely additive-only in a way that's already safe, leave them and note why in the Work log.

**Preserve the trust-tier reconciliation** (`ingest.go:229-234`, the separate unconditional `UPDATE agent_profiles SET default_trust_tier = ? WHERE slug = ?`) unless you find a specific reason it also needs gating — it's a narrower, source-driven field (not user-editable content), and the decision log doesn't flag it as part of this bug.

## Done means

- A `source='internal'` agent profile's content fields (`system_prompt`, `description`, `tools`, `tool_permissions`, `mcp_servers`, `settings`, etc.) are written once, on first ingest, and never overwritten by a subsequent boot's re-run of `AutoIngestAgents` for that same row.
- A genuinely NEW internal profile (a new file under `internal/agent/builtin/profiles/*.md` with a slug that has no existing row) still gets created normally on the boot where it first appears — this fix must not block first-time seeding, only re-overwriting an already-seeded row.
- A test proves the fix: seed an internal profile, directly mutate its DB row's content (simulating either a hypothetical GUI edit or just verifying the no-reoverwrite contract directly at the `upsertAgentDef`/`AutoIngestAgents` level), re-run `AutoIngestAgents` with the same (unchanged) file-derived def, and assert the mutated content survives. Check `internal/service/ingest_test.go`'s existing `TestAutoIngestAgents_*` suite (e.g. `TestAutoIngestAgents_UpdateOnReingest`, `TestAutoIngestAgents_SourceFlipFromBuiltinToInternal`) for a test that currently encodes the OLD always-overwrite behavior and needs updating, not just a new test bolted on.
- The Work log documents the outcome of the reality-check investigation above (whether the literal "GUI edit reverted" scenario was reproducible today, and if not, what the closest real mechanism found was) — this is expected findings documentation per `EXECUTION-PROCESS.md`'s "if reality doesn't match the docs, stop and document, don't guess," not a blocker on shipping the fix.
- No changes to the `agent_profiles` table schema, and no dependency introduced on Phase 1's `roles`/`agents` composition split.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including `internal/service/...`.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
