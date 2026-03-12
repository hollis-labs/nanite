---
version: 1
type: bootstrap
iteration: 1
updated_at: 2026-03-11T00:00:00Z
---

# Mentat Bootstrap

## Current state
- Iteration 1 (post-consolidation)
- Status: Unified project created from mentat-chat clone. Migration in progress via MMA.
- Tasks: Check Volon with project_id="mentat"
- Boot profiles: meta-agent, worker

## What happened
- mentat-new created as clone of mentat-chat (full git history preserved)
- Behavior rules, tool-first architecture, tool-broker design docs migrated
- Project portfolio docs stored in Cortex (app/mentat/projects namespace)
- Boot profiles: meta-agent added, worker updated for project_id="mentat"
- PCC and boot system migrated from mentat CLI patterns

## Next steps
- Remaining MMA migration tasks (MC-005 through MC-021)
- Once migration complete: rename mentat-new → mentat
