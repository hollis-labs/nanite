# [Info] Praise and notes — `dev_tools.go` and `general_tools.go`

**Scope:** `internal/mcp/dev_tools.go`, `internal/mcp/general_tools.go`
**Topic:** Praise, design observations
**Date:** 2026-04-10

## Problem

_Not a problem — praise for patterns that should be protected from regression, plus a few non-actionable design observations._

## Evidence

### Things that are good

- **Directory denylist in the walkers.** The hardcoded skip of `.git`, `node_modules`, `vendor`, `dist` demonstrates the author was thinking about default-noisy subtrees. The list is incomplete (see finding 10) but the pattern is right.
- **`dev_edit` ambiguity guard.** Requiring `old_string` to be unique unless `replace_all` is set is a thoughtful safety check that prevents silent over-writes. Good ergonomic choice borrowed from Claude Code / Aider.
- **`dev_edit` equal-strings guard** at `dev_tools.go:L337-L339` — refuses no-op edits. Small touch that catches a common LLM mistake.
- **TLS verification is on by default** in `web_fetch`. The handler uses a bare `&http.Client{...}` which inherits `http.DefaultTransport`'s `TLSClientConfig: nil` → default verification. No argument lets the caller disable it. Correct posture; protect it from any future "convenience" flag.
- **Scanner line-size cap at 1MB** in `dev_read` and `dev_grep` provides accidental protection against a single-line adversarial file. Worth keeping as the explicit ceiling.
- **`math_eval` parser rejects trailing garbage.** `evalExpr` checks `p.pos < len(p.input)` after parsing and errors on leftover tokens. Small but correct.
- **Existing test coverage hits path scoping.** `TestDevGlob_RespectsAllowedPaths`, `TestDevEdit_PathScoping`, `TestDevBash_RejectsBadWorkingDir` — the tests exist and check the correct allowlist primitive. The fact that the allowlist itself is then broken by the symlink gap (finding 02) is not the tests' fault; they test what was designed. Expand them per finding 02's recommendation.
- **`dev_grep` / `dev_glob` use `regexp` (RE2)** which has no catastrophic backtracking. A hostile regex pattern cannot trigger exponential matching time. Notable contrast with PCRE-based grep wrappers.

### Non-actionable observations

- **`callThink` is a scratchpad tool that does nothing.** The whole value is advertising its name so the model routes reasoning through it. This is a known pattern from other assistants and is perfectly fine; no fix needed. The implementation (general_tools.go:L424-L437) is clean.
- **The `dev_tools` and `general_tools` split is a reasonable separation.** Path-scoped vs non-scoped tools have different trust models, and keeping them in separate transports means registration can go through different allow/deny paths in future. Worth preserving when the fix for finding 01 lands.
- **`code_exec_tools.go` already shows the shape of a hardened exec tool** — small, routes through `sandbox.AgentExec`, uses `truncate.Output` for result sizing. Use it as the template when touching any of the findings here.
- **Response body is not returned with the `Content-Type` header** in `web_fetch`. Returning the body without headers is (accidentally) the safer posture — no accidental leak of `Set-Cookie`, `WWW-Authenticate`, or `Authorization: Bearer ...` that some misconfigured servers echo. Preserve this on any future rewrite (see finding 03 recommendation 5).

### Test coverage notes

- **Well-covered:** `dev_bash` happy path, timeout, workDir rejection. `dev_glob` basic + mtime sort. `dev_edit` single/all/ambiguous/path-scoping. `dev_read` / `dev_grep` / `dev_write` basic. `web_fetch` basic. `json_parse` nested + top-level array + invalid. `datetime`, `base64`, `url`, `hash`, `math_eval` happy paths.
- **Missing:** malicious-input cases. Path traversal via symlinks. Non-existent-target via EvalSymlinks fallback. `dev_grep` on large files. `math_eval` deep nesting. `web_fetch` against `http://127.0.0.1:*` / `http://169.254.169.254/`. `url_decode`/`base64_decode` on attacker-crafted inputs. Context cancellation.

The `dev-tools-tooling-and-tests` follow-up scope should fill these gaps — the reviewer is deferring tooling/test additions per the skill's scoped-review rule.

## Impact

_Informational. No action required beyond "don't regress the good parts."_

## Recommendation

1. When fixing findings 01-08, do not break the patterns listed under "Things that are good" above — particularly the ambiguity guard on `dev_edit`, the TLS-verification default, and the scanner line cap.
2. Add a regression test for each of the "well-covered" items — they currently protect good behavior, and any refactor should preserve them.
3. The `dev-tools-tooling-and-tests` follow-up scope should own the "Missing" test list above.

## References

- `internal/mcp/dev_tools.go` — overall
- `internal/mcp/general_tools.go` — overall
- `internal/mcp/code_exec_tools.go` — the template to emulate for sandbox-routed exec
- `internal/mcp/dev_tools_test.go`, `internal/mcp/general_tools_test.go` — current test coverage
