-- Phase 3 S2a (2026-04-15): user-configurable embedding provider.
--
-- Embedders are off by default (privacy-first). When embedding_mode = 'disabled'
-- or embedding_provider = '', no embedder is wired into the memory store and
-- similarity recall is unavailable. Setting embedding_mode = 'explicit' with a
-- supported provider activates embedding (subject to credential / reachability
-- checks done at container wiring time).
ALTER TABLE user_settings ADD COLUMN embedding_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN embedding_model TEXT NOT NULL DEFAULT '';
ALTER TABLE user_settings ADD COLUMN embedding_mode TEXT NOT NULL DEFAULT 'disabled';
