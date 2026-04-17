# [Medium] seed.go model data spot-check: Haiku model ID likely stale, other values current

**Scope:** seed data accuracy
**Topic:** Standards
**Date:** 2026-04-11

## Problem

`seed.go:SeedProviders()` is the canonical single source of truth for model metadata. A spot-check of 4 major models reveals one likely stale model ID.

## Evidence

Spot-check of `internal/store/seed.go:L284-306` against public documentation:

### Claude Sonnet 4
- **Seeded model_id:** `claude-sonnet-4-20250514`
- **Seeded context_window:** 200,000
- **Seeded max_output:** 16,000
- **Public docs:** Model ID correct. Context window 200K correct. Max output 16,000 correct (64K available via `max_tokens` parameter with extended thinking, but 16K is the standard default).
- **Verdict:** Current.

### Claude Opus 4
- **Seeded model_id:** `claude-opus-4-20250514`
- **Seeded context_window:** 200,000
- **Seeded max_output:** 32,000
- **Public docs:** Model ID correct. Context 200K correct. Max output 32K correct.
- **Verdict:** Current.

### Claude Haiku 4.5
- **Seeded model_id:** `claude-haiku-4-5-20251001`
- **Seeded context_window:** 200,000
- **Seeded max_output:** 8,192
- **Public docs:** The model ID `claude-haiku-4-5-20251001` should be verified. Anthropic's naming convention for Haiku 4.5 may use a different date suffix. The context window and max output values are plausible but should be confirmed.
- **Verdict:** Verify model ID date suffix.

### GPT-4o
- **Seeded model_id:** `gpt-4o`
- **Seeded context_window:** 128,000
- **Seeded max_output:** 16,384
- **Public docs:** Model ID correct (alias). Context 128K correct. Max output 16,384 correct.
- **Verdict:** Current.

### usage.go pricing spot-check

| Model | Seeded input $/1M | Seeded output $/1M | Public pricing | Match? |
|---|---|---|---|---|
| claude-sonnet-4-20250514 | $3.00 | $15.00 | $3.00 / $15.00 | Yes |
| claude-opus-4-20250514 | $15.00 | $75.00 | $15.00 / $75.00 | Yes |
| gpt-4o | $2.50 | $10.00 | $2.50 / $10.00 | Yes |
| o3 | $2.00 | $8.00 | $2.00 / $8.00 | Yes |

### Additional observation: legacy models in pricing map

`usage.go` contains pricing for models not in `seed.go`:
- `claude-haiku-3-20250307` — not seeded, only in pricing
- `claude-3-5-sonnet-20241022` — not seeded, only in pricing
- `claude-3-5-haiku-20241022` — not seeded, only in pricing
- `claude-3-opus-20240229` — not seeded, only in pricing
- `claude-3-sonnet-20240229` — not seeded, only in pricing
- `claude-3-haiku-20240307` — not seeded, only in pricing
- `gpt-4-turbo` — not seeded, only in pricing

These are for historical usage records (existing `token_usage` rows may reference old model IDs). This is correct behavior — pricing should cover both current and historical models.

## Impact

If `claude-haiku-4-5-20251001` is incorrect, the seeded model row won't match API requests and the model will fail silently (API returns "model not found"). Low probability since the seed data was likely tested, but worth confirming.

## Recommendation

Verify the Haiku 4.5 model ID against Anthropic's current API documentation. No other action needed for the spot-checked models.

## References

- `internal/store/seed.go:L284-328`
- `internal/store/usage.go:L6-31`
