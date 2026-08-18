# Convert builtin/seed agent profiles to one-time seed data

**Phase:** 0
**Status:** implemented
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

**Reality-check investigation (done first, per the task file's own instruction).**

Confirmed the boot-time overwrite mechanism exactly as described: `container.go`'s
`NewContainer` appends `builtin.InternalProfiles()` (all stamped `Source="internal"`)
to file-discovered `agentDefs`, then calls `AutoIngestAgents(cfg.Store, agentDefs,
knownTools)` unconditionally on every boot (`internal/service/container.go:469`).
Before this fix, `upsertAgentDef` (`internal/service/ingest.go`) called
`st.UpdateAgent(profile)` unconditionally whenever an existing row was found by
ID/slug, regardless of `source` — `UpdateAgent` (`internal/store/agents.go:486`)
overwrites `system_prompt`, `description`, `tools`, `tool_permissions`,
`mcp_servers`, `settings`, and every other content column. This half of the
decision log's claim is fully verified and real.

Tried to reproduce the literal "edit a builtin agent in the GUI, restart, watch it
revert" scenario against current code (by reading the real handler code paths, not
a live server round-trip — sufficient to answer reachability). Confirmed
`internal/agent/source_class.go`'s `ManageClass.Classify` (added by commit
`9898f13`, 2026-05-25) returns `ManageClassInternal` for `source=="internal"`,
whose `Editable()` is `false`. `handleUpdateAgent`/`handleDeleteAgent`
(`internal/api/agents.go:179`,`:343`) and `requireMutableAgent`
(`internal/api/agent_capabilities.go:678`, gating all ~14 known-tool/known-skill/
procedure/knowledge-seed mutation handlers) both check `class.Editable()` and
reject non-managed sources with `writeNotManaged`. So the literal GUI-edit-form
scenario does **not** appear reproducible through the ordinary GUI/API surface as
of today — matches the task file's explanation #1 (the gate closed this vector on
2026-05-25, three months before the decision log was written).

However, the investigation found a **real write path the task file's own analysis
missed** (its explanation #3, "some other write path not covered by
AgentConfigService/requireMutableAgent"): `internal/mcp/self_tools_transport.go`'s
`callUpdateAgent` (bound to the `agent_update` self-tool, `internal/mcp/
self_tools.go:155`, no trust-tier or `ManageClass` gating anywhere in the file)
calls `st.Store.GetAgent(id)` then `st.Store.UpdateAgent(a)` directly with zero
editability check. Any agent session with the `agent_update` self-tool available
can update a `source='internal'` profile's content directly, bypassing the
editability gate entirely — and before this fix, the next boot's `AutoIngestAgents`
would have silently reverted that edit. This is a live, currently-reachable version
of the bug, just triggered via a self-tool call rather than the GUI's edit form.
Logged as a new entry in `TASKS/ESCALATIONS.md` (2026-08-18, "Item 10:
editability-gate reality check found a real bypass") per the task file's own
invitation to escalate this class of finding; **not fixed here** — out of scope for
this task (bounded to `internal/service/ingest.go`'s boot-time behavior, not the
API/self-tool editability gate). Recommended follow-up: gate
`callUpdateAgent`/`callCreateAgent` in `self_tools_transport.go` the same way
`requireMutableAgent` does.

**Implementation.**

Fix lives entirely in `upsertAgentDef` (`internal/service/ingest.go`), no schema
change, no Phase 1 roles/agents dependency:

- Added `alreadySeededInternal bool`, computed right after identity resolution:
  `existing != nil && existing.Source == builtin.SourceInternal` (the "more
  explicit" shape from the task file's two known-good options — gated on the
  **existing row's** source, not the incoming `def.Source`/`profile.Source`, per
  the task file's own steer, since the slug-collision scenario in the Context
  section means these could theoretically differ).
- Inside the `existing != nil` branch, the previously-unconditional
  `st.UpdateAgent(profile)` call is now skipped when `alreadySeededInternal` is
  true. First-time creation (`existing == nil`) is untouched — new internal
  profiles still seed normally. The builtin→internal migration flip
  (`existing.Source == "builtin"`, not yet `"internal"`) also still content-syncs
  on the boot where it first flips, because `alreadySeededInternal` is false at
  that point — this preserves `TestAutoIngestAgents_SourceFlipFromBuiltinToInternal`
  unchanged (verified: it passes as-is, no edit needed to encode new behavior,
  since that test's existing row starts as `source='builtin'`, never `'internal'`,
  at the point `upsertAgentDef` reads it).
- Trust-tier reconciliation (`ingest.go`'s `UPDATE agent_profiles SET
  default_trust_tier = ?`) is left unconditional, per the task file's instruction
  — it's a narrower, source-driven field, not user-editable content.
- Checked the two secondary seed paths per the task file's explicit ask:
  - `seedProcedures` → `InsertAgentProcedure` (`internal/store/
    agent_procedures.go`) does `ON CONFLICT(agent_id, name) DO UPDATE SET
    body = excluded.body, scope = excluded.scope` — **not** idempotent-safe; it
    re-stomps a procedure body from the file on every boot regardless of any DB
    customization. Gated the same way as the main content sync.
  - `seedRoleSkills` → `InsertAgentKnownSkill` (`internal/store/
    agent_known_skills.go`) does an unqualified `INSERT OR REPLACE` — resets
    `pinned`/`activation_count`/`added_at`/`last_used_at` to the seed call's
    zero values every boot, which would silently revert a GUI unpin of a
    role-seeded skill. Gated the same way.
  - `seedRoleToolsFromIngest` → `InsertAgentKnownTool` (`internal/store/
    agent_known_tools.go`) also does `INSERT OR REPLACE`, but left **ungated**
    per the task file's own verification that this one is safe: confirmed
    `BumpActivation` (same file) is a documented no-op (FU-14), so
    `activation_count`/`last_used_at` never diverge from the zero values the
    seed call writes; `pinned`/`sort_order`/`reason` are wholly file-derived.
    Additive/reconciling by nature, not overwrite-prone.

**Tests** (`internal/service/ingest_test.go`):

- `TestAutoIngestAgents_InternalProfileNotReoverwritten` (new) — the primary
  Done-means scenario: seed a fresh internal profile, directly mutate its DB row's
  `system_prompt`/`description` (simulating a customization landing in the DB by
  a path other than the file), re-run `AutoIngestAgents` with the same unchanged
  def, assert the mutation survives. Also proves, in the same test, that a
  genuinely new internal profile in the same batch still gets created normally
  (freeze doesn't block first-time seeding) and that the already-seeded profile
  stays frozen in that same mixed batch.
- `TestAutoIngestAgents_InternalProfileProceduresAndRoleSkillsNotReoverwritten`
  (new) — same shape, covering the `seedProcedures`/`seedRoleSkills` gate: mutates
  a procedure body and unpins a role-seeded skill directly via the store, re-runs
  `AutoIngestAgents`, asserts both customizations survive.
- Extended `TestAutoIngestAgents_SourceFlipFromBuiltinToInternal` with a third
  boot pass after the flip: mutates the now-`internal` row's content, re-ingests
  with further-changed file content, asserts the mutation (not the file content)
  survives — proves the freeze actually engages once a row has flipped to
  `internal`, not just that the flip itself still content-syncs.
- Existing `TestAutoIngestAgents_UpdateOnReingest` (source `"user"`) and the
  original `TestAutoIngestAgents_SourceFlipFromBuiltinToInternal` assertions
  needed **no changes** — verified by inspection and by running the full existing
  `TestAutoIngestAgents_*` suite before and after: neither test exercises an
  `existing.Source == "internal"` row at the point `upsertAgentDef` reads it, so
  neither encoded the old always-overwrite behavior in a way this fix breaks.

**Checks.**

- `go build ./cmd/nanite/` — pass.
- `go vet ./...` — pre-existing, unrelated failure in `internal/service/
  container.go:1186`/`:1206`/`:1257` ("stopReaper"/"stopRuntimeReaper" possible
  context leak) — confirmed present on `main` before this change (ran `go vet
  ./internal/service/...` against the unmodified shared checkout, same two
  findings), not introduced by this diff, and outside this task's Touches list.
  No other vet findings.
- `go test ./internal/service/ -run TestAutoIngestAgents -v -count=1` — all 15
  tests pass (12 pre-existing + 3 new/extended).
- `go test ./internal/service/... -count=1` (the full package, not just the
  ingest tests) — pass, `ok github.com/hollis-labs/nanite/internal/service
  128.215s` and `ok .../internal/service/install 3.832s`.
- `go test ./... -count=1` — **not fully clean**, but the 2 failures are
  pre-existing and unrelated to this task, confirmed by reproducing them
  against the unmodified base commit (`git stash` of both changed files, rerun,
  `git stash pop` to restore): `internal/envelope`'s
  `TestEnvelopeSchemas_AllTypesHaveSchemas` and `internal/mcp`'s
  `TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas` both fail with
  `envelope type "question-form" has no schema file` / `no schema registered for
  envelope type "question-form"` — this is `13-cut-question-form.md`'s
  in-flight removal of the question-form envelope leaving the shared base
  branch in a mid-flight state; zero relation to agent-profile ingestion (this
  diff touches only `internal/service/ingest.go`/`ingest_test.go`). Every other
  package passes, `internal/service`/`internal/service/install` included. Box
  was also under heavy concurrent load from other parallel Phase 0 workers'
  own `go test ./...` runs the whole time, hence the long wall-clock time.

No schema change. No dependency introduced on Phase 1's roles/agents split.
`TASKS/INDEX.md`'s row for `10-seed-builtin-agent-profiles` updated to
`implemented`.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
