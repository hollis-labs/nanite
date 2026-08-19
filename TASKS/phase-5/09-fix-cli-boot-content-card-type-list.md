# Fix CLI-agent boot content to source the Card type list dynamically instead of hardcoded/stale content

**Phase:** 5
**Status:** not-started
**Depends on:** none, but should land after `04`-`07` (the primitive-composition rebuilds) so the dynamically-sourced list reflects the final, post-rebuild type set rather than needing a second pass
**Touches:** `internal/runtime/agent/sandbox_content_envelope.go` (`envelopeSchemaContent` — planted `.sandbox/envelope-schema.md`), `internal/runtime/agent/sandbox_content_claude.go` (`claudeMDBody` — the CLAUDE.md addendum planted into Claude-CLI boot dirs), `internal/chat/envelope.go` (`EnvelopeRegistry()`/`registeredTypes` — the live, in-process registry to query instead of hardcoding), `internal/plugin/builtin/adapter-claude/plugin.go:168` (the planting call site)

## Context

Architecture doc `08-cards.md`: *"CLI-launched agents are currently told to consult files for 'the current card type list' that don't exist in this repo, and separately given a stale, incomplete list directly in their boot prompt. Fix as part of the CLI boot-directory content work — don't hardcode a static type list into planted content again; source it from the manifest the codegen check already validates against."*

### Two separate stale-content problems, not one — verified, both real

1. **`.sandbox/envelope-schema.md`** — content: `envelopeSchemaContent` const (`internal/runtime/agent/sandbox_content_envelope.go:8-155`), planted via `internal/plugin/builtin/adapter-claude/plugin.go:168` into every Nanite-managed boot dir regardless of provider. Contains a hardcoded "Registered Envelope Types" table of **exactly 7 types** — `giphy-modal`, `document-viewer`, `report-card`, `kb-result`, `ticket-confirmation`, `ticket-form`, `resolution-capture` — with the line *"These are the only types the frontend can render. Using any other type causes the envelope to be silently dropped."* **5 of these 7 are cut in Phase 0** (`giphy-modal`, `kb-result`, `ticket-confirmation`, `ticket-form`, `resolution-capture` — items 15a/15c). Post-Phase-0, this file describes 5 nonexistent types and is missing all ~17-20 real remaining ones (the 8 Phase-7 primitives, `approval-card`/`proposal-card`/`document-viewer`/`report-card`/`error-report`, `artifact-mini`, `elicitation-prompt`, plus this phase's composed replacements) — a materially worse mismatch than before Phase 0 even landed.
2. **`claudeMDBody`** (`internal/runtime/agent/sandbox_content_claude.go:43-59`) — the CLAUDE.md addendum planted into every Claude-CLI boot dir. Lines 48-49 point agents at `config/envelopes.yaml` and `internal/envelope/schemas/*.schema.json` for "the current registered envelope types and per-type schemas" — **both paths confirmed not to exist anywhere in this repo.** Per current root `CLAUDE.md`, the real manifest is external (`github.com/hollis-labs/go-envelopes` module's `manifest/envelopes.yaml`).

**Broader staleness, same two nonexistent paths, found beyond the two planted-content files**: live doc-comments in `internal/mcp/self_tools.go:270,293`, `internal/mcp/elicitation.go:20`, `internal/mcp/self_tools_describe_test.go:254-255`, `internal/service/chat_loop_terminated.go:12-13,18`, `internal/service/chat_loop_budget_soft_warning.go:15-16` (note: this file is cut by Phase 0 item 12, confirm before touching), `internal/service/chat_loop_state.go:103`, `internal/chat/envelope_test.go:10`. Not all of these are boot-planted content, but they're the same underlying doc-drift — sweep them alongside the two primary fixes.

### The fix does not need new codegen — a live in-process registry already exists

`internal/chat/envelope.go` already maintains `registeredTypes map[string]bool` (populated via `InitCoreTypes(types []string)`, called once at startup from `envelopes.LoadCore`'s output) and a richer `EnvelopeRegistry() *envelopes.Registry` accessor. Since boot-dir content planting happens server-side at agent-launch time (not compile time), `sandbox_content_envelope.go`/`sandbox_content_claude.go` can query `chat.EnvelopeRegistry()` directly when building the planted string, rather than hardcoding a table or pointing at a stale path — **no new manifest-sync mechanism is needed, the data is already loaded in memory by the time a boot dir gets planted.**

## What to do

1. Rewrite `envelopeSchemaContent`'s generation to build its "Registered Envelope Types" table from `chat.EnvelopeRegistry()` at plant time, not a hardcoded Go string literal.
2. Rewrite `claudeMDBody`'s pointer text to describe the real, current manifest source (the external `go-envelopes` module) instead of the two nonexistent paths — or, better, also derive the type list dynamically here rather than pointing an agent at a path it would need to separately resolve.
3. Sweep the other doc-comment sites listed above referencing the same two nonexistent paths — update or remove, confirming each file's Phase 0 disposition first (e.g. `chat_loop_budget_soft_warning.go` may already be deleted by Phase 0 item 12 by the time this task runs).
4. Verify a freshly-launched CLI agent's planted boot content reflects the real, current type list (post this phase's `04`-`07` rebuilds) — not a stale snapshot from planning time.

## Done means

- No planted CLI boot content hardcodes a static envelope-type list or points at a nonexistent file path.
- A freshly-booted CLI agent's `.sandbox/envelope-schema.md` and CLAUDE.md addendum both accurately reflect the live, current type registry.
- The broader doc-comment sweep (the additional file list above) is completed, each confirmed still-live before editing.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
