# Cut nanite-native adapter's `.nanite/config.yaml` → `agent_profiles` sync entirely (redo of Phase 1 #13)

**Phase:** 2
**Status:** not-started
**Depends on:** none directly; closes `TASKS/phase-0/16-cut-external-agent-import.md`'s leftover carve-out
**Touches:** `internal/plugin/builtin/adapter-nanite-native/plugin.go` (`Plugin.Load`, `Adapter.Discover`), `internal/agent/discovery.go` (adapter-tier wiring)

## Context

This was Phase 1 task 13 (`TASKS/phase-1/13-review-nanite-native-adapter-config-conflation.md` in the `phase-1-execution` worktree), originally flagged "do not dispatch mechanically, awaiting operator's own review."

Operator review happened 2026-08-19. Finding: this is a real, deliberate mechanism, not an accidental leftover — traced via git history to commit `c81a42bc` ("replace agentrc-sync with nanite-native adapter plugin") and its design spec `docs/superpowers/specs/2026-04-08-agent-adapter-architecture-design.md`, which describes nanite-native as "the richest adapter" — real, intentional agent composition (roles + skills + context) via `.nanite/config.yaml`, meant for any project deploying Nanite.

But the standing, already-litigated decision in `docs/engineering/architecture/01-agent-construction.md` — *"Files as agent storage, except builtin/seed content"* is cut — already covers this exact mechanism regardless of how deliberately it was built. Operator's own words: *"The adapter is doing what it was designed to do but we are eliminating it ON PURPOSE."* No file-based agent import survives except the one-time builtin/seed content at install (see `TASKS/phase-2/05-freeze-internal-agent-profiles-on-reingest.md`).

Two separate write paths both need to go, confirmed by direct code reading:
1. `Plugin.Load()`'s direct, ungated `store.UpsertAgentBySlug(ap)` calls for every entry in `.nanite/config.yaml`'s `agents:` map, stamped `Source: "nanite"`. `UpsertAgentBySlug` (`internal/store/agents.go:838`) is a raw unconditional overwrite (`GetAgentBySlug` → `UpdateAgent` if found, `CreateAgent` if not) — no freeze, no source/bootPass gating, independent of Phase 1 `08`'s fix or this phase's `05` fix. Confirmed live in this session's own dogfeed boot logs: 7 real `agent_profiles` rows (`nanite-backend`, `nanite-frontend`, `nanite-plugin-dev`, `nanite-planner`, `nanite-reviewer`, `nanite-reviewer-backend`, `nanite-reviewer-frontend`) synced every boot.
2. `Adapter.Discover()`'s independent re-parsing of the same file into `agent.Definition`s, feeding the (gated) `AutoIngestAgents`/`upsertAgentDef` pipeline via `internal/agent/discovery.go`'s adapter tier (priority 5+) — a second, parallel ingestion path for the same config file.

This closes `TASKS/phase-0/16-cut-external-agent-import.md`'s leftover carve-out — that task explicitly kept nanite-native's `Discover` path alive "because it's also how the nanite-native adapter's Discover... gets invoked, and that one is not being cut." That reasoning is now void; both paths go.

`Adapter.PopulateSandbox` (writes a minimal `.nanite/config.yaml` into an ephemeral CLI-subprocess sandbox directory at launch time) is a different mechanism — DB/config → disposable per-launch file, not file → DB storage — presumed to survive this cut. Confirm during implementation, don't assume blind.

This repo's own `.nanite/config.yaml` `agents:` entries (`nanite-backend`, `nanite-frontend`, etc.) are a separate, legitimate harness-level dev-boot-persona convention read directly by Claude Code per this project's own root `CLAUDE.md` ("If the user says 'Boot <agent>'...") — unrelated to and unaffected by this cut, since that convention operates client-side, independent of the Nanite application's own database.

## What to do

1. Remove `Plugin.Load()`'s agent-sync behavior in full.
2. Remove `Adapter.Discover()`'s agent-definition composition from the same file and its wiring into the adapter discovery tier (`internal/agent/discovery.go`).
3. Confirm/verify `PopulateSandbox` is untouched and still functions — don't assume, check.
4. Add a note to `TASKS/phase-0/16-cut-external-agent-import.md`'s Work Log that this follow-up closes the carve-out it left open.
5. Determine whether the rest of `adapter-nanite-native`'s `CLIAgentAdapter`/`AgentComposer` implementation still has a real role after this cut, or whether the whole package becomes dead code — worker judgment call, document the finding, don't force-delete or force-preserve without checking.
6. Regression-check this repo's own "Boot nanite-backend" (and other agent personas) dev-boot convention still works after the cut — it must be completely unaffected.

## Done means

- Both write paths (`Plugin.Load`'s direct sync, `Adapter.Discover`'s feed into `AutoIngestAgents`) are gone; no code path syncs `.nanite/config.yaml` agent entries into `agent_profiles` anymore.
- `PopulateSandbox`'s per-launch sandbox-file generation is confirmed still functioning (or, if it turned out to also need cutting, that finding is explicitly documented with reasoning).
- This repo's own dev-boot-persona convention ("Boot nanite-backend" etc.) is confirmed unaffected.
- `TASKS/phase-0/16-cut-external-agent-import.md`'s Work Log is updated to note this closes its carve-out.
- Verdict recorded on whether any of `adapter-nanite-native`'s remaining code is now dead.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
