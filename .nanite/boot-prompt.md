# Session Boot — 2026-05-26 (post-PR-219 wrap)

> **Memory + knowledge:** Vanta-primary (`vanta-primary-since: 2026-04-19`). Recall Vanta first (`memory_recall`/`conduit_lookup`), file-based is legacy fallback. Writes → Vanta only via `capture-to-vanta`. See `~/.claude/CLAUDE.md` for full contract.

## Where We Left Off

PR #219 (chore: migrate `go-agent-*` modules to `agentkit v0.3.0`) merged + branch deleted. Local synced. Also pushed `agentkit@5b8aaad` (CHANGELOG go-runner v0.6.0 → v0.5.0).

## Current State

- **Local main** `9c74927` ≡ `origin/main` (clean, synced).
- **agentkit v0.3.0** is now the single source for `agentcontext`, `agentlaunch`, `agentsessions`, `agentruntime`, `broker`. Selectors unchanged from the absorbed `go-agent-*` versions; no API adaptations were needed.
- **Capability roadmaps shipped:** backend runtime, frontend UX, capability admin, Phase 6 shared launch, beta P0.
- **Active sprint:** `SP-20260518-0013` harness hardening — 17 tasks. Wave 1 (heartbeat `0073`, budget `0036`, output `0071`/`0068`, status taxonomy) is the load-bearing thread.

## Next Actions

1. **`CW-20260526-0002` — recovery broker's retry-dispatch loses provider on CLI exits.**
   - Likely seam: `observeSessionForRecovery` builds the meta bag without `MetaKeyProvider` (the HTTP path sets it correctly).
   - Audit `internal/runtime/agent/recovery/orchestration.go` for how `MetaKeyProvider` threads into `agent.Boot`.
   - Suggested test: simulate CLI terminal exit, assert the dispatched retry boots with the original provider.
   - Fix is small but plumbs through the broker boundary — expect the meta-bag change to span two packages.
2. After 0002, pick the next thread:
   - **Wave 1 of the harness sprint** (start with heartbeat `0073`), OR
   - **`CW-20260526-0001`** — registry-backed vs DB-seeded provider dropdown (design decision, parked, needs your call).

## Key Context

- **Deploy via Cerberus.** `cerberus_resource_deploy nanite-api-service` then `cerberus_resource_reload nanite-api-service`. If reload reports "launchd not loaded" use `cerberus_resource_apply` instead. Run `make build-ui` before deploy when UI changed.
- **Squash-merge gotcha** (created PR #218 originally): commits pushed to a branch *after* its squash-merge lands never reach main. Open a follow-up PR — don't assume the branch is "done."
- **Import paths to use going forward:** `github.com/hollis-labs/agentkit/{agentcontext,agentlaunch,agentsessions,agentruntime/runtimekind,broker}`. The standalone `go-agent-*` modules are no longer in `go.mod`.
