# A5 Audit: Per-model context window via models.dev

**Ticket:** CW-20260426-0014
**Date:** 2026-04-26
**Auditor:** a5-audit (sub-agent, architecture-sequence Phase 1)
**Verdict:** LEAKS_FOUND (4 silent/hardcoded fallbacks; 2 structural gaps)

---

## Summary

The primary hot-path — `chatServiceImpl.contextWindowSize()` in `internal/service/chat_generate.go` — correctly implements a three-tier priority: user_settings override → models.dev catalog → DefaultContextWindowSize. However, four distinct code sites use `DefaultContextWindow = 200000` without access to the per-model value, and the toolclient token-pruning layer is structurally disconnected from models.dev entirely.

---

## Inventory of context-window read sites

| # | Site (file:line) | Source | Status | Notes |
|---|---|---|---|---|
| 1 | `internal/service/chat_generate.go:1432–1444` (`contextWindowSize`) | user_settings → models.dev catalog → returns 0 | ✓ correct (path-of-record) | Returns 0 on miss; `NewContextWindow(0,…)` then activates `DefaultContextWindowSize`. The 0 fallback is intentional but undocumented at call site. |
| 2 | `internal/service/context.go:177` (`AssembleSlots`) | Receives `providerWindowSize` from caller | ✓ correct (pass-through) | `NewContextWindow(providerWindowSize, …)` — if caller passes 0, `DefaultContextWindowSize` activates inside `NewContextWindow`. |
| 3 | `internal/context/window.go:14,37` (`NewContextWindow`) | `DefaultContextWindowSize = 200_000` | ✓ intentional fallback — documented | The pkg-level constant is the last-resort guard when no provider data is available. Comment at declaration is adequate but missing a note that callers should prefer per-model values before passing 0. |
| 4 | `internal/chat/context_client.go:23,92` (`AssembleContext` legacy path) | `DefaultContextWindow = 200000` hardcoded | ✗ **silent fallback** | `AssembleContext` computes `budget = int(float64(DefaultContextWindow) * budgetPct)` without consulting models.dev or user_settings. This path is still reachable from `AssembleContext` calls. |
| 5 | `internal/chat/context_client.go:457` (`EnforceTokenBudget`) | `DefaultContextWindow = 200000` hardcoded | ✗ **silent fallback** | `ceiling := int(float64(DefaultContextWindow) * HardCeilingPct)` — the hard-ceiling enforcer uses the constant regardless of the actual model. `ceilingOverride` param exists but is always passed as `0` from `chat_generate.go:466`. |
| 6 | `internal/api/context_breakdown.go:120` (`handleGetContextBreakdown`) | `chat.DefaultContextWindow` hardcoded | ✗ **silent fallback** | Diagnostic endpoint shows `ceiling` based on the constant. Misleading to users on models with larger windows (e.g. Gemini 1M, o3 200K). |
| 7 | `internal/toolclient/config.go:13,42,63` + `broker.go:138–140` | `DefaultContextWindowTokens = 200000` hardcoded | ✗ **silent fallback** | `ToolBroker.SelectTools` computes tool token budget from `Config.ContextWindowTokens`, which is always initialized to 200000 and never updated from models.dev or per-session state. Tools for 1M-context models (Gemini) are pruned to a 200K-scoped budget. |
| 8 | `internal/store/user_settings.go:221–223` (Save path) | `contextWindowTokens = 200000` hardcode default | ✓ intentional fallback — acceptable | Default applied only when `us.ContextWindowTokens <= 0` before persisting to DB. This is a write-path guard, not a read-path override. Acceptable but should have a comment tying it to CW-20260419-0001. |
| 9 | `pkg/models/registry.go:612–619` (`ContextWindowFor`) | per-model → per-provider → `DefaultContextWindowTokens` | ✓ correct (tiered fallback) | Correct three-tier resolution. Used by `syncCatalogToRegistry`. |
| 10 | `internal/service/container.go:461–464` (`syncCatalogToRegistry`) | models.dev `OnRefresh` hook → `pkg/models.SyncFromCatalog` | ✓ correct (path-of-record for registry enrichment) | Pushes live ContextWindow values into the pkg/models overlay so `ContextWindowFor` serves live data. |
| 11 | `internal/api/sessions.go:305–311` (compaction HTTP path) | `settings.ContextWindowTokens` (may be 0) | ~ partial | Passes `settings.ContextWindowTokens` directly (may be 0 if not set), which triggers `DefaultContextWindowSize` inside `NewContextWindow`. Does not consult models.dev catalog. Harmless for most sessions but misses 1M-tier models on the compaction-on-demand endpoint. |

---

## Findings

- **Path-of-record sites:** 4 (items 1, 2, 3, 9, 10 — the hot-path is correct)
- **Silent fallback sites needing fix or doc-comment:** 4 (items 4, 5, 6, 7)
- **Intentional / acceptable fallback sites:** 2 (items 3, 8)
- **Partial / degraded sites:** 1 (item 11)
- **Hardcoded constants for CW-20260419-0001:** 4 constants across 3 packages

### Highest-risk findings

**Risk 1 — `EnforceTokenBudget` (item 5): wrong ceiling on every provider call**
`chat_generate.go:466` always passes `ceilingOverride=0`, so `EnforceTokenBudget` computes a hard ceiling from the hardcoded 200K constant for every model, including Gemini 2.5 Pro (1M) and o3 (200K — coincidentally correct). For any model where the actual window differs from 200K, this gate either over-prunes (smaller windows — no such current model) or under-enforces (larger windows). The ceiling value is also surfaced in `breakdown.Ceiling` for telemetry/frontend, making it misleading on Gemini.

**Risk 2 — `toolclient.Config.ContextWindowTokens` never updated from models.dev (item 7)**
The tool token-pruning budget is capped at 200K for all models. On Gemini 2.5 Pro/Flash (1M window), this means tool definitions are pruned to fit a 200K budget fraction (40K), leaving 800K of unused headroom. No mechanism exists to inject the per-session model's window into `ToolBroker` at selection time.

**Risk 3 — Legacy `AssembleContext` budget (item 4)**
`AssembleContext` is the non-slot context path, still reachable and still computing its budget against the hardcoded 200K constant. Any call that uses `AssembleContext` instead of `AssembleSlots` silently caps at 200K regardless of model.

---

## Recommended remediation

### Item 5 — `EnforceTokenBudget` ceiling (HIGH — pre-Phase-3)
Pass the computed `contextWindowSize` through to `EnforceTokenBudget` as the `ceilingOverride` parameter. The caller (`assembleTurnContext`) already has `providerName` + `model` and calls `contextWindowSize()`. Short fix:

```go
// chat_generate.go — in the token budget enforcement block
ceiling := int(float64(s.contextWindowSize(providerName, model)) * chat.HardCeilingPct)
// if ceiling == 0 (unknown model), EnforceTokenBudget falls back to DefaultContextWindow internally
chatMessages, tools, breakdown, budgetErr = chat.EnforceTokenBudget(systemPrompt, chatMessages, tools, ceiling)
```

Alternatively, thread `windowSize` (already computed for `assembleTurnContext`) into the budget call directly.

### Item 7 — `toolclient.Config.ContextWindowTokens` (HIGH — pre-Phase-3)
Wire the per-session model's context window into `ToolBroker.Config.ContextWindowTokens` before each `SelectTools` call. Options: (a) accept it as a `SelectTools` parameter, (b) make `Config` mutable per-call. Recommended: add `windowSize int` parameter to `SelectTools` and forward the value from `chat_generate`.

### Item 4 — Legacy `AssembleContext` budget (MEDIUM)
Either remove the legacy `AssembleContext` path (if all callers have migrated to `AssembleSlots`) or add a `contextWindowSize int` parameter to `AssembleContext`. Add a `// TODO(CW-20260419-0001): this path uses hardcoded 200K` comment at line 92 in the interim.

### Item 6 — `context_breakdown.go` ceiling (LOW — diagnostic only)
The breakdown API endpoint (`GET /api/sessions/:id/context`) is diagnostic, not behavioral. The fix is to look up the session's current model from session state and call `models.ContextWindowFor(modelID)` instead of the constant. Low urgency.

### Item 11 — API compaction path (LOW)
`internal/api/sessions.go:311` passes `settings.ContextWindowTokens` (which may be 0) without consulting models.dev. Add a fallback: if `windowSize == 0`, use `models.ContextWindowFor(session.Model)` before calling `AssembleSlots`.

---

## Follow-ups

### New tickets filed

None — all silent fallbacks are locatable improvements, not broken paths for the currently registered model set (Anthropic models are all 200K in the static registry, which matches the constant). The gaps become acute when Gemini sessions are common or when models.dev delivers values that diverge from static registry entries.

### Recommended scope for CW-20260419-0001 (parked)

When CW-20260419-0001 (publisher system config / externalize constants) is unparked, these four constants should be consolidated:

| Constant | Location |
|---|---|
| `DefaultContextWindow = 200000` | `internal/chat/context_client.go:23` |
| `DefaultContextWindowSize = 200_000` | `internal/context/window.go:14` |
| `DefaultContextWindowTokens = 200000` | `internal/toolclient/config.go:13` |
| `DefaultContextWindowTokens = 200000` | `pkg/models/registry.go:625` |

The `pkg/models` constant is the canonical one; the other three should import and reference it rather than redeclaring.

### Recommended pre-Phase-3 follow-up tickets

The following should be filed as separate tickets before Phase 3 (live traffic):
- Fix `EnforceTokenBudget` ceiling (item 5) — thread `windowSize` through the hot path
- Fix `toolclient.Config.ContextWindowTokens` (item 7) — per-request window injection

These are behavioral gaps on 1M-window models and will silently miscap budgets in production.
