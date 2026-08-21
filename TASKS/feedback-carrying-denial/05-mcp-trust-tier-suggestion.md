# MCP trust-tier rejection: Suggestion via the existing recover pipeline

**Phase:** 2 — The four extension points (`TASKS/feedback-carrying-denial`)
**Status:** not-started
**Depends on:** `01-shared-kind-taxonomy-and-auto-repair-gate.md`
**Touches:** `internal/mcp/manager.go` (`ExecuteTool`, `ExecuteToolOnServer`, the two
`ValidateResultSize` call sites only).

## Context

`docs/engineering/architecture/23-feedback-carrying-denial.md`: "`internal/mcp/validate.go`'s
`ValidateResultSize`, `ValidationError{Reason}` — falls through to a flat `\"Error: %v\"`.
Real telemetry exists (`span.RecordError`), but it's operator-visible only; the model gets no
guidance (e.g. \"narrow the query / paginate\") despite this being one of the more
mechanically fixable failures in the list."

**This is the one surface that already flows through `internal/recover`'s real repair
pipeline — traced concretely this planning session, not assumed from the doc's prose:**

- `internal/mcp/manager.go:752` (inside `ExecuteTool`) and `:819` (inside
  `ExecuteToolOnServer`) both call `ValidateResultSize(tier, len(out))`; on failure both wrap
  it identically: `return "", fmt.Errorf("call tool %s on %s: %w", toolName, serverName,
  err)`. `%w` preserves the `errors.As`/`errors.Unwrap` chain down to the underlying
  `mcp.ValidationError{Field: WarnResultSizeExceeded, Reason: "result %d bytes exceeds tier
  cap %d"}` (`internal/mcp/validate.go:220-230`).
- This error is returned as the tool-call error from `Manager.ExecuteTool`, which is exactly
  what `internal/service/tool.go`'s `callTransport` invokes when `s.mcpManager != nil`
  (`tool.go:398-399`) — `Execute` (`tool.go:364-381`) then routes any `callErr` straight into
  `attemptRepair` (`tool.go:380`, `:412`).
- `attemptRepair`'s first line is `kind := recoverpkg.Classify(origErr)`
  (`recover.go:163-222`). **Today**, `ValidateResultSize`'s message ("result %d bytes exceeds
  tier cap %d") matches **none** of `Classify`'s branches — not `isUnrecoverableProse`
  (no "permission denied"/"forbidden"/"unauthorized"/service-unavailable/network phrases),
  not any of the recoverable prose patterns (no "schema"/"card_show"/"envelope" keywords) — so
  it falls through to `Classify`'s final `return KindNone` (line 221), and `attemptRepair`'s
  Gate 0 (`!kind.IsRecoverable()`) returns the flat `fmt.Sprintf("Error: %v", origErr)` (line
  415) verbatim. This confirms doc 23's claim exactly, and pins down *why* — not a deliberate
  design choice, an accident of not matching any existing pattern.
- **The fix does not need a new `Classify()` prose branch, and must not import
  `internal/mcp` into `internal/recover`** (task `01`'s Context explains why that direction
  would invert this package's intentionally narrow dependency footprint). Instead: construct
  the `*recover.RecoverableError` **directly at the point of failure**, inside
  `internal/mcp/manager.go`, with `Kind: recover.KindResultTooLarge` already set. `Classify`'s
  existing idempotent path — `var rec *RecoverableError; if errors.As(err, &rec) && rec !=
  nil { return rec.Kind }` (`recover.go:169-172`) — recognizes an already-`*RecoverableError`
  value immediately and returns its `Kind` with **zero new code in `internal/recover`
  itself**. `internal/mcp` importing `internal/recover` (a small, low-level package) is the
  natural dependency direction; confirmed no cycle risk (`internal/mcp` does not import
  `internal/recover` today, and `internal/recover`'s only imports are `internal/envelope` and
  `jsonschema`, neither of which imports `internal/mcp`).
- `%w`-wrapping a `*RecoverableError` (via `fmt.Errorf("call tool %s on %s: %w", ...,
  rec)`) still round-trips through `errors.As` correctly — Go's `errors.As` walks the
  `Unwrap()` chain `fmt.Errorf`'s `%w` verb produces, so keeping the existing
  `"call tool %s on %s: %w"` wrapping (for consistent operator-facing log/span text) around
  the new `*RecoverableError` is safe and preserves today's span/log behavior unchanged.

## What to do

1. **`internal/mcp/manager.go`** — at both `ValidateResultSize` failure branches (lines
   752-756 and 819-823), replace the plain `err` passed to `fmt.Errorf` with a constructed
   `*recover.RecoverableError`:
   ```go
   if err := ValidateResultSize(tier, len(out)); err != nil {
       span.RecordError(err)
       span.SetStatus(codes.Error, err.Error())
       rec := &recover.RecoverableError{
           Kind:        recover.KindResultTooLarge,
           ToolName:    toolName,
           SentArgs:    input,
           ErrorReason: err.Error(),
           Suggestion:  "narrow the query, request specific fields, or paginate if the tool supports it",
           Source:      "mcp_trust_tier",
       }
       return "", fmt.Errorf("call tool %s on %s: %w", toolName, serverName, rec)
   }
   ```
   (`input` is already in scope as the function parameter in both `ExecuteTool` and
   `ExecuteToolOnServer` — populating `SentArgs` costs nothing and matches the convention
   every other `RecoverableError` construction follows.) Import `internal/recover` in
   `manager.go` (confirm no existing import cycle by running `go build ./...` after — Context
   above already traced this as safe, but verify against the real build, not just the trace).
2. Do **not** modify `internal/mcp/validate.go`'s `ValidateResultSize`/`ValidationError` at
   all — the existing `Reason` string stays exactly as-is; this task's `Suggestion` text is
   static (the doc's own illustrative guidance — "narrow the query / paginate" — is generic
   enough that no per-call customization is needed; a future task could make it
   tool-specific if a real need shows up, not this one).
3. Tests: extend `internal/mcp/manager_trust_test.go` (or wherever `ExecuteTool`/
   `ExecuteToolOnServer`'s trust-tier behavior is already tested) to assert that a
   result-size-exceeded failure now returns an error that `errors.As`-unwraps to a
   `*recover.RecoverableError` with `Kind == recover.KindResultTooLarge` and a non-empty
   `Suggestion`. Add or extend a test in `internal/service/tool_test.go` (wherever
   `attemptRepair`'s gate behavior is tested, likely alongside task `01`'s new coverage)
   asserting that when `callTransport` returns this specific error, `attemptRepair` returns
   `buildAgentErrorEnvelope`'s JSON shape (via task `01`'s `AutoRepairEligible() == false`
   short-circuit) with **zero** calls into `recoverpkg.Repair` — the exact same assertion
   shape task `01` establishes for the other three policy Kinds, applied here as the one
   surface that actually exercises the real `attemptRepair` path end-to-end.

## Done means

- Both `ValidateResultSize` failure sites in `internal/mcp/manager.go` construct and return a
  `*recover.RecoverableError{Kind: KindResultTooLarge, ...}` wrapped with the existing
  `fmt.Errorf("call tool %s on %s: %w", ...)` text.
- `attemptRepair` (via `Execute` → `callTransport` → `s.mcpManager.ExecuteTool`), given a
  result-too-large failure, returns the rich JSON envelope (kind, reason, suggestion, source)
  with no LLM repair attempt made — verified end-to-end by a test, not just by tracing the
  gate logic.
- `internal/recover` itself is unmodified by this task (confirms task `01`'s design — the
  idempotent already-wrapped-error path does the work).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, and the build confirms no
  import cycle between `internal/mcp` and `internal/recover`.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
