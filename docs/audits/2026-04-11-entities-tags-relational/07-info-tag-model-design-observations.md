# [Info] Tag model design observations and praise

**Scope:** internal/store/, internal/api/, internal/service/
**Topic:** Tag model correctness / Architecture
**Date:** 2026-04-11

## Problem

No problem. This finding documents the tag model design and notes positive patterns.

## Evidence

### Tag storage model

Tags are stored as **per-entity JSON arrays** in TEXT columns. There is no shared/normalized tag table. Each entity type independently manages its own tags:

| Entity | Column | Default | Validation | Writer |
|--------|--------|---------|------------|--------|
| `agent_profiles` | `tags TEXT NOT NULL DEFAULT '[]'` | `'[]'` | `agentvalidation.ValidateAgentConfig` | API create/update, nanite-native adapter sync |
| `sessions` | `tags TEXT DEFAULT '[]'` | `'[]'` | None at store level; autoTags validates | `autoTags()` in chat_generate.go |
| `bookmarks` | `tags TEXT DEFAULT '[]'` | `'[]'` | None | None (dead feature) |

This is a consistent, intentional design: tags are metadata on the entity, not a shared taxonomy. The JSON array approach is appropriate for a single-user application where tags are display labels, not a queryable dimension.

### Positive patterns

1. **Agent tags flow correctly through the adapter sync pipeline.** The nanite-native adapter (`internal/plugin/builtin/adapter-nanite-native/plugin.go:L148-161`) creates agent profiles with tags derived from the agent's roles:
   ```go
   tags := []string{"nanite"}
   tags = append(tags, agentDef.Roles...)
   tagsJSON, _ := json.Marshal(tags)
   ```
   This is clean and produces valid JSON.

2. **Agent tag validation is thorough.** `agentvalidation.ValidateAgentConfig` (`validation.go:L141-147`) correctly validates tags as a JSON string array. This catches malformed input at the API boundary.

3. **Auto-tag generation is well-guarded.** `autoTags()` (`chat_generate.go:L1080-1121`) validates the LLM response, bounds the tag count (0-5), and only writes if the JSON parses cleanly. Silent failure on bad LLM output is the right behavior.

4. **The Claude adapter uses agent tags for CLAUDE.md rendering.** `internal/plugin/builtin/adapter-claude/plugin.go:L450-453` reads agent tags and formats them into the managed section. This confirms tags are consumed downstream.

5. **`ForkSession` preserves tags in the Go struct.** The fork logic copies `src.Tags` to the new session struct. (Note: finding 03 documents that the tags are not actually written to the DB due to a missing column in `CreateSession`'s INSERT.)

## Impact

The per-entity JSON array design is appropriate for the current workload (single-user, display-only tags). No performance or correctness issues stem from this choice at current scale.

## Recommendation

No action required. The design is consistent and intentional.

## References

- `internal/store/migrations/001_schema.sql:L52,L100,L146` -- tag columns.
- `internal/agentvalidation/validation.go:L141-147` -- agent tag validation.
- `internal/service/chat_generate.go:L1080-1121` -- autoTags.
- `internal/plugin/builtin/adapter-nanite-native/plugin.go:L148-161` -- adapter tag sync.
- `internal/plugin/builtin/adapter-claude/plugin.go:L450-453` -- Claude adapter tag rendering.
