# Session Boot — 2026-05-26 (post-CW-0001 dropdown hybrid)

> **Memory + knowledge:** Vanta-primary (`vanta-primary-since: 2026-04-19`). Recall Vanta first (`memory_recall`/`conduit_lookup`), file-based is legacy fallback. Writes → Vanta only via `capture-to-vanta`. See `~/.claude/CLAUDE.md` for full contract.

## Where We Left Off

Two tickets landed back-to-back: `CW-20260526-0002` (recovery broker dropped provider on CLI exits — fix at `5450cb2`) and `CW-20260526-0001` (registry-backed dropdown via the hybrid catalog at `42b7da7`). Local main is **2 commits ahead of origin/main** (both unpushed). Also reconciled `SP-20260518-0013` sprint state: 14 of 17 tasks confirmed shipped via PR #213 + PR #215; `0064` (MEMORY.md plant) and `0068` (subagent progress narration) reopened to `todo` for verification — no in-code marker found.

## Current State

- **Local main** `42b7da7` (2 ahead of origin/main).
- **agentkit v0.3.0** is the single source for `agentcontext`/`agentlaunch`/`agentsessions`/`agentruntime`/`broker`.
- **Provider dropdown** is now registry-backed via `internal/providercatalog`. New API providers surface in the dropdown from `initProviders` alone; DB `seededProviders` survives only as a backing seed for model FK integrity + nil-catalog fallback. Models still come from DB / `pkg/models.AllSeeded()`.
- **Sprint SP-20260518-0013** — 4 tasks remain open: `0064` + `0068` (reopened pending verification), `0116` (bulk create — backlog), `0117` (intent-routed-tools spike — backlog, design call needed).

## Next Actions

1. **Push** `5450cb2` + `42b7da7` to `origin/main` when ready.
2. Pick one of the open sprint items:
   - **`CW-20260519-0064`** — verify MEMORY.md plant is/isn't in claude boot dirs; implement or close with evidence.
   - **`CW-20260519-0068`** — verify subagent progress narration is/isn't visible in a long multi-subagent turn; implement or close.
   - **`CW-20260519-0116`** — bulk create/update across MCP/API/CLI (collapse N-call fan-out).
   - **`CW-20260519-0117`** — intent-routed tools + batch primitive (design spike — needs operator direction first).

## Key Context

- **Deploy via Cerberus.** `cerberus_resource_deploy nanite-api-service` then `cerberus_resource_reload nanite-api-service`. If reload reports "launchd not loaded" use `cerberus_resource_apply` instead. Run `make build-ui` before deploy when UI changed.
- **Squash-merge gotcha** (created PR #218 originally): commits pushed to a branch *after* its squash-merge lands never reach main. Open a follow-up PR — don't assume the branch is "done."
- **Import paths going forward:** `github.com/hollis-labs/agentkit/{agentcontext,agentlaunch,agentsessions,agentruntime/runtimekind,broker}`. The standalone `go-agent-*` modules are no longer in `go.mod`.
- **Provider catalog (CW-20260526-0001).** To add a new API provider, edit `cmd/nanite/main.go:initProviders` — the `apiProvSpec` literal supplies registry registration AND dropdown catalog entry in one place. The catalog is consumed in `internal/api/providers.go:handleListProviders`; the merge order is catalog → DB fallback → boot-profile. `internal/store/seed.go:seededProviders` survives as a backing seed for model FK integrity, NOT as the dropdown source of truth.
- **Sprint-state heuristic** (learned 2026-05-26): a Torque row with `status=done` + a "WOUND DOWN" BlockedReason means "removed from the dispatch queue, work shipped." But if PR #213's commit body doesn't list the CW id AND `grep -rn <CW-ID>` returns no hits, the row may have been administratively closed — verify before trusting.
