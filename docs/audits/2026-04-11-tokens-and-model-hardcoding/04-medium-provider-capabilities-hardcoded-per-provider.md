# [Medium] Provider Capabilities() methods hardcode token limits and context windows

**Scope:** provider abstraction
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

Each provider's `Capabilities()` method returns hardcoded `MaxTokens` and `ContextWindowSize` values that represent a single model's limits, even though providers serve multiple models with different limits. These values are duplicated between `Capabilities()` and `seed.go`, with no mechanism to keep them synchronized.

## Evidence

| Provider | File:Line | MaxTokens | ContextWindowSize | Which model? |
|---|---|---|---|---|
| Anthropic | `anthropic.go:768-769` | 16384 | 200000 | Claude Sonnet (but Opus has 32000 max_output) |
| OpenAI | `openai.go:258-259` | 16384 | 128000 | GPT-4o (but o3/o4-mini have 100000 max_output) |
| Gemini | `gemini.go:291-292` | 65536 | 1048576 | Gemini 2.5 Pro |
| Mistral | `mistral.go:235-236` | 16384 | 131072 | Mistral Large (but Codestral has 262144 context) |
| Azure OpenAI | `azure_openai.go:251-252` | 16384 | 128000 | GPT-4o |
| OpenRouter | `openrouter.go:232` | 0 | 200000 | "Upper bound" |
| OpenZen | `openzen.go:239` | 0 | 200000 | "Upper bound" |
| Ollama | `ollama.go:225` | 0 | 0 | Variable |

Meanwhile, `seed.go` (`SeedProviders()`) defines per-model values:

| Model | seed.go context_window | seed.go max_output | Capabilities() match? |
|---|---|---|---|
| Claude Sonnet 4 | 200000 | 16000 | MaxTokens=16384 vs 16000 -- **MISMATCH** |
| Claude Opus 4 | 200000 | 32000 | MaxTokens=16384 -- **MISMATCH** |
| GPT-4o | 128000 | 16384 | Match |
| o3 | 200000 | 100000 | MaxTokens=16384 -- **MISMATCH** |
| Gemini 2.5 Flash | 1048576 | 8192 | MaxTokens=65536 -- **MISMATCH** |

The Anthropic adapter also hardcodes `MaxTokens: 16384` in the request body at `anthropic.go:296`, which means Opus requests are capped at 16384 output tokens even though the model supports 32000.

## Impact

1. **Opus output silently capped at 16384 tokens** instead of 32000. Long-form outputs from Opus will be truncated. This is a functional bug when using Opus.
2. **o3 and o4-mini capped at 16384** instead of 100000. Same class of bug.
3. Context window mismatches cause the context broker to compute incorrect budgets for models that differ from the "representative" model hardcoded in Capabilities().
4. Gemini `buildRequest()` at `gemini.go:97` hardcodes `MaxOutputTokens: 8192` in every request, regardless of the model's actual capability.

## Recommendation

`Capabilities()` should accept the model name as a parameter, or the system should look up per-model limits from the seed data (which is already in the database). The per-model limits in `seed.go` are closer to correct than the per-provider hardcodes in `Capabilities()`.

Immediate fix for the Opus/o3 output cap: the request-building code should read `max_output` from the model's database row rather than using the provider-level constant.

## References

- `pkg/provider/anthropic.go:L296` (request body MaxTokens)
- `pkg/provider/anthropic.go:L768-769` (Capabilities hardcode)
- `pkg/provider/openai.go:L258-259`
- `pkg/provider/gemini.go:L97` (buildRequest hardcode)
- `pkg/provider/gemini.go:L291-292`
- `pkg/provider/mistral.go:L235-236`
- `internal/store/seed.go:L284-328` (per-model values)
