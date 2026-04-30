# SP2 + SP3 — Chat surface entries — implementer report

## Worktree base verification

- HEAD at start of session: `fefbe5b` (stale — one cluster behind)
- Cluster tip at start: `151fc7b` (`fix/c112-regression-cluster`)
- Reset performed: yes — `git reset --hard fix/c112-regression-cluster` brought worktree to `151fc7b` before any edits.
- HEAD after work: `6b779e4` (SP2 + SP3 combined commit on top of `151fc7b`).

## Decision-rules pass (covers both tickets)

1. **Where enforced?** Chat surface allow-list in `dispatch.ChatToolSurface` — capability layer, not prompt rule. Correct location.
2. **Preemptive or reactive?** Reactive. Tools are *available*, not gated. No "must call before X" wording added anywhere.
3. **Another layer doing this?** No. Currently nothing makes these tools available to the Chat agent; the gap is the bug.
4. **Tool description vs system prompt?** Recall description got the elevator + contract + example treatment in the tool description — NOT in `default.md`.
5. **Runtime classifier injection?** Not needed — capability availability is always-on, not per-intent.
6. **Handcuffs OFF or PUT ON?** OFF — restores capabilities (reminders, pins, recall) the agent was always meant to have; c120 misattributed the surface gap to an MCP parser bug.
7. **c117 test:** Would these rules cause the agent to dodge a "let's do some testing — show me X" request? No. They expand capability without adding any prohibitive framing.
8. **Prompt density:** Untouched. Zero `default.md` edits — word/bullet/negative-phrasing counts unchanged.

Anti-pattern check:
- No preemptive gate. The `nanite_memory_recall` description explicitly calls out reactive-only use ("do NOT call this on every turn or as a precondition for normal action") and references the c114 describe-gate anti-pattern by name.
- No new prompt rules. The agent will reach for these tools when it needs them; the lens is *available*, not *required*.

## SP2 (CW-20260430-0002) — reminders + pins on Chat surface

Three exact-name entries appended to `ChatToolSurface` in `internal/dispatch/role.go`, sibling style to `nanite_remember` / `nanite_validate` / `nanite_panel_open`:

- `"nanite_set_reminder"` — deterministic time/turn-count reminders.
- `"nanite_pin"` — pin content into SlotUserContext budget.
- `"nanite_unpin"` — remove a pin by ID.

Inline comment block above the three entries explains: "Always-meant-to-be-on Chat-loop primitives… c120 surfaced as a misattributed 'MCP parser bug'", referencing CW-20260426-0009 / CW-20260428-0014 (origin tickets) and CW-20260430-0002 (this surface fix).

`IsChatSurfaceTool` already iterates the slice using `name == prefix || strings.HasPrefix`, so exact names work without code changes.

## SP3 (CW-20260430-0003) — memory recall on Chat surface + description audit

### Surface entry

`"nanite_memory_recall"` added to `ChatToolSurface` directly under `"nanite_remember"`. Inline comment explicitly identifies it as the "Layer 4 read-side complement to nanite_remember", flags reactive use only, and warns that an "always recall before X" gate would re-introduce the c114 describe-gate anti-pattern.

### Description audit (Bucket 2 standard)

Audited the `nanite_memory_recall` Tool struct in `internal/mcp/memory_tools.go` against the lessons-doc Bucket 2 standard.

Pre-edit verdict:

| Element | Present? |
|---|---|
| Elevator pitch (what + when) | partial — had what, weak on when |
| Contract (params, return shape) | no — params only via InputSchema; no return-shape doc |
| ONE golden example | no |
| Cross-reference to `nanite_remember` | no |
| Reactive "when to use" framing | no — completely absent |

Pre-edit text was a single sentence: "Recall memories from previous sessions. Use this to retrieve saved facts, decisions, preferences, or corrections relevant to the current conversation."

Post-edit description (mirrors `nanite_remember` shape):

- **When to use** — explicit reactive framing: "after a tool call fails… recall on the failed tool name before retrying". Includes user-grounding case ("the way we agreed last week").
- **When NOT to use** — explicit anti-preemptive guidance referencing c114 describe-gate by name.
- **Contract** — every parameter documented with default behavior and how to filter for `nanite_remember` lessons (`tags: ["learning"]` or `tool:<name>`).
- **Output shape** — text block format spelled out, including the empty-match string.
- **Golden example** — `{scope: "all", query: "nanite_show_card schema", tags: ["learning"], limit: 3}` showing the Layer 4 read-side flow.
- **Cross-reference** — explicit "See `nanite_remember` (write side) for how lessons land in the first place."

No handler/schema changes — description only. The InputSchema was left intact.

## Files changed

- `/Users/chrispian/Projects-apps/nanite/.claude/worktrees/agent-af9a6babe51b4bddc/internal/dispatch/role.go` — appended four exact-name surface entries (3 reminders/pins + 1 memory recall) with inline comments.
- `/Users/chrispian/Projects-apps/nanite/.claude/worktrees/agent-af9a6babe51b4bddc/internal/dispatch/role_test.go` — added two positive surface tests.
- `/Users/chrispian/Projects-apps/nanite/.claude/worktrees/agent-af9a6babe51b4bddc/internal/mcp/memory_tools.go` — rewrote `nanite_memory_recall` Description to Bucket 2 standard.

No edits to `internal/agent/builtin/default.md`. No drive-by refactors. No changes outside the three files.

## Tests added

- `TestIsChatSurfaceTool_AcceptsRemindersAndPins` — asserts `IsChatSurfaceTool` returns true for `nanite_set_reminder`, `nanite_pin`, `nanite_unpin`. Calls out the SP2 origin (c120 misattribution) in the failure message so a future regressor sees why this test exists.
- `TestIsChatSurfaceTool_AcceptsMemoryRecall` — asserts `IsChatSurfaceTool("nanite_memory_recall")` returns true plus a sibling sanity check on `nanite_remember` (Layer 4 closure: both halves must be on the surface).

Existing tests untouched and passing.

## Verification

- `go build ./cmd/nanite/` — pass.
- `go test ./...` — pass (full suite, no skipped/failed packages).
- `go test ./internal/dispatch/... ./internal/mcp/...` — pass (focused).

## Commit SHAs

```
6b779e4 SP2 + SP3 — Chat surface: reminders/pins + memory recall (CW-20260430-0002 / -0003)
```

(Single combined commit; both tickets edit the same file and share an architectural justification.)

## Deviations / open questions

None. Scope held — only the three named files touched, only the surface entries + description added, no `default.md` edits, no system-prompt rules, no preemptive gates. The surface tests are positive-only as the brief specified ("one positive test per new entry"); no regressions in the existing negative tests.
