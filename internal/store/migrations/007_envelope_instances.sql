-- 007_envelope_instances.sql
-- Server-side identity for rendered envelopes so typed responses can correlate
-- back to their source emission. See plans/phase-3-s5-envelope-typed-responses.md §T2.

CREATE TABLE IF NOT EXISTS envelope_instances (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions(id),
    envelope_type   TEXT NOT NULL,
    envelope_json   TEXT NOT NULL,
    emitted_at      TEXT NOT NULL,
    responded_at    TEXT,
    response_status TEXT,
    response_json   TEXT
);

CREATE INDEX IF NOT EXISTS idx_envelope_instances_session_time
    ON envelope_instances(session_id, emitted_at);
