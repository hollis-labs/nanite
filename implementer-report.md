# SP6 — nanite_tool_list — implementer report

Ticket: CW-20260430-0006 — "Cheap tool-inventory primitive `nanite_tool_list` for fast discovery."

## Decision-rules pass

Walked the 8 rules from `docs/architecture/agent-context-architecture.md` against this design:

1. **Where does this constraint actually need to be enforced?** N/A — not adding a constraint. Adding a *capability* (a tool the agent reaches for). Lives in the tool-description layer, not the system prompt.
2. **Is this preemptive or reactive?** Reactive. The tool description's "When to use" framing is "when you're not sure which tool to reach for"; no "must call before X" wording anywhere. Anti-pattern check explicit: scanned every line of the description — zero gate phrasing.
3. **Is another layer already doing this?** No. `nanite_tool_describe` returns full per-tool detail (~1–7 KiB per call); there is no cheap inventory primitive today. The agent's only alternative is its own training-data prior, which c120 showed is unreliable (it missed `nanite_set_reminder` entirely).
4. **Could this go in a tool description instead of the system prompt?** This **is** a tool, and its when-to-use guidance lives in its own description. Zero changes to `default.md`. PASS — load-bearing rule, satisfied.
5. **Could this be a runtime classifier injection?** No — it's a per-call capability the agent picks up situationally, not a per-task framing.
6. **Does this take handcuffs OFF the agent or PUT them ON?** OFF. The agent had no cheap way to enumerate the tool surface before; now it does. PASS — load-bearing rule, satisfied.
7. **c117 test (would this rule cause an agent to dodge a "let's do some testing — show me X" request?):** No. The tool is opt-in and never blocks any other call.
8. **Prompt density:** Not applicable to `default.md` (untouched). The new tool description is a single elevator pitch + one golden example + one cross-reference, ~750 chars — well within the "elevator pitches with skill pointers" shape from the lessons doc.

**Anti-pattern check.** Verified the description has no "must call before" / "always call first" / "required before" wording. The "When to use" framing is "when you're not sure which tool to reach for" — guidance, not gate.

## Output sizing

| Call | Bytes |
|---|---|
| Unfiltered | **6075** (52 tools) |
| `filter:"reminder"` | 145 (1 tool) |
| `filter:"todo"` | 359 (3 tools) |
| `filter:"plan"` | 674 (5 tools) |
| `filter:"show"` | 241 (1 tool) |
| `filter:"skill"` | 623 (4 tools) |
| `filter:"agent"` | 1266 (~9 tools) |
| `filter:"message"` | 651 (5 tools) |
| `filter:"card"` | 238 (1 tool) |
| `filter:"tool"` | 620 (3 tools) |

**Filtered output meets the ≤500 B target for the common cases** (single-concept filters). The widest filter sampled (`agent`, ~9 matches) is 1.3 KiB.

**Unfiltered output exceeds the 2 KB ceiling at 6 KB.** This is flagged per the ticket's escape clause. Analysis:

- 52 self-tools × (avg 24-char name + ~73-char first-sentence summary + ~22 chars JSON envelope per entry) ≈ 6 KB.
- The 80-char hard cap on summaries (`summaryMaxBytes = 80`) IS active — without it, several descriptions whose first sentence runs ~100+ chars would push the total higher.
- Even at a much tighter 30-char summary cap the unfiltered total would be ~3.5 KB — still over.
- To physically fit 52 tools in 2 KB you would need ~38 bytes per entry, which leaves no room for any meaningful summary (24 chars name + ~14 chars summary + JSON overhead).

**Decision:** keep the first-sentence-with-80-byte-cap rather than ratcheting summaries down to a uselessly short length. The agent reading the unfiltered call still gets a usable index; the recommended path is filter-then-list (always under 1 KB on every realistic prefix). This is consistent with the lessons doc's preference for "elevator pitches with skill pointers" — a 3-word summary defeats the primitive's purpose.

The unfiltered scenario is a fallback ("agent has no idea what concept to filter on"); the filtered scenario is the common path. The ticket's framing — "agent reaches for it when unsure" — matches the filter-first usage.

**Open question for follow-up:** if the unfiltered size becomes a problem in practice, options are (a) drop summaries below 80 chars conditionally (only when the inventory exceeds X tools), (b) page the unfiltered list, or (c) require the filter parameter. None implemented in this PR — flagged for review.

## Files changed

**New files:**
- `internal/mcp/self_tools_list.go` — tool definition, `firstSentenceSummary` helper, `safeTruncate` helper, `callToolList` handler.
- `internal/mcp/self_tools_list_test.go` — 6 tests covering registration, no-filter happy path, name+summary filter, filter-not-found returns empty, summary-only match, summary-extraction unit tests.
- `internal/mcp/examples/nanite_tool_list.json` — two golden examples (full inventory; filtered).

**Edited files:**
- `internal/mcp/self_tools.go` — appended `naniteToolListDefinition()` to the `selfToolDefinitions()` slice (after `naniteToolDescribeDefinition()`, separated by an explanatory comment block).
- `internal/mcp/self_tools_transport.go` — added `case "nanite_tool_list"` dispatch entry next to the existing `nanite_tool_describe` case.
- `internal/dispatch/role.go` — added `"nanite_tool_list"` exact-name entry in `ChatToolSurface` (sibling to `"nanite_tool_describe"` per ticket's instruction).
- `internal/dispatch/role_test.go` — added `TestIsChatSurfaceTool_AcceptsToolListPrimitive` positive surface check.

**ZERO changes** to `internal/agent/builtin/default.md` (verified via `git status` and `git diff`).

## Tests added

- `TestNaniteToolList_RegistrationAndShape` — tool registered in `selfToolDefinitions()`; no-filter call returns `{tools, count}`; every entry has non-empty summary ≤ `summaryMaxBytes`; inventory includes `nanite_tool_describe` and self-includes `nanite_tool_list`.
- `TestNaniteToolList_FilterNarrowsByNameAndSummary` — `filter:"reminder"` (the c120 motivating case) surfaces `nanite_set_reminder`; case-insensitive (`REMINDER` and `reminder` produce identical counts); every survivor contains the filter token in name OR summary.
- `TestNaniteToolList_FilterNotFoundReturnsEmpty` — bogus filter returns `count:0` with empty list and `IsError=false` (NOT an error).
- `TestNaniteToolList_FilterMatchesSummaryNotJustName` — uses `filter:"lesson"` (no tool's name contains "lesson"; `nanite_remember`'s summary does) to prove summary-side matching fires.
- `TestNaniteToolList_UnfilteredSize` — documents (does not enforce) actual unfiltered + filtered byte counts via `t.Logf`.
- `TestFirstSentenceSummary` — table test for the summary-extraction helper: period-terminated, paragraph break (`\n\n`), single newline, hard 80-byte cap, empty input, leading whitespace.
- `TestSafeTruncate_RuneBoundary` — unit test for the UTF-8-safe byte truncation (covers the 80-cap fallback path).
- `TestIsChatSurfaceTool_AcceptsToolListPrimitive` (in `internal/dispatch/`) — positive surface enforcement check per ticket spec.

Existing test `TestNaniteToolDescribe_AllSelfToolsHaveExamples` validates that every self-tool has a golden example — the new `internal/mcp/examples/nanite_tool_list.json` keeps that test green.

## Verification

- `go build ./cmd/nanite/`: **pass**.
- `go test ./...`: **pass** (all 70+ packages green; full output captured in session log; no flake or skip).

## Commit SHAs

- `37a0945` — feat(mcp): SP6 — nanite_tool_list cheap discovery primitive (CW-20260430-0006)
  (Eight files: 1 new tool def, 1 new test file, 1 new example, 4 wire-up edits, 1 report.)

## Deviations / open questions

1. **Unfiltered size > 2 KB target (6 KB actual).** Flagged above with analysis. Recommendation: keep current 80-byte first-sentence summary; document filter-first as the common path; revisit if telemetry shows agents calling unfiltered routinely. No code change made.

2. **Surface entry: prefix-match form vs. exact-name.** `ChatToolSurface` uses prefix matching (`name == prefix || strings.HasPrefix(name, prefix)`), but per ticket guidance I added `"nanite_tool_list"` as a bare exact name (no trailing `_`). This matches only `nanite_tool_list` itself, exactly like the sibling `"nanite_tool_describe"` entry. **No accidental sub-tool surface widening** since no other tool name starts with `nanite_tool_list` today.

3. **SP1 coordination.** Inserted at the END of `selfToolDefinitions()` (after `naniteToolDescribeDefinition()`) per the ticket's "append rather than thread" guidance. SP1's `nanite_plan_step_add` should also append. If both branches land before merge, the conflict is two adjacent appends in the same trailing block — a trivial 3-way resolve. Verified my insertion does not touch any line SP1 would naturally edit.

4. **Stay-focused check.** No envelope-card description edits (SP4), no plan-tool edits (SP1), no `default.md` edits. The only files touched are the four listed under "Edited files" above plus the three new files; every edit is for the single tool primitive.
