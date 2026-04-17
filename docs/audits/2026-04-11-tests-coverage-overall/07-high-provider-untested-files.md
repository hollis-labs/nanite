# [High] Provider package: 12 source files with zero test coverage including 5 provider adapters

**Scope:** pkg/provider/
**Topic:** Test Quality — broad coverage gaps in provider layer
**Date:** 2026-04-11

## Problem

The `pkg/provider/` package has 9385 lines of source across ~30 files. 12 source files (3127 lines) have no matching test file. This includes 5 provider adapters (OpenAI, Ollama, Mistral, Azure OpenAI — none tested) and the security-critical `scope_guard.go` (covered separately in finding 02).

## Evidence

Files with zero test coverage in `pkg/provider/`:

| File | Lines | Risk level | Notes |
|------|-------|-----------|-------|
| `scope_guard.go` | 202 | Security-critical | See finding 02 |
| `openai.go` | 362 | High | Full provider adapter, streaming, tool use |
| `azure_openai.go` | 363 | High | Azure variant of OpenAI, additional auth logic |
| `mistral.go` | 335 | High | Provider adapter with Mistral-specific streaming |
| `ollama.go` | 331 | High | Local provider, different API shape |
| `cost_monitor.go` | 208 | Medium | Cost tracking, pricing lookups |
| `progress_tracker.go` | 293 | Medium | Progress state machine |
| `provider.go` | 186 | Low | Interface definitions, types |
| `registry.go` | 39 | Low | Simple map registry |
| `cli_adapter.go` | 28 | Low | Interface definition |
| `cli_detect.go` | 38 | Low | Binary lookup |
| `api_key.go` | 27 | Low | Key validation helper |

Files WITH test coverage (well-tested):
- `anthropic.go` (771 lines) — `anthropic_test.go` exists
- `gemini.go` (396 lines) — `gemini_test.go` exists
- `pty.go` + all `pty_*.go` adapters — individual test files exist
- `circuit.go`, `ratelimit.go`, `retry.go`, `cache.go` — all tested
- `event_pipeline.go` — tested
- `subprocess.go` — tested
- `capabilities.go`, `model_ops.go`, `openrouter.go`, `openzen.go` — tested

## Impact

Four of the eight HTTP API provider adapters have no tests. These are trust boundaries (outbound HTTP to external APIs). The Anthropic adapter is tested because it was the first; Gemini was added later with tests; OpenAI, Ollama, Mistral, and Azure OpenAI were apparently added without test coverage. Any regression in streaming parsing, tool-use response handling, or error mapping for these providers will go undetected.

The tooling-sweep noted `unused` findings for `parseAiderJSON` and `parseKiroJSON` — dead parser code in PTY adapters. The HTTP adapters have the same risk: without tests exercising the response parsing, there is no proof the parsers work.

## Recommendation

Priority order for adding tests:
1. `openai.go` — most widely used provider after Anthropic
2. `ollama.go` — local provider, different failure modes (connection refused, model not loaded)
3. `azure_openai.go` — Azure-specific auth (managed identity, API key header)
4. `mistral.go` — Mistral-specific streaming format
5. `cost_monitor.go` — pricing correctness matters for usage tracking

Use the existing `anthropic_test.go` as the template: mock `httptest.NewServer`, return provider-specific response JSON, verify streaming events are correctly parsed.

## References

- `pkg/provider/anthropic_test.go` — reference implementation for provider adapter testing
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — `unused` findings for PTY parsers suggest similar dead code risk
