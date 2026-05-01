# Limits audit — hardcoded size caps and dynamicization candidates

**Ticket:** CW-20260430-0008 (P2)
**Sprint:** SP-20260501-0001 (UAT iteration, Round 2)
**Author:** subagent (CW-20260430-0008)
**Status:** Phase 1 (audit) + Phase 2 (rule proposal) + Phase 3 (one pilot conversion)

This document enumerates every hardcoded size-related cap in `internal/`,
`cmd/`, and `pkg/`, classifies each as a hard byte cap (security /
blast-radius — stays hard), a candidate for percentage-of-context-window
sizing, or a hybrid (% with floor / ceiling). It is the audit deliverable
that gates per-limit conversion tickets.

The models.dev integration (`pkg/models/registry.go`) exposes two public
accessors that the dynamicized caps will consume:

- `models.ContextWindowFor(modelID string) int` — total tokens
- `models.MaxOutputFor(modelID string) int` — max output tokens

`pkg/models.DefaultContextWindowTokens` (currently 200000) is the
system-wide fallback used when the model lookup misses.

## Phase 1 — Enumeration

### A. Tool-result truncation and caching (agent-facing payloads)

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 1 | `internal/truncate/truncate.go:16` `MaxChars = 4000` | 4 KB | **Hybrid (% with floor/ceiling)** | Pilot — dynamic compute via `models.ContextWindowFor`; floor = current 4000, ceiling = 32000. | "Empirical hand-pick" called out in the ticket. With 1M Gemini windows, 4 KB is artificially tight; with 200K Anthropic, 4 KB is fine. Sized as ~0.4 % of the window (`window/4 * 0.004` bytes), bracketed by floor/ceiling. |
| 2 | `internal/truncate/truncate.go:18` `MaxLines = 200` | 200 lines | Hybrid (% with floor/ceiling) | Convert to `floor(MaxChars / 20)` so it scales with MaxChars. Defer to a follow-up ticket. | Tied to MaxChars; 200 lines × 20 chars/line ≈ 4000 chars. Should track `MaxChars` rather than have its own knob. |
| 3 | `internal/tool/cache.go:31` `DefaultSoftTruncBytes = 2 KiB` | 2 KiB | Hybrid (% with floor/ceiling) | Convert to ~0.2 % of window, floor 2 KiB, ceiling 16 KiB. Deferred. | This is the cache-pointer threshold. CW-20260419-0004 lowered it from 64 KiB → 2 KiB precisely because tool results were accumulating in the conversation slot at 4 KiB each and burning the rate budget at 200K windows. With 1M windows the same accumulation is a 5× cheaper proposition; the threshold should track. |
| 4 | `internal/tool/cache.go:35` `DefaultHardCapBytes = 1 MiB` | 1 MiB | **Hard byte cap** | Keep hard. | Memory-safety concern (cache row size in SQLite blob). Bigger windows don't change SQLite blob ergonomics. |
| 5 | `internal/store/user_settings.go:281` `toolResultSoftTrunc = 2048` (fallback when user setting is 0) | 2 KiB | Hybrid (% with floor/ceiling) | Mirror the `DefaultSoftTruncBytes` follow-up. Deferred. | Same knob, different default-resolution layer. |
| 6 | `internal/service/chat_loop_state.go:232` `scratchpadMaxValueBytes = 8 KiB` | 8 KiB | Hard byte cap | Keep hard. | P4 scratchpad design (D2). Already justified — agent-ergonomics concern, and the agent-facing description in `self_tools.go:842` advertises this number. |
| 7 | `internal/service/chat_loop_state.go:233` `scratchpadMaxTotalBytes = 64 KiB` | 64 KiB | Hard byte cap | Keep hard. | P4 scratchpad design (D2); same agent-ergonomics rationale. |

### B. Per-tool ad-hoc caps (built-in tools)

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 8 | `internal/mcp/dev_tools.go:310` `devBashStreamCap = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | "Memory wedge" defense per existing comment. Stream-level memory bound, not context-budget concern. |
| 9 | `internal/mcp/dev_tools.go:316` `devGrepPerFileCap = 10 MiB` | 10 MiB | Hard byte cap | Keep hard. | File-skip threshold (don't load generated blobs). Filesystem I/O, not LLM-context. |
| 10 | `internal/mcp/dev_tools.go:321` `devGrepResultCap = 1 MiB` | 1 MiB | Hybrid (% with floor/ceiling) | Convert. Floor 256 KiB, ceiling 4 MiB. Deferred. | Bounds dev_grep result; this *does* feed into LLM context, so larger windows can absorb larger result payloads. |
| 11 | `internal/mcp/dev_tools.go:326` `devGrepPatternCap = 4 KiB` | 4 KiB | Hard byte cap | Keep hard. | Anti-abuse defense on the regex pattern itself. Not a context concern. |
| 12 | `internal/mcp/general_tools.go:252` `webFetchBodyCap = 1 MiB` | 1 MiB | Hybrid (% with floor/ceiling) | Convert. Floor 512 KiB, ceiling 4 MiB. Deferred. | Same shape as #10 — body feeds into LLM context. |
| 13 | `internal/mcp/self_tools_python.go:34` `pythonSandboxMaxOutputBytes = 256 KiB` | 256 KiB | Hard byte cap | Keep hard. | Sandbox output bound; aligns with sandbox/exec.go. |
| 14 | `internal/sandbox/exec.go:15` `maxOutputBytes = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Same rationale — sandbox memory bound. |
| 15 | `internal/background/types.go:86` `DefaultMaxOutputBytes = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Subprocess output capture bound. Surfaced to user as `max_output_bytes` arg on `nanite_spawn_subagent` (see `internal/mcp/self_tools.go:765`). |
| 16 | `internal/mcp/self_tools_list.go:82` `summaryMaxBytes = 80` | 80 chars | Hard byte cap | Keep hard. | Per-tool one-line summary budget for `nanite_tool_list`. Agent-ergonomics — readable line length is independent of context window. |

### C. MCP trust-tier ceilings (security-rationale per `docs/mcp-trust-model.md`)

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 17 | `internal/mcp/validate.go:45` builtin `MaxResultBytes = 2 MiB` | 2 MiB | **Hard byte cap (security)** | Keep hard. **Explicitly out of scope per ticket.** | Trust model: built-in code is "full host confidence." Document calls these out as security boundary, not ergonomics. |
| 18 | `internal/mcp/validate.go:53` plugin_stdio `MaxResultBytes = 512 KiB` | 512 KiB | Hard byte cap (security) | Keep hard. Out of scope. | Plugin code is one trust-step removed from built-in. |
| 19 | `internal/mcp/validate.go:*` plugin_http `MaxResultBytes = 256 KiB` | 256 KiB | Hard byte cap (security) | Keep hard. Out of scope. | HTTP transport adds a network failure surface. |
| 20 | `internal/mcp/validate.go:*` third_party_http `MaxResultBytes = 128 KiB` | 128 KiB | Hard byte cap (security) | Keep hard. Out of scope. | Strictest tier. The "fail-closed" default. |
| 21 | `internal/mcp/http_transport.go:60` `maxHTTPResponseBytes = 10 MiB` | 10 MiB | Hard byte cap (security) | Keep hard. Out of scope. | Safety net under the tier ceiling. Documented in trust model. |
| 22 | `internal/mcp/stdio_transport.go:25` `maxStdioResponseBytes = 10 MiB` | 10 MiB | Hard byte cap (security) | Keep hard. Out of scope. | Mirror of #21 for stdio. |
| 23 | `internal/mcp/validate.go:*` per-tier `MaxInputSchema/MaxToolName/MaxDescription/MaxToolsPerServer` (multiple constants) | various | Hard byte cap (security) | Keep hard. Out of scope. | Discovery-time bounds; not result-payload sizing. |

### D. Repair LLM caps (CW-20260429-0028)

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 24 | `internal/recover/repair.go:79` `DefaultRepairMaxTokens = 4096` | 4096 tokens | **% of context window (output cap)** | Convert. ~5 % of `models.MaxOutputFor` for the repair model, floor 4096, ceiling 8192. Deferred. | The repair LLM emits a single small JSON blob; 4096 was sized for Sonnet 4 output. With Haiku (8192) or Gemini 2.5 Pro (65536) the cap can flex — repair payloads grow with `repaired_args` size. The known wiring gap (`provider.ChatRequest.MaxTokens` plumbing — see comment) lands first; this conversion is a follow-up to that work. |
| 25 | `internal/recover/repair.go:84` `MaxArgsBytes = 16 KiB` | 16 KiB | Hybrid (% with floor/ceiling) | Convert. ~0.5 % of window, floor 16 KiB, ceiling 64 KiB. Deferred. | sent_args truncation cap; bigger windows can carry bigger args without bloating the repair prompt. |
| 26 | `internal/recover/repair.go:89` `MaxSchemaBytes = 32 KiB` | 32 KiB | Hybrid (% with floor/ceiling) | Convert. Same shape as #25. Deferred. | Same rationale. |

### E. Provider error-response readers

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 27 | `pkg/provider/anthropic.go:29` `maxAnthropicErrBody = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Anti-abuse on provider error bodies. Provider concern, not LLM-context. |
| 28 | `pkg/provider/openai.go:30` `maxOpenAIErrBody = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Same. |
| 29 | `pkg/provider/gemini.go:60` `maxGeminiErrBody = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Same. |
| 30 | `pkg/provider/mistral.go:29` `maxCompatErrBody = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Same. |
| 31 | `pkg/provider/ollama.go:27` `maxOllamaErrBody = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Same. |
| 32 | `pkg/provider/anthropic.go:725` MaxTokens=128 (probe call) | 128 tokens | Hard byte cap | Keep hard. | Inspector / probe mini-call; intentional tight bound. |

### F. HTTP request body / multipart

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 33 | `internal/server/server.go:45-46` `defaultMaxRequestBodyBytes = 10 MiB`, `defaultMaxUploadBodyBytes = 32 MiB` | 10 / 32 MiB | Hard byte cap | Keep hard. | HTTP DoS defense. Configurable via `internal/config/appconfig.go:114-115`. |
| 34 | `internal/plugin/registrations.go:22` `maxPluginHTTPBodyBytes = 10 MiB` | 10 MiB | Hard byte cap | Keep hard. | Same. |
| 35 | `internal/api/plugins.go` / `internal/api/artifacts.go` `ParseMultipartForm(32 << 20)` | 32 MiB | Hard byte cap | Keep hard. | Multipart parsing memory bound. |
| 36 | `internal/plugin/install/download.go:18` `DefaultMaxArchiveBytes = 100 MiB` | 100 MiB | Hard byte cap | Keep hard. | Plugin archive size bound. |
| 37 | `internal/plugin/install/extract.go:17-18` `DefaultMaxTotalUncompressedBytes = 500 MiB`, `DefaultMaxFileBytes = 100 MiB` | 500 / 100 MiB | Hard byte cap | Keep hard. | Zip-bomb defense. |
| 38 | `internal/plugin/catalog/fetch.go:25` `MaxCatalogBytes = 4 MiB` | 4 MiB | Hard byte cap | Keep hard. | Catalog manifest size bound. |
| 39 | `internal/api/plugins.go:32-39` archive-extract caps | 100 / 500 MiB | Hard byte cap | Keep hard. | Same as #36–37. |
| 40 | `internal/store/session_objects.go:16` `DefaultSessionObjectMaxBytes = 1 MiB` | 1 MiB | Hard byte cap | Keep hard. | Single session-object value cap (SQLite ergonomics). |

### G. Context-window enforcement (already dynamic — observed for completeness)

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 41 | `internal/chat/context_client.go:23` `DefaultContextWindow = 200000` | 200K tokens | Already dynamic | — | Already overlaid by `s.contextWindowSize(provider, model)` (`internal/service/chat_generate.go:1925`). Kept as fallback when model lookup misses. **Do not touch.** |
| 42 | `internal/chat/context_client.go:28` `HardCeilingPct = 0.80` | 80 % | Already dynamic | — | Already a percentage (% of context window). Existing design. |
| 43 | `internal/chat/context_client.go:20` `DefaultBudgetPct = 0.75` | 75 % | Already dynamic | — | Already a percentage. |
| 44 | `internal/contextbroker/broker.go:39` `DefaultBudget.MaxTokens = 50000` | 50K tokens | Hybrid (% with floor/ceiling) | Convert. ~25 % of window, floor 50K, ceiling 200K. Deferred. | Context retrieval budget; should scale with window since the broker feeds into the same conversation slot. |
| 45 | `internal/chat/hint_dispatch.go:53` `thinkBlockMaxTokens = 200` | 200 tokens | Hard byte cap | Keep hard. | Render-budget for the v1/v2 think block. Agent-ergonomics — the block is a *hint*, not a payload. Bigger windows don't make hints want to grow. |
| 46 | `internal/context/window.go` per-slot `MaxTokens` (set via `slot.MaxTokens` from budget map) | various | Already dynamic | — | Each slot's budget is configured via `BudgetConfig` from caller. Already context-aware. |

### H. Per-tool call cap (CW-20260419-0012)

| # | Location | Value | Category | Disposition | Rationale |
|---|---|---:|---|---|---|
| 47 | `internal/service/chat_loop_state.go:77` `defaultMaxRequestToolsCalls = 6` | 6 calls | Hard byte cap | Keep hard. | Iteration limit, not a size limit. Agent-ergonomics — already user-tunable via `user_settings`. Surfaced here because `CLAUDE.md` mentions it adjacent to result-cache; not actually a payload cap. |

## Phase 2 — Proposed rule

**Open question (orchestrator-pending):** Is "% of context window" the right
shape for *all* surface-facing caps?

**Proposed rule (orchestrator decides before per-limit conversions land):**

> **Agent-ergonomics caps stay hard. Payload-fits-in-context caps become
> percentages.**
>
> A cap is **agent-ergonomics** when its purpose is to keep an artifact
> easy for the agent to read, summarize, or act on — independent of how
> big the surrounding context can grow. Example: `summaryMaxBytes = 80`
> for `nanite_tool_list`. A 1M-token window doesn't make a 50 KB tool
> inventory more usable; the agent still has to scan it linearly. Same
> for `thinkBlockMaxTokens = 200`, the per-tool call cap, and the
> scratchpad value/total caps.
>
> A cap is **payload-fits-in-context** when its purpose is to keep an
> artifact from blowing the conversation slot's token budget. Example:
> `truncate.MaxChars = 4000`, `DefaultSoftTruncBytes = 2 KiB`. With a
> larger window, the same payload is a smaller fraction of available
> space — the cap can flex.

**Three-bucket disposition (codified above as the table column):**

1. **Hard byte cap (security / blast-radius / agent-ergonomics).** Keep
   hardcoded. Examples: trust-tier ceilings, sandbox memory bounds, HTTP
   body bounds, scratchpad caps, summary line length, think-block budget.
2. **% of context window with floor/ceiling.** Convert to dynamic. The
   floor preserves the current empirical default for small-window models
   and tests; the ceiling caps growth so a 10M-token window doesn't
   produce a 100 KB single-result blob. Examples: `truncate.MaxChars`,
   `DefaultSoftTruncBytes`, `devGrepResultCap`, `webFetchBodyCap`,
   repair `MaxArgsBytes` / `MaxSchemaBytes`.
3. **Pure % of context window (no floor — only ceiling).** Reserved for
   caps where the floor wouldn't make sense. None identified in this
   audit.

**Why "hybrid" (with floor) is the default for #2:** Tests run without
a configured model; CI must remain deterministic. Local-only models
(Ollama, PTY CLIs) report 0 max_output and small or unknown context
windows. The floor preserves current behavior on every code path that
doesn't have a real catalog entry — including every existing test —
without requiring per-test wiring.

**Caps the rule eliminates:** none. Every cap above either (a) stays
hard or (b) becomes a percentage with a floor at the current value, so
no behavior changes by default. Conversion is purely upside for
larger-window models.

## Phase 3 — pilot

The pilot converts **`internal/truncate/truncate.go:16`
`MaxChars = 4000`** to a model-aware computation. Chosen because:

- It is squarely in the "agent-facing payload" bucket — every tool
  result that misses the cache pointer flows through it.
- It has a clean caller path: only `chat_tool_executor.go:681` exercises
  it on the production hot path. Two other callers
  (`internal/shell/exec.go:86`, `internal/mcp/code_exec_tools.go:193`)
  are out-of-line code paths that will continue using the static cap
  until per-call conversions ship.
- It has a tight test surface (`truncate_test.go`) that pins behavior at
  the static default — easy to extend with a stubbed-window test.
- It is not touched by other Round 2 tickets (`git log` confirms the
  last meaningful edit was 760db6c "fix: strip LLM-coaching leaks…").

### Behavior

A new function `truncate.OutputForModel(text, toolName, modelID, opts...)`
computes a per-call `maxChars` budget from `pkg/models.ContextWindowFor`:

```
windowTokens   := models.ContextWindowFor(modelID)
windowBytes    := windowTokens * 4         // common 4-bytes-per-token approximation
proposed       := int(float64(windowBytes) * 0.004)  // ~0.4 % of window
maxChars       := clamp(proposed, MaxChars, MaxCharsCeiling)
```

Constants pinned:
- `MaxChars = 4000` (existing — floor, kept for backward compatibility).
- `MaxCharsCeiling = 32000` (new — caps growth at 32 KB even on a 10M
  window).

Effective sizes by model:

| Model | Context window | windowBytes | proposed (0.4%) | clamped | Δ vs. 4 KB floor |
|---|---:|---:|---:|---:|---|
| Claude Sonnet 4 | 200K | 800 KB | 3.2 KB | 4 KB (floor) | unchanged |
| Claude Opus 4 | 200K | 800 KB | 3.2 KB | 4 KB (floor) | unchanged |
| GPT-4o | 128K | 512 KB | 2.0 KB | 4 KB (floor) | unchanged |
| Gemini 2.5 Pro | 1M | 4 MB | 16 KB | 16 KB | **+12 KB headroom** |
| Llama 3.1 (Ollama) | 131K | 524 KB | 2.1 KB | 4 KB (floor) | unchanged |
| (unknown / no catalog) | 200K (fallback) | 800 KB | 3.2 KB | 4 KB (floor) | unchanged |

Telemetry: a single `slog.Info` with attributes `tool`, `model`,
`window_tokens`, `effective_max_chars`, `original_len`, `truncated_len`
fires whenever `OutputForModel` actually trims a result. The existing
"chat-service: tool result truncated" line in
`chat_tool_executor.go:697` is left in place; the new line is
package-level (`truncate: dynamic cap trimmed`) so operators can grep
on either.

### Wiring

`postProcessToolResults` gains a `modelID string` parameter. The single
production caller (`chat_generate.go:1235`) already has `model` in
scope. The single test caller (`chat_tool_executor_error_honesty_test.go:95`)
gets an empty-string model — exercising the floor branch deliberately,
since the test's whole purpose is to pin error-result handling.

### Test

`truncate_test.go` adds two cases:

1. `TestOutputForModel_LargeWindow_Expands` — pin a 1M-token model
   (Gemini 2.5 Pro path), feed 8 KB of content, expect `Truncated=true`
   when the static `Output` would have truncated, but `len(Content) >
   len(static.Content)` confirming the dynamic cap let more content
   through. Window is stubbed via the existing models.dev catalog
   overlay.
2. `TestOutputForModel_UnknownModel_FallsBackToFloor` — empty model ID
   matches the fallback branch; behavior is identical to `Output`.

### Rollback

Reverting Phase 3 is the diff-revert on `internal/truncate/truncate.go`,
`internal/service/chat_tool_executor.go` (one parameter back), and
`internal/service/chat_generate.go` (one call site back). No DB
migrations, no config schema changes.

## Vanta-key proposal

`decisions.nanite.architecture.dynamic_size_limits` — drafted in
`implementer-report.md`. Not written from the worktree; the orchestrator
captures.
