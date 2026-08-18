# Fix model pinning in `.nanite/durable-agents/*.yaml`

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `.nanite/durable-agents/atlas-curator.yaml`, `.nanite/durable-agents/atlas-librarian.yaml`, `.nanite/durable-agents/content-strategist.yaml`, `.nanite/durable-agents/content-writer.yaml`, `.nanite/durable-agents/ideation-partner.yaml`, `.nanite/durable-agents/loom-weaver.yaml` (read-only reference: `.nanite/durable-agents/loom-curator.yaml`, `.nanite/durable-agents/orchestrator.yaml`, `pkg/models/registry.go`, `internal/service/durable_agents.go`)

## Context

TASKS.md Phase 0 item 1: "Fix model pinning — 6 of 8 `.nanite/durable-agents/*.yaml` still pin a retired model ID." This is verified fact, not a guess — confirmed directly against the repo.

There are exactly 8 files in `.nanite/durable-agents/`. 6 of them (`atlas-curator.yaml`, `atlas-librarian.yaml`, `content-strategist.yaml`, `content-writer.yaml`, `ideation-partner.yaml`, `loom-weaver.yaml`) contain `model: claude-sonnet-4-20250514`. The other 2 (`loom-curator.yaml`, `orchestrator.yaml`) already have `model: ""` (blank).

**Why blank, not a hardcoded replacement string.** `loom-curator.yaml` carries an inline comment (CW-20260817) explaining exactly why it was already fixed this way, and it names the precise anti-pattern this task must not recreate:

> "left blank (not pinned) on purpose — store.ResolveProviderAndModel.go resolves an empty instance Model to the current runtime default at request time. This file previously pinned `claude-sonnet-4-20250514`, a snapshot the Anthropic API has since removed (verified live: every wake turn 404'd with `model: claude-sonnet-4-20250514` until this was cleared). This is the exact anti-pattern durable_agents.go's CW-20260526-0003 comment already warns about ('previous bare-literal fallbacks here were the source of the `claude-sonnet-4` 404 bug') — orchestrator.yaml already leaves this blank for the same reason. Every other `.nanite/durable-agents/*.yaml` still pins the same stale snapshot; out of scope here (Loom Curator only) but worth a follow-up sweep."

That "follow-up sweep" is this task. `internal/service/durable_agents.go` (around the `legacyProfileToInstance` construction, ~line 263) has the matching CW-20260526-0003 comment on the Go side: "Provider/Model are left as the profile's literal values (which may be empty). Resolution to the runtime default happens at request time via `store.ResolveProviderAndModel` — keeping instance rows empty when the profile is empty means a later operator edit to `user_settings` or `providers.default_model` is honored without re-seeding the instance. The previous bare-literal fallbacks here were the source of the `claude-sonnet-4` 404 bug."

`pkg/models/registry.go` (~line 126-132) independently confirms the retirement and the current replacement, in the canonical model registry itself:

```go
// claude-sonnet-4-20250514 was retired by Anthropic sometime after
// 2026-04-11 (see docs/audits/2026-04-11-tokens-and-model-hardcoding/
// 06-medium-seed-data-staleness.md, which spot-checked it as current
// at that date) — confirmed 2026-08-13 via a live 404 not_found_error
// from the Messages API. claude-sonnet-4-5-20250929 confirmed working
// end-to-end through this app against a real Anthropic key the same day.
ID: "claude-sonnet", ModelID: "claude-sonnet-4-5-20250929",
```

`pkg/models/registry.go` also has its own top-of-file warning (~line 61-73) against reintroducing a bare-literal default anywhere in Go code: "Default-model and default-provider constants were removed from this package in CW-20260526-0003. Runtime callers MUST resolve defaults through `store.ResolveProviderAndModel` ... Background: a single Go literal terminating every 'what model?' fallback chain hid the bare-alias `claude-sonnet-4` 404 bug." That warning is about Go code, not YAML seed files, but the underlying lesson — don't let a point-in-time model snapshot become a silent, unmaintained hardcode — is exactly why blanking (matching `loom-curator.yaml`/`orchestrator.yaml`) is the right fix here, not swapping one pinned literal (`claude-sonnet-4-20250514`) for another (`claude-sonnet-4-5-20250929`) that will itself go stale someday with nothing to catch it.

**The retired ID:** `claude-sonnet-4-20250514`. **What it resolves to today if referenced via the registry's `ID: "claude-sonnet"` alias:** `claude-sonnet-4-5-20250929`. But the correct *fix* for these 6 files is not to hardcode that new literal — it's to blank the field, exactly like the 2 files that already got fixed, so the value is resolved once at request time (`store.ResolveProviderAndModel`) instead of being re-baked into 6 more files that will need the same manual sweep next time Anthropic retires a snapshot.

## What to do

For each of the 6 files below, change:
```yaml
model: claude-sonnet-4-20250514
```
to:
```yaml
model: ""
```
matching the exact pattern (and ideally a short comment referencing this fix, consistent with `loom-curator.yaml`'s style — doesn't need to be as long, just enough for the next reader to know this was deliberate, not an oversight) already present in `.nanite/durable-agents/loom-curator.yaml` and `.nanite/durable-agents/orchestrator.yaml`:

- `.nanite/durable-agents/atlas-curator.yaml` (line 6)
- `.nanite/durable-agents/atlas-librarian.yaml` (line 6)
- `.nanite/durable-agents/content-strategist.yaml` (line 6)
- `.nanite/durable-agents/content-writer.yaml` (line 6)
- `.nanite/durable-agents/ideation-partner.yaml` (line 6)
- `.nanite/durable-agents/loom-weaver.yaml` (line 6)

Do not touch `loom-curator.yaml` or `orchestrator.yaml` — they're already correct and are the reference pattern.

If you find any evidence during implementation that one of these 6 agents is actually *meant* to pin a specific, different model on purpose (as opposed to inheriting the runtime default) — stop and escalate rather than guessing; nothing found during verification suggested that, but this task's job is the mechanical fix, not a new per-agent model-selection decision.

## Done means

- All 6 files have `model: ""` instead of `model: claude-sonnet-4-20250514`.
- `grep -rn "claude-sonnet-4-20250514" .nanite/durable-agents/` returns nothing (except possibly inside explanatory comments in `loom-curator.yaml`, which should stay as historical documentation).
- `go build ./cmd/nanite/` and `go test ./...` still pass (this is a data file change; no Go code touched, so this should be a no-op verification, but run it per the standard worker checklist).
- If a durable-agent smoke/reconciliation test reads these YAML files directly (check `internal/service/durable_agent_recipes_test.go` and similar), confirm it still passes with blank models.

## Work log

**2026-08-18 — worker report:** Changed `model: claude-sonnet-4-20250514` → `model: ""` in all 6 target files, each with a short comment referencing the CW-20260817 fix and pointing to `loom-curator.yaml`. `loom-curator.yaml`/`orchestrator.yaml` left untouched. No evidence any of the 6 agents needs a pinned model on purpose. `go build`/`go vet`/`go test` all pass (pure data-file change, no code touched). No escalations.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
