# Testing

Stub. What's genuinely established from this codebase's own history:

- **`go build ./cmd/nanite/`, `go vet`, and `go test ./...` are the baseline** before any PR, per this project's own `CLAUDE.md`.
- **A regression test should reproduce the actual reported failure, not just exercise the fixed code.** The strongest examples in this codebase's history are named after the exact scenario they prevent (e.g. `TestSelectForAgent_LateAlphabetAllowlistedToolSurvivesCap`) and were verified to fail against the pre-fix code before being trusted.
- **The dev methodology that actually finds bugs here is dogfooding, not pre-merge test suites.** The single most repeated pattern across this codebase's real history: a passing test suite plus a genuine post-PR review (GitHub Copilot, or a live session actually using the feature) catches real bugs the implementing session's own testing missed — repeatedly, not as a one-off. Don't treat "tests pass" as equivalent to "verified working." Put a live session to real use against anything that touches a load-bearing path before considering it done.
- **When fixing a migration or schema-shaped bug, verify against a copy of real production data**, not just a fresh test fixture — several of this codebase's sharpest incidents only reproduced against data that had actually evolved through the buggy path.

## Not yet documented

Frontend test conventions (real test files exist and are well-targeted at fragile areas — reconnect, session-takeover, partial-data hydration — but no written standard yet), coverage targets, integration vs. unit test boundaries, how CI actually runs these.
