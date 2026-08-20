# `render_card` reaction — marker construction and delivery

**Phase:** 1 — Core mechanism (`TASKS/harness-reactive-self-tools`)
**Status:** implemented
**Depends on:** `03-reaction-engine-core.md` (needs `reactions.Fire`'s `render_card` resolution to produce the envelope JSON this task embeds).
**Touches:** a new small helper in `internal/selftools` (or `internal/selftools/reactions`, if it belongs closer to the resolver — your call, document it) that a self-tool handler calls to turn a resolved `render_card` payload into the existing marker string; no changes expected to `internal/service/chat_generate.go`, `internal/api/tools_call.go`, or `internal/mcpserver/handlers.go` in this task (that's `06`, and it's a separate concern — see below).

## Context

`docs/engineering/architecture/11-harness-reactive-self-tools.md`'s "The import-cycle constraint, and what it means for each reaction kind" section, specifically the `render_card` bullet: *"The resolver's job for this kind is to build the envelope payload and hand it back to the calling self-tool handler, which embeds it in the tool's own `ToolResult` text as a `<!--ENVELOPE_DATA:...-->` marker — the same signal-out mechanism `card_show`/`todo_list` already use today."*

Confirmed directly against this checkout (not assumed) before writing this task: `internal/mcp/self_tools_transport.go:961` (`callShowCard`) and `:1187` (a `todo`-adjacent handler) both build this marker today via `fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, string(envJSON))`, where `envJSON` is hand-constructed per-tool (`buildShowEnvelope` for `callShowCard`). This task's job is to give a harness-reactive self-tool the same *outcome* (a correctly-marker-wrapped envelope in its `ToolResult` text) without hand-authoring the envelope-construction logic per tool — the construction is driven by the reaction's DB-backed config (`03`'s resolved JSON) instead.

**Important scope boundary, do not conflate with `06`:** the three existing marker *consumers* (`internal/service/chat_tool_executor.go`+`chat_generate.go:captureEnvelopeData`, `internal/api/tools_call.go:extractEnvelopeMarker`, `internal/mcpserver/handlers.go:convertEnvelopeMarkers`) are **already marker-agnostic** — each one scans tool-result text for the literal `<!--ENVELOPE_DATA:...:ENVELOPE_DATA-->` string regardless of which tool or code path produced it. That means a `render_card` reaction's marker, produced by this task in the identical format `callShowCard` already uses, is picked up correctly by all three existing consumers with **zero changes to any of them**. `06-collapse-envelope-marker-consumers.md` is a separate, lower-priority DRY cleanup of those three consumers' own duplicated *parsing* logic — it is not a prerequisite for this task's own render_card path to work end-to-end, and this task does not depend on it.

## What to do

1. Build a small helper — e.g. `func EmbedRenderCardMarker(label string, resolvedEnvelopeJSON string) string` — that produces exactly the same marker format `callShowCard` already produces: `fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, resolvedEnvelopeJSON)`. Do not invent a new marker format or delimiter — matching the existing format byte-for-byte is what makes this task not need to touch the three existing consumers.
2. Wire this into the self-tool-handler-facing side of `03`'s `Fire()` result: when `Fire()`'s `Result` includes a resolved `render_card` payload, the calling handler (built for real in `07-worked-example-task-update-report.md`, but this task should prove the wiring works with a minimal test double or the worked example's own handler if `07` is already in flight) calls this new helper and appends the output to its own `ToolResult` text, alongside whatever confirmation text the handler already returns to the LLM.
3. Decide and document where this helper lives: `internal/selftools` (next to the self-tool handlers that call it) is the natural default, matching how `callShowCard` today builds its own marker inline in the same package as the handler. Only put it in `internal/selftools/reactions` instead if you find a concrete reason `03`'s resolver should own marker formatting too — document your reasoning either way.
4. Confirm (a regression test, not just reasoning) that a marker produced by this helper round-trips correctly through at least one of the three existing consumers unchanged — e.g. feed the constructed string through `extractEnvelopeMarker` (`internal/api/tools_call.go`) directly in a test and confirm it extracts the same JSON that went in. This is the concrete proof that the "zero changes needed to the three consumers" claim in this task's Context actually holds, not just an assumption.

## Done means

- The new helper exists, produces byte-identical marker formatting to `callShowCard`'s existing construction.
- A regression test proves a `render_card` reaction's resolved payload, once embedded via this helper, is correctly extracted by at least one of the three existing real consumer functions with no changes to that function.
- Your package-placement call (step 3) is documented in this file's Work Log.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Implemented 2026-08-20, directly against the shared `nanite` checkout (task `03` — `internal/selftools/reactions` — already landed).

**What was built:**

- `internal/selftools/render_card_marker.go` — `func EmbedRenderCardMarker(label string, resolvedEnvelopeJSON string) string`, producing exactly `fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, resolvedEnvelopeJSON)` — byte-identical to `callShowCard`'s inline construction at `self_tools_transport.go:962`. No new delimiter, no reshaping of the input JSON — it embeds `resolvedEnvelopeJSON` verbatim, matching item 1 of "What to do" exactly.
- `internal/selftools/render_card_marker_test.go` — two direct unit tests: `TestEmbedRenderCardMarker_MatchesCallShowCardFormat` (asserts output equals the same `fmt.Sprintf` call `callShowCard` uses, byte for byte) and `TestEmbedRenderCardMarker_EmptyLabel` (edge case — empty label still yields a well-formed marker, no panic).
- `internal/api/tools_call_test.go` — added `TestExtractEnvelopeMarker_RoundTripsRenderCardReactionPayload`, the Done-means regression test. It builds a synthetic `reactions.Result` (a minimal test double, per this task's own note that `07`'s worked-example handler is not yet landed — confirmed still true, `07` is next in queue) with one `render_card` `FiredReaction` whose `Payload` is real output from `reactions.ResolveRenderCard` (task `03`'s resolver, called with a realistic `render_card` config + payload). It pulls the resolved JSON via `Result.RenderCardPayload()` (the accessor `03` built specifically for this task to call), embeds it with the new `EmbedRenderCardMarker` helper alongside handler confirmation text (mirroring "What to do" item 2's "alongside whatever confirmation text the handler already returns"), wraps it in an `*mcp.ToolResult`, and feeds it through `extractEnvelopeMarker` (`internal/api/tools_call.go`, **zero changes made to that function**) — asserting the extracted string matches the resolved payload exactly, and separately re-parsing it to confirm the `{kind, version, type, data}` wire shape and template substitution survived the round trip intact.

**Package-placement decision (step 3):** `internal/selftools`, sibling to `self_tools_transport.go` (where `callShowCard`/`buildShowEnvelope` already live), not `internal/selftools/reactions`. Reasoning, per the task's own default-and-override framing:

- The architecture doc's own "import-cycle constraint" section is explicit about the division of labor: *"The resolver's job for this kind is to build the envelope payload and hand it back to the calling self-tool handler, which embeds it in the tool's own `ToolResult` text as a marker."* Resolving (building the envelope JSON) is `03`'s job and stays in `reactions` (`ResolveRenderCard`); embedding it as a marker in a `ToolResult` is a self-tool-handler concern, and self-tool handlers live in `internal/selftools`, not `internal/selftools/reactions`.
- `reactions` has no concept of `mcp.ToolResult` or the marker string format today (confirmed: it imports only `encoding/json`, `fmt`, `net/http`, `internal/store` — no `internal/mcp`), and shouldn't gain one just to host this helper — that would blur the resolve/deliver split the import-cycle constraint doc draws deliberately.
- No concrete reason surfaced (per step 3's override condition) for `03`'s resolver to own marker formatting too — `ResolveRenderCard`'s own doc comment already says as much explicitly: *"Does not build or embed the `<!--ENVELOPE_DATA:...-->` marker itself — that's 04's job, done by the calling self-tool handler."*
- Placing it in `internal/selftools` also puts it next to `callShowCard`'s own inline construction, the explicit byte-for-byte precedent this helper must match — easiest for a future reader (and `07`'s worked-example handler) to find and diff against.

**Deviation from plan:** none of substance. The task anticipated needing "a minimal test double or the worked example's own handler if `07` is already in flight" — confirmed `07` is not in flight, so the regression test uses a synthetic `reactions.Result` rather than a real handler, exactly as the task's own fallback instructs. No wiring was added inside `SelfToolsTransport` itself (no handler calls `Fire()` yet) — that remains `07`'s job; this task's scope is the helper + the round-trip proof, both done.

**Build/test results (2026-08-20, this checkout):**
- `go build ./cmd/nanite/` — ok.
- `go vet ./...` — same one pre-existing, unrelated finding in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` context-leak lints) noted as already-present and out of scope; `go vet ./internal/selftools/... ./internal/api/...` (the two touched packages) is clean.
- `go test ./...` — all packages pass, including `internal/selftools` and `internal/api` (both ran real, not just cached, verified separately with `-run`/`-v` against the new tests specifically).
- `git status --short` before starting showed a clean tree relative to `HEAD` aside from pre-existing untracked design docs/task directories not owned by this task; after implementation the diff is scoped to this file plus the three new/changed Go files listed above.

## Review notes

<!-- Reviewer fills in. -->
