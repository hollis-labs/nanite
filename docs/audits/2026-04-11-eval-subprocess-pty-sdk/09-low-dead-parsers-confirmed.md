# [Low] Dead parser functions: parseAiderJSON, parseKiroJSON, codexTurnCompleted

**Scope:** CLI adapter parsers
**Topic:** Antipatterns — dead code
**Date:** 2026-04-11

## Problem

Three functions in the PTY adapter parsers are defined but never called outside of their own file (no call site in production code).

## Evidence

### parseAiderJSON — `pkg/provider/pty_aider.go:L112-128`
```go
// parseAiderJSON is reserved for future use when Aider adds structured JSON output.
func parseAiderJSON(line []byte) ([]StreamEvent, error) {
```
The comment says "reserved for future use." The actual parsing is handled inline within `parseAiderLine` at `:84-98`, which already tries JSON parsing. This function is a dead duplicate.

### parseKiroJSON — `pkg/provider/pty_kiro.go:L109-125`
```go
// parseKiroJSON parses a structured JSON event from Kiro.
func parseKiroJSON(line []byte) ([]StreamEvent, error) {
```
Same pattern — `parseKiroStreamLine` at `:70-86` already handles JSON inline. This function is never called.

### codexTurnCompleted — `pkg/provider/pty_codex.go:L50-53`
```go
type codexTurnCompleted struct {
    Type   string `json:"type"`
    TurnID string `json:"turn_id"`
}
```
This is a type definition, not a function, but the struct is never instantiated or used. The `turn.completed` event is handled directly in `parseCodexStreamLine` without unmarshaling into this struct.

Previously flagged in `2026-04-11-whole-repo-tooling-and-tests-sweep`.

## Impact

No functional impact. Minor maintenance burden — dead code creates confusion about which parser path is actually used.

## Recommendation

Remove all three. The comment on `parseAiderJSON` ("reserved for future use") is speculative — if Aider adds structured JSON, the existing inline JSON handling in `parseAiderLine` already covers it.

## References

- `2026-04-11-whole-repo-tooling-and-tests-sweep` — original identification
