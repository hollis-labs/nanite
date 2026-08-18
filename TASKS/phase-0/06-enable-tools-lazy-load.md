# Turn on NANITE_TOOLS_LAZY_LOAD by default

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/chat/tool_partition.go` (`IsToolsLazyLoadEnabled`), `internal/chat/tool_partition_test.go` (`TestIsToolsLazyLoadEnabled_DefaultOff` and neighboring tests), `internal/service/chat_generate.go` (the lazy-load call site, no logic change expected but must be re-verified), `internal/service/chat_tools_lazyload.go` (partition-state helpers, read-only relevance)

## Context

Architecture doc `docs/engineering/architecture/04-harness.md`, "Tool lazy-loading" section:

> `NANITE_TOOLS_LAZY_LOAD` (essential/lazy tool partitioning — loads full tool schemas on demand instead of always) is fully built and tested but off by default. Decision: turn it on, tune based on real behavior.

Decision log `docs/architecture-decision-log-2026-08-17.md` §12 ("Harness — turn loop, CLI/API routing, tool lazy-load, run-another-agent unification"):

> **`NANITE_TOOLS_LAZY_LOAD` (essential/lazy tool partitioning) — turn on, tune as needed.** Fully built, tested, was believed already enabled but is off by default. Firmer decision than grounding's "evaluate then decide" — just enable it and tune based on real behavior.

This is a locked decision (it does not appear in the log's "Not yet decided / still open" section), not an open question to re-litigate. But note the phrasing: "tune based on real behavior" is part of the decision itself, not optional follow-through — the mechanism (essential/lazy partitioning of an agent's tool universe, with a `request_tools` hydration escape hatch for lazy entries) has never actually run in a real session because it's been off since it was built. Flipping the default is a one-line change; confirming it behaves correctly against a real agent with a large tool surface is the part that actually validates the decision.

**Verified against real code** (2026-08-18):

- `internal/chat/tool_partition.go:308-319` — `IsToolsLazyLoadEnabled()` reads `os.Getenv("NANITE_TOOLS_LAZY_LOAD")`, lower-cased/trimmed; returns `true` only for `"true"/"1"/"yes"/"on"`; everything else (including unset) returns `false`. The doc comment directly above it (lines 308-311) currently reads: "Default is OFF per the locked decision: telemetry confirms savings before flipping ON." — this comment documents the *old* decision and must be rewritten as part of this task; it's actively misleading once the default flips.
- Called from `internal/service/chat_generate.go` (~line 404-421): `if chat.IsToolsLazyLoadEnabled() && !selection.Progressive { ... }`. Partitioning only ever applies when the progressive-discovery path (`selection.Progressive`) is NOT active — the code comment there explains why: progressive discovery already curates a builtins-only surface, so stacking partition on top would double-curate. This gating logic doesn't change; only the default flips.
- The actual partition mechanics live in `PartitionTools` (`internal/chat/tool_partition.go`), gated by three tunable constants in the same file: `ToolEssentialCap = 25` (line ~27), `RecentToolWindow = 3` (line ~32), `ToolHysteresisFloor = 5` (line ~37). These are the real levers "tune based on real behavior" refers to — this task does NOT change their values, but the real-session verification step below is what would surface whether they need tuning in a follow-up.
- Test coverage: `internal/chat/tool_partition_test.go` has `TestIsToolsLazyLoadEnabled_DefaultOff` (line 294, asserts unset env → OFF), `TestIsToolsLazyLoadEnabled_TrueValues` (line 301), `TestIsToolsLazyLoadEnabled_FalsyValues` (line 310, asserts `"false"/"0"/"no"/"off"/"random"` → OFF). These tests currently encode the *old* default and need updating alongside the implementation.

## What to do

1. In `internal/chat/tool_partition.go`, flip `IsToolsLazyLoadEnabled()`'s default: an unset (or empty) env var should now return `true`. Preserve a real opt-out path — recognize `"false"/"0"/"no"/"off"` (case-insensitive, trimmed) as explicit disable. Decide how to treat a genuinely unrecognized non-empty value (e.g. `"random"`) — the current test (`TestIsToolsLazyLoadEnabled_FalsyValues`) treats it as falsy; preserve that "unknown → safe default" behavior rather than treating garbage input as an implicit enable.
2. Rewrite the doc comment above the function (lines 308-311) to describe the new decision and cite it (point at `docs/architecture-decision-log-2026-08-17.md` §12 and `docs/engineering/architecture/04-harness.md`, the same way the current comment cites "the locked decision" it's now superseding).
3. Update `internal/chat/tool_partition_test.go`: rewrite `TestIsToolsLazyLoadEnabled_DefaultOff` to assert default-ON (rename it — e.g. `TestIsToolsLazyLoadEnabled_DefaultOn`); keep `TestIsToolsLazyLoadEnabled_TrueValues` as-is; keep/adjust `TestIsToolsLazyLoadEnabled_FalsyValues` so it still asserts the explicit-opt-out values disable the feature (this test now covers the *only* way to turn it off, so it matters more than before, not less).
4. Re-read `internal/service/chat_generate.go`'s lazy-load block (~line 404-421) and confirm no code there assumes "lazy load is rare/off" in a way that breaks once it's the common case (e.g. logging levels, any dead-code-looking branch that was never exercised because the flag was always off in practice).
5. Real-session verification (this is the part the decision explicitly calls "tune based on real behavior," not a formality): run at least one real interactive chat session (via `nanite chat` or the GUI) using an agent whose tool universe exceeds `ToolEssentialCap` (25) — enough MCP servers/builtins wired in that partitioning actually activates. Confirm:
   - The lazy hint (`RenderToolLazyHint`'s output — "Tool catalog (lazy): N tools available...") actually appears in what the model sees.
   - The agent can successfully call `request_tools` to hydrate a lazy tool's full schema mid-session and then invoke it.
   - No tool the agent actually needed got stranded in the lazy set in a way that broke the turn (watch for "unknown tool" or schema-mismatch errors).
   - Prompt-cache behavior isn't visibly worse (this mechanism interacts with cache stability per the file's own hysteresis comments — `ToolHysteresisFloor` exists specifically to avoid cache-key churn).
   Record what you observed (tool counts, any friction, whether the constants look right) in the Work log below — if something looks like it needs tuning, note it as a explicit follow-up rather than silently adjusting the constants inside this task (that's a separate, evidence-driven change).

## Done means

- `IsToolsLazyLoadEnabled()` returns `true` when `NANITE_TOOLS_LAZY_LOAD` is unset/empty, and `false` only for recognized falsy values (an explicit opt-out still works).
- The stale "Default is OFF" doc comment is rewritten to reflect the new decision and cites its source.
- `internal/chat/tool_partition_test.go` reflects the new default (test renamed/rewritten, not just changed to pass); `go test ./internal/chat/... ` passes.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` all pass.
- At least one real chat session (not just unit tests) exercised the partitioned path end-to-end with an agent whose tool count exceeds the essential cap, and the observations are recorded in the Work log — this is the acceptance bar for "tune based on real behavior," not optional polish.
- No change to `ToolEssentialCap`/`RecentToolWindow`/`ToolHysteresisFloor` unless the real-session check surfaced a concrete reason to (if it did, note it as a follow-up, don't silently tune inline).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
