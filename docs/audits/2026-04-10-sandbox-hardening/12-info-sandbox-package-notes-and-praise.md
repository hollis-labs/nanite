# [Info] Sandbox package — design observations and things the package does well

**Scope:** sandbox — package-wide
**Topic:** Praise, design observations, questions for the maintainer
**Date:** 2026-04-10

Not action items — context for the maintainer about what this package gets right and what questions the reviewer held back from making formal findings.

## I1. The `AgentExec` vs `UserExec` split is the right shape

Having two entry points with different isolation levels (agent = strict; user = looser but denylist-enforced) matches how the callers think about the problem. Security-sensitive code paths flow through `AgentExec`; the user's explicit shell commands flow through `UserExec`. The asymmetry is documented in the function godoc. Keep this structure — don't collapse it "for consistency" in a future refactor.

## I2. Env-var-name-based filtering is a decent first line on its own

Once the substring heuristic is replaced with a proper allowlist (finding `08-medium-secret-key-substring-heuristic.md`), the overall pattern — "agent-exec gets a minimal env; user-exec gets the user's env minus secrets" — is reasonable. The beta audience is developers; they can live with a minimal env for the agent path.

## I3. `defer proxy.Stop()` in `AgentExec` is the right lifecycle choice

One proxy per exec is simpler than a shared proxy pool and avoids cross-exec bleeding of connection state. The cost is a listener-create-per-exec overhead, which is measurable but not hot-path. Worth keeping. See finding `04` for the one lifecycle bug in Stop itself.

## I4. Denylist docs clearly label it as a second line

The package comment on `defaultDenyPatterns` explicitly says "These are destructive or dangerous commands that no automation should execute regardless of the approval mode." Good framing. The finding on substring matching is about implementation, not intent.

## I5. Test coverage is adequate for the package shape but thin on the security edges

`exec_test.go` covers the happy path, env filtering, denylist blocks, and timeouts. `proxy_test.go` covers CONNECT/HTTP allowed/denied. `sandbox_test.go` covers Dir + Populate. What's missing: negative tests for path traversal in session IDs, profile-injection tests for seatbelt, network allowlist bypass tests (literal IPs, loopback DNS), and process-tree cleanup tests on timeout. Those correspond one-to-one with findings 01, 02, 07. Adding them as part of the fix is the natural path.

## I6. The `--die-with-parent` on Linux is excellent

Correct choice. Works around the orphan-children problem that finding 07 describes on macOS. Keep it.

## I7. Questions for the maintainer

- **Does `nanite_code_execute` need to be user-exposed at all?** If it's an internal-only tool for the agent (and the agent is the only caller), tightening the session_id validation to "must match a known session in the store" is trivial and closes finding 01 at the boundary without touching sandbox internals. Is there a use case for an externally-called code-exec endpoint?

- **Are plugins expected to spawn `AgentExec` themselves?** If yes, plugins inherit all of these findings and need a documented contract about what `SessionID` they can pass. If no, document that `AgentExec` is Nanite-internal.

- **Proxy + bwrap interaction on Linux:** finding 06 recommends dropping proxy mode on Linux for beta. Is there a way the beta can ship with `networkAllow: nil` as the only supported Linux mode, and full-sandbox plus pre-fetched artifacts as an alternative? This would simplify the security story a lot.

- **Is there a planned "sandbox v2" doc?** If yes, several of these findings (especially 03, 06, 09) are candidates for that roadmap rather than blockers for beta. If no, finding 03 is a beta blocker regardless.

## I8. Things the reviewer did NOT look at (declared blind spots)

To stay in scope, this review did not inspect:

- The plugin system (`internal/plugin/*`) except where it intersected sandboxing. No plugin findings here.
- The adapter code (`internal/plugin/builtin/adapter-*`) except as a caller of Populate. The managed-section protocol is noted in the reviewer context but not re-reviewed.
- Provider / LLM code (`internal/provider/*`, `internal/chat/*`) except for the one call site of `sandbox.Dir`.
- The MCP tool catalog outside `code_exec_tools.go` and the registration line in `cmd/nanite/main.go`.
- Tests that would require actual execution of bwrap or sandbox-exec (marked as "requires verification" candidates).
- Windows / BSD behavior beyond reading `os_other.go`.
- Any cross-package concurrency (chat engine, store, etc.).

These are legitimate follow-up scopes; see the index's "Recommended next steps" section.
