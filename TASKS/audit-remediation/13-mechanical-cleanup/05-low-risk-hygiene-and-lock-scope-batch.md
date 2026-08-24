# Low-risk hygiene grab-bag: 12 findings with no shared root cause, each too small for its own task

**Phase:** Wave 8 — Mechanical cleanup (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** reviewed
**Depends on:** none within this batch.
**Touches:** `internal/mcp/manager.go`, `internal/plugin/install/validate.go`, `internal/agent/builtin/embed_mux_devmode.go`, `internal/agent/builtin/profiles.go`, `internal/contextbroker/source_pcc.go`, `internal/contextbroker/source_memory.go`, `internal/contextbroker/source_conduit.go`, `internal/context/tokens.go`, `internal/contextbroker/broker.go`, `internal/loopdetect/detector.go`, `internal/recovery/orphansweep/orphan_sweep.go`, `internal/elicitation/service_test_helpers.go`, `internal/chat` (architecture note only, no required edit), `internal/elicitation/service.go`, `internal/plugin/host.go`.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 8 — mechanical cleanup · **Dispatch unit:** `W8`
> - **Depends on:** `11/05`, `11/10`, `09/02`
> - **Blocks:** none
> - **Parallel-safe with:** `13/01`, `13/04`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

**This is a genuinely mixed grab-bag, not a themed batch — say so plainly rather than forcing a fake shared narrative.** The 12 findings here span concurrency/lifecycle, complexity, naming/correctness, duplication, and architecture-observation categories across 8 unrelated packages. What they share is only this: each is individually too small in isolation to justify its own task file, per the remediation guide's explicit "do not create one task per occurrence" instruction, and each is low severity or informational — except two, which are flagged below as heavier than the rest of this batch and get more attention accordingly.

Two items are not like the others and deserve more weight:

- **`GO-PLUGIN-004`** is medium severity, not low/informational, and carries a **real, traced concurrency bug** (a narrow TOCTOU window in concurrent plugin load/unload), not just a style observation. `requires_architect_decision: true` for this one specifically — the readability-extraction half is a mechanical style call, but whether/how to close the TOCTOU window is a real design question. **A planner may want to split the TOCTOU-gap portion out into its own dedicated task later**, given it's a real concurrency finding riding alongside a pure-hygiene complexity observation — this task doesn't do that split, but flags the option explicitly so it isn't lost.
- **`GO-RUNTIME-008`** is purely informational and requires **no code change at all** — it exists only to get `agent.Boot` added to the project's master complexity-tracking list (alongside `cmdServe`/`UnloadPlugin`/`Executor.Run`/`Service.Spawn`), since it was missed by the mechanical-scan-sorted table that produced that list. Do not treat this row as an invitation to refactor `agent.Boot` — the audit judged it essential complexity, correctly handled, with a verified cleanup closure at every one of its ~12 early-return points.

Everything else in this batch is `requires_architect_decision: false`.

## What to do

| Finding | File(s) | What |
|---|---|---|
| `GO-MCPTOOL-009` (low, medium confidence) | `internal/mcp/manager.go` (`Manager.RemoveServer`) | Holds the registry-wide lock for the full duration of subprocess kill+reap (`Process.Kill()` + blocking `Wait()`, no timeout) — every other `Manager` operation for unrelated MCP servers queues behind the same lock while one server tears down. Reachable from plugin hot-unload; narrow-window, not reproduced as an actual hang. Consider releasing the lock before calling `closer.Close()`, restructuring so only the registry-map mutation (not the subprocess teardown itself) happens under the lock.
| `GO-PLUGIN-005` (low) | `internal/plugin/install/validate.go` (`validateCrossRefs`, cyclomatic 35, 126 lines) | Mechanically repetitive (9 near-identical per-manifest-section uniqueness checks) but not incorrect — a textbook "may be clearer than fragmented helpers" case, not urgent. Optional: a generic `checkUniqueKeys[T any]` helper could collapse ~80 lines with no behavior change. Only do this if it doesn't reduce clarity; skip if the 9 sections have any subtle per-section variation that a generic helper would obscure.
| `GO-AGENT-005` (low, medium confidence) | `internal/agent/builtin/embed_mux_devmode.go:21`, `internal/agent/builtin/profiles.go` | A devmode-only builtin agent still stamps the legacy `Source="builtin"` value the sibling package's own doc says is retired (`"internal"`/`SourceInternal` is the current convention). Traced consequence: this profile falls through to `ManageClassManaged` (GUI/API-editable) instead of the hidden/read-only treatment every other embedded harness profile gets. Devmode-only blast radius. Change `embed_mux_devmode.go:21` to stamp `SourceInternal` matching the rest of the package's convention.
| `GO-MEM-004` (informational) | `internal/contextbroker/source_pcc.go`, `internal/contextbroker/source_memory.go`, `internal/contextbroker/source_conduit.go` | Each `contextbroker` source computes `ContextItem.Relevance` via an unrelated method (static lookup table, store-provided confidence + ad-hoc boost, hardcoded literals) yet all are merged and cross-compared on one global scale with no shared contract beyond "roughly 0-1." Architecture observation, not a defect — this task's scope is documenting the gap (e.g. a short doc comment near the shared merge/sort point noting the informal contract each source must honor), not building an enforcement mechanism. `requires_architect_decision: true` for whether to go further than documentation (a shared normalization step) — out of scope for this task beyond flagging it.
| `GO-MEM-005` (informational) | `internal/context/tokens.go`, `internal/contextbroker/broker.go` | Two independent "chars/4" token-estimation formulas (one floors, one always rounds up) measure the same content at two different stages of the same budget pipeline, diverging by a few tokens for any length not a multiple of 4. `contextbroker` deliberately avoids importing `internal/context` (documented one-way dependency boundary), so unifying this would need a new shared lower-level package — out of scope for this mechanical task. Document the divergence with a short comment at both sites so it isn't mistaken for a bug by a future reader; no functional fix required here.
| `GO-MEM-006` (low — **more consequential than pure hygiene, mention prominently**) | `internal/loopdetect/detector.go` (`Detector.windows`) | No production eviction path — `Reset(sessionID)` exists specifically for this per its own doc comment, but has zero production callers. For a long-running daemon, every distinct session that ever calls a tool adds a permanent map entry — unbounded growth over process uptime, not a cosmetic nit. Wire `Detector.Reset(sessionID)` to fire on session end (find the session-lifecycle end hook this package's consumers use), or add a bounded LRU/TTL eviction inside `Detector` itself if there's no clean single "session ended" signal available. Pick whichever fits the existing session-lifecycle wiring with less new surface area, and note the choice.
| `GO-MEM-009` (low) | `internal/recovery/orphansweep/orphan_sweep.go` (`RuntimeReaper`'s sweep function) | Explicitly discards its own `ctx` parameter (`_ = ctx`), so an in-flight `ListRunningRows()`/`MarkRuntimeOrphaned` DB call cannot be cancelled mid-flight by the reaper's shutdown signal — `Stop()` simply blocks until that call returns on its own. Low risk (local, bounded SQLite call) but a genuine "context not propagated" gap. Propagate `ctx` into the underlying DB calls so shutdown can cancel an in-flight sweep.
| `GO-CHAT-003` (low) | `internal/elicitation/service_test_helpers.go` | No `_test.go` suffix, so this file **compiles into the production binary** — including `elicitWithDuration`, a near line-for-line reimplementation of `Elicit`'s body that could silently diverge from real `Elicit` behavior on any future change. Rename to the `export_test.go` pattern (or otherwise give it a `_test.go`-suffixed name) so it's excluded from production builds; verify nothing outside test files references its exported symbols before renaming.
| `GO-CHAT-008` (informational) | `internal/chat` (architecture note, no required edit) | Wide fan-out (11 internal packages) mixes broadly-consumed wire-vocabulary types with subsystem-specific response-handler wiring in the same package — not a cycle, not urgent, but means a caller wanting only `chat.Envelope`/`ResponseV1` transitively depends on the entire subagent+elicitation+mcp+plugin graph anyway. No code change required for this task; record the observation (this task file itself is the record) for a future architect pass considering whether subsystem-specific response handlers belong in a separate thin wiring subpackage.
| `GO-CHAT-009` (informational) | `internal/elicitation/service.go` (`Elicit`) | Three-way `select` has a theoretical response/timeout race inherent to any `select`-based timeout (Go picks pseudo-randomly among simultaneously-ready cases); negligible at production's 5-minute default timeout, proportionally larger (still very unlikely) at the sub-second timeouts `GO-CHAT-003`'s test helper uses. No fix recommended by the audit; no action required beyond noting it here.
| `GO-PLUGIN-004` (medium — **heavier item, has a real concurrency finding**, `requires_architect_decision: true`) | `internal/plugin/host.go:1240-1585` (`UnloadPlugin`, cyclomatic 63, 346 lines) | Two separable things: (1) essential complexity dominates — a documented, carefully lock-disciplined 18-category teardown sweep; ~10 of the 18 categories are still hand-inlined map-iterate-delete logic that could be extracted into small `sweepXByPlugin` helpers mirroring the pattern the file already uses for the other ~8 categories, purely mechanical, no behavior change. (2) Separately, a real narrow TOCTOU gap: the dependency check runs under an early lock/unlock (`host.go:1249-1260`), then `p.Unload()` runs lock-free by design at `host.go:1264` (documented, to avoid deadlock on re-entrant plugin calls) — a concurrent `LoadPlugin(B)` where B depends on the plugin being unloaded can pass its own dependency check in that window, leaving B loaded with a now-missing dependency. This is traced as a real, narrow race, not speculative. **Decision needed** on the TOCTOU gap: whether an unloading-in-progress guard is worth adding (closing the window) given concurrent load/unload of interdependent plugins is a real but likely rare scenario, versus accepting it as a known, documented limitation. The extraction-for-readability half can proceed independently of that decision — do the mechanical extraction regardless, and treat the TOCTOU decision as the item requiring sign-off. **Consider recommending to whoever sequences this batch that the TOCTOU-gap portion be split into its own dedicated task** if the decision is "close the gap" (a real fix, not hygiene) rather than "accept and document" — this task doesn't perform that split, it only flags the option.
| `GO-RUNTIME-008` (informational, **no code change**) | `internal/runtime/agent/agent.go:323` (`Boot`) | Second-highest cyclomatic complexity function in the entire codebase (cyclomatic 50, cognitive 59, maintainability index 10, 234 lines) — missed by the mechanical-scan-sorted table and this cluster's own pre-flagged list. Judged **essential**, same shape as `cmdServe`: a correctly-applied `cleanup` closure verified at every one of ~12 early-return points, a genuinely necessary 3-way `select` at the end. **The only action for this row is administrative**: add `agent.Boot` to the project's master complexity/Top-10 tracking list alongside `cmdServe`/`UnloadPlugin`/`Executor.Run`/`Service.Spawn` (wherever that list is maintained — check `12-quality-ratchet-and-standards/` for the tracking doc this should feed into). Do not refactor `Boot`.

### Wave 5 constraint on `GO-PLUGIN-004` / `GO-PLUGIN-006`

The operator approved a selective, pressure-driven decomposition posture for
plugin `Host` on 2026-08-23. This task's already-scoped extraction of
`UnloadPlugin`'s hand-inlined category sweeps into named helpers is the current
step and the evidence-gathering boundary; it is **not** authorization to migrate
every remaining raw registration map into a sub-registry.

After the helper extraction, record whether any specific named registration
category still has materially split registration/unregistration ownership,
unsafe or hard-to-reason-about lock coordination, or demonstrable testability
friction. Only such a category may seed a separate narrow sub-registry task,
following the existing `cardRulesRegistry`, `panelRegistry`, `FilterRegistry`,
or `MutablePluginMux` pattern. If no category clears that bar, no additional
`Host` decomposition is scheduled. This carries forward `10/03`'s approved
principle: address concrete growing pains, not field/method counts by
themselves.

## Done means

- [ ] `GO-MCPTOOL-009`: `RemoveServer` no longer holds the registry-wide lock across the full subprocess kill+reap; a test (or existing coverage) confirms unrelated `Manager` operations aren't blocked during one server's teardown.
- [ ] `GO-PLUGIN-005`: either left as-is with rationale recorded, or collapsed via a generic helper with no behavior change (existing tests still pass).
- [ ] `GO-AGENT-005`: `embed_mux_devmode.go:21` stamps `SourceInternal`; the devmode-only profile now gets hidden/read-only treatment matching its siblings.
- [ ] `GO-MEM-004`, `GO-MEM-005`, `GO-CHAT-008`, `GO-CHAT-009`: each documented (short comment or note) per the row above; no functional change required for these four.
- [ ] `GO-MEM-006`: `Detector.Reset(sessionID)` wired to a real session-end signal, or bounded eviction added inside `Detector` — growth is no longer unbounded for a long-running process.
- [ ] `GO-MEM-009`: `ctx` propagated into `RuntimeReaper`'s underlying DB calls; shutdown can cancel an in-flight sweep.
- [ ] `GO-CHAT-003`: `service_test_helpers.go` renamed so it no longer compiles into the production binary; production build size/symbol table no longer includes `elicitWithDuration`.
- [ ] `GO-PLUGIN-004`: the ~10 hand-inlined teardown categories extracted into helpers mirroring the existing pattern (mechanical, done regardless); TOCTOU-gap decision recorded (close vs. accept-and-document), and if "close," either implemented here or explicitly handed off as a new follow-on task per the split option above.
- [ ] `GO-PLUGIN-004` / `GO-PLUGIN-006`: after helper extraction, any remaining
  named category-level ownership/locking/testability pain is recorded; either a
  narrow sub-registry follow-up is seeded for that category or the record says
  no category justified further decomposition. No all-category migration sweep
  is performed.
- [ ] `GO-RUNTIME-008`: `agent.Boot` added to the master complexity tracking list; zero code changes to `Boot` itself.
- [ ] `go build ./...`, `go vet ./...`, and `go test ./internal/mcp/... ./internal/plugin/... ./internal/agent/... ./internal/contextbroker/... ./internal/loopdetect/... ./internal/recovery/... ./internal/elicitation/...` pass.

## Work log

- 2026-08-24: Implemented the operator's current scope, which supersedes several
  stale rows and checkboxes above. AD-35 closes `GO-PLUGIN-004` now with a
  dedicated `Host.lifecycleMu` held across each complete `LoadPlugin` and
  `UnloadPlugin` transaction. `h.mu` remains released around plugin callbacks.
  No `UnloadPlugin` teardown helper was extracted; that structural work already
  lives in `14-followups/README.md` P3. A regression holds A inside a reentrant
  unload callback, proves dependent B cannot enter `Load`, then proves B fails
  its dependency check after A is removed.
- `GO-MCPTOOL-009`: `Manager.RemoveServer` now removes the server, trust/source
  metadata, tool slice entries, and uniform-name index entries under `m.mu`,
  releases the lock, and only then calls `Close`. Tests prove removal is visible
  and an unrelated writer proceeds while a closer blocks. The 14/02 warning on
  close failure remains intact and is asserted with server name and error.
- `GO-PLUGIN-005`: live `validateCrossRefs` contains eight uniqueness maps, not
  the stale count of nine. It remains unchanged: the eight loops also carry
  section-specific required-field, pattern, composite-key, or matcher rules, so
  a generic uniqueness helper would add indirection without removing those
  distinct semantics. No `GO-PLUGIN-006` sub-registry work was inferred; AD-35
  assigns the postponed teardown extraction to P3.
- `GO-AGENT-005`: the devmode mux profile now stamps `SourceInternal`; its
  devmode-only regression proves both the provenance value and the resulting
  internal/read-only management class.
- AD-32 / `GO-MEM-004`: upgraded the stale documentation-only task row to the
  decided enforcement scope. `Broker.Fetch` centrally normalizes relevance from
  all four live sources (`conduit`, `memory`, `pcc`, `session`) to a finite
  `[0,1]` contract: NaN/negative values become 0 and positive overflow becomes
  1. The interface/item documentation and a four-source boundary regression pin
  the contract. `source_pcc.go` was not changed; its 14/02 not-found-as-absence
  and real-error propagation tests remain green. `GO-MEM-005` receives no code
  or duplicate comments; the accepted estimator divergence already has its P2
  follow-up.
- `GO-MEM-006`: chose bounded state internal to `Detector`, avoiding session-hook
  expansion. A FIFO of session IDs caps retained windows at 4096 by default;
  `WithMaxSessions` makes the cap small and deterministic in tests. Regressions
  prove oldest-session eviction clears detection state and `Reset` removes its
  FIFO entry.
- `GO-MEM-009`: threaded `context.Context` through `RuntimeStore.ListRunningRows`
  and `MarkRuntimeOrphaned`, the production adapter, fakes, and the live reaper
  calls. Regressions prove both calls receive the sweep context and canceled
  list work returns `context.Canceled`. The production store already accepted
  these contexts.
- `GO-CHAT-003`: moved only the test helper from
  `service_test_helpers.go` to `export_test.go`; `go list` confirms neither
  helper filename is in the production `GoFiles` set. `GO-CHAT-008`/`009`
  receive no code; the former already has the decided P1 architecture follow-up
  and the latter remains an accepted theoretical select race.
- `GO-RUNTIME-008`: no master complexity list exists. Closed administratively
  here as directed; `agent.Boot` is unchanged. No shared tracker file was
  edited.
- Focused verification passed: all changed packages with `-count=1`; devmode
  builtin tests; and `-race` regressions for plugin lifecycle serialization,
  MCP blocking-close/warning behavior, four-source relevance normalization,
  detector FIFO/reset behavior, and RuntimeReaper context propagation.
  Correctness lint (`errcheck,errorlint,nilerr`) reports `0 issues`.
- Repository verification: `go build ./...` and `go vet ./...` passed. A first
  `go test -count=1 ./...` run failed in `internal/service` when an asynchronous
  `driveBootSession` goroutine panicked with `send on closed channel` at
  `internal/service/chat_boot_drive.go:297` (`driveBootSession.func2`, created
  at line 290). Because the panic came from the background goroutine, that run
  did not attribute it to an exact `Test...` name. Both plausible spawning
  tests passed independently at `-count=100` and together under JSON output at
  `-count=1000`; `go test -count=1 ./internal/service` and a fresh full ordinary
  run also passed. This is the same pre-existing SendInput-failure-handler
  TOCTOU already recorded in `TASKS/ESCALATIONS.md` (2026-08-22, blame commit
  `7a0e37936`), not a new 13/05 finding; the repeat occurrence is a durable
  escalation candidate but was not fixed or re-filed here. Finally,
  `go test -race -count=1 ./...` passed, including the 233.972s store tail.
- Final combined-tree ratchet verification ran after the 13/04 correction and
  re-review landed on main. The complete pinned comparator discovered 109
  tracked Go packages and passed at baseline 3255/current 3254: no linter
  increased, while `gocognit` decreased from 257 to 256. Its Stage 2 check
  reported `errcheck=0`, `errorlint=0`, and `nilerr=0`; the separate
  audit-config correctness-only invocation also reported `0 issues`. The
  temporary 3256 attribution above is therefore resolved, and the committed
  baseline was not changed by this task.
- 2026-08-24 follow-up fix for the fresh review's P2 test-validity finding:
  `TestDetector_ResetRemovesSessionFromFIFO` previously stopped after adding
  `s3`, which merely refilled the map after `Reset(s1)` and never exercised an
  eviction. The repaired sequence adds `s4`, asserts the retained map remains
  exactly at cap two, and proves the correct oldest live session (`s2`) is
  evicted while `s3` and `s4` remain. Mutation-removing `Reset`'s FIFO cleanup
  now fails the test with `retained windows = 3, want cap 2`; restoring the
  production code makes it pass. No production change was required.
- Post-fix verification passed: ordinary and race `internal/loopdetect` suites;
  the complete pinned 109-package comparator at baseline 3255/current 3254
  with Stage 2 `errcheck=0`, `errorlint=0`, `nilerr=0`; `go build ./...`;
  `go vet ./...`; `go test -count=1 ./...`; and a fresh
  `go test -race -count=1 ./...` (store tail 233.916s). The unrelated
  `internal/worker.TestShutdown` failure seen during the prior review did not
  recur; `internal/worker` passed the full race run in 4.132s.

## Review notes

- **PASS (fresh review and re-review, 2026-08-24).** The original review of
  implementation `76eabc61` plus task finalization `2c2c06ed` independently
  confirmed every production boundary: full Host lifecycle serialization
  without holding `h.mu` across reentrant callbacks; MCP registry removal under
  lock followed by `Close` after unlock with the 14/02 warning preserved; the
  unchanged eight-map `validateCrossRefs` structure; devmode
  `SourceInternal`; central finite `[0,1]` relevance normalization across all
  four live sources without changing PCC absence-versus-error behavior;
  bounded detector state; RuntimeStore context propagation and cancellation;
  the test-only elicitation helper rename; and the stated no-code dispositions.
  The pre-existing `driveBootSession` panic remained correctly attributed to
  its existing escalation. That review found one issue: the original detector
  reset test was a false positive because adding only `s3` refilled the cap and
  never forced FIFO eviction.
- Re-review of fix `356f1b97` confirmed its diff changes only the detector test
  and this task record, with no production change. Independently removing only
  `Reset`'s FIFO cleanup made the strengthened `s1,s2,Reset(s1),s3,s4`
  sequence fail with `retained windows = 3, want cap 2`; restoring the cleanup
  made the exact test and full `internal/loopdetect` package pass. A
  `-race -count=100` run of the eviction and reset regressions also passed,
  proving the repaired test observes both the configured cap and correct live
  FIFO eviction rather than map refill alone.
- Independent final gates passed on the exact fixed tree: the pinned
  golangci-lint v2.11.4 comparator discovered 109 tracked packages and reported
  baseline 3255/current 3254 with no increase (`gocognit` 257 to 256), while
  Stage 2 remained `errcheck=0`, `errorlint=0`, `nilerr=0`; `go build ./...`;
  `go vet ./...`; `go test -count=1 ./...`; and
  `go test -race -count=1 ./...`. The full race run included
  `internal/store` at 242.594s and `internal/worker` at 4.214s; the prior
  transient `internal/worker.TestShutdown` failure did not recur. No review
  findings remain.
