-- +goose Up
-- 165_execution_metrics_harness_profile.sql
--
-- Record which harness profile a turn ran under, so an A/B result stays
-- attributable (D-33). profile_digest is a sha256 over the profile's whole
-- definition, so an in-place edit of a profile file is distinguishable.
-- effective_limits_json is the resolved values with the layer that supplied
-- each one. Empty for rows written before this column existed and for utility
-- calls, which run no chat loop.
--
-- ADD COLUMN only: a running process that does not name these columns is
-- unaffected.

ALTER TABLE execution_metrics ADD COLUMN profile_name TEXT NOT NULL DEFAULT '';
ALTER TABLE execution_metrics ADD COLUMN profile_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE execution_metrics ADD COLUMN effective_limits_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE execution_metrics DROP COLUMN effective_limits_json;
ALTER TABLE execution_metrics DROP COLUMN profile_digest;
ALTER TABLE execution_metrics DROP COLUMN profile_name;
