# Collapse the three `ENVELOPE_DATA` marker-extraction implementations into one

**Phase:** 2 — Telemetry, consumer cleanup, worked example (`TASKS/harness-reactive-self-tools`)
**Status:** not-started
**Depends on:** none. Not required for `04-render-card-construction.md`'s render_card path to work — the three consumers below are already marker-agnostic and pick up a correctly-formatted marker regardless of which code produced it (see `04`'s own Context for why). This task is a separable DRY cleanup the design doc flags, not a functional prerequisite for anything else in this batch. Can land before, after, or in parallel with the rest of Phase 1/2.
**Touches:** `internal/service/chat_tool_executor.go` + `internal/service/chat_generate.go` (`captureEnvelopeData`, `internal/service/chat_generate.go:3049`), `internal/api/tools_call.go` (`extractEnvelopeMarker`, `internal/api/tools_call.go:129`), `internal/mcpserver/handlers.go` (`convertEnvelopeMarkers`, `internal/mcpserver/handlers.go:30`) — a new shared location for the collapsed function (package TBD, see step 1).

## Context

`docs/engineering/architecture/11-harness-reactive-self-tools.md`'s "The import-cycle constraint..." section, closing paragraphs: *"today, three independent consumers... each hand-scan for the same marker — the exact 'duplicated by necessity, not oversight' shape the taxonomy doc already diagnosed for `dispatch_to_agent`'s three call sites... This design's fix is the same shape as [reflex-taxonomy] `03`'s: collapse the three independent interpretations into one shared function... called from each door that structurally has to exist."*

Confirmed directly against this checkout (not assumed) before writing this task — all three are real, independently-implemented marker scans:

- `internal/service/chat_generate.go:3049`, `captureEnvelopeData(result string, pending []string) []string` — index-based (`strings.Index`) extraction, called from `internal/service/chat_tool_executor.go:701` and `:813`.
- `internal/api/tools_call.go:121-129`, `extractEnvelopeMarker(result *mcp.ToolResult) string` — its own constant pair (`envelopeMarkerOpen`/`envelopeMarkerClose`) and its own extraction logic, structurally similar to `captureEnvelopeData` but a separate implementation, called at `internal/api/tools_call.go:101`.
- `internal/mcpserver/handlers.go:27-32`, `convertEnvelopeMarkers(text string) string` — a third, again-separate implementation, using its own local `startTag`/`endTag` constants.

**Unlike `dispatch_to_agent`'s three call sites** (which the parent taxonomy doc collapsed into one shared decision *engine*, `internal/agent/reflexes/resolve.go`'s `Resolve()`, called from all three), these three marker-extraction functions have no import-cycle reason to stay separate — none of `internal/service`, `internal/api`, or `internal/mcpserver` has a constrained-import problem calling a shared helper the way `internal/mcp`/`internal/selftools` do. The duplication here is plain code duplication, not an enforced-by-the-import-graph necessity. That makes this a lower-risk, more mechanical DRY task than `03`'s reflex-decision-engine collapse was — find where all three packages can safely import a new shared function without creating a cycle (check each of `internal/service`, `internal/api`, `internal/mcpserver`'s own existing import graph first — do not assume; a shared home under `internal/chat` (this project's own envelope-types home per this project's `CLAUDE.md`) is a reasonable starting guess but must be confirmed, not assumed).

## What to do

1. **Locate a shared home.** Confirm (via `go list -deps` or direct inspection of each file's own imports) that `internal/service`, `internal/api`, and `internal/mcpserver` can all import a single target package without a cycle. `internal/chat/envelope.go` (already the source of truth for envelope *types* per this project's own `CLAUDE.md`) is the natural first candidate — verify, don't assume, since `internal/chat`'s own import graph may or may not already reach all three callers cleanly.
2. **Extract one shared function**, e.g. `func ExtractEnvelopeMarker(text string) (json string, ok bool)`, matching the exact marker delimiters all three existing implementations already agree on (`<!--ENVELOPE_DATA:` / `:ENVELOPE_DATA-->`) and the exact extraction semantics the majority of the three already share (confirm all three actually agree on single-vs-multiple-marker handling, trimming, and malformed-marker behavior before assuming they're interchangeable — if they've silently diverged, document the divergence in this file's Work Log and pick one behavior deliberately rather than accidentally standardizing on whichever you happened to port first).
3. **Rewire all three real call sites** (`chat_tool_executor.go`/`chat_generate.go:captureEnvelopeData`'s callers, `tools_call.go:101`, `mcpserver/handlers.go`'s caller) to use the new shared function instead of their own local implementation. Delete the three now-dead local implementations and their now-dead local marker-delimiter constants.
4. **No behavior change** is the goal — if step 2 found a real divergence between the three (see above), that's the one place this task is allowed to change observable behavior, and it must be called out explicitly and deliberately in the Work Log, not buried in a generic "refactor" description.

## Done means

- One shared marker-extraction function exists in a location all three callers can import without a cycle; the three original hand-scan implementations are deleted, not left dead alongside the new one.
- Every existing test that exercised any of the three original functions (directly or via its caller) still passes unchanged, proving this is a behavior-preserving refactor (or, if step 2 found a real divergence, the Work Log documents exactly what changed and why, and any test asserting the old divergent behavior is updated deliberately, not silently).
- A regression test proves the shared function works correctly when called from all three original call sites' actual code paths (not just the extracted function in isolation).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your shared-home package choice (step 1) is documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
