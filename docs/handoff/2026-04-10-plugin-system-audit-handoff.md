# Plugin System Audit Handoff — 2026-04-10

## Mission

Act on the plugin system plan-eval audit. Two parallel tracks: fix real bugs in current `internal/plugin/` code, and execute the comprehensive plan at `docs/architecture/plugin-execution-plan-2026-04-10.md` augmented with the gaps the audit surfaced.

## Inputs

- **Audit:** `docs/audits/2026-04-10-plugin-system-plan-eval/` — `index.md` plus finding files `01`–`13`.
- **Plan:** `docs/architecture/plugin-execution-plan-2026-04-10.md` — comprehensive execution plan the audit evaluated.
- **Reviewer context:** `.nanite/agents/reviewer-backend.md` — trust boundaries, priority targets, and the "DO NOT re-flag" list.

## Framing corrections

These override framing inside the audit itself.

- **The plan is comprehensive, not beta-scoped.** It is the full "everything the plugin system needs" roadmap, not a release-window list. Ignore any audit language that calls it "too large."
- **Disregard finding 07 (`07-high-beta-scope-too-large-for-release.md`).** It assumes a deadline that doesn't exist and estimates work using human-team heuristics that don't apply to autonomous agent execution.
- **No time or session estimates.** Not hours, days, sessions, sprints, weeks.
- **No release-readiness gating.** Do not classify work as "beta-blocker" or "post-beta." Valid framing: "real bug in current code, fix it" vs. "forward-looking plan work, execute it."
- **Execution is autonomous parallel agents.** Human-team coordination sequencing does not apply.

## Real bugs in current code

Defects independent of the plan.

- **Critical — `UnloadPlugin` holds `h.mu` across `p.Unload()`.** Same pattern the plan's Track A.2 fixes for `Shutdown()`; plan misses this second site. Deadlocks on any hot-uninstall where `Unload` (or a B.6 `Unregister*` call) re-enters the host. `internal/plugin/host.go:L1010-1076`. Detail: `01-critical-unloadplugin-deadlock-missed-by-plan.md`.
- **High — Transport serializes every RPC via a single mutex.** One in-flight call blocks all others. `internal/plugin/subprocess/transport.go:L17-74`. Detail: `02-high-transport-serialization-blocks-rpc-proliferation.md`.
- **High — Transport read timeout permanently kills the plugin connection.** 30s default (or context deadline) closes the stdout pipe; subsequent `Call` fails forever, Manager still reports `StateRunning`. `internal/plugin/subprocess/transport.go:L101-139`, `manager.go:L112-180`. Detail: `03-high-transport-timeout-kills-connection-permanently.md`.
- **High — Event hook panic crashes the host.** `Host.EmitEvent` and `TriggerDispatcher.Dispatch`/`sendWithRetry` spawn goroutines with no `recover()`. `internal/plugin/host.go:L1080-1106`, `triggers.go:L44-82, L150-185`. Detail: `04-high-event-hook-panic-crashes-host.md`.
- **High/Hybrid — Unregister systemic gap.** Current-code bug (11 of 14 registration categories lack plugin-ID ownership and unload cleanup; `connectorOwners` is never read) and plan-completeness gap (plan B.6 treats event hooks as the one sharp edge when nine other categories need the same backfill). `internal/plugin/host.go:L56-82, L1035-1069`. Detail: `05-high-unregister-systemic-gap-understated.md`. Spans both tracks; ownership backfill must precede any `Unregister*` work.

## Comprehensive plan augmentations

Plan-completeness and plan-accuracy gaps. Fold into the corresponding tracks.

- **Pre-execution inventory.** Plan's Track F/G treats catalog/signature/install as greenfield, but `internal/plugin/catalog.go`, `signature.go`, `repos.go`, and `manage.go` already exist and work. Produce a file-by-file keep/rewrite/delete/move disposition for every file under `internal/plugin/` before any track that touches them. Detail: `06-high-existing-catalog-signature-code-ignored.md`.
- **Track A sub-ordering.** Current Track A bundles drift deletion, P0 bugfixes, fragments-engine removal, and git rationalization behind one gate. Split into sub-tracks, each reaching clean build/test independently. Detail: `08-medium-sequencing-cleanup-must-precede-rearchitecture.md`.
- **Test plan.** No commitment to concurrent-transport tests, panic-recovery tests, full register-then-unregister coverage, JSON-RPC reader fuzzing, or integration tests that exercise a real subprocess without the `Harness` bypass. `WithJSONRoundtrip` on by default. Per-track test-update checklists. Detail: `09-medium-testing-is-an-afterthought.md`.
- **Ownership backfill before Unregister methods.** Split plan B.6 into B.6a (plugin-ID tracking at every `Register*` site that lacks it; prefer wrapping over breaking the `EventHook` interface) and B.6b (`Unregister*` methods and `UnloadPlugin` updates). Detail: `05`.
- **Transport hardening.** Add a new B.0 making transport concurrent (single reader goroutine, per-ID response channel map, short write-mutex, pending-channel cleanup on cancel/close) and fix the timeout-kills-connection bug. Must land before any B.5/B.10 RPC expansion. Detail: `02`, `03`.
- **Panic recovery at event dispatch.** Wrap event hook invocations, `TriggerDispatcher.Dispatch`, and `sendWithRetry` in `recover()` with structured logging. Gate: "panic in a test event hook produces a log line and does not crash the process." Detail: `04`.
- **Lifecycle hardening and resource limits.** Recover around `p.Load(h)`/`p.Unload()`, bounded total shutdown time, health-failure escalation, larger stderr ring buffer plus per-plugin log files, `Pdeathsig` on Linux, `RLIMIT_AS`/`RLIMIT_NOFILE` defaults, config reload semantics, crash-loop recovery, lifecycle contract doc. Detail: `10-medium-lifecycle-completeness-and-resource-limits.md`.
- **Trust and install security.** Build-tag-gated dev mode (not runtime-only), trust-downgrade warning on update, per-version first-install confirmation, separate dev-mode plugin directory, catalog key rotation/revocation procedure, permission model follow-up scope. Detail: `11-medium-trust-and-install-security.md`.
- **Collateral code items.** Dead `connectorOwners`, unused `nextID` atomic, `SetPluginConfig` no consumer, sync `matchFilter`/`renderPayload` in trigger caller, `time.Sleep` in restart backoff, `ringBuffer` write race, `parseEntrypoint` whitespace splitting, `allplugins.go` vs `core_plugins.yaml` drift, no command-name collision detection. Detail: `12-low-collateral-observations.md` L2–L10.

## Risky proposed changes from the plan

Execute carefully; modify before executing where noted.

- **Track A bundled cleanup.** Split per finding 08 first. Each sub-track gets its own gate and commit point.
- **Track B.5/B.10 new RPC method classes.** Do not execute until the B.0 transport rework lands and passes tests. Concurrent transport is a prerequisite.
- **Track B.6 `PluginID()` interface change.** Use the `ownedEventHook` wrapping approach from finding 05 instead of breaking the SDK interface.
- **Track C.3 SDK wire-protocol type move.** Bootstrap order in plan §C.3 is correct but brittle; verify each build step compiles cleanly before proceeding. Inventory `framework/libs/go-plugin/` before Track C.2.
- **Track F/G new catalog/install sub-packages.** Do the pre-execution inventory first; decide disposition of existing `catalog.go`, `signature.go`, `repos.go`, `manage.go` before creating new files at conflicting paths.
- **Plan's "finding #1"–"finding #6" references.** Live in a chat log the receiving agent does not have. Inline them into the plan or stop and ask the user before executing affected tracks. Do not guess. Detail: `13-info-observations-and-praise.md` I5.

## Things that need verification before acting

- Stress-test the subprocess transport — concurrent `command/execute` calls overlap rather than serialize, and timed-out calls do not permanently break the transport.
- Walk every `Host.Register*` in `host.go` and confirm finding 05's 14-category ownership-gap table matches current code.
- Grep for consumers of `internal/plugin/catalog.go`, `signature.go`, `repos.go`, `manage.go`, `catalog_test.go`, `signature_test.go`, and the `/api/plugins/catalog` route before any rewrite or delete.
- `internal/plugin/auto_triggers.go` disposition — plan never mentions it. Read and decide.
- `config/envelopes.yaml` contents before executing the Track A.3 fragments-engine sweep.
- `internal/chat/commands_builtin.go` and chat-engine command result path — grep + trace before B.12.
- Cross-platform verification for Track A.2 scaffold fix, G.3 `os.Rename`, and Track I subprocess `SysProcAttr`. Darwin-only agents miss Windows regressions.

## What's NOT in this handoff

Do not chase these.

- Time estimates of any granularity.
- Release-window judgments ("beta-blocker," "post-beta," "RC-ready").
- Analysis of whether work "fits in" any release.
- Sequencing motivated by human-team coordination overhead.
- Re-framing plan tracks as "v1 vs v2" based on calendar pressure.

## Suggested first move

Read `01-critical-unloadplugin-deadlock-missed-by-plan.md` and `index.md`. Then either pick a real-bug finding to fix directly (01–05, collateral items from 12) or pick a plan track. If picking a plan track, the first action is the finding 06 pre-execution inventory: walk `internal/plugin/` file by file and record keep/rewrite/delete/move for each, capturing existing catalog/signature/install/manage code the plan treats as greenfield. Without it, any track touching `internal/plugin/` will bleed time on "this already exists" surprises. Then do the finding 05 ownership-gap walk before any Track B.6 work.
