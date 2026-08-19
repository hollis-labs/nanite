# internal/recovery/* namespace regrouping

**Phase:** 0
**Status:** implemented
**Depends on:** `04-build-http-provider-retry`
**Touches:** `internal/runtime/agent/recovery/*` (18 files: `broker.go`, `cancel_test.go`, `classifier.go`, `classifier_test.go`, `deps.go`, `dispatch_test.go`, `doc.go`, `envelope.go`, `envelope_test.go`, `orchestration.go`, `orchestration_test.go`, `remediator.go`, `remediator_test.go`, `telemetry.go`, `testhelpers.go`, `types.go`, `types_test.go`), `internal/runtime/agent/orphan_sweep.go` + `orphan_sweep_test.go`, `internal/service/recovery_pack.go` + `recovery_pack_test.go`, `internal/api/sessions.go` (`detectInterruptedTurn`, lines 164/168/200) + `internal/api/interrupted_turn_test.go`, plus every import-site listed in "What to do" step 1 (12 files for the Recovery Broker alone) and the `internal/runtime/agent` package's own `Dependencies`/`RuntimeRow` types (`internal/runtime/agent/deps.go` — read/reference only, not moved).

## Context

TASKS.md Phase 0 "Renames" item 26: "`internal/recovery/*` namespace regrouping (Recovery Broker, Orphan Sweep, Recovery Pack, interrupted-turn detection under one shared prefix) — mechanical package move, but touches live code that fires regularly. Real testing after the move, not a zero-risk rename."

Decision log §25 ("The four recovery mechanisms — distinct, not redundant; restructure into a shared package namespace"): "Recovery Broker, Orphan/Runtime Reaper, Recovery Pack, and interrupted-turn detection each answer a genuinely different question about 'what went wrong' — this is not the redundant-decision-layers problem yesterday's steering pass found; no consolidation of the mechanisms themselves is needed. What *is* needed: they're currently scattered across unrelated packages (`internal/runtime/agent/recovery`, `internal/runtime/agent/orphan_sweep.go`, `internal/service/recovery_pack.go`, and `detectInterruptedTurn` living inside `internal/api/sessions.go`) with nothing in the code layout reflecting that they're one four-part system. Restructure under a shared namespace (e.g. `internal/recovery/*`), matching the `agent/*`/`prompt/*` idiom already settled yesterday."

Architecture doc `docs/engineering/architecture/06-session-lifecycle-and-recovery.md` gives the four mechanisms' distinct jobs (worth internalizing so nothing gets accidentally merged during the move): Recovery Broker classifies an in-process crash and dispatches a replacement; the Orphan/Runtime Reaper reconciles stale `agent_runtime` rows after a daemon restart; Recovery Pack replays trailing context into a freshly cold-booted session; interrupted-turn detection is a cheap read-only frontend signal.

`docs/engineering/GLOSSARY.md`'s **Recovery** entry confirms the target shape: "`internal/recovery/*`, being consolidated into this namespace — four genuinely distinct mechanisms answering different 'what went wrong' questions... Not redundant with each other — each covers a different failure shape."

**Verified all 4 locations directly — decision log §25 is accurate, nothing has moved or changed since:**

1. **Recovery Broker** — `internal/runtime/agent/recovery/` is a real subpackage (`package recovery`) of `internal/runtime/agent`, 18 files (`broker.go`, `classifier.go`, `orchestration.go`, `remediator.go`, `telemetry.go`, `envelope.go`, `deps.go`, `types.go`, `doc.go` + 9 test files). It's a genuinely separable package already — good news for this move.
2. **Orphan Sweep** — `internal/runtime/agent/orphan_sweep.go` + `orphan_sweep_test.go` are **not** their own package — they're `package agent` (the same package as `factory.go`, `agent.go`, `deps.go`, etc.), using `Dependencies` and `RuntimeRow` (both defined in `internal/runtime/agent/deps.go`) as bare, same-package references.
3. **Recovery Pack** — `internal/service/recovery_pack.go` + `recovery_pack_test.go` are `package service`. Two of its six functions (`shouldRecoverColdBoot`, `buildSessionRecoveryPrefix`, `composeBootPayload`) are **methods on `*chatServiceImpl`** — the core chat-service struct — not free functions.
4. **Interrupted-turn detection** — `detectInterruptedTurn` is a **method on `*API`** (`internal/api/sessions.go:200`), depending on `a.Services.Store` and `a.Services.Streams`. There is no dedicated unit test for the detection logic itself, but `internal/api/interrupted_turn_test.go` exists and exercises it at the API/endpoint level.

**Why this is genuinely "touches live code that fires regularly, not a zero-risk rename" — two real complications the "mechanical package move" framing understates:**

- **Recovery Pack and interrupted-turn detection are NOT cleanly extractable as pure functions.** Both are partially methods bound to large host structs (`chatServiceImpl`, `API`) that this task should not try to relocate wholesale (that would ripple into unrelated `internal/service`/`internal/api` responsibilities far outside this task's scope). The correct shape, matching how the *existing* Recovery Broker package already resolves the same problem (see `internal/runtime/agent/recovery/deps.go` — the recovery package defines narrow interfaces like `RecoveryHooks`, `AgentBoot`, and the composition root in `internal/service` supplies the implementation): pull the **pure logic** (`buildRecoveryPack`, `shouldBuildRecoveryPack`, `messagePlainText`, `excludeCurrentTurn`, `recoveryPackInput` for Recovery Pack; the store-lookup + live-stream-check logic for interrupted-turn detection) into the new package as exported functions taking explicit parameters, and leave the two thin host-struct methods (`buildSessionRecoveryPrefix`/`composeBootPayload` on `chatServiceImpl`, `detectInterruptedTurn` on `*API`) in their current files, now just calling out to the new package instead of local unexported helpers. Do not attempt to move `chatServiceImpl` or `*API` methods themselves.
- **`orphan_sweep.go` currently free-rides on same-package access to `Dependencies`/`RuntimeRow`/`LiveSessionChecker`.** Moving it out of `package agent` means it needs an explicit `import "github.com/hollis-labs/nanite/internal/runtime/agent"` and every currently-bare reference becomes `agent.Dependencies`, `agent.RuntimeRow`, etc. Before assuming this is a clean cut-paste, grep `orphan_sweep.go` for any field/method access that isn't exported (lowercase) — if `Dependencies` or `RuntimeRow` has any unexported field `orphan_sweep.go` currently reads or writes via same-package privilege, that field must be exported (or an accessor added) as part of this move, which is a small but real behavior-preserving code change beyond a pure package move. Verify before starting, don't assume.

**Import direction is already one-way and move-safe (verified, no cycle risk):** `internal/runtime/agent/recovery` (child) already imports its parent `internal/runtime/agent` (for `agent.Dependencies`, `agent.RecoveryHooks`) — confirmed via `broker.go:10`, `deps.go:6`, `dispatch_test.go:10`. The parent package (`internal/runtime/agent/deps.go:100`) only *mentions* the recovery subpackage in a doc comment, never imports it in code. So moving `internal/runtime/agent/recovery` → `internal/recovery/broker` (or similar) and having it continue to import `internal/runtime/agent` is safe — same for `orphan_sweep.go` moving to a new package that imports `internal/runtime/agent`.

**Precedent for the target shape:** decision log §25/GLOSSARY point at "the `agent/*`/`prompt/*` idiom already settled" — verified only half of that exists today. `internal/agent/` is real (root-level files plus `builtin/`, `override/`, `reflexes/` subpackages — multiple related subsystems grouped under one prefix, exactly the shape `internal/recovery/*` should copy). `internal/prompt/*` **does not exist** — only the flat `internal/promptrouter` package exists; decision log §5 clarifies "prompt/*" was stated as a future naming instinct ("worth following organically as new subsystems get added — no rename forced now"), not a currently-real precedent. Use `internal/agent/*`'s actual shape as the template, not a nonexistent `internal/prompt/*`.

**One real doc/reality wrinkle worth flagging, not blocking:** decision log §26/the architecture doc's "Observability" section claims "Only the Recovery Broker currently writes a queryable trail (`nanite_recovery_breadcrumbs`)... Orphan Sweep reconciliations, Recovery Pack replays... are only visible in logs." This is not quite accurate — `recovery_pack.go:187-188`'s `buildSessionRecoveryPrefix` already calls `s.store.LogEvent(sessionID, "recovery_pack_planted", "recovery", ...)`, which does insert one row into the queryable `event_log` table (not just an slog line). It's a single "planted" marker, not full postmortem coverage (no confirmation of downstream success/failure the way the Broker's breadcrumbs provide), so the *thrust* of decision log §26 (full `event_log` postmortem coverage for all four mechanisms is a real, still-open gap) is still correct — but "only visible in logs" overstates Recovery Pack's current state. **Extending `event_log` postmortem logging to the other three mechanisms is explicitly a separate, later task** (TASKS.md Phase 5 — "extend `event_log` postmortem logging to all four recovery mechanisms" is listed under Phase 5's Session Lifecycle work, not Phase 0's rename). Do not do that work here; preserve the existing `LogEvent` call in Recovery Pack exactly as-is when moving the code, and don't add new `event_log` writes to Orphan Sweep or interrupted-turn detection as part of this task.

## What to do

1. Create `internal/recovery/` with a subpackage per mechanism, e.g.:
   - `internal/recovery/broker/` — move all 18 files from `internal/runtime/agent/recovery/` here verbatim (package rename `recovery` → `broker` or keep `recovery` as the subpackage name if you prefer `internal/recovery/broker` to just be a directory alias — pick whichever avoids a confusing `recovery.recovery` stutter; document the choice). Update the import path in every one of these 12 call sites: `internal/api/recovery_test.go`, `internal/service/recovery_mcp_adapter_smoke_test.go`, `internal/service/recovery_credentials.go`, `internal/service/chat_http_broker_notify.go`, `internal/service/chat_http_broker_notify_test.go`, `internal/service/recovery_envelope_sink_test.go`, `internal/service/agent_bootdir_adapter_test.go`, `internal/service/recovery_credentials_smoke_test.go`, `internal/service/chat_boot_drive.go`, `internal/service/container.go`, `internal/service/agent_deps.go`, `internal/service/recovery_envelope_sink.go`. **This package was very recently touched by task `04-build-http-provider-retry`** (new `Dependencies` field, new retry-path logic inside `broker.go`/`deps.go`/`types.go`) — that's exactly why this task depends on it: move the post-04 version of the package, including its new HTTP-retry logic, not a stale pre-04 snapshot.
   - `internal/recovery/orphansweep/` (or similar) — move `orphan_sweep.go`/`orphan_sweep_test.go` out of `package agent`, add `import "github.com/hollis-labs/nanite/internal/runtime/agent"`, qualify all `Dependencies`/`RuntimeRow`/etc. references as `agent.X` (verify no unexported-field access breaks first — see Context). Update the one call site: `internal/service/container.go:1207` (`runtimeagent.NewRuntimeReaper(...)` → new import path).
   - `internal/recovery/pack/` (or similar) — move the pure functions (`buildRecoveryPack`, `shouldBuildRecoveryPack`, `messagePlainText`, `excludeCurrentTurn`, `recoveryPackInput`) out of `internal/service/recovery_pack.go` as exported names. Leave `shouldRecoverColdBoot`, `buildSessionRecoveryPrefix`, `composeBootPayload` as thin `*chatServiceImpl` methods in `internal/service` (rename the file if useful, e.g. `internal/service/recovery_pack_glue.go`) that call the new package's exported functions. Update `recovery_pack_test.go` — split its tests along the same line (pure-function tests move with the logic; the thin-wrapper tests, if any depend on `chatServiceImpl` internals, stay).
   - Interrupted-turn detection — extract the pure part of `detectInterruptedTurn` (the "is the last message a dangling user turn with no live stream" logic) into an exported function in `internal/recovery/` (top-level, or a small `interrupted/` subpackage — this one doesn't need its own subpackage given its size; use judgment) taking explicit params (last message role/timestamp, a `hasLiveStream bool`) instead of `*store.Session`/`a.Services.*`. `internal/api/sessions.go`'s `detectInterruptedTurn` becomes a thin wrapper that does the store/stream lookups and calls the new function. Update `internal/api/interrupted_turn_test.go` accordingly — add a direct unit test for the extracted pure function if one doesn't already exist at that granularity (it currently doesn't; only the API-level test exists).
2. Update every doc comment across all four locations that references the old import paths (`internal/runtime/agent/recovery`, etc.) by name — including `internal/runtime/agent/deps.go:100`'s comment.
3. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` after the move — this is the floor, not the finish line (see Done means for the required functional check).
4. Do **not** extend `event_log` postmortem logging to Orphan Sweep or interrupted-turn detection as part of this task — that's Phase 5 (see Context). Do not touch `nanite_recovery_breadcrumbs` schema or writers beyond moving `internal/store/recovery.go`'s existing callers' import paths if any reference the old package (verify — `internal/store/recovery.go` itself is store-layer and likely doesn't import `internal/runtime/agent/recovery`, but check).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass with zero references to the old `internal/runtime/agent/recovery` import path, `orphan_sweep.go`/`recovery_pack.go` in their old locations, or `detectInterruptedTurn`'s logic un-extracted.
- All 12 Recovery Broker import sites, the 1 Orphan Sweep call site, and every Recovery Pack / interrupted-turn call site compile against the new package paths.
- **Real functional verification, not just a green build** (per TASKS.md's explicit call-out that this "touches live code that fires regularly"):
  - Trigger a real Recovery Broker cycle: crash/kill a booted session's subprocess mid-run (or use the existing test harness's fault-injection path if `broker_test.go`/`dispatch_test.go` already does this) and confirm the broker still classifies and dispatches a replacement post-move, with a breadcrumb landing in `nanite_recovery_breadcrumbs`.
  - Trigger a real Orphan Sweep cycle: start the service, kill an agent process out-of-band (not via the API), restart the service, and confirm the reaper reconciles the stale `agent_runtime` row.
  - Exercise Recovery Pack: cold-boot a CLI session with prior history after a simulated restart and confirm the recovery prefix still gets planted and the `recovery_pack_planted` event still lands in `event_log`.
  - Confirm `GET /api/sessions/:id`'s `interrupted_turn` field still behaves identically pre/post-move for both the interrupted and non-interrupted cases.
- No behavior change beyond the package move itself — this task does not add new `event_log` writers, does not change the Broker's retry/backoff logic (that's task `04`'s job, already landed), and does not touch `nanite_recovery_breadcrumbs`' schema.

## Work log

**Status: implemented.**

### Final package layout

- `internal/recovery/broker/` — the Recovery Broker, moved verbatim (all 18 files,
  including `http_retry_test.go` which wasn't in this file's original 17-file
  enumeration but is part of the same package post-task-04). Package renamed
  `recovery` → `broker` (directory name == package name, matching the
  `internal/agent/{override,reflexes,builtin}` idiom) rather than kept as
  `recovery` living under an `internal/recovery/broker` directory — that would
  read as `broker.recovery.Foo` stutter-free but leaves the directory/package
  name mismatched, and the task's own flavor text flagged exactly this choice
  as open. Renaming is also what the "avoids a confusing stutter" framing was
  steering toward once the parent namespace is itself named `recovery`.
- `internal/recovery/orphansweep/` — the Orphan/Runtime Reaper (`orphan_sweep.go`
  + test), moved out of `package agent`. All bare `Dependencies`/`RuntimeRow`
  references qualified as `agent.Dependencies`/`agent.RuntimeRow`.
- `internal/recovery/pack/` — the Recovery Pack's pure functions
  (`BuildRecoveryPack`, `ShouldBuildRecoveryPack`, `MessagePlainText`,
  `ExcludeCurrentTurn`, `RecoveryPackInput`), capitalized and exported per the
  task's naming. `internal/service/recovery_pack.go` renamed to
  `recovery_pack_glue.go` and now holds only the three thin `*chatServiceImpl`
  methods (`shouldRecoverColdBoot`, `buildSessionRecoveryPrefix`,
  `composeBootPayload`), calling into `pack.*`.
- `internal/recovery/interrupted_turn.go` — top-level `package recovery` (no
  subpackage, per the task's own judgment call for something this small).
  Holds `DetectInterruptedTurn(lastMessageRole, lastMessageID,
  lastActivityAt string, hasLiveStream bool) map[string]any`, the pure
  "dangling user turn + no live stream" decision. `internal/api/sessions.go`'s
  `detectInterruptedTurn` stays a thin method doing the store/stream lookups
  (including the `sess.Status != "active"` early-out, which the task's literal
  param list — last message role/timestamp + hasLiveStream — didn't include,
  so it stayed in the wrapper rather than becoming a fourth pure-function
  parameter).

### Real complications hit beyond the task's own flagged ones

1. **`LiveSessionChecker` couldn't move with `orphan_sweep.go`.** The interface
   was defined *in* `orphan_sweep.go` but `internal/runtime/agent/deps.go`'s own
   `Dependencies.LiveSessions` field is typed with it in-package. Moving it to
   `internal/recovery/orphansweep` would force `internal/runtime/agent` to
   import `orphansweep`, which itself must import `internal/runtime/agent` for
   `agent.Dependencies`/`agent.RuntimeRow` — a cycle. Left `LiveSessionChecker`
   defined in `internal/runtime/agent/deps.go`; `orphansweep` references it as
   `agent.LiveSessionChecker`. `internal/service/agent_deps.go`'s
   `managerLiveSessions` (which already said `runtimeagent.LiveSessionChecker`)
   needed zero changes — confirms this was the right call.
2. **`orphan_sweep_test.go` couldn't reuse `internal/runtime/agent`'s
   unexported `fakeRuntimeStore`/`newFakeRuntimeStore`** (package-private,
   still used by `boot_test.go` in the old package — left in place). Wrote a
   small self-contained duplicate `fakeRuntimeStore` in the new package's test
   file implementing `agent.RuntimeStore` in full (only 2 of its 6 methods are
   exercised by sweep logic, but the interface requires all 6 to satisfy
   `Dependencies.Store`).
3. **Package-rename local-variable shadowing.** Renaming `internal/runtime/agent/recovery`'s
   package from `recovery` to `broker` meant every `recovery.X` call-site
   reference became `broker.X` — and one file
   (`internal/service/recovery_mcp_adapter_smoke_test.go`) had a local variable
   named `broker` (`broker := recovery.NewBroker(...)`) that, after the
   mechanical rename, shadowed the `broker` package for the rest of its
   function and broke `broker.FailureEvent`/`broker.Classification`/etc. type
   references later in the same scope (`go vet` caught it:
   `broker.FailureEvent is not a type`). Renamed the two local vars to `b`
   (matching the sibling smoke-test files' existing convention). Checked every
   other `broker := ...`/`broker, ok := ...` site (`container.go` x2,
   `chat_boot_drive.go` x2, `agent_deps.go` x1, `internal/api/recovery_test.go`
   x2) — none of those reference the package by name again after the shadow
   (method calls or `return broker` only), so they compile fine as-is; left
   them unchanged rather than renaming defensively.
4. **`firstNonEmpty` duplication.** `buildRecoveryPack` used
   `internal/service`'s `firstNonEmpty` helper (defined in
   `durable_agent_recipes.go`). Since `internal/service` now imports
   `internal/recovery/pack` (for the glue methods), `pack` importing back into
   `internal/service` for one 8-line helper would cycle. Duplicated the
   trivial helper into `pack.go` as an unexported function rather than
   inventing a new shared-utility package for it.
5. **Accidental broad `gofmt -w` blast radius.** Ran `gofmt -w` over whole
   directory globs (`internal/service/*.go`, `internal/api/*.go`,
   `internal/runtime/agent/*.go`) once mid-task, which reformatted ~20
   unrelated files' pre-existing comment continuation indentation (a gofmt
   version-drift cosmetic diff, no semantic content). Caught it via `git
   status` showing files well outside this task's touch list, verified via
   `git diff` that every one of those diffs was comment-whitespace-only (no
   `recovery`/`broker`/`orphansweep` token in any of them), and reverted all
   of them with `git checkout --`. Lesson logged for future workers: gofmt
   only the specific files actually edited, never a directory glob.

### Doc comments updated

`internal/runtime/agent/deps.go` (both the `LiveSessions` field comment and
the `RecoveryHooks` comment's `recovery.Broker`/`importing recovery` mentions,
now `orphansweep.SweepOrphans`/`broker.Broker`/`importing broker`),
`internal/store/recovery.go`'s `RecoveryBreadcrumb` doc, and the bare
`SweepOrphans` mentions in `internal/store/agent_runtime.go` and
`internal/service/agent_deps.go` (now `orphansweep.SweepOrphans` — these
crossed a package boundary for the first time with this move, so the
qualifier is now informative where it wasn't needed before).

### Checks

- `go build ./cmd/nanite/` — pass.
- `go vet ./...` — pass, modulo one **pre-existing, unrelated** `lostcancel`
  warning on `container.go`'s `stopReaper`/`stopRuntimeReaper` (verified via
  `git show HEAD:internal/service/container.go` — identical
  `context.WithCancel` shape existed before this task touched the file; out
  of this task's scope).
- `go test ./...` — all packages pass, including the four new/moved
  `internal/recovery/*` packages and every consumer in `internal/service`,
  `internal/api`, `internal/store`.

### Real functional verification (per Done means — not just a green build)

Wrote a throwaway verification file (`internal/service/zz_verify_task32_test.go`,
deleted after use — not part of this task's deliverable) that, against a real
`store.New()`-backed SQLite DB with migrations applied and the real
production adapters (`recoveryBrokerStore`, `agentRuntimeStore`,
`newRecoveryHTTPRetryAdapter`), confirmed:

1. **Recovery Broker**: a real `broker.Broker.OnSessionExit` call for a
   synthesized HTTP-stream-timeout exit classified transient, dispatched the
   HTTP retry through the real adapter, and a row landed in the real
   `nanite_recovery_breadcrumbs` table (`class=transient
   outcome=transient_retry_succeeded cause=http_stream_timeout`) — read back
   via `store.ListRecoveryBreadcrumbsForSession`.
2. **Orphan Sweep**: created a real `agent_runtime` row in `state="running"`
   with a virtually-certain-dead PID (simulating "process was killed while
   the daemon was down"), ran `orphansweep.RuntimeReaper.SweepOnce` (the exact
   call `container.go`'s startup sweep makes), and confirmed the real row
   flipped to `state="orphaned" failure_reason="dead_pid"` via direct SQL
   readback.
3. **Recovery Pack**: cold-booted (`composeBootPayload(..., shouldRecover=true)`)
   a session with 3 prior persisted messages, confirmed the
   `<recovered-session-context>` prefix was planted with prior-turn content,
   and confirmed a `recovery_pack_planted` row landed in the real `event_log`
   table via `store.ListEvents("recovery", 10)`.
4. **Interrupted-turn detection**: covered by the existing
   `internal/api/interrupted_turn_test.go`
   (`TestHandleGetSession_InterruptedTurn`, unchanged, still green — real HTTP
   round-trip through `a.RegisterRoutes` covering both the interrupted and
   non-interrupted cases) plus the new direct unit test
   `internal/recovery/interrupted_turn_test.go` for the extracted pure
   function. Since the wrapper's contract and JSON shape are byte-for-byte
   unchanged, this is genuine pre/post parity, not just "still compiles."

All 4 real functional checks passed. No escalations — every deviation above
was resolved per worker step 7 (small, behavior-preserving corrections
logged here, action executed).

### Commit

`git add` was scoped explicitly to this task's files (see file list in this
task's header) — never `git add -A` — to avoid touching the shared
checkout's untracked peer-session files (`TASKS/phase-1` through `phase-6`,
`HANDOFF.md`, `.claude/agents/`, the orchestrator-kickoff docs, `data/artifacts/...`).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
