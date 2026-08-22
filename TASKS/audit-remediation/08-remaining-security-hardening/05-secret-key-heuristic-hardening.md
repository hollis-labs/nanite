# Secret-key-name substring heuristic misses common credential-bearing env var names

**Phase:** Wave 3 — Remaining security hardening (guide §4; unsequenced — see folder README)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/sandbox/exec.go` (`isSecretKey` and its call sites in the AgentExec/UserExec environment-overlay construction path)
**Requires architect decision:** false

> **Divergence from `findings.json`:** the catalog flags this finding's `requires_architect_decision` as `true`. This task sets it to `false` because the audit itself already names two concrete, sufficient directions (see Proposed direction below) — the remaining choice is an implementation-level tradeoff an implementer can make, not an open architectural question. Escalate if a reviewer disagrees.

## Findings addressed

- **GO-SEC4-004** (medium severity, high confidence, security — **still-open re-confirmation** of a prior `docs/audits/2026-04-10-sandbox-hardening/` finding) — report §8.12.

## Context

`internal/sandbox/exec.go`'s `isSecretKey` uses a substring heuristic (`KEY`/`SECRET`/`TOKEN`/`PASSWORD`/`CREDENTIAL`/`AUTH`) to decide which environment variable names get redacted/protected before being exposed to a sandboxed subprocess. It still misses `DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `GH_PAT`, and other credential-bearing names that don't contain any of the tracked substrings. **This exact gap was already found in the 2026-04-10 sandbox-hardening audit and remains unfixed** — the report is explicit this is a re-confirmation, not a new discovery: "unchanged from the 2026-04-10 finding."

**Trust classification (guide's Wave 3 instruction, applied explicitly):** the highest-risk surface is **AgentExec** specifically — where the code executing inside the sandbox is agent/LLM-influenced and could, via prompt injection or adversarial task content, be steered toward probing for exactly these missed variable names — not **UserExec** (the operator's own shell, where the operator already trusts themselves with their own secrets). Classify AgentExec's exposure as **agent-controlled**: an LLM-driven process attempting to exfiltrate environment secrets is a real, non-speculative threat model for this surface, distinct from UserExec, which is closer to **operator CLI-controlled** and lower real risk.

**Desired invariant:** no credential-bearing environment variable name reaches an AgentExec-sandboxed subprocess's environment unredacted, regardless of whether its name happens to contain one of the six currently-tracked substrings.

## What to do

**Scope:** `internal/sandbox/exec.go`'s `isSecretKey` and its call sites in the AgentExec/UserExec overlay construction path.

**Proposed direction** (two options; implementer chooses, per the audit's own framing that this is a clear-enough implementation-level choice):

1. **Keep the existing substring heuristic as a fast first pass, and add an exact-match hard-deny list** for known credential-bearing `*_URL`/`*_TOKEN`-adjacent names (`DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `GH_PAT`, and similarly-shaped well-known secret-bearing names) as a second, always-checked layer. Lowest-risk, additive, doesn't change behavior for anything already caught.
2. **Switch AgentExec's overlay specifically to an explicit allowlist model** — only pass through env vars the operator has explicitly approved for agent-sandboxed execution. Stronger guarantee, but a bigger behavior change: anything not on the allowlist silently disappears from the agent-sandboxed environment, which could break legitimate agent workflows relying on some currently-passed-through variable.

Whichever is chosen, **UserExec's existing behavior should not regress** — the finding's own risk framing is specifically about AgentExec.

**All production callers:** enumerate every call site that constructs the AgentExec/UserExec environment overlay via `isSecretKey` (the report doesn't enumerate them individually — trace `exec.go`'s own call graph before assuming there's exactly one).

## Non-goals

Do not build a fully general secrets-scanning system. Do not change UserExec's default behavior unless a subsequent decision determines its risk model has also changed.

## Tests required

- Unit tests asserting `DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `GH_PAT` (the four named-missed examples) are now redacted/blocked from AgentExec's environment.
- A regression test capturing the original gap (these four names previously passed the heuristic unredacted) so a future refactor can't silently reintroduce it.

## Prevention

Whichever direction is chosen, document the decision rule (hard-deny list vs. allowlist) directly in `isSecretKey`'s doc comment, so a future maintainer adding a new well-known secret-shaped env var name knows where to add it.

## Verification

`go test ./internal/sandbox/...` (new/updated `isSecretKey` tests); manual/documented check that AgentExec sessions no longer receive the four named variables unredacted.

## Risk / rollback

Option 1 (hard-deny list) is low-risk/additive — rollback is a simple revert. Option 2 (allowlist model) is a bigger behavior change for AgentExec specifically and could break existing agent workflows relying on implicit env passthrough — if chosen, flag clearly in Work Log and Review notes as a behavior change, not just a hardening fix, so it gets appropriately scrutinized.

## Done means

- [ ] Chosen direction implemented in `isSecretKey` / AgentExec overlay construction
- [ ] The four named example variables (`DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `GH_PAT`) confirmed redacted from AgentExec
- [ ] UserExec behavior unchanged (or explicitly, deliberately changed with rationale recorded)
- [ ] Regression + new-coverage tests above pass

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
