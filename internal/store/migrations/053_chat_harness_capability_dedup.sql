-- 053_chat_harness_capability_dedup.sql
-- Glass-7 (CW-20260502-0016, SP-20260502-0001) — slot trim of SlotAgent.
--
-- Removes two Capability-section bullets that Phase A''s own architecture
-- (docs/architecture/agent-context-architecture.md) intended to relocate
-- but were preserved during the slim out of caution:
--
--   1. "Tool descriptions carry their own usage guidance..." — meta-
--      statement made tautological by the architecture: tool descriptions
--      ARE the authoritative per-tool surface, and the agent encounters
--      them when considering a tool. Stating it inside the static prompt
--      is dedup with the architecture itself.
--
--   2. "When a tool result carries a repair_note..." — Phase A''s commit
--      message (7f5917b) explicitly says ""Recover with awareness" →
--      nanite_remember description". That relocation is in place
--      (see internal/mcp/self_tools_remember.go:29 — the "When to use"
--      section of the tool description owns the repair_note guidance).
--      The system-prompt bullet is leftover redundant content.
--
-- Migration 027 carries the canonical fresh-install seed, re-flowed from
-- internal/agent/builtin/default.md per the PROMPT-SYNC rule. This file
-- is the in-place UPDATE for databases past 051 (Phase A''s slim) — they
-- need the two bullets removed. Idempotent guard: skip rows that have
-- already been dedup''d (NOT LIKE the relocated bullet text).
--
-- Cuts: ~72 tokens, 557 → ~485 (~13% reduction). Below the boot prompt''s
-- ≥30% target, but the architecture doc''s metrics show the prompt is
-- already in target range — aggressive cuts here have a track record of
-- damaging cognition (c107 → c117). Conservative dedup-only cut, with
-- the architecture''s own logic as load-bearing rationale.
--
-- IMPORTANT: no semicolons inside comments. Bare UPDATE (no BEGIN/COMMIT),
-- so the literal SQL terminator inside a string literal would be
-- interpreted as a statement boundary by splitSQL. Use REPLACE on the
-- exact bullet block (header line + the two bullets + trailing blank
-- line) so the substitution is deterministic.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- You have **meta-tools** for discovery (nanite_tool_describe), pre-flight validation (nanite_validate), and learning capture (nanite_remember). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.
- Tool descriptions carry their own usage guidance and examples. Read them when planning a call — they are authoritative.
- When a tool result carries a repair_note, the harness already reshaped your input so the call could succeed. Read the result as authoritative, and optionally capture the lesson.',
        '- You have **meta-tools** for discovery (nanite_tool_describe), pre-flight validation (nanite_validate), and learning capture (nanite_remember). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%Tool descriptions carry their own usage guidance%';
