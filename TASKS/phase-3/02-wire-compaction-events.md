# Wire `compaction_events` — assign the writer at all production `CompactionPipeline{}` sites

**Phase:** 3
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
