# Team authority — real enforced grants, not a copy of `parent_dispatch_allowlist`'s advisory pattern

**Phase:** 1 — Schema & storage foundation (`TASKS/teams`)
**Status:** not-started
**Depends on:** `01` (Team Slot vocabulary — this table's `from_slot`/`to_slot` values should correspond to real `TeamSlotDefinition.Name` values, though no FK is proposed since slots live inside `teams.slots_json`, not a normalized table)
**Touches:** `internal/store/migrations/` (new migration), `internal/store/team_authority.go` (new — `TeamAuthorityGrant` struct, CRUD, `AuthorizedForVerb` check function).

## Context

**This task exists to correct a real, load-bearing misreading in the design doc — read this before writing any code.** `docs/engineering/architecture/15-teams.md`'s "Authority: generalize, don't invent" section claims: *"`internal/dispatch`'s `agent_parent_dispatch_allowlist` (migration 060) already governs 'which *tools* may a dispatching parent authorize a spawned child to use.' A Team's `may_spawn`/`may_message`/`may_not_review` is the same shape one level up — *role*-level rather than *tool*-level authorization... This should generalize that existing mechanism... rather than be built as an unrelated Team-native permission system."*

Verified directly against the real code (this planning session's research, not assumed): `agent_parent_dispatch_allowlist` is **not a table** — it is a real column, `agent_profiles.parent_dispatch_allowlist TEXT NOT NULL DEFAULT '[]'` (migration `060_agent_parent_dispatch_allowlist.sql`), a JSON array of **role slugs** (seeded default: `["researcher","planner","worker"]`). It is **already role-slug-level**, not tool-level — the design doc's "generalize from tool-level to role-level" framing is backwards. More importantly: **it is advisory only.** Its sole real consumer is `internal/service/tool.go`'s `parseParentDispatchAllowlist` (lines ~246/862), which feeds `describer.CallerAgent.DispatchAllowlist` — rendered into the `task_execute` self-tool's LLM-facing description. No enforcement call site anywhere rejects a `task_execute` call whose target role isn't on the allowlist. This is consistent with `docs/engineering/architecture/00-overview.md`'s own guiding principle, quoted in full: *"Hints, not control. Steering nudges; it doesn't gate with a deterministic pre-decision layer. Hard-gating experiments in this codebase produced dead-end conversations — this isn't a style preference, it's an observed failure mode."*

**The consequence for this task:** the design doc explicitly wants Team authority to be a real, load-bearing gate — its own text says it "should compose with the existing `dispatch.TrustTier`/`ErrUntrustedRole` enforcement point" for approval-bypass cases. `dispatch.TrustTier` (`internal/dispatch/trust.go`) is a genuine hard gate, confirmed: three tiers (`untrusted`/`normal`/`trusted`), resolved via `TrustResolver.ResolveTrust(ctx, agentProfileID)` from `agent_profiles.default_trust_tier` (`internal/store/trust.go:22`), enforced at `internal/subagent/service.go:785-787` (`if tier == TrustUntrusted { return "", dispatch.ErrUntrustedRole }`, comment: "must NOT be bypassed"), caught cleanly at `internal/selftools/self_tools_transport.go:1839`. **This task must build something with `TrustTier`'s enforcement shape (a real `if`-gate at the actual dispatch/message call site), not `parent_dispatch_allowlist`'s shape (a hint folded into an LLM-facing tool description).** Do not copy the wrong precedent because the design doc names it as the thing to generalize.

**Verb set is deliberately not final** — the design doc is explicit: *"`may_message` is one illustrative verb, not the root authority primitive... 'May exchange messages at all' (transport access) and 'may direct/command/approve' (semantic authority) are different questions... No verb set is locked by this session; this is a warning against letting the one convenient example ossify into the whole mechanism."* Build the schema so a future verb (`may_delegate`/`may_approve`/`may_signal`) is a CHECK-constraint widening, not a new column/migration per verb.

**This task does not wire enforcement into real call sites** — that's task `08` (spawn-time, `may_spawn`) and task `09` (message-time, `may_message`/`may_not_review`), once slot resolution and routing exist to check against. This task builds the storage and a pure, directly-testable check function.

## What to do

1. New migration (confirm the actual next-available number at dispatch time — see `01`'s numbering note):
   ```sql
   CREATE TABLE team_authority_grants (
       id            TEXT PRIMARY KEY,
       team_id       TEXT NOT NULL REFERENCES teams(id),
       from_slot     TEXT NOT NULL,
       verb          TEXT NOT NULL CHECK (verb IN ('may_spawn','may_message','may_not_review')),
       to_slot       TEXT NOT NULL,
       created_at    TEXT NOT NULL DEFAULT (datetime('now'))
   );
   CREATE INDEX idx_team_authority_grants_team_slot ON team_authority_grants(team_id, from_slot, verb);
   ```
   `to_slot = 'self'` is a legitimate value (per the design doc's `reviewer.may_not_review: self` example — a slot excluding itself as a valid target, not addressing another slot literally named "self"). Document your call if you widen the `verb` CHECK now for a verb beyond the three named — the design doc doesn't lock this, so adding one speculatively is a judgment call, not a violation, as long as you document why.

2. **Go types + store CRUD**, `internal/store/team_authority.go`:
   - `TeamAuthorityGrant` struct + `CreateTeamAuthorityGrant`, `ListTeamAuthorityGrants(teamID string)`, `DeleteTeamAuthorityGrant`.
   - A real check function: `AuthorizedForVerb(ctx context.Context, teamID, fromSlot, verb, toSlot string) (bool, error)` — this is the actual enforcement primitive tasks `08`/`09` call at their real dispatch/message call sites, modeled on `TrustResolver.ResolveTrust`'s "resolve, then a hard `if`-gate at the real call site" shape — **not** `parseParentDispatchAllowlist`'s "resolve, then only render into an LLM-facing description" shape. **Fail closed, not fail open**: an unknown `team_id`/`from_slot`/no matching grant row must return `false`, never `true` by default — this codebase has a documented, real history of exactly this class of bug (`TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md`, a fail-open regression in `Resolve()`'s combining-algorithm lookup found by a fresh reviewer) — write an explicit regression test for the fail-closed case, don't just assume the implementation gets it right.

## Done means

- Migration applies cleanly against a real backup copy of the database, not just an empty fixture.
- Round-trip test for `TeamAuthorityGrant` CRUD.
- `AuthorizedForVerb` regression tests covering: a granted verb returns `true`; an ungranted verb/slot pair returns `false`; `to_slot='self'` correctly excludes self-targeting for `may_not_review`; an unknown team ID or slot name returns `false` (explicit fail-closed test, not incidental).
- No call site in `internal/dispatch`, `internal/selftools`, or `internal/service` is modified by this task — enforcement wiring is tasks `08`/`09`'s job. `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
