# Truncation review, broadly — deliberate care wherever touched next

**Phase:** 3
**Status:** not-started
**Depends on:** none, but coordinate with any other Phase 1-3 task that happens to touch a truncation path (e.g. tool-catalog rendering work in Phase 1) — don't duplicate a fix mid-flight elsewhere.
**Touches:** `internal/tool_result_cache` (or equivalent — the Phase 3 S4a tool-result cache described in this project's `CLAUDE.md`), the file-based truncation fallback (locate exact package — `internal/truncate` or similar, confirm during implementation), the 80-character progressive-discovery tool-catalog description truncation (`internal/service/tool.go`'s catalog rendering, referenced in task 01/05's research).

## Context

Architecture doc `03-steering.md`, "Two correctness gaps carried into implementation": *"Truncation, broadly (not just one specific case), has a real documented history of causing bugs here — needs deliberate care wherever it's touched next, not 'add a limit and move on.'"* Decision log §11 is more specific about the history: *"Truncation has a real, documented history of causing bugs here (a truncated error message once caused an agent to hallucinate a tool didn't exist; naive reordering broke prompt caching) — whatever touches truncation next needs to be done deliberately, not as a repeat of 'add a truncation limit and move on.'"*

This is not scoped as a single bug fix — it's a standing caution the review wants captured as a real task so it isn't lost, covering **every** truncation path in the system, not just the tool-catalog one. Known truncation surfaces to audit (not necessarily exhaustive — enumerate more during implementation):

1. The tool-result cache (per this project's `CLAUDE.md`: results over 64 KiB get cached with a `tool_result://<id>` pointer shown to the LLM) and the MCP trust-tier size ceilings layered on top of it (2 MiB/512 KiB/256 KiB/128 KiB per tier, per `docs/mcp-trust-model.md`).
2. Any file-based truncation fallback for oversized content.
3. The progressive-discovery tool-catalog's 80-character description truncation (referenced in this phase's other tasks as the specific historical incident site).
4. Error-message truncation anywhere in the tool-execution or provider-response path (the specific incident that caused an agent to hallucinate a tool didn't exist).

This item, like `06-tool-concurrency-safety-classification.md`, is drawn from architecture doc `03-steering.md`'s explicit "carried into implementation" list rather than `TASKS.md`'s terse Phase 3 summary line — included for the same reason: the architecture doc is the authoritative detail behind `TASKS.md`'s deliberately short phrasing.

## What to do

1. Inventory every truncation path in the codebase (grep for length caps, byte-size checks, `[:N]` slicing on user-facing/model-facing text, etc.) — build a real list, not just the four known ones above.
2. For each, verify: does truncating mid-content ever produce something the model could plausibly misread as "this doesn't exist" or "this is empty" rather than "this was cut short"? Does truncation ever interact with prompt-cache stability (reordering/repositioning content non-deterministically)?
3. Where a truncation path lacks a clear "this was truncated" signal to the model (a marker, a pointer, an explicit note), add one — the two named historical incidents (hallucinated missing tool, broken cache from naive reordering) are both failures of silent/careless truncation, not truncation itself being wrong.
4. Do not introduce a new truncation limit anywhere as a side effect of this review without the same deliberate treatment — this task is explicitly about not repeating "add a limit and move on."

## Done means

- A real inventory of truncation paths exists (recorded in this file's Work Log), not just the ones named in the architecture doc.
- Every truncation path that lacks a clear "content was cut" signal to the model gets one.
- No new truncation path introduced elsewhere during this same phase's other tasks lacks the same signal — this task's findings should be checked against Phase 3's other work (e.g. the tool-catalog rendering touched by `05-add-filter-tool-selection.md`) before that work is marked done.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
