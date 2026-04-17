# [Info] Comprehensive map of all hardcoded model/token/pricing references

**Scope:** entire codebase
**Topic:** Reference map
**Date:** 2026-04-11

## Problem

This is a reference document, not a finding. It maps every hardcoded model name, token limit, and pricing value in the Go codebase, classified by whether it belongs in seed.go or is a rogue hardcoding.

## Evidence

### A. Model name hardcodings

#### Seed data (canonical — belongs here)
- `internal/store/seed.go:L56` — `claude-sonnet-4-20250514`
- `internal/store/seed.go:L284-328` — all 32 model rows in SeedProviders

#### Provider defaults (provider-scoped — acceptable)
- `pkg/provider/anthropic.go:291,678` — `claude-sonnet-4-20250514` (empty-model fallback)
- `pkg/provider/openzen.go:46,175` — `claude-sonnet-4-20250514` (empty-model fallback)

#### System-level defaults (rogue — should reference a constant)
- `cmd/nanite/main.go:160` — `claude-sonnet-4-20250514`
- `internal/service/chat_generate.go:120` — `claude-sonnet-4-20250514`
- `internal/service/delegation.go:54` — `claude-sonnet-4-20250514`
- `internal/service/chat.go:116` — `claude-sonnet-4-20250514`
- `internal/service/container.go:417` — `claude-sonnet-4-20250514`
- `internal/builders/agent_builder.go:63,83` — `claude-sonnet-4-20250514`

#### Model-name-based branching (maintenance debt)
- `internal/chat/engine.go:L110-125` — InferProvider switch on model prefixes
- `pkg/provider/ollama.go:L321-331` — EmbeddingDimensions switch
- `pkg/provider/openai.go:L352-361` — EmbeddingDimensions switch
- `pkg/provider/azure_openai.go:L353-363` — EmbeddingDimensions switch
- `pkg/provider/gemini.go:L390-396` — EmbeddingDimensions switch
- `pkg/provider/mistral.go:L329-335` — EmbeddingDimensions switch

#### Embedding model hardcodings
- `internal/service/container.go:258` — `text-embedding-3-large`
- `internal/service/container.go:264,266` — `nomic-embed-text`
- Provider Capabilities() — see finding 07

#### Test fixtures (expected — not rogue)
- `pkg/provider/anthropic_test.go:133`, `pty_claude_test.go:18`
- `internal/chat/engine_test.go:74`, `internal/service/chat_test.go:335`
- `internal/store/usage_test.go`, `user_settings_test.go`, `session_overrides_test.go`
- `internal/agent/parser_test.go:14,41`, `convert_test.go:15,49`
- `internal/builders/builder_test.go:59,110,398,417`
- `internal/store/execution_metrics_test.go:20,58,130`
- `internal/chat/activity_test.go:73,74`
- `pkg/provider/model_ops_test.go` (various)

### B. Token limit hardcodings

#### Seed data (canonical)
- `internal/store/seed.go` — all context_window and max_output values per model (see finding 06 for accuracy)

#### Provider Capabilities (rogue — should derive from model data)
- `pkg/provider/anthropic.go:768-769` — MaxTokens=16384, ContextWindowSize=200000
- `pkg/provider/openai.go:258-259` — MaxTokens=16384, ContextWindowSize=128000
- `pkg/provider/gemini.go:291-292` — MaxTokens=65536, ContextWindowSize=1048576
- `pkg/provider/mistral.go:235-236` — MaxTokens=16384, ContextWindowSize=131072
- `pkg/provider/azure_openai.go:251-252` — MaxTokens=16384, ContextWindowSize=128000
- `pkg/provider/openrouter.go:232` — ContextWindowSize=200000
- `pkg/provider/openzen.go:239` — ContextWindowSize=200000

#### Request body hardcodes (rogue — should derive from model data)
- `pkg/provider/anthropic.go:296` — MaxTokens=16384 in request body
- `pkg/provider/gemini.go:97` — MaxOutputTokens=8192 in request body

#### Context defaults (semi-rogue — should share a constant)
- `internal/toolclient/config.go:13` — DefaultContextWindowTokens=200000
- `internal/chat/context_client.go:22` — DefaultContextWindow=200000

#### Budget defaults
- `pkg/provider/event_pipeline.go:42` — TokenBudget=100000

### C. Pricing hardcodings

#### usage.go (canonical for usage recording)
- `internal/store/usage.go:L6-31` — 15 model-specific pricing entries
- `internal/store/usage.go:L38` — fallback pricing {3.0, 15.0}

#### cost_monitor.go (rogue — should unify with usage.go)
- `pkg/provider/cost_monitor.go:L60-75` — 3 provider-level pricing entries

### D. Provider-name defaults
- `cmd/nanite/main.go:157` — `"anthropic"` (utility provider default)
- `internal/service/chat.go:112` — `"anthropic"`
- `internal/service/container.go:413` — `"anthropic"`

## Recommendation

See individual findings (01-08) for specific recommendations per category. This map is for reference.

## References

All file references are inline above.
