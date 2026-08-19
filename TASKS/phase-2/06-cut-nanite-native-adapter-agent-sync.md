# Cut nanite-native adapter's `.nanite/config.yaml` → `agent_profiles` sync entirely (redo of Phase 1 #13)

**Phase:** 2
**Status:** implemented
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

**2026-08-19 — worker report.**

**Both write paths cut, per "Touches":**
- `internal/plugin/builtin/adapter-nanite-native/plugin.go`'s `Plugin.Load()`: removed the entire `.nanite/config.yaml` -> `store.UpsertAgentBySlug` sync (config read, `composeSystemPrompt`, tag-building, upsert loop, and the "disable removed agents" `ListAgentsBySource("nanite")` sweep). `Load()` is now a no-op that just sets `p.host`/`p.status` and logs; it no longer calls `host.GetService("store")` at all, so the plugin loads successfully with zero store dependency. Removed the now-unused `encoding/json` import.
- `Adapter.Discover()`: replaced the `.nanite/config.yaml` -> `[]agent.Definition` composition with `return nil, nil` unconditionally, mirroring the exact precedent `TASKS/phase-0/16-cut-external-agent-import.md` set for `adapter-claude/codex/gemini/opencode`'s own `Discover` no-ops (doc comment, signature kept for the `CLIAgentAdapter` interface contract).
- Both methods' doc comments cite this task and `docs/engineering/architecture/01-agent-construction.md`'s "What's cut" section explicitly, per this task's own Context ("The adapter is doing what it was designed to do but we are eliminating it ON PURPOSE").
- Updated `Plugin.Description()` and `plugin.yaml`'s `description:` field (both previously said "Syncs .nanite/ agent definitions...", now stale) for accuracy — no behavior change, no test asserted the old string.

**`internal/agent/discovery.go` (adapter-tier wiring) — corrected, not deleted:** `DiscoverOptions.Adapters`' doc comment and the "Priority 2+" loop's comment both previously asserted "the nanite-native adapter's own discovery is unaffected... stays live" — now false, corrected to state that every currently-registered `CLIAgentAdapter.Discover()` (all five: the four external-format adapters plus nanite-native) returns `(nil, nil)`, so this tier contributes nothing to `agentDefs` in practice. **Judgment call: did not delete the `opts.Adapters.DiscoverAll(...)` loop or the `Adapters` field itself.** Reasons: (1) the same `AdapterRegistry` instance built by `container.go`'s `newRuntimeAdapterRegistry()` is also passed through to the unrelated, still-live `PopulateAllSandboxes`/`SyncAllProjectRoots` call sites (`container.go:1067,1429` -> `internal/sandbox/sandbox.go`'s `Adapters.PopulateAllSandboxes`) — deleting the field/mechanism would require touching `container.go`'s struct wiring beyond a comment, outside this task's stated "Touches"; (2) `CLIAgentAdapter.Discover` remains a real extension point for any future plugin-provided adapter, not something intrinsically tied to nanite-native. Also corrected one stale in-line comment at the `agent.Discover(...)` call site in `internal/service/container.go` (comment-only, no behavior change) since it directly and specifically described the exact mechanism this task changed, in the file that constructs the registry passed into it — leaving it would actively mislead a future reader.

**Step 3 — `PopulateSandbox` confirmed untouched and still functions, not assumed:** Read the full method body — unchanged by this diff (byte-identical). Confirmed via `git diff` that no edit touched it. Confirmed it's still wired: `container.go`'s single `adapterRegistry` (from `newRuntimeAdapterRegistry`, which still registers `nanitenative.New().Adapter()`) is passed both into `agent.Discover(...)` (the now-inert import path) *and* into service structs consumed by `internal/sandbox/sandbox.go`'s `PopulateAllSandboxes` (the real per-launch sandbox-population path) — same registry instance, two different consumers, only one of which (`Discover`) is affected by this cut. Ran the package's own `TestPopulateSandbox` and the BLG-20260412-009 atomicity regression test (`atomicity_test.go`) — both pass unchanged. `internal/sandbox` package's own test suite (`go test ./internal/sandbox/...`) passes. No cutting was needed or done to `PopulateSandbox`.

**Step 6 — "Boot nanite-backend" dev-boot-persona convention confirmed unaffected:** This convention is read client-side by Claude Code itself (per this project's root `CLAUDE.md`: "If the user says 'Boot <agent>', look up the agent in `.nanite/config.yaml` under `agents:`...") — it never goes through Nanite's Go application, HTTP API, or `agent_profiles` table at all. `git status`/`git diff` confirm `.nanite/config.yaml` itself was never touched by this task's edits (only files under `internal/`, `TASKS/`). Re-read the file directly: its `agents:` block (`nanite-backend`, `nanite-frontend`, `nanite-plugin-dev`, `nanite-planner`, `nanite-reviewer`, `nanite-reviewer-backend`, `nanite-reviewer-frontend`) is intact and unchanged. Confirmed unaffected — this cut only removes the *separate* mechanism where the *same file* also used to get read server-side by `adapter-nanite-native` and synced into the Nanite app's own `agent_profiles` DB table (the exact 7-row sync this task's Context cites from this session's own dogfeed boot logs) — that DB-facing sync is what's now gone; the client-side file-reading convention this repo's own `CLAUDE.md` describes is untouched.

**Step 5 — dead-code determination for the rest of `adapter-nanite-native` (judgment call, documented not force-deleted/force-preserved):**
- **Package is NOT wholly dead.** `PopulateSandbox` remains real, live, load-bearing infrastructure (per-launch CLI-subprocess sandbox population), confirmed above.
- `SyncProjectRoot` was already a no-op before this task ("nanite-native — it manages its own files") — pre-existing, unrelated, correctly left untouched.
- `Load` and `Discover` are now no-ops as of this task (see above).
- **`AgentComposer`'s three methods (`ComposePrompt`, `ListRoles`, `ListSkills`) have zero callers anywhere in the codebase** (verified via repo-wide grep for `AgentComposer`, `.ComposePrompt(`, `Adapter().ListRoles(`/`ListSkills(`, and a `GetAdapter("nanite-native")` lookup — none exist outside this package's own interface declaration/implementation and its own unit tests). **This is pre-existing dead code, not created by this cut** — nothing in the codebase ever called these three methods even before this task. Documented here rather than deleted: removing them would touch `internal/agent/adapter.go`'s public `AgentComposer` interface definition, which is outside this task's stated "Touches" and is a genuinely separate scope decision (whether to remove the interface entirely or just this implementation). Flagged as a follow-up candidate for a future cleanup task.
- `dirExists()` (bottom of `plugin.go`) was already an unused private helper before this task (confirmed via grep — zero call sites in the package) — pre-existing, unrelated to this cut, left alone (Go doesn't flag unused private funcs at compile time; harmless).
- `store.UpsertAgentBySlug` and `store.ListAgentsBySource` (`internal/store/agents.go`) had exactly one production caller each — this plugin's now-removed `Load()` body. After this cut, both methods have zero production callers anywhere in the codebase (confirmed via grep; only test stub implementations of the `AgentReader`/`AgentWriter` interfaces in `internal/service/agent_test.go` and `internal/service/chat_test.go` reference them, to satisfy the interface). Not removed — out of this task's stated "Touches" (`internal/store/agents.go`, `internal/service/store.go` were not named), and removing them would mean editing the `AgentReader`/`AgentWriter` interface contracts and every stub implementation, a larger, separate decision. Flagged as a follow-up cleanup candidate.

**Regression tests added** (`internal/plugin/builtin/adapter-nanite-native/plugin_test.go`):
- `TestDiscover_Noop` — writes a real `.nanite/config.yaml` with a populated `agents:` block (the exact shape that used to compose into `agent.Definition`s) and asserts `Discover` returns `nil`, mirroring the precedent `TASKS/phase-0/16` set for the four external-format adapters.
- `TestLoad_NoStoreSync` — calls `Load` with `hostplugin.NewHostWithStore(nil)` and asserts it succeeds; proves `Load` no longer depends on the `"store"` service at all (the old code would have failed its `svc.(*store.Store)` type assertion in this exact scenario).

**`TASKS/phase-0/16-cut-external-agent-import.md`'s Work Log updated** with a 2026-08-19 entry noting this task closes its carve-out.

**Baseline checks:** `go build ./cmd/nanite/` — passes. `go vet ./...` — passes with only two pre-existing findings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible-context-leak warnings), confirmed via `git stash`/re-vet to exist unmodified on the base branch before this task's changes (only line numbers shifted by an added comment). `go test ./...` — all packages pass, including `internal/plugin/builtin/adapter-nanite-native`, `internal/agent`, `internal/service`, `internal/service/install`, and `internal/sandbox`.

**No deviations from the task's stated action; no blocking escalations.** The one judgment call requiring reasoning (whether to structurally remove `discovery.go`'s adapter-tier loop vs. correct its comments) is documented above with rationale, per the task's own explicit invitation to make that call rather than force either direction.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
