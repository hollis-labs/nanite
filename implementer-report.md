# SP4 — envelope golden examples — implementer report

Ticket: CW-20260430-0004 (SP4 — golden examples per envelope card type).
Worktree branch: `worktree-agent-a1c224baf5cbf21b2` (off `fix/c112-regression-cluster`).

## Worktree base verification

- HEAD on entry: `fefbe5b` (stale — matched the "danger" base named in the ticket).
- Reset performed: **yes** — `git reset --hard fix/c112-regression-cluster` to `fafd87a` (Round-2 cluster tip, matched the ticket's expected SHA).
- HEAD after reset: `fafd87a chore: remove transient implementer-report.md (SP2+SP3 round)`.

## Decision-rules pass (lessons doc §"Decision rules for new work")

1. **Where does the constraint belong?** Tool description (Bucket 2). Examples are tool-knowledge, not identity. Pass.
2. **Preemptive or reactive?** Reactive. The cheat-sheet anchors at description-evaluation time but the deep examples are pulled via `nanite_tool_describe` only when the agent reaches for them. No "must call X before Y" gate. Pass.
3. **Is another layer doing this?** No. Schema validation enforces field shape on the boundary; examples teach shape *before* the call. Distinct concerns, no redundancy. Pass.
4. **Tool description vs system prompt?** Description. `default.md` was not touched. Pass — Rule 4 is load-bearing here and is honored.
5. **Could this be a runtime classifier injection?** No — the examples apply whenever the agent considers `nanite_show_card`, not per-task. Description is the right home. Pass.
6. **Handcuffs off or on?** Off. The agent gains anchored shapes for unfamiliar types; nothing new is forbidden. Pass.
7. **c117 test (would this dodge a "show me X" request?):** No — the framing is "here's the shape, here's the example" rather than "you must verify before rendering." Pass.
8. **Prompt-density measure:** `default.md` unchanged (~310 words target). The lessons doc explicitly notes prompt-density does NOT apply to tool description; the description grew by 1272 bytes (2706 → 3978) which sits inside the per-tool-description budget. Pass — Rule 8 is load-bearing here and is honored.

Anti-pattern check (lessons doc §"Anti-patterns"):
- No preemptive gate (anti-pattern 2): the new prose says "Unfamiliar type or first failure? Call describe..." — invitational, not gating.
- No redundant enforcement (anti-pattern 3): cheat-sheet is informational; schema validator is the single enforcement layer.
- No process leakage into substrate (anti-pattern 6): all changes are description-side; no `default.md` edits.

## Placement choice

**Option B (hybrid).** Two layers:

1. **Inline cheat-sheet in the description** — one line per show_card-emittable type listing required fields and notable optional fields. Compact (~1.2 KB), scannable, anchored at description-evaluation time. Covers the 10 passive-renderable types reachable through `nanite_show_card`.
2. **Full golden examples** in `internal/mcp/examples/nanite_show_card.json`, reachable via `nanite_tool_describe(name="nanite_show_card")`. Covers all 15 core card types from the ticket — the 10 show_card-emittable types as full callable examples plus 6 reference shapes for the decision-flow / backend-only types (session-task, error-report, approval-card, proposal-card, question-form, confirmation-card) marked `REFERENCE` so agents don't try to fire them through `nanite_show_card`.

**Why hybrid, not pure inline (Option A):** 15 examples × ~250-700 bytes each ≈ 6-10 KB. Inlining all 15 would push the description past 12 KB and collapse scannability — failing the "elevator pitch" shape the lessons doc prefers (§"Three principles" 2). The cheat-sheet preserves the elevator pitch; the JSON file is the skill-pointer depth surface. The describe path was already wired with golden examples for `nanite_show_card`; this ticket extends and corrects it rather than adding a parallel skill location.

**Why not pure-external (skill .md file):** the existing `internal/mcp/examples/<tool>.json` infrastructure already serves this exact role (golden examples surfaced through `nanite_tool_describe`), with embed-fs packaging, malformed-file detection, schema enforcement on the data shape (now via the new test). Adding a parallel `internal/mcp/skills/envelope-cards.md` would create a second knowledge home and violate the "trust each layer" guidance.

## Description size

- `nanite_show_card` description before: **2706 bytes**.
- `nanite_show_card` description after: **3978 bytes** (+1272 bytes / +47%).
- The new content is the per-type cheat-sheet block (10 bulleted lines) plus a reframed pointer to `nanite_tool_describe`. The "Discovery" paragraph that used to imply a session-level "first call describe" rule was removed; it was the closest thing to preemptive-gate phrasing in the prior description.

## Examples table

15 core types — every one has a golden example reachable from `nanite_show_card`'s description (cheat-sheet inline + full example via `nanite_tool_describe`). Domain choices stay inside devops / engineering / project-mgmt as the ticket requires — no `foo`/`bar`.

| # | Type | Source values picked | Note |
|---|---|---|---|
| 1 | `report-card` | "Weekly Sprint Health" + 3 metrics + summary | Canonical grounding case; sources arg as JSON-string, fixed `generated_at` ISO-8601 |
| 2 | `document-viewer` | "Sprint retro notes — 2026-04-25" with markdown body | **Bug fix:** prior example used `body_markdown` (not in schema); now uses `content` + `format` |
| 3 | `info-card` | Maintenance window notice with `variant: warning` | Realistic ops-comms tone |
| 4 | `list-card` | Open PRs with description per item | Demonstrates per-item `description`; `ordered: false` |
| 5 | `metric-card` | Cache hit rate 94.2% with trend up | Number value + unit + trend + previous all populated |
| 6 | `progress-card` | Migration progress 62% with 4-step checklist | Demonstrates `steps` array shape |
| 7 | `table-card` | Top error sources by service with sortable columns | Real column key/label mapping |
| 8 | `timeline-card` | Incident #2026-0419 with 4 events | ISO 8601 timestamps + status enum |
| 9 | `diff-card` | Retry policy v3.2 → v3.3 | `format: code`; before/after with label+content |
| 10 | `metric-card` (force-inline) | Active users count with `render_target=""` | Shows the empty-string override pattern |
| 11 | `session-task` | Quarterly capacity audit, status `in_progress` | REFERENCE: emitted by chat engine, not `nanite_show_card` |
| 12 | `error-report` | `tool_error` from clockwork_task_list with details | REFERENCE: harness-emitted on tool failures |
| 13 | `approval-card` | Deploy auth-service v3.7 with risk_level medium | REFERENCE: decision-flow pipeline |
| 14 | `proposal-card` | create_task proposal with field schema | REFERENCE: shows nested `payload` + `schema` shape |
| 15 | `question-form` | Deploy environment + smoke-test + notes form | REFERENCE: 3 question shapes (select, radio, textarea) |
| 16 | `confirmation-card` | Delete plan confirmation with risk: high | REFERENCE: decision-flow pipeline |

(16 entries, 15 unique types — entry 10 is a second `metric-card` example covering the force-inline pattern, kept from the prior file because it teaches a distinct lever.)

The `REFERENCE` examples are marked in their `notes` field as not-emittable through `nanite_show_card` so an agent reading them through `nanite_tool_describe` doesn't try to fire them through the wrong path. The `enum` on `nanite_show_card.type` already excludes them, so a real call would be rejected at the input-schema layer regardless — the notes are a softer, agent-readable framing of that boundary.

## Files changed

- `internal/mcp/self_tools.go` — replaced the "Discovery:" paragraph with a per-type required-fields cheat sheet + a softer pointer to `nanite_tool_describe`. Description size 2706 → 3978 bytes.
- `internal/mcp/examples/nanite_show_card.json` — extended from 6 examples to 16 (10 passive-renderable + 6 reference). Fixed the existing `document-viewer` example which used `body_markdown` (not in schema) instead of `content`. Added `generated_at` to the report-card example. Added explicit notes calling out the `REFERENCE`-only types.
- `internal/mcp/self_tools_describe_test.go` — added two tests:
  - `TestNaniteToolDescribe_ShowCardExamplesCoverAllCoreTypes` — locks in the 15-type coverage list from CLAUDE.md / ticket.
  - `TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas` — walks every example and validates `data` against the registered per-type schema (catches the `body_markdown`-style regression class going forward).
- `implementer-report.md` (this file).

**No `default.md` changes** (per ticket hard rule).

## Tests added

- `TestNaniteToolDescribe_ShowCardExamplesCoverAllCoreTypes` — covers all 15 required types from the ticket.
- `TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas` — schema-presence check the report template called out; validates each example against the per-type schema so a future field-name typo in the example file fails CI rather than the agent.

## Verification

- `go build ./cmd/nanite/` — **pass**.
- `go test ./...` — **pass** (full repo suite, 73 packages).
- New tests pass:
  - `TestNaniteToolDescribe_ShowCardExamplesCoverAllCoreTypes` — pass.
  - `TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas` — pass.
- Pre-existing tests still pass:
  - `TestNaniteToolDescribe_ShowCardAcceptance` — pass.
  - `TestNaniteToolDescribe_ExamplesIncludePassiveRenderableCoverage` — pass.
  - `TestNaniteToolDescribe_AllSelfToolsHaveExamples` — pass.

## Commit SHAs

To be added after commit (single commit on this worktree branch, per ticket "One commit is fine.").

## Deviations / open questions

- **Existing example bug fixed inside the same commit.** The pre-existing `document-viewer` example used the field name `body_markdown`, which the schema rejects (the schema requires `content` with optional `format: markdown|html`). I treated this as in-scope because (a) leaving it in place would teach the agent a broken shape — exactly the failure mode the ticket is fixing, and (b) the new schema-validation test would have caught and failed on it anyway, so silently ignoring it would have left the test red on landing. Mentioned here for transparency.
- **`giphy-modal` is in the show_card enum but not in the ticket's 15-type list.** I left the existing fetch-then-render note plus the cheat-sheet line for it; no new full example was added (the prior file already had giphy coverage indirectly via the description). The ticket's 15-type list is what the new cheat-sheet and JSON file's full examples cover; `giphy-modal` is a one-line cheat-sheet entry and a chained-call mention.
- **No skill .md file created.** The repo currently has no `internal/mcp/skills/` directory and adding one for this ticket would create a new knowledge home unrelated to the existing examples-via-describe path. The describe path serves the same role with infrastructure already in place. If a future sprint introduces a skills/ convention, the JSON examples can be cross-referenced from a markdown index without losing the structured-validation property.
- **No changes to `nanite_tool_describe`'s wiring.** The tool already emits the `examples` array; the only update is the data the array carries.
- **Source-of-truth tag.** Examples drawn from `config/envelopes.yaml` (manifest) + `internal/envelope/schemas/<type>.schema.json` (per-type data schemas). The repo-root `config/envelopes.schema.json` validates the manifest shape, not per-card data shape; that distinction is what made some prior debate confusing — calling it out for the planning record.
