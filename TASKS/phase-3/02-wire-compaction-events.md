# Wire `compaction_events` — assign the writer at all production `CompactionPipeline{}` sites

**Phase:** 3
**Status:** implemented
**Depends on:** `TASKS/phase-0/29-cut-prompt-templates.md` (already relocates the compaction-disclosure content into a single hardcoded string — confirm landed; this task does not duplicate that work, only wires the event writer)
**Touches:** `internal/api/sessions.go:671` (manual `/compact` endpoint), `internal/service/chat_generate.go:2372,2636` (**three** production `CompactionPipeline{}` construction sites total — see Context), `internal/context/handoff_stash.go` (`CompactionEventWriter` interface), `internal/store/compaction_events.go` (`Store.WriteCompactionEvent` — already real), new adapter in `chat_generate.go` mirroring `storeCompactionEventReader`

## Context

Architecture doc `06-session-lifecycle-and-recovery.md`: *"`compaction_events`... is fully built on both read and write sides but the writer is never assigned at either production call site — a one-line wiring gap that also silently kills the 'CompactionContract disclosure' feature."* Decision log §20 says "both" call sites — **verified during planning this is actually three, not two**: `internal/api/sessions.go:671`, `internal/service/chat_generate.go:2372`, and `chat_generate.go:2636`. None of the three currently set `pipeline.CompactionEventWriter`.

### The interface and its store-layer implementer both already exist — the gap is purely the adapter + assignment

`CompactionEventWriter.WriteCompactionEvent(ctx, CompactionEvent) error` (`internal/context/handoff_stash.go:43`) has a real store-layer implementer, `Store.WriteCompactionEvent(ctx, store.CompactionEvent) error` (`internal/store/compaction_events.go:32`) — different package-local `CompactionEvent` types, needs a small field-by-field adapter, not new logic. **An exact precedent to copy already exists in the same file**: `chat_generate.go:2734-2760`'s `storeCompactionEventReader` bridges `CompactionEventStore.GetLatestCompactionEvent` → `ctxpkg.CompactionEventReader` the same way. This task's real work is writing the mirror-image `storeCompactionEventWriter` and assigning it at all three sites.

### Confirms the disclosure feature is currently silently dead, exactly as the docs describe

`renderCompactionDisclosure` (`internal/chat/context.go:43-69`, per `TASKS/phase-0/29-cut-prompt-templates.md`'s own Context section, already read in full) starts with `evt, err := s.GetLatestCompactionEvent(...)`, which returns `nil` today since nothing writes `compaction_events` — the disclosure feature is silently dead until this task lands. Phase 0 #29 already did the disclosure-*content* relocation (a single hardcoded Go string, mode-branching collapsed since modes are cut) as a *prerequisite* to cutting `prompt_templates` — **that content work is done; this task's job is purely wiring the event writer that makes the already-relocated content actually reachable.**

## What to do

1. Write `storeCompactionEventWriter` in `internal/service/chat_generate.go` (or the same file `storeCompactionEventReader` lives in), mirroring its exact adapter pattern — field-by-field translation from `ctxpkg.CompactionEvent` (or whatever the pipeline-side type is) to `store.CompactionEvent`.
2. Set `pipeline.CompactionEventWriter = storeCompactionEventWriter{s: s.store}` (or equivalent) at all **three** confirmed construction sites: `internal/api/sessions.go:671`, `chat_generate.go:2372`, `chat_generate.go:2636`.
3. Verify the disclosure feature actually fires post-wiring: trigger a real compaction in a dev session, confirm a `compaction_events` row lands, confirm `renderCompactionDisclosure` now returns real content on the next turn instead of its `nil`-fallback path.

## Done means

- All three production `CompactionPipeline{}` sites have a real, working `CompactionEventWriter` assigned.
- A real compaction event, triggered in a dev session, produces a `compaction_events` row and a visible disclosure message on the next turn.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

**Confirmed the three sites, current locations (line numbers had shifted from the task's approximate ones due to Phase 0/1/2 merges landing since):**
- `internal/api/sessions.go:406` — manual `/compact` endpoint (`handleCompactSession`).
- `internal/service/chat_generate.go:2230` — `recoverFromContextOverflow` (compact-recoverable / context-overflow path).
- `internal/service/chat_generate.go:2484` — `enforceBudgetOrCompact` (pre-loop budget gate).

All three construct `&ctxpkg.CompactionPipeline{...}` and call `.Run(ctx)` or `.RunForce(ctx)` — confirmed genuinely 3 distinct sites, not 2, matching the task's own correction of the decision log's "both."

**Wrote `storeCompactionEventWriter` in `internal/service/chat_generate.go`**, immediately after the existing `storeCompactionEventReader`/`NewCompactionEventReader` (lines ~2549-2587), mirroring its exact shape: an unexported adapter struct wrapping `CompactionEventStore`, a method satisfying `ctxpkg.CompactionEventWriter.WriteCompactionEvent(ctx, ctxpkg.CompactionEvent) error` that does a field-by-field translation to `store.CompactionEvent` (with `append([]string(nil), ...)` defensive copies on the three slice fields, matching the reader's pattern), and an exported `NewCompactionEventWriter(s CompactionEventStore) ctxpkg.CompactionEventWriter` constructor. No new logic invented — this is a pure translation shim, same as the reader.

**Wired `CompactionEventWriter` at all three sites:**
- `internal/api/sessions.go:406` — `CompactionEventWriter: service.NewCompactionEventWriter(a.Services.Store)` (the `service` package was already imported in this file for `service.BuildSummarizer`/`service.ClassifyCompactionMode`, so no new import needed).
- `chat_generate.go:2230` and `chat_generate.go:2484` — `CompactionEventWriter: NewCompactionEventWriter(s.store)` (same package, unqualified call).

Ran `gofmt -w` on both touched files after adding the new struct field (alignment of the other literal fields shifts to match the longer `CompactionEventWriter:` key).

**Real-compaction verification (Done means bullet 2).** This ran in a sandboxed, non-interactive worker environment with no configured LLM API key and no existing "dev session" HTTP harness readily available, so I built the most literal, fully-real substitute achievable: a new permanent regression test, `TestCompactionEventWriter_WiredAtAllThreeSites_EndToEnd` in `internal/service/compaction_event_writer_wiring_test.go`. It drives the **exact same construction shape** used at all three production sites — real on-disk-format SQLite store (`store.New`, full migration path), real `chat.NewContextClient` + `NewContextService` (the actual `AssembleSlots` production path, not a stub), a real `ctxpkg.CompactionPipeline` (unmodified production stages: drop-enrichment → dedupe → summarize-oldest → strip-tool-blocks), and the real `NewCompactionEventWriter` adapter I just wrote — with only the LLM call itself stubbed via the `Summarizer` interface (the same seam production already uses; no network/API-key dependency). The test:
1. Seeds a session with 12 real messages (long enough to clear `stageSummarizeOldest`'s `SummarizeMinTokens=200` negative-savings guard).
2. Calls the real `svc.AssembleSlots` to get a real `ContextWindow`.
3. Builds `&ctxpkg.CompactionPipeline{...CompactionEventWriter: NewCompactionEventWriter(s)}` and calls `pipeline.RunForce(ctx)` (mirrors the manual `/compact` endpoint's call, which also runs every stage unconditionally — avoids needing to hand-craft an exact token budget to trip `NeedsCompaction()`).
4. Asserts `s.GetLatestCompactionEvent(ctx, sessionID)` returns a real, non-nil row (proving the writer persisted).
5. Calls `svc.AssembleSlots` again (the real "next turn" context-build path) and asserts the returned `SystemPrompt` contains `"Compaction Notice"` — the disclosure template's title, proving `renderCompactionDisclosure` (`internal/chat/context.go`, reached via `assembleAgentSlotContent`) actually fired and injected real content instead of its nil-fallback.

**Negative-check performed and then reverted** (not left in the diff): temporarily commented out the `CompactionEventWriter:` line in the test and re-ran it — it failed exactly at the "expected a compaction_events row... got nil" assertion, confirming the test genuinely catches the wiring-gap regression this task fixes, not a tautology. Restored the real wiring line afterward; `git status --short` confirmed no stray `.bak`/temp files were left behind.

**One real timing bug found and fixed while building the test, not a design change**: `internal/chat/context.go`'s freshness check (`isCompactionEventFresh`) is a strict `eventCreatedAt > lastAssistantMessage.CreatedAt` comparison, and both `store.CreateMessage` and the compaction pipeline's event timestamp are second-precision (`time.Now().UTC().Format(time.RFC3339)`). Running the test's message-seeding and compaction back-to-back landed both inside the same wall-clock second, so a genuinely fresh event compared equal (not `>`) to the last assistant message and the disclosure came back empty — not a wiring bug, a timestamp-precision race in the test's own construction. Added a `time.Sleep(1100ms)` between seeding messages and running compaction with a comment explaining why. This is a test-only fix; no production code changes as a result (the second-precision freshness check itself is out of this task's scope and not something the task asked me to touch).

**Updated a now-stale test comment**: `internal/chat/compaction_disclosure_test.go`'s `TestInterpolateDisclosure_syntheticEvent` doc comment previously said "compaction_events has no production writer wired yet... can't be triggered end-to-end by a real compaction in a live session today" — updated to note the writer is now wired (this task) and point at the new end-to-end test, while keeping the synthetic-input unit test itself unchanged (it's still useful as a direct, fast unit test of `interpolateDisclosure`'s formatting, independent of the store round-trip).

**Baseline checks:**
- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — two pre-existing warnings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` "not used on all paths" possible-context-leak lint), confirmed via `git stash` that these exist on the pre-task baseline too and are unrelated to this change (I did not touch `container.go`).
- `go test ./...` — all packages pass, including the new end-to-end test.

**No deviation from the task's stated scope.** `sessions.compaction_summary`/`compacted_at` (mentioned in the architecture doc's Cleanup section as "cut once compaction_events is wired") were already cut independently in `TASKS/phase-0/26-cut-session-compaction-summary-fields.md` (landed prior to this task, confirmed via that task's own file) — out of scope here and not touched.

**Commit:** left as a single commit in this worktree — see the branch for the exact SHA (`git log -1`).

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
