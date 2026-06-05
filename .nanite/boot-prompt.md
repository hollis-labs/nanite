# Session Boot — 2026-05-26 (post-CW-0003 default-model SSOT)

> **Memory + knowledge:** Vanta-primary (`vanta-primary-since: 2026-04-19`). Recall Vanta first (`memory_recall`/`conduit_lookup`), file-based is legacy fallback. Writes → Vanta only via `capture-to-vanta`. See `~/.claude/CLAUDE.md` for full contract.

## Where We Left Off

PR #220 merged: default-model SSOT. Bare-alias `claude-sonnet-4` was a Go-literal terminating every "what model?" fallback chain — that's gone. Defaults now resolve via `store.ResolveProviderAndModel` (explicit → `user_settings.default_*` → `providers.default_model` → error). New `internal/store/seedcatalog/` package houses compile-time seed/routing floors. Two follow-up tickets opened: **CW-20260526-0003** (catalog SSOT — kill `pkg/models.allModels` Go-literal pricing) and **CW-20260526-0004** (broader hardcoded-data audit).

## Current State

- **Local main** `20bfafc` ≡ `origin/main` (clean).
- **Default resolution** is DB-driven. Operators change `providers.default_model` or `user_settings.default_*` without recompile. Anthropic SDK wrapper errors on empty Model with `ErrModelRequired`.
- **Sprint SP-20260518-0013** still has 4 open tasks (next focus): `CW-20260519-0064`, `0068`, `0116`, `0117`.

## Next Actions

Sprint wave 1 — pick one:

1. **`CW-20260519-0064`** — verify MEMORY.md plant is/isn't in claude boot dirs; implement or close with evidence.
2. **`CW-20260519-0068`** — verify subagent progress narration is/isn't visible in a long multi-subagent turn; implement or close.
3. **`CW-20260519-0116`** — bulk create/update across MCP/API/CLI (collapse N-call fan-out).
4. **`CW-20260519-0117`** — intent-routed tools + batch primitive (design spike — needs operator direction first).

After wave 1, parked: **CW-20260526-0004** (audit; outputs feed `0003`), then **CW-20260526-0003** (catalog SSOT).

## Key Context

- **Deploy via Cerberus.** `cerberus_resource_deploy nanite-api-service` then `cerberus_resource_reload nanite-api-service`. Reload is the cutover step — deploy alone may report "launchd unchanged" and leave the old pid.
- **SSOT pattern (from CW-0003):** runtime defaults belong in DB. `internal/store/seedcatalog/` is the seed-only floor — only `seed.go`, `defaults.go` provider-fallback, and `chat.InferProvider` routing-floor import it. Operator-facing defaults flow through `store.ResolveProviderAndModel`. Anti-pattern surfaced: a single Go literal terminating multiple fallback chains silently masks misconfiguration.
- **Squash-merge gotcha:** post-merge `git branch -d <branch>` fails because git sees the local commits as unmerged (the diff landed under a new squash SHA). Use `-D` after confirming the squash is on main.
