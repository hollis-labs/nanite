-- Cut the `templates` table (output-formatting-snippet mechanism).
--
-- TASKS.md Phase 0 Cuts, item 30: real CRUD/REST surface (/api/templates,
-- /api/templates/{name}/apply) but no confirmed real consumer (seed
-- function never called, no frontend caller, no other Go call site).
-- See TASKS/phase-0/30-cut-templates-table.md for the full audit.
--
-- Distinct from prompt_templates/agent_prompt_templates (system-prompt
-- fragments, a separate table cut separately by 29-cut-prompt-templates) --
-- templates is a standalone Go-text/template-style output-snippet table
-- with no relationship to the prompt-composition system. Do not confuse
-- the two while reading this file.

-- +goose Up
DROP TABLE IF EXISTS templates;

-- +goose Down
CREATE TABLE IF NOT EXISTS templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    template TEXT NOT NULL,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
