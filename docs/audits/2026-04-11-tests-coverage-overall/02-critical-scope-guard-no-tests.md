# [Critical] scope_guard.go (security control) has no dedicated test file

**Scope:** pkg/provider/
**Topic:** Test Quality — security control untested
**Date:** 2026-04-11

## Problem

`pkg/provider/scope_guard.go` (202 lines) is a security control that monitors stream events for operations outside the allowed scope. It has no dedicated test file. The only test coverage is a single function `TestScopeGuardViolations` (47 lines) embedded in `event_pipeline_test.go`, which tests only basic tool-use pattern matching with string patterns.

## Evidence

`pkg/provider/scope_guard.go:L21-202` defines `ScopeGuard` with these methods:
- `CheckEvent` — dispatches to type-specific checkers
- `checkToolUse` — checks tool name + file path arguments against allowed patterns
- `checkTextContent` — scans text content for scope violations
- `checkError` — checks error events
- `checkFileAccess` — validates file paths against allowed patterns

The existing test at `pkg/provider/event_pipeline_test.go:L94-152` covers:
- Tool use with matching/non-matching patterns (basic string match)
- Wildcard `*` pattern

The existing test does NOT cover:
- `checkFileAccess` with path traversal attempts (`../`, absolute paths outside scope)
- `checkTextContent` scanning
- `checkError` handling
- Violation mode enforcement (`log` vs `block` vs `warn`)
- Edge cases: empty patterns, empty tool names, nil event data
- The interaction between `ScopeGuard` and the event pipeline (guard wired into streaming)

## Impact

The reviewer-backend context explicitly names `scope_guard.go` as "guardrails against prompt injection escaping the intended session scope" and states "Any weakness here is a security finding." A security control with 47 lines of test coverage for 202 lines of implementation, testing only one of four check methods, is a coverage gap that undermines the control's reliability.

## Recommendation

Create `pkg/provider/scope_guard_test.go` with:
1. Table-driven tests for `checkFileAccess` with path traversal attempts
2. Tests for `checkTextContent` with content that references out-of-scope paths/tools
3. Tests for violation mode enforcement (log should not block, block should reject)
4. Tests for edge cases (empty patterns, nil data)
5. Integration test: wire scope guard into an event pipeline and verify end-to-end blocking

## References

- Reviewer-backend context: "scope_guard.go — guardrails against prompt injection escaping the intended session scope. Any weakness here is a security finding."
- `pkg/provider/event_pipeline_test.go:L94-152` — existing minimal test
