# [Info] seed.go is well-structured as the canonical model data source

**Scope:** seed data
**Topic:** Standards
**Date:** 2026-04-11

## Problem

No problem. This is an observation about the current state.

## Evidence

`internal/store/seed.go:SeedProviders()` (lines 255-358) defines:

- **16 providers** covering all supported provider types
- **32 models** across Anthropic (3), OpenAI (4), Ollama (1), Gemini (3), Mistral (4), Azure (1), PTY CLIs (8), OpenRouter (6), OpenZen (4)
- Uses `INSERT OR IGNORE` for idempotent boot-time seeding
- Called on every server start (not just first-time seed)
- Per-model metadata: model_id, display_name, context_window, max_output, supports_tools

The `Seed()` method (lines 9-251) handles first-time-only data (workspaces, catalog sources) and a smaller subset of providers/models. `SeedProviders()` is the comprehensive one called on every boot.

## Impact

The dual-method structure (`Seed()` for first-time, `SeedProviders()` for every boot) means the initial seed in `Seed()` is partially redundant — it seeds Anthropic, Gemini, Mistral, Azure, and PTY providers that `SeedProviders()` will also upsert on the next boot. This isn't harmful (INSERT OR IGNORE makes it idempotent) but the Anthropic model in `Seed()` line 56 uses `max_output: 16000` while `SeedProviders()` line 286 also uses `16000` — they agree, which is good.

## Recommendation

None. The structure is sound. The dual-method approach is a minor redundancy, not a bug.

## References

- `internal/store/seed.go:L9-251` (Seed)
- `internal/store/seed.go:L255-358` (SeedProviders)
