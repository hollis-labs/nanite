# Human-reject feedback on approval requests

**Phase:** 2 — The four extension points (`TASKS/feedback-carrying-denial`)
**Status:** not-started
**Depends on:** `02-permission-engine-structured-denial.md` (same two files — sequence,
don't run as true concurrent commits; this task adds no new dependency on `02`'s actual
content, just avoids two agents editing `internal/permission/engine.go` and
`chat_tool_executor.go`'s permission-switch block at the same time)
**Touches:** `internal/api/types.go` (`RespondApprovalRequest`), `internal/api/approvals.go`
(`handleRespondApproval`), `internal/permission/engine.go` (`ApprovalResponse`, `Respond`),
`internal/service/chat_tool_executor.go` (the approval-ask-then-deny block only).

## Context

`docs/engineering/architecture/23-feedback-carrying-denial.md` calls this "the sharpest
gap... the one gap with no existing partial coverage at all — pure addition." Confirmed this
planning session:

- `internal/api/types.go:665-668`: `RespondApprovalRequest{Decision string, Scope string}` —
  no field for human-supplied text at all.
- `internal/permission/engine.go:51-55`: `ApprovalResponse{Decision Decision, Scope Scope,
  TimedOut bool}` — same gap on the return side.
- `internal/service/chat_tool_executor.go:194-226`, the `DecisionAsk` → deny branch: `resp :=
  s.permissions.WaitForApproval(ctx, req); if resp.Decision != permission.DecisionAllow {
  ls.recordToolCall(tu.Name, false); denyReason := "user denied"; if resp.TimedOut {
  denyReason = "approval timed out" }; denyMsg := fmt.Sprintf("PERMISSION DENIED: %s — %s",
  tu.Name, denyReason) ...; plan.denyReason = "user denied"` (line 221 — note this is
  hardcoded a **second time**, independent of `denyReason` above; both need to change
  together or the two will drift). A human rejecting a tool call has no way to tell the
  agent *why* — the model always sees the literal string `"user denied"`.

**No DB persistence needed — verified, not assumed.** `internal/permission/engine.go` has no
`store`/`*sql.DB` reference anywhere in the file: `ApprovalRequest`/`ApprovalResponse` live
entirely in `pendingApprovals sync.Map` and a per-request `chan ApprovalResponse`
(`engine.go:40-55`, `184-198`). `Feedback` is transient by construction, same as
`Decision`/`Scope` already are. It still gets a durable record for free: once this task sets
`ErrorReason`/`Suggestion` from `Feedback` in `chat_tool_executor.go`, that text flows into
`chat.ToolCallRef.ErrorReason` and the persisted `tool_result` content block exactly the same
way every other denial message already does — no new column, no new write path.

## What to do

1. **`internal/api/types.go`** — add `Feedback string `json:"feedback"`` (or
   `` `json:"feedback,omitempty"` `` — your call, document it) to `RespondApprovalRequest`.
2. **`internal/api/approvals.go`'s `handleRespondApproval`** — thread `req.Feedback` through
   to whatever `Engine.Respond` call you land in the next item (its signature needs a new
   parameter regardless of exact shape — see item 3). No validation needed beyond what
   `a.decode` already does; empty feedback is valid (the human declined to explain,
   `chat_tool_executor.go` falls back to the existing generic text in that case).
3. **`internal/permission/engine.go`** — add `Feedback string` to `ApprovalResponse`
   (`engine.go:51-55`). Widen `Respond`'s signature (`engine.go:226`,
   `func (e *Engine) Respond(requestID string, decision Decision, scope Scope, sessionID
   string) bool`) to accept feedback and set it on the `ApprovalResponse` sent down
   `req.Response` (line 249). Update every call site of `Respond` (currently just
   `approvals.go`'s handler, from item 2).
4. **`internal/service/chat_tool_executor.go`**, lines 194-226 — replace the two independent
   hardcoded `"user denied"` assignments (line 197's `denyReason` and line 221's
   `plan.denyReason`) with logic sourced from `resp.Feedback`:
   ```go
   denyReason := "user denied"
   if resp.TimedOut {
       denyReason = "approval timed out"
   }
   suggestion := ""
   if resp.Feedback != "" {
       suggestion = resp.Feedback
   }
   rec := &recover.RecoverableError{
       Kind:        recover.KindPermissionDenied,
       ToolName:    tu.Name,
       ErrorReason: denyReason,
       Suggestion:  suggestion,
       Source:      "human_approval_reject",
   }
   denyMsg := buildAgentErrorEnvelope(rec)
   ```
   Set `plan.denyReason = denyReason` (matching the existing `plan.denyReason = "user
   denied"` convention's intent — the *deny reason* stays the short tag; the human's actual
   words ride as `Suggestion`/in the JSON envelope, not as a second overload of
   `denyReason`). Keep the existing consecutive-failure warning block (lines 205-215)
   unchanged — it doesn't reference `denyReason`/`denyMsg`'s shape.
   Do **not** set `Suggestion` when `resp.TimedOut` is true and `resp.Feedback` is empty
   (there's no human text in a timeout — nothing to surface) — only populate `Suggestion`
   when a human actually typed something.
5. Tests: extend `internal/permission/engine_test.go` for `Respond`'s widened signature and
   `ApprovalResponse.Feedback` round-tripping; extend `internal/api/approvals_test.go` (or add
   one if none exists — check first) asserting `RespondApprovalRequest.Feedback` decodes and
   reaches `Engine.Respond`; extend whatever test covers the `chat_tool_executor.go`
   ask-then-deny path asserting a human's feedback text appears in the denial envelope's
   `suggestion` field, and that a timeout with no feedback produces no `suggestion` field at
   all (not an empty-string one — matches `buildAgentErrorEnvelope`'s existing
   omit-when-empty convention).

## Done means

- `RespondApprovalRequest.Feedback` exists, decodes from the API request body, and reaches
  `Engine.Respond` → `ApprovalResponse.Feedback`.
- A human's rejection feedback text appears in the agent-facing denial envelope's
  `suggestion` field when supplied; the existing `"user denied"`/`"approval timed out"`
  short-tag behavior is preserved as `ErrorReason` (renders as `reason` in the envelope) in
  all cases.
- No new migration, no new table, no new column — confirmed and documented as such in the
  Work Log (cross-reference this task's Context).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
