# Retire the boot-profile catalog as a standalone system

**Phase:** 2
**Status:** not-started
**Depends on:** `01-wire-runtime-kind-routing.md` (the catalog's own `pty-*` handling must not be the last live routing path once this lands); `TASKS/phase-0/18a-cut-dead-storage-and-config.md` (already cuts `agent_boot_plans`, a related-but-distinct earlier attempt at the same problem — confirm landed, do not re-cut it here)
**Touches:** `internal/bootprofile/*` (full package — `loader.go`, `profile.go`, `compiler.go`, `registry.go`, `slots.go`, `requirements.go`, `agentcontext_adapter.go`, `launchplan_bridge.go` or equivalent), `internal/config/config.go` (`BootProfileCatalogPath`/`ResolvedBootProfileCatalogPath`), `examples/boot-profiles/` (example catalog — retire alongside), `docs/boot-profile-cli-harness.md` (update or retire — **currently live-linked from this project's own root `CLAUDE.md`**, see Context), `internal/service/chat_bootprofile_resolve.go`, `internal/service/chat_boot_drive.go`

## Context

Architecture doc `02-agent-launching.md`: *"The boot-profile catalog is retired as a standalone system. It never competed with agent construction — it only ever overrode prompt content, with a real `agents` row still resolved underneath. Two pieces carry forward as first-class, DB-configurable mechanisms available to every agent... Nothing else carries forward. 'Lineage' ... is dropped entirely. Cross-app portability ... is deliberately opt-in, not a structural default."* Decision log §7 has the full reasoning, including: *"`agent_boot_plans` (previously flagged as dead/unwired code, a judgment call) is now understood in this light too — it looks like an earlier, abandoned attempt at the same 'plant items into a boot dir' problem this catalog already solves differently. Reinforces treating it as safe to cut rather than revive."* — that cut is Phase 0's job (`18a`), not this task's; confirm it landed rather than re-doing it.

### `LineageAlias`/`LineageID` — confirmed real, confirmed drop-entirely

`internal/bootprofile/profile.go:52-53` (`LineageAlias string`, `LineageID string`), consumed throughout `loader.go` (catalog keys by `LineageAlias`, required non-empty), `compiler.go` (`lineage_alias`/`lineage_id` rendered into compiled output). Per decision log §7: *"Built specifically for Tesseract to observe how an agent definition changed across versions over time — not a continuity/hand-off mechanism as originally guessed... 'we got no value out of it.' Nothing to port, nothing to reconcile against the new `agents.id` model."*

### **`docs/boot-profile-cli-harness.md` is currently live, not a stale doc — do not retire it silently**

This project's own root `CLAUDE.md` currently reads: *"Boot profiles let an operator register shared catalog YAML that surfaces as additional rows in the chat composer's provider/model dropdown... See `docs/boot-profile-cli-harness.md` for the catalog schema, end-to-end happy path..."* This CLAUDE.md reference must be rewritten (not just the underlying doc retired) as part of this task, or a developer reading the live project instructions will be pointed at a doc describing a system that no longer exists. Same caution applies to `examples/boot-profiles/`, which CLAUDE.md calls out as wired into a real smoke test (`go test ./internal/service/ -run TestBootProfileSmoke_`) — confirm that test's fate explicitly (retire it alongside the catalog, or repoint it if any of its assertions genuinely carry forward to the two ported mechanisms).

## What to do

1. Confirm `02-port-forward-dynamic-resolver.md` and `03-mandatory-post-compaction-reread.md` have landed (or land in the same batch) — those two carry forward the only pieces of this system with real value; this task should not delete anything before its replacement exists, per this project's general build-then-cut sequencing principle.
2. Delete `internal/bootprofile/*` in full once nothing references it — confirm via grep no remaining import.
3. Remove `internal/config.Config`'s `BootProfileCatalogPath` field and its resolution logic.
4. Retire `examples/boot-profiles/` and the `TestBootProfileSmoke_*` test suite, or repoint the smoke test at whatever `03`/`04` build if genuinely equivalent coverage is wanted (a worker judgment call — don't force artificial test coverage of a retired system just to preserve a test name).
5. Rewrite this project's root `CLAUDE.md` (and any other project doc referencing the boot-profile catalog by name) to describe the two carried-forward mechanisms instead of the retired catalog — do not leave the current "Boot profiles let an operator register shared catalog YAML..." paragraph pointing at a deleted system.
6. Retire or clearly supersede `docs/boot-profile-cli-harness.md` — per the same standing policy Phase 6 item 2 applies elsewhere, either delete it (its content is fully superseded by this task's replacement) or leave it with an explicit "superseded" banner if it retains real historical value. This is a small, self-contained instance of that broader policy question, not something that needs to wait for Phase 6.

## Done means

- `internal/bootprofile` package is fully deleted; `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass with no remaining references.
- `LineageAlias`/`LineageID` are gone from the codebase entirely — confirmed no lingering references anywhere, including tests.
- Root `CLAUDE.md` accurately describes the post-retirement mechanism, not the deleted catalog.
- `docs/boot-profile-cli-harness.md` is either deleted or carries an explicit superseded banner.
- Every prior boot-profile-catalog-driven launch still launches correctly via the new mechanisms (`03`/`04`) — verified in a real session, not just build/test passing.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
