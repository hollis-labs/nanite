-- +goose Up
-- 084_seed_legacy_durable_agent_instances.sql
-- Compatibility seed for durable-agent profiles created before the Phase 5
-- durable_agent_instances control-plane table existed.
--
-- Older Agridd installs have durable identities as agent_profiles tagged with
-- "durable-agent". The admin surface lists configured durable instances, so
-- seed one instance per tagged legacy profile when no matching instance slug
-- already exists.

INSERT OR IGNORE INTO durable_agent_instances (
    id,
    name,
    slug,
    profile_id,
    lifecycle_class,
    provider,
    model,
    runtime_kind,
    launch_source_type,
    launch_source_id,
    work_root,
    status,
    current_session_id,
    failure_reason,
    metadata_json,
    created_at,
    updated_at,
    archived_at
)
SELECT
    'legacy-profile-' || id,
    name,
    slug,
    id,
    CASE
        WHEN class IN ('advisor', 'process', 'template') THEN class
        ELSE 'advisor'
    END,
    -- CW-20260526-0003: Provider/Model left blank when the profile doesn't
    -- set one. Chat-time resolution via store.ResolveProviderAndModel walks
    -- user_settings → providers.default_model so a later operator edit is
    -- honored without re-seeding. The previous bare 'claude-sonnet-4'
    -- fallback below produced 404s at Anthropic (the bare alias is invalid).
    COALESCE(NULLIF(default_provider, ''), ''),
    COALESCE(NULLIF(default_model, ''), ''),
    'api',
    CASE
        WHEN class = 'process' THEN 'process_tick'
        WHEN class = 'template' THEN 'task_template_run'
        ELSE 'durable_advisor'
    END,
    id,
    '',
    CASE
        WHEN default_state IN ('sleeping', 'active') THEN default_state
        ELSE 'sleeping'
    END,
    '',
    '',
    '{"seeded_from":"agent_profiles","legacy_profile":true}',
    COALESCE(NULLIF(created_at, ''), datetime('now')),
    COALESCE(NULLIF(updated_at, ''), datetime('now')),
    NULL
FROM agent_profiles
WHERE status = 'active'
  AND (
    durable = 1
    OR instr(tags, '"durable-agent"') > 0
  );

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
