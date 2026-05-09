-- 030_compaction_disclosure_prompts.sql
-- CW-20260420-0025 (P8 CompactionContract Part A — disclosure prompts)
--
-- Seeds four disclosure-prompt templates (one per compaction mode: general, code,
-- plan, research). Each is injected into the system prompt for the FIRST chat
-- turn after a compaction event, anchored to the active CompactionMode. They
-- describe what the summarizer preserved vs. summarized, and point the LLM at
-- recovery affordances: the HandoffStash id (if a stash was written pre-compaction)
-- and the chat_search self-tool (CW-20260420-0026).
--
-- Variables interpolated by ComposePromptForAgent at runtime:
--   {{handoff_stash_id}}        — stash id or "(none)"
--   {{coverage_window_start}}   — turn id at start of compacted span or "(unknown)"
--   {{coverage_window_end}}     — turn id at end of compacted span or "(unknown)"
--   {{summary_token_count}}     — tokens in the summary
--   {{evicted_pointer_count}}   — count of evicted cache pointers
--   {{preserved_source_count}}  — count of preserved sources
--
-- D1 + D2 (LOCKED): one disclosure variant per CompactionMode, all four
-- referencing the same recovery affordances (stash_id, chat_search,
-- summary metadata).
--
-- Priority 15 places these AFTER chat-role-harness identity (priority 1) and
-- BEFORE workspace context (priority 20) — disclosure is identity-adjacent
-- grounding the LLM should read before applying workspace rules.
--
-- Templates are deliberately NOT auto-assigned to any agent. Runtime injection
-- (per session, gated on freshness of the latest compaction_events row) is in
-- internal/chat/context.go, not on the agent_prompt_templates JOIN path.
--
-- Token budget: each rendered template ≤ 300 tokens (≈ 1100 chars).
-- Idempotent: INSERT OR IGNORE on all four rows.

BEGIN;

INSERT OR IGNORE INTO prompt_templates
    (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
VALUES (
    'blt-compact-disclose-gen-001',
    'Compaction Disclosure (general)',
    'compaction-disclosure-general',
    'mode',
    '## Compaction Notice

This conversation was compacted just before your turn. An LLM-generated summary replaced the older messages. Treat the summary as a lossy paraphrase, not a transcript.

**Preserved:** gist of decisions, problems, and outcomes.
**Lost:** raw text of older turns, full tool inputs/outputs, exact numbers and quotes.

**Recovery:**
- **Handoff stash id:** `{{handoff_stash_id}}` — if non-`(none)`, holds decisions, open questions, file refs, ticket IDs captured pre-compaction. Read before answering about earlier-session state.
- **`chat_search`** `{query, scope?, limit?}` — search pre-compaction turns for specifics the summary glosses over. Use whenever the user references something the summary does not explicitly mention.
- **Summary metadata:** mode=general, window `{{coverage_window_start}}` → `{{coverage_window_end}}`, ≈ {{summary_token_count}} tokens, {{evicted_pointer_count}} cache pointer(s) evicted, {{preserved_source_count}} preserved source(s).

If the summary is silent on prior detail, search rather than guess.',
    '["handoff_stash_id","coverage_window_start","coverage_window_end","summary_token_count","evicted_pointer_count","preserved_source_count"]',
    15,
    1,
    datetime('now'),
    datetime('now')
);

INSERT OR IGNORE INTO prompt_templates
    (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
VALUES (
    'blt-compact-disclose-code-001',
    'Compaction Disclosure (code)',
    'compaction-disclosure-code',
    'mode',
    '## Compaction Notice (code session)

This coding conversation was compacted before your turn. A summary replaced the older messages.

**Preserved:** file paths, function/symbol names, errors, fixes applied, final state of recent edits.
**Lost:** verbatim diffs, full file contents, complete command output, tool-result bodies. Exact text of an earlier edit or stderr is not in the summary.

**Recovery:**
- **Handoff stash id:** `{{handoff_stash_id}}` — if non-`(none)`, holds decisions and active file refs from before compaction.
- **`chat_search`** `{query, scope?, limit?}` — recover exact pre-compaction text: error strings, command output, prior code blocks. Prefer search over guessing when the user mentions a specific symbol, path, or error.
- **Summary metadata:** mode=code, window `{{coverage_window_start}}` → `{{coverage_window_end}}`, ≈ {{summary_token_count}} tokens, {{evicted_pointer_count}} cache pointer(s) evicted, {{preserved_source_count}} preserved source(s).

When in doubt about exact code or output, search before re-running tools.',
    '["handoff_stash_id","coverage_window_start","coverage_window_end","summary_token_count","evicted_pointer_count","preserved_source_count"]',
    15,
    1,
    datetime('now'),
    datetime('now')
);

INSERT OR IGNORE INTO prompt_templates
    (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
VALUES (
    'blt-compact-disclose-plan-001',
    'Compaction Disclosure (plan)',
    'compaction-disclosure-plan',
    'mode',
    '## Compaction Notice (planning session)

This planning conversation was compacted before your turn. A summary replaced the older messages.

**Preserved:** decisions locked, alternatives considered, rationale, open questions, ticket IDs, committed next steps.
**Lost:** prose discussion behind each decision, full quotes, exploratory back-and-forth.

**Recovery:**
- **Handoff stash id:** `{{handoff_stash_id}}` — if non-`(none)`, holds `decisions_locked`, `open_questions`, `active_ticket_ids`. Read first when the user references a prior decision.
- **`chat_search`** `{query, scope?, limit?}` — recover exact wording of an earlier proposal, requirement, or counter-argument. Cite real quotes from search, not reconstructions.
- **Summary metadata:** mode=plan, window `{{coverage_window_start}}` → `{{coverage_window_end}}`, ≈ {{summary_token_count}} tokens, {{evicted_pointer_count}} cache pointer(s) evicted, {{preserved_source_count}} preserved source(s).

If a prior decision is being revisited, search for the original framing first.',
    '["handoff_stash_id","coverage_window_start","coverage_window_end","summary_token_count","evicted_pointer_count","preserved_source_count"]',
    15,
    1,
    datetime('now'),
    datetime('now')
);

INSERT OR IGNORE INTO prompt_templates
    (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
VALUES (
    'blt-compact-disclose-research-001',
    'Compaction Disclosure (research)',
    'compaction-disclosure-research',
    'mode',
    '## Compaction Notice (research session)

This research conversation was compacted before your turn. A summary replaced the older messages.

**Preserved:** findings discovered, sources consulted, citations, conclusions drawn, specific data points already extracted.
**Lost:** raw exploration prose, full source excerpts, side-investigations, data the summarizer did not flag as a finding. Numbers recalled "approximately" need re-verification.

**Recovery:**
- **Handoff stash id:** `{{handoff_stash_id}}` — if non-`(none)`, key sources and active references from pre-compaction live here.
- **`chat_search`** `{query, scope?, limit?}` — recover exact source text or data point behind a summarized finding. Re-verify quotes and figures by search before repeating them.
- **Summary metadata:** mode=research, window `{{coverage_window_start}}` → `{{coverage_window_end}}`, ≈ {{summary_token_count}} tokens, {{evicted_pointer_count}} cache pointer(s) evicted, {{preserved_source_count}} preserved source(s).

Cite from search results, not from the summary, when precision matters.',
    '["handoff_stash_id","coverage_window_start","coverage_window_end","summary_token_count","evicted_pointer_count","preserved_source_count"]',
    15,
    1,
    datetime('now'),
    datetime('now')
);

COMMIT;
