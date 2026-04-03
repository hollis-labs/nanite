-- Phase 4: Turn snapshots for chat loop debugging.
ALTER TABLE execution_metrics ADD COLUMN debug_snapshots TEXT DEFAULT '';
