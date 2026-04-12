# Audit: tokens-and-model-hardcoding

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `tokens-and-model-hardcoding`

**Interpretation:** Every place model names, token limits, context windows, and pricing are hardcoded in the Go backend. Classification of each as canonical (seed.go) vs. rogue hardcoding. Spot-check of seed data accuracy. Map of model-name-based behavior switching.

**Files read in full:**
- `internal/store/seed.go` — canonical model/provider data
- `internal/store/usage.go` — pricing map and cost estimation
- `pkg/provider/cost_monitor.go` — budget enforcement pricing
- `pkg/provider/event_pipeline.go` — pipeline config defaults
- `internal/toolclient/config.go` — tool broker context window defaults
- `internal/chat/context_client.go` — context budget defaults

**Files read in relevant sections:**
- `internal/chat/engine.go:L100-135` — InferProvider
- `cmd/nanite/main.go:L150-180` — utility model resolution
- `internal/service/chat_generate.go:L108-125` — model resolution
- `internal/service/delegation.go:L40-60` — delegation model resolution
- `internal/service/chat.go:L108-120` — ChatService constructor
- `internal/service/container.go:L240-280, L410-425` — embedder and utility model wiring
- `internal/builders/agent_builder.go:L60-85` — interactive builder defaults
- `pkg/provider/anthropic.go:L280-300, L670-690, L760-771` — Capabilities, defaults
- `pkg/provider/openai.go:L250-265, L295-300, L350-362` — Capabilities, embedding dims
- `pkg/provider/gemini.go:L90-100, L280-295, L325-335, L388-396` — request hardcode, Capabilities, embedding
- `pkg/provider/mistral.go:L228-242, L273, L329-335` — Capabilities, embedding dims
- `pkg/provider/azure_openai.go:L250-255, L353-363` — Capabilities, embedding dims
- `pkg/provider/openrouter.go:L225-234` — Capabilities
- `pkg/provider/openzen.go:L45-50, L175, L232-241` — defaults, Capabilities
- `pkg/provider/ollama.go:L220-230, L255-260, L320-331` — Capabilities, embedding dims

**Skipped:** Frontend code, plugin YAML files, test fixtures (cataloged but not audited for correctness).

## Methodology

**Categories applied:** Antipatterns (primary), Standards (seed data accuracy spot-check).

**Categories skipped:** Security (no trust boundary relevance for hardcoded constants), Concurrency (no concurrency in constant definitions), Memory (no resource leak risk), Error Handling (not applicable), Test Quality (test fixtures are expected to hardcode values), Idioms (subsumed by Antipatterns).

**Tooling deferred:** `go vet`, `golangci-lint`, `staticcheck` — not relevant for a constants/hardcoding audit. No code execution needed; this is a static analysis pass.

**Approach:** Comprehensive grep for model name patterns, token limit integers, pricing floats, and model-name-based branching across all `.go` files. Each hit classified as canonical (seed.go / usage.go), provider-scoped (acceptable), rogue (should reference shared data), or test fixture (expected).

## Findings

### By severity

**Critical (0)**
_none_

**High (0)**
_none_

**Medium (6)**
- [01 -- Default model fallback scattered across 6 call sites](01-medium-model-name-fallback-scatter.md)
- [02 -- InferProvider uses model-name prefix matching to select providers](02-medium-infer-provider-model-name-branching.md)
- [03 -- CostMonitor always estimates cost using Anthropic rates](03-medium-cost-monitor-hardcoded-anthropic-default.md)
- [04 -- Provider Capabilities() methods hardcode token limits per-provider](04-medium-provider-capabilities-hardcoded-per-provider.md)
- [05 -- Two independent pricing maps with different schemas](05-medium-dual-pricing-maps.md)
- [06 -- seed.go spot-check: Haiku model ID may be stale](06-medium-seed-data-staleness.md)

**Low (2)**
- [07 -- Embedding model names and dimensions hardcoded per-provider](07-low-embedding-model-hardcoding.md)
- [08 -- Context window default (200000) duplicated in three packages](08-low-context-window-defaults-duplicated.md)

**Info (2)**
- [09 -- seed.go is well-structured as canonical model data source](09-info-seed-go-coverage-and-structure.md)
- [10 -- Comprehensive map of all hardcoded model/token/pricing references](10-info-hardcoding-map.md)

### By topic

**Model name hardcoding**
- [01 -- Default model fallback scattered across 6 call sites](01-medium-model-name-fallback-scatter.md)
- [02 -- InferProvider uses model-name prefix matching](02-medium-infer-provider-model-name-branching.md)
- [07 -- Embedding model names hardcoded per-provider](07-low-embedding-model-hardcoding.md)
- [10 -- Comprehensive hardcoding map](10-info-hardcoding-map.md)

**Token limits and context windows**
- [04 -- Provider Capabilities() hardcode token limits](04-medium-provider-capabilities-hardcoded-per-provider.md)
- [08 -- Context window default duplicated](08-low-context-window-defaults-duplicated.md)
- [10 -- Comprehensive hardcoding map](10-info-hardcoding-map.md)

**Pricing and cost estimation**
- [03 -- CostMonitor hardcodes Anthropic rates](03-medium-cost-monitor-hardcoded-anthropic-default.md)
- [05 -- Two independent pricing maps](05-medium-dual-pricing-maps.md)

**Seed data accuracy**
- [06 -- Seed data spot-check](06-medium-seed-data-staleness.md)
- [09 -- seed.go structure observation](09-info-seed-go-coverage-and-structure.md)

## Recommended next steps

1. **Extract `DefaultModel` and `DefaultProvider` constants** (finding 01). Single-constant fix, touches 6 files. Low risk.
2. **Fix InferProvider misroutes** (finding 02). The `o4-mini`, Gemini API, and Mistral API misroutes are functional bugs when sessions lack explicit provider assignments. Short-term: add missing prefix rules. Long-term: database lookup.
3. **Fix Anthropic adapter MaxTokens for Opus/o3** (finding 04). The request body hardcodes 16384, silently capping Opus at half its output capacity. This is the most user-visible bug in this audit.
4. **Unify pricing sources** (findings 03, 05). CostMonitor should consume `usage.go:modelPricing` or a shared derivative.
5. **Verify Haiku 4.5 model ID** (finding 06). Quick check against Anthropic docs.

## Known issues skipped

- `models.dev` integration is explicitly deferred per the audit scope instructions. This audit documents the current state that the integration would replace.
- Provider abstractions audit (2026-04-11) already covers Gemini API key in URL and unbounded responses. No overlap with this scope.

## Noticed but out of scope

- **`internal/chat/engine.go:InferProvider` covers PTY CLIs but not Junie, Kiro, Copilot, Aider, or Qwen CLIs.** If a session has model `junie-cli` with no explicit provider, it falls through to `"anthropic"`. Suggested scope: `pty-adapter-routing`.
- **`pkg/provider/cost_monitor.go:104` always passes `"anthropic"` — the provider name is not propagated through the event pipeline.** The event pipeline's `StreamEvent` struct lacks a provider field. Suggested scope: `event-pipeline-provider-context`.
- **`internal/store/seed.go:Seed()` and `SeedProviders()` have overlapping model definitions.** The initial seed creates a subset of what SeedProviders creates. Not harmful (INSERT OR IGNORE) but could be simplified. Suggested scope: `seed-consolidation`.
