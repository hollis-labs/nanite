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
    COALESCE(NULLIF(default_provider, ''), 'anthropic'),
    COALESCE(NULLIF(default_model, ''), 'claude-sonnet-4'),
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
