# Tool-cap audit — CW-20260519-0115

> Audit + rework of count-based limits that throttle intentional agent
> work. The principle: caps should catch runaway, not requested work.
> A runaway is detected by *pattern* (repeated identical calls, error
> loops, no-progress, consecutive failures), not by *count*. Count-based
> caps belong as high backstops only.

## Triggering incident

Session **c267** was blocked at the **10th of 13** operator-requested
`torque_task_create` calls. The cap (`user_settings.tool_per_turn_cap`,
DB default **10**) treated a legitimate 13-task creation request exactly
the same as a 13-iteration infinite loop. The agent reported the partial
honestly ("10 of 13 created, 3 remaining") — the resumable-stop *message*
was good — but the cap firing on intentional work was the bug.

Interim fix raised the operator's row to **100**. This task is the
proper rework.

## Audit table

| # | Limit | File:Line | Kind | Default | What it actually catches | Verdict | Action |
|---|---|---|---|---|---|---|---|
| 1 | `user_settings.tool_per_turn_cap` → `loopState.limits.defaultPerToolCap` | `internal/store/migrations/011_tool_broker_execution.sql` / `internal/service/chat_loop_state.go:146` | **per-tool count** (calls to one tool per turn) | **10** | Almost always punishes intentional bulk work (the c267 case). Genuine runaway is caught earlier by pattern detectors (rows 4-7). | **Demote to high backstop** | **Raise default 10 → 150.** Migration 066 updates fresh DBs + existing rows still at the old 10 (or interim 100). |
| 2 | `toolclient.DefaultMaxCallsPerTurn` | `internal/toolclient/permissions.go:11` | **per-turn count** (on `ToolPermissions.MaxCallsPerTurn`) | **25** | **Not actually enforced anywhere.** Field is populated by parsers + back-compat fallbacks (`broker.go:538,550`, `permissions.go:107,112,115`) but no runtime call site reads it as a count cap. Vestigial. | **Document as vestigial** | Keep the constant + struct field (back-compat with serialized profiles + agent YAML). Add a code comment flagging it as decorative until an enforcement site is wired back. No behavior change. |
| 3 | `agent.AgentToolPermissions.MaxCallsPerTurn` | `internal/agent/parser.go:92` | **YAML frontmatter field** mirroring row 2 | unset (0) | Same as row 2 — declared, not enforced. | Document as vestigial via row 2 | No action (the mirror tracks row 2). |
| 4 | `loopState.limits.runawayFailCap` (`AgentConstraints.RunawayFailCap`) | `internal/service/chat_loop_state.go:78,141` | **pattern: consecutive tool failures** (hard terminator) | **10** | Catches the actual runaway: 10 consecutive tool errors → terminate with `chat-loop-terminated` envelope. | **Keep — this is the right kind of signal** | None. This is the pattern signal that should bear the load. |
| 5 | `loopState.limits.consecutiveFailCap` (`AgentConstraints.ConsecutiveFailCap`) | `internal/service/chat_loop_state.go:71` | **pattern: consecutive failures** (soft warning) | **3** | Soft-warning threshold — at 3 consecutive failures the harness emits a `tool_warning` SSE event so the UI shows "agent will pause after one more failure." Does NOT terminate the loop (CW-20260417-0485 made it soft). | **Keep** | None. |
| 6 | `detectStuckLoop` (`chat_generate.go:3075`) — same-result-repeated | `internal/service/chat_generate.go:3083-3104` | **pattern: identical tool output** | trigger at **2 identical results** | Adds note on the 1st repeat; blocks the tool for the rest of the turn on the 2nd. This is the right kind of runaway detector. | **Keep — load-bearing** | None — but rely on it more heavily now that row 1 is no longer doing per-tool runaway containment. |
| 7 | `loopState.limits.idleTimeout` (`AgentConstraints.IdleTimeoutSeconds`) | `internal/service/chat_loop_state.go:79,92` | **pattern: wall-clock no-progress** | **900s** interactive / **300s** subagent | Catches stalled runs (no activity); `lastActivity` resets on every tool call / delta. CW-20260519-0073 added subagent scope. | **Keep** | None. |
| 8 | `defaultMaxTurns` (`AgentConstraints.MaxTurns`) | `internal/service/chat_loop_state.go:61` | **turn-count** (soft warning after CW-20260504-0001) | **75** | Soft warning only — does not terminate. Fires one-shot `checkSoftMaxTurnsWarning` telemetry/SSE at the budget mark. The `chat-loop-terminated` envelope's `max_turns` code is no longer emitted. | **Keep as soft** | None. Already pattern-shaped (one-shot signal, agent continues). |
| 9 | `defaultHardCeiling` (`AgentConstraints.HardCeiling`) | `internal/service/chat_loop_state.go:62` | **turn-count** (absolute backstop) | **200** | Hard absolute backstop on `ls.iteration`. Catches true runaway loops on turn count, regardless of MaxTurns. | **Keep — this is also a high backstop** | None. 200 is plenty for legitimate work; pattern signals (4-7) trip first on real runaways. |
| 10 | `defaultMaxRequestToolsCalls` | `internal/service/chat_loop_state.go:98,187` | **per-meta-tool count** + **pattern** (consecutive_empty) | **6 calls** OR **2 consecutive empty** | The `request_tools` meta-tool has its own broker (`handleRequestTools`) that already combines count with a pattern signal (`consecutive_empty >= 2`) AND a reflection-and-retry pivot before the hard halt. This is the **target architecture** for count caps — pattern-first, count-second, with a graceful pivot before terminating. | **Keep — already pattern-augmented** | None. Use as the reference pattern for any future count cap. |

## Rework summary

| Cap | Old | New | Where the catch moves |
|---|---|---|---|
| `user_settings.tool_per_turn_cap` (DB default) | 10 | **150** | Same-result-repeated (row 6), consecutive-fail-cap (row 4), idle timeout (row 7) |
| Per-tool blocked-message text (`chat_tool_executor.go:99`) | Conflated two failure modes ("stuck loop OR cap hit") | Branched: stuck-loop message (`detectStuckLoop` path) vs. backstop message (count-cap path), the latter surfaces `X of N calls used` and explicitly tells the agent this is a failsafe, not a runaway signal | n/a (UX of the cap-hit message) |
| `toolclient.DefaultMaxCallsPerTurn` | 25 (not enforced) | 25 (not enforced) — comment added flagging vestigial status | n/a (no enforcement to move) |

The fresh-DB default change is via **migration 011** (idempotent — only affects fresh DBs). The existing-DB migration is **066** (`UPDATE user_settings SET tool_per_turn_cap = 150 WHERE tool_per_turn_cap IN (10, 100)`) — picks up both the original default and the interim raise from the c267 audit. Operator rows above 100 (custom higher caps) or at 0 (explicit no-cap) are preserved.

## Why 150?

- The c267 case was 13 task creations — well under 150.
- A full-project enumeration (50 files read + 30 grep + 20 edits) lands around 100. 150 gives a 50% comfort margin.
- 150 is well below the per-turn output-token budget for any provider — the harness can fit 150 tool-result blocks in a single context without overflow at any reasonable result size (the cache-and-pointer pattern handles large bodies anyway).
- Real infinite loops trip the pattern detectors (4-7) at single-digit counts (`detectStuckLoop` at 2; `runawayFailCap` at 10). By the time count 150 is reached, those signals have either already fired and stopped the loop, or the work is genuinely the operator's intent.

## Resumable-stop contract — preserved + strengthened

The c267 "10 of 13, 3 remaining" message was produced by the **agent**, not the harness — the agent looked at how many `tool_use` blocks it had successfully completed before the blocked-tool message arrived, then composed the partial-completion summary in its final text.

For the harness to continue supporting that shape after this change:

- **Blocked-tool message** (the LLM-facing payload that lands in the tool_result block) now explicitly includes `(N of CAP calls used this turn)` in the count-cap path, so the agent has the data to build "X done, Y remaining" without having to count its own tool blocks. It also explicitly instructs the agent to offer the remainder in a follow-up turn.
- **Stuck-loop path** (the same-result-repeated branch) keeps the original "isn't available for the rest of this turn" framing — that case IS a runaway and the agent should pivot, not resume.
- The `chat-loop-terminated` envelope schema is **untouched**. None of its codes (`max_turns`, `hard_ceiling`, `runaway_tool_failures`, `idle_timeout`, `retry_budget_exhausted`) are affected by this change.

## Follow-ups / risks

- **Pattern-detector coverage gaps to watch:**
  - `detectStuckLoop` is per-tool (keyed on `toolName + raw output`). A pathology that alternates between two tools (e.g. `dev_read` → `dev_write` → `dev_read` → `dev_write` with identical content each cycle) would not trip it. The 150 backstop would catch it eventually; in practice the consecutive-fail-cap will trip first if any of those calls error. If a non-erroring 2-tool oscillation ever shows up in telemetry, add a cross-tool repeat detector.
  - The same-result detector compares `lastResults[toolName]` against `resultText` byte-for-byte. A pathology that returns very-slightly-different results each cycle (timestamps, ULIDs, etc.) would not trip it. Mitigation could be a normalized fingerprint (strip timestamps/IDs before compare) — out of scope for this ticket but worth filing if a case appears.
- **`toolclient.DefaultMaxCallsPerTurn` (rows 2-3) is vestigial.** Either wire an enforcement site for `ToolPermissions.MaxCallsPerTurn` (e.g. as a per-agent override of the chat-loop default) or remove the field outright. Filed as a follow-up — out of scope here because the field is currently inert and removing it would touch agent frontmatter parsing and stored profile JSON in a way the rework doesn't need.
- **Per-agent override path.** Agents that want a higher (or lower) per-turn cap currently have no per-agent knob — only the global `user_settings.tool_per_turn_cap`. Adding `AgentConstraints.ToolPerTurnCap` would close that gap. Out of scope; file if the 150 floor turns out to be wrong for a specific agent profile.

## Code-pointer index (for future audits)

- Chat-loop count cap site: `internal/service/chat_loop_state.go:480-545` (`recordToolCall`, `isToolExhausted`, comments).
- Chat-loop cap-hit message: `internal/service/chat_tool_executor.go:96-150`.
- Per-turn cap load from UserSettings: `internal/service/chat_generate.go:712-715`.
- Same-result-repeated detector: `internal/service/chat_generate.go:3075-3105`.
- Consecutive-fail terminator (pattern, hard): `internal/service/chat_loop_state.go:427-455` (`shouldStop`).
- Idle-timeout terminator (pattern, hard): same `shouldStop` Layer 2.
- request_tools count-and-pattern broker (target architecture): `internal/service/chat_generate.go:2849-2960` (`handleRequestTools`).
- DB column: `internal/store/migrations/011_tool_broker_execution.sql` (creation, default), `066_tool_per_turn_cap_backstop.sql` (cutover).
- Vestigial cap: `internal/toolclient/permissions.go:11-...` (`DefaultMaxCallsPerTurn`).
