-- +goose Up
-- CW-20260419-0018 (UAT c17) lowered the tool-result soft-truncate
-- default from 64 KiB to 2 KiB. Root cause: a 89 KiB clockwork_task_list
-- result sailed through the cache-pointer gate because the per-user
-- threshold was 65536, inflated the conversation slot, and blew the
-- per-minute rate budget (38,928 tokens vs 30,000 limit). Because the
-- compaction retry guard is one-shot per generation, the second budget
-- hit failed unrecoverably.
--
-- At 2 KiB most MCP list tools and file reads become pointers + a 2 KiB
-- preview. Tiny results (health checks, small lookups) still pass
-- through untouched.
--
-- Only rows with the exact old default are updated, so any explicit
-- user customization is preserved. Migration 011 was also updated so
-- fresh databases start at 2048.

UPDATE user_settings SET tool_result_soft_truncate_bytes = 2048
    WHERE tool_result_soft_truncate_bytes = 65536;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
