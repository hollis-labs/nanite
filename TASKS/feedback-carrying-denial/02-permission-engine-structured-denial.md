# Permission engine + execution-rules backstop: structured denial

**Phase:** 2 — The four extension points (`TASKS/feedback-carrying-denial`)
**Status:** not-started
**Depends on:** `01-shared-kind-taxonomy-and-auto-repair-gate.md`
**Touches:** `internal/permission/engine.go` (`CheckResult`, `Check`, `defaultDecision`),
`internal/permission/rules.go` (`RuleSet.Evaluate`), `internal/service/tool_execution_rules.go`
(`enforceExecutionRules*`), `internal/service/chat_tool_executor.go` (permission-deny,
unknown-decision, and execution-rules-deny blocks only — not the approval-ask-then-deny
block, that's task `03`).

## Context

`docs/engineering/architecture/23-feedback-carrying-denial.md` groups two real surfaces
under "Tool permission denial": `internal/permission/engine.go`'s `CheckResult{Decision,
Reason string}` — `Reason` "always a flat prose string (e.g. `\"plan mode — write
operations blocked\"`), surfaced verbatim as `\"PERMISSION DENIED: %s — %s\"`
(`chat_tool_executor.go`). No suggestion field." — and `internal/service/tool_execution_rules.go`'s
"backstop is a single static string with zero context on what *is* allowed."

Both confirmed verbatim this planning session:

- `internal/permission/engine.go:132`, plan-mode deny: `Reason: "plan mode — write
  operations blocked"`. `engine.go:164-179`'s `defaultDecision` produces five more
  mode-specific `Reason` strings (`accept-edits`/`default` × destructive/read-only/write),
  none with any suggestion. `internal/permission/rules.go:56`, a configured deny-rule's
  `Reason: fmt.Sprintf("denied by rule: tool=%s pattern=%s", r.Tool, r.Pattern)` — carries the
  matched rule's own tool/pattern, but nothing about what *would* be allowed.
- `internal/service/chat_tool_executor.go:168`: `denyMsg := fmt.Sprintf("PERMISSION DENIED: %s
  — %s", tu.Name, permResult.Reason)` — the permission-engine `Reason` string, verbatim, is
  the entire agent-facing message.
- `internal/service/chat_tool_executor.go:235`: a defensive fail-closed branch for an unknown
  `permResult.Decision` value — also constructs a flat `denyMsg` with no structure.
- `internal/service/tool_execution_rules.go:98`: `return false, "tool not in agent's allowed
  tool set"` — a single, literally-static string, confirmed the only return value on this
  path (`enforceExecutionRulesViaAgentTools`, the sole real gate as of
  `TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md`). The function already
  has the `granted []string` slice in scope (from `s.store.ListAgentToolNames`, line 69) —
  the data for a real suggestion is sitting right there, unused.
- `internal/service/chat_tool_executor.go:280-296`: `if allowed, reason :=
  s.enforceExecutionRulesForTool(ctx, agentID, tu.Name); !allowed { ... denyMsg :=
  fmt.Sprintf("EXECUTION_RULES_DENIED: %s — %s", tu.Name, reason) ... }` — same flat-string
  pattern.

**Kind mapping, this planning session's own call** (see this batch's README for the full
rationale): the permission-engine's mode/rule-based denials use `KindPermissionDenied` — a
policy *decision* about this specific input/mode. `tool_execution_rules.go`'s agent_tools
membership gate uses `KindCapabilityForbidden` — a grant/allowlist *absence*, not a judgment
call; the two are genuinely different in kind (one asks "should this be allowed," the other
asks "was this ever granted"), and separating them lets a future consumer of the envelope
(a UI, a lesson-capture pass) treat "ask an operator to change the rule" differently from
"ask an operator to grant this tool to this agent."

## What to do

1. **`internal/permission/engine.go`** — add `Suggestion string` to `CheckResult` (mirrors
   `RecoverableError.Suggestion`; this is `CheckResult`'s own field, not a struct-sharing
   arrangement with `internal/recover` — `internal/permission` should not import
   `internal/recover`, see item 4 below for where the two structures actually connect).
   Populate it at every `Decision: DecisionDeny` return site:
   - `Check`'s plan-mode branch (line 132): something like `"switch to default or
     accept-edits mode to allow write operations"`.
   - `defaultDecision`'s branches that return `DecisionAsk` for destructive/write operations
     are not denials — leave them alone (scope is deny-only, per doc 23; `DecisionAsk`'s
     `Reason` already explains itself contextually and the human sees the raw request, not a
     denial).
2. **`internal/permission/rules.go`'s `Evaluate`**, deny-rule branch (line 51-58) — compute a
   real suggestion by scanning the same `RuleSet`'s `askRules`/`allowRules` for another rule
   matching the same `toolName` (a different `Pattern`): if found, suggest that pattern
   (e.g. `"an ask/allow rule exists for tool=%s with a different pattern (%s) — check whether
   your input matches that instead"`); if none found, a generic-but-honest fallback (e.g.
   `"no rule permits tool=%s under any pattern"`). Keep this cheap — a linear scan over an
   already-in-memory `RuleSet`, no new I/O.
3. **`internal/service/tool_execution_rules.go`** — widen `enforceExecutionRulesViaAgentTools`,
   `enforceExecutionRules`, and `enforceExecutionRulesForTool`'s return from `(bool, string)`
   to `(bool, string, string)` (allowed, reason, suggestion) or an equivalent small named
   struct — your call, document whichever you pick. At the real denial (line 98), build the
   suggestion from the already-computed `granted` slice (e.g. `"granted tools for this agent:
   " + strings.Join(granted, ", ")`, truncated/bounded if `len(granted)` is large — pick a
   sane cap, e.g. first 20 names plus a count, and document the cap). The three early
   fail-open returns (`s.tools == nil`, agent-lookup failure, `s.store == nil`) stay
   `(true, "", "")` — no denial, no suggestion needed.
4. **`internal/service/chat_tool_executor.go`** — at the three call sites this task owns:
   - Line ~166-181 (`DecisionDeny`): construct
     `rec := &recover.RecoverableError{Kind: recover.KindPermissionDenied, ToolName: tu.Name,
     ErrorReason: permResult.Reason, Suggestion: permResult.Suggestion, Source:
     "permission_engine"}` (or `"permission_rule:" + ruleTool` when `permResult.MatchedRule !=
     nil`, giving a more specific Source than the bare engine tag) and render
     `denyMsg := buildAgentErrorEnvelope(rec)` in place of the current flat `fmt.Sprintf`.
     Keep the existing `slog.Warn`/stream-event/`plan.denyReason` plumbing unchanged — only
     the `denyMsg` construction and `block.Content`/`ref.ErrorReason` values change shape
     (they now carry the JSON envelope string, same as every other recoverable-error surface
     the agent already sees via `attemptRepair`'s bypass returns).
   - Line ~230-248 (unknown `permResult.Decision`, fail-closed): same treatment, `Kind:
     recover.KindPermissionDenied`, `ErrorReason: fmt.Sprintf("unknown permission decision
     %q", permResult.Decision)`, `Source: "permission_engine"`, no `Suggestion` (there's
     nothing actionable to suggest for an unrecognized enum value — leave it empty).
   - Line ~280-296 (execution-rules deny): `Kind: recover.KindCapabilityForbidden`, `Source:
     "execution_rules"`, `ErrorReason`/`Suggestion` from item 3's widened return.
   `buildAgentErrorEnvelope` is unexported in `internal/service/tool.go` but
   `chat_tool_executor.go` is the same package (`service`) — no export/import needed.
5. Tests: extend `internal/permission/engine_test.go`/`rules_test.go` to assert `Suggestion`
   is populated (non-empty) on every deny path this task touches; extend
   `tool_execution_rules_test.go` for the widened return signature and the granted-tools
   suggestion content; extend whatever test covers `chat_tool_executor.go`'s tool-plan
   construction (or add one) asserting the denial `block.Content` is valid JSON matching
   `buildAgentErrorEnvelope`'s shape with the right `kind`/`source`.

## Done means

- `CheckResult.Suggestion` exists and is populated on every mode-based and rule-based deny
  path; `defaultDecision`'s ask paths are untouched.
- `tool_execution_rules.go`'s three functions return a real, non-static suggestion built from
  the agent's actual granted-tool set on denial.
- All three `chat_tool_executor.go` call sites this task owns render
  `buildAgentErrorEnvelope`'s JSON shape (not a flat string) with the correct `Kind`
  (`KindPermissionDenied` or `KindCapabilityForbidden`) and a populated `Source`.
- Existing tests for `permission`, `tool_execution_rules.go`, and the touched
  `chat_tool_executor.go` paths pass with updates reflecting the new signatures/shapes (not
  silently skipped).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
