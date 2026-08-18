# Housekeeping: delete stale agent profile files (`agridd-project-manager.md`, `proxima.md`)

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `.nanite/agents/agridd-project-manager.md` (deleted), `.nanite/agents/proxima.md` (deleted), `.nanite/agents/torque-task-writer.md` (unchanged — explicitly kept). No Go code changes required (verified — see Context).

## Context

TASKS.md Phase 0 item 24: "Housekeeping: review/delete `.nanite/agents/agridd-project-manager.md`/`proxima.md`; keep `torque-task-writer.md`." Decision log §3 ("Agent-profile housekeeping (captured, not urgent)"): "`.nanite/agents/agridd-project-manager.md` and `proxima.md` — flagged for review then deletion. May be recreated under new names, or dropped entirely, once the new agent spec exists. No action needed now. `.nanite/agents/torque-task-writer.md` — confirmed keep, solid as-is."

Both files exist and were read in full for this task:

- `.nanite/agents/agridd-project-manager.md` (18KB) — a work-coordination advisor profile for "the durable-agent runtime substrate (working title: agridd)," monitoring Torque task state and recommending dispatch timing.
- `.nanite/agents/proxima.md` (31KB) — "Operator's primary chat-interface agent... `operator → Proxima → everyone else`," a singleton relay/concierge durable agent.
- `.nanite/agents/torque-task-writer.md` (35KB, kept, not touched by this task) — a durable agent that validates and creates well-formed Torque tasks on behalf of other agents.

**Verified there is no live functional reference to either file** — this is the check the task explicitly asked for before scoping as a clean delete:

- `internal/agent/builtin/profiles_test.go`'s `TestInternalProfiles_LoadsAllExpectedSlugs` test comment confirms both are in the "zero-ref" bucket by design: "Phase 2 migrated the zero-ref product/tooling agents (analyst, code-auditor, file-backend, agent-builder, **agridd-project-manager**, **proxima**, torque-supervisor, torque-task-writer) out to the managed config layer (`.nanite/agents/`) where they are operator-editable." Neither slug appears in the compiled-in `internal/agent/builtin/profiles/` embed or in any harness dispatch/prompt-framing logic that hardcodes a dependency on them (unlike `backend`, `worker`, `planner`, `default`, etc., which the same test explicitly does pin as harness-referenced).
- `internal/service/durable_agent_recipes.go` mentions `agridd-project-manager` only in a **code comment**, as historical context for why the generalized `project-manager` recipe exists: "The older `agridd-project-manager` profile is the POC this was generalized from and is slated for retirement; new applies should use this one [`.nanite/agents/project-manager.md`, a separate file, not touched by this task]." The recipe's actual `ProfileRule: "operator_selected"` means the operator picks a profile at apply-time from whatever's live in the DB — it does not hardcode a lookup of `agridd-project-manager.md` or `proxima.md` by path or slug.
- The `"proxima-relay"` recipe (same file) is an unrelated, generically-named durable-agent recipe (`Name: "Proxima Relay"`) — it does not read or depend on `.nanite/agents/proxima.md`; it's a separate concept that happens to share a word.
- Other hits (`internal/store/durable_agents_test.go`'s `"legacy-proxima"` fixture slug, `internal/service/durable_agent_recipes_test.go`'s `"architect-proxima"` YAML fixture) are unrelated test-only string literals, not references to the file's content or path.
- Remaining references are documentation-only (decision log itself, `docs/architecture-agents-tasks-2026-08-18.md`, `docs/system-audit/2026-08-17/*`, `docs/architecture/agent-roles-design.md`, `docs/engineering/TASKS.md`) — informational, not functional.

**One live mechanism this deletion does interact with, worth understanding before deleting**: `.nanite/agents/` (project-level) is a real, currently-active discovery tier (`internal/agent/discovery.go`, "Priority 2: `.nanite/agents/` (project)") — files here are parsed and upserted into the DB's agent profile table on every boot via `AutoIngestAgents` (`internal/service/ingest.go`). This is the *same* file-reingest-on-boot mechanism TASKS.md item 10 is separately fixing ("stop `AutoIngestAgents` re-overwriting `source='internal'` rows every boot"), but that fix is not a prerequisite for this task — deleting the `.md` files simply means they stop being discovered and re-ingested on the next boot. It does **not** retroactively delete any existing DB row for these two profiles if one already exists in a given environment's database (the ingest mechanism is additive/upsert, not a two-way sync that prunes DB rows for vanished files) — an already-materialized `agent_profiles` row for `agridd-project-manager`/`proxima` would become an orphan (source file gone, but the DB row lingers) rather than disappearing automatically. Decide in What to do whether that orphan is acceptable (it is inert — nothing references the slug — matching decision log §3's "no action needed now" framing) or worth a one-line cleanup.

## What to do

1. Delete `.nanite/agents/agridd-project-manager.md` and `.nanite/agents/proxima.md`.
2. Do not touch `.nanite/agents/torque-task-writer.md` — confirmed keep.
3. Re-run the discovery/ingest path (or just restart the dev instance) and confirm no error/warning fires from the two profiles no longer being discoverable — `AutoIngestAgents` should simply stop seeing them, not fail.
4. Decide whether to leave any pre-existing `agent_profiles` DB rows for these two slugs as inert orphans (acceptable, matches decision log §3's low-urgency framing — nothing references the slug so an orphaned row has zero functional effect) or clean them up with a one-line `DELETE FROM agent_profiles WHERE slug IN ('agridd-project-manager', 'proxima')`-equivalent Store call. Given this is explicitly scoped as "small, low-risk" (per this task's origin instructions) and decision log §3 says "no action needed now," leaving any pre-existing row as an inert orphan is an acceptable default — but note the decision either way in the Work Log so it isn't silently ambiguous later.

## Done means

- Both files are gone from `.nanite/agents/`.
- `torque-task-writer.md` is untouched.
- A fresh boot/ingest pass produces no error or warning related to the removed files.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass (expect no code changes required, only the file deletions — if any test hardcodes an expectation that these files exist, that's a real finding to report, not silently work around).
- The DB-orphan question (see What to do, step 4) is explicitly decided and recorded, not left ambiguous.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
