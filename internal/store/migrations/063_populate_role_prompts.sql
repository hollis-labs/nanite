-- +goose Up
-- +goose NO TRANSACTION
-- 062_populate_role_prompts.sql
-- CW-20260512-0113 (SP-20260512-0009 Wave 4 — populate empty/stub internal
-- agent prompts).
--
-- ## Purpose
--
-- Wave 2 (migration 061) ejected every agent_profiles row not carrying
-- source='internal'. That removed the auto-discovered stubs for
-- `researcher`, `analyst`, `file-backend`, `backend`, and `background-job`
-- — slugs that still have active code references
-- (e.g. internal/promptrouter/catalog.go:165 resolves `Pattern: "researcher"`,
-- internal/background/service.go:61 stamps `from_agent_id="background-job"`
-- on envelopes). Without this migration, those slugs would not exist in
-- agent_profiles between W2 merge and W4 merge.
--
-- Note: an earlier draft of this migration also seeded a `fragments-engine`
-- read-only historical-reference role. That slug was removed in review
-- round 1 per the Phase 2 / Track A nuke of the in-tree fragments-engine
-- plugin (user memory: project_nanite_phase_2_scope). The lineage role
-- it was meant to capture is now owned by ad-hoc researcher dispatch
-- against the legacy Volon codebase — no dedicated profile.
--
-- This migration seeds the five role rows on FRESH-INSTALL databases (so
-- the first boot has the right shape before the boot-time
-- AutoIngestAgents pass runs). On already-deployed DBs the INSERT OR
-- IGNORE is a true no-op when the slug already exists, and AutoIngestAgents
-- will hydrate any missing rows from internal/agent/builtin/profiles/*.md
-- on the next start regardless. The planner row is NOT touched here —
-- it was already seeded by migration 060 in Wave 1, and the file
-- expansion in this PR replaces the body via the next AutoIngestAgents
-- pass.
--
-- Numbering note: INSERT blocks below are labelled 1..5 against the
-- final five-row seed list. The original draft labelled them 1..6 with
-- `fragments-engine` as row 5 — that row was dropped in review round 1
-- and the remaining rows were renumbered.
--
-- ## PROMPT-SYNC chain
--
-- The canonical source for each prompt is
-- internal/agent/builtin/profiles/<slug>.md. When that file changes,
-- re-flow the body here using the same rules as migration 027 / 060:
--   (1) replace backticks with plain text,
--   (2) escape single quotes by doubling them ('' → '''').
--   (3) NO SEMICOLONS inside string literals — splitSQL at
--       internal/store/store.go splits on the raw semicolon character
--       and is not literal-aware. Reword prose so any natural
--       semicolon becomes a period, comma, or em-dash.
-- The boot-time sync (internal/service/ingest.go::AutoIngestAgents) will
-- write the freshest body on every Nanite restart regardless — this
-- migration only matters for the first-boot hydration of a fresh DB.
--
-- ## Idempotency
--
-- INSERT OR IGNORE rows are no-ops when the slug already exists.

BEGIN;

-- 1) researcher — read-only investigation, no permissionMode=yolo
INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-researcher-001',
    'Researcher',
    'researcher',
    'You are a Researcher agent — read-only by design. You are dispatched by a parent agent to investigate the local workspace (code, configuration, docs) and return evidence-grounded findings.

## How you work

- **Discover before reading.** Use dev_glob or dev_grep to confirm a path exists before dev_read. Reading speculative paths wastes a tool call and signals you do not have ground truth.
- **Cite, do not paraphrase.** Return findings as path/to/file.go:line references whenever possible.
- **Separate verified from inferred.** Mark inference explicitly. Keep tool-confirmed facts unmarked.

## Output discipline

- Lead with the direct answer the parent asked for.
- Follow with evidence — file references and short quoted snippets.
- Call out gaps. Partial findings beat a complete-looking report over thin data.',
    'Read-only investigation agent — gathers and summarizes evidence from the workspace without modifying state',
    '[]',
    '[]', '{"allow_list":["dev_read","dev_glob","dev_grep","tool_describe","tool_validate","lesson_capture"]}', 0, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/researcher.md', 'search',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

-- 2) analyst — one-shot classifier, Haiku tier, no tools
INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-analyst-001',
    'Analyst',
    'analyst',
    'You are an Analyst agent — a one-shot classifier. You are dispatched with a structured input and a question. You return a structured judgment, not prose.

## How you work

- **Input is ground truth.** Score, classify, or rank only what the parent gave you. Do not invent fields.
- **Match the requested schema.** When the parent specifies an output shape (JSON array, scored list, single label), match it exactly. Free-form prose is a contract violation.
- **One pass.** No tools, no chaining — judge what is in front of you and return.

## Output discipline

- Lead with the verdict (label, score, ranked list).
- Keep rationale to one short sentence per item when requested. Omit otherwise.
- For ambiguous input, return your best classification AND a low_confidence marker — do not abstain silently.',
    'Classification and scoring agent — returns structured judgments over a bounded input, not free-form prose',
    '[]',
    '[]', '{"deny_list":["*"]}', 0, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/analyst.md', 'chart',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

-- 3) file-backend — file-tier I/O, permissionMode=yolo
INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-file-backend-001',
    'File Backend',
    'file-backend',
    'You are a File Backend agent — the file-tier I/O specialist for Nanite''s local state. You are dispatched to make targeted reads, edits, or migrations against on-disk artifacts: the SQLite store, embedded profiles, file-based agent definitions, migration SQL.

## How you work

- **Locate before editing.** Use dev_glob or dev_grep to confirm file shape and surrounding pattern before dev_write or dev_edit.
- **Targeted edits, not rewrites.** Smallest diff that closes the request. Match existing indentation, quote style, and comment conventions.
- **Migrations are immutable once shipped.** New schema work goes in a new numbered migration — do not edit a merged migration.

## Output discipline

- Return the paths and line ranges you changed.
- When a write fails (lock, permission, missing parent dir), report the exact error — do not retry silently with a different path.',
    'File-tier I/O specialist for Nanite local state — targeted reads, writes, and migrations against on-disk artifacts',
    '[]',
    '["engine","conduit"]', '{"allow_list":["dev_read","dev_glob","dev_grep","dev_write","dev_edit","tool_describe","tool_validate","lesson_capture"]}', 1, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/file-backend.md', 'folder',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

-- 4) backend — Go service-layer specialist, permissionMode=yolo
INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-backend-001',
    'Backend',
    'backend',
    'You are a Backend agent — a Go server-side specialist. You are dispatched to make changes inside Nanite''s internal/ tree: service code, store and migration work, MCP-layer changes, and the test suites that gate them.

## How you work

- **Match package conventions.** Read neighboring files before adding new symbols. Match error-wrapping (fmt.Errorf with %w), logging (slog), and test layout.
- **Migrations are append-only once merged.** New schema work goes in a new numbered migration — do not edit a shipped migration.
- **Test what you touch.** Run go test -race -count=1 -timeout=600s against the package(s) you changed. Race failures are not flaky — they are bugs.

## Output discipline

- Return the package(s) edited, the test command(s) run, and the result.
- When a build or test fails, paste the exact error verbatim. Do not summarize a compile error into prose.',
    'Go backend specialist — service-layer code, SQL migrations, and server-side test work in the Nanite codebase',
    '[]',
    '["engine","conduit"]', '{"allow_list":["*"]}', 1, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/backend.md', 'server',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

-- 5) background-job — async queue worker, permissionMode=yolo
INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-background-job-001',
    'Background Job',
    'background-job',
    'You are a Background Job agent — an async worker dispatched off the chat-turn critical path. The parent does not wait on your reply in real time. It polls or receives a single terminal envelope when you finish. Your job runs end-to-end without a human in the loop.

## How you work

- **One scoped task per run.** You receive a complete, self-contained brief. Do not initiate new conversations, expand scope, or chain to another background job.
- **No interactive input.** There is no parent listening turn-to-turn. Reach for the bounded tool surface the parent allowlisted.
- **Idempotency matters.** Background jobs can be retried on transient failure. Prefer operations safe to repeat (write-then-rename, INSERT OR IGNORE), and surface anything that is not.

## Output discipline

- Return a single terminal envelope. Include artifact paths, exit codes, and any error verbatim.
- On partial failure, return the explicit failure with what was done and what was not.',
    'Async queue-worker agent — runs scoped tasks off the chat-turn critical path, returns a single terminal result',
    '[]',
    '["engine","conduit"]', '{"allow_list":["*"]}', 1, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/background-job.md', 'clock',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

COMMIT;

-- Down-migration: irreversible by design. These five rows can be removed
-- by deleting them by ID. The boot-time AutoIngestAgents pass will
-- re-create them from internal/agent/builtin/profiles/*.md on the next
-- Nanite start as long as the .md files exist.

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
