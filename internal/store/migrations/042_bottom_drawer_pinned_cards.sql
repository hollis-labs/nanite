-- +goose Up
-- C1 (CW-20260428-0012): pinned drawer cards.
-- Stores the pinned lifecycle state of bottom-chat-drawer cards. Transient
-- cards live FE-only and pinning persists the pointer here so it survives
-- reload and session restore.
--
-- card_type vocabulary v1: markdown, diff, image, scratchpad, artifact-mini,
-- agent-envelope. Forward-compat values are accepted and unknown types render
-- as placeholders FE-side.
--
-- content_ref is a typed pointer.
--   agent-envelope - envelope_id matches envelope_instances.id when present
--   artifact-mini  - artifact_id matches artifacts.id
--   markdown, diff, image - an opaque payload key the FE resolves
-- The interpretation lives FE-side and the backend just stores the string.
--
-- Cap is enforced at the API layer (10 pins per session). DB allows more for
-- forward-compat. The cap is a UI policy, not an invariant.
--
-- IMPORTANT: no semicolons inside comments (splitSQL naive-split limitation).
--
-- NOT to be confused with pinned_content (J11, migration 039) which is the
-- agent-facing context-pin feature. This table is a FE-only drawer-pin lifecycle.

CREATE TABLE IF NOT EXISTS bottom_drawer_pinned_cards (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    card_type   TEXT NOT NULL DEFAULT '',
    content_ref TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}',
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_bottom_drawer_pinned_cards_session
    ON bottom_drawer_pinned_cards (session_id, position ASC, created_at ASC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
