# Deep Review: entities-tags-relational

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `entities-tags-relational`
**Interpretation:** Subsystem review of entity/tag relational correctness across `internal/store/`, `internal/api/`, and `internal/service/`. Focus on how tags are stored (per-entity vs per-object), FK integrity on entity deletion paths, tag CRUD completeness, and schema-code consistency.

**Files read in full:**
- `internal/store/migrations/001_schema.sql` -- squashed schema, all tag columns
- `internal/store/agents.go` -- AgentProfile struct, CRUD, DeleteAgent
- `internal/store/sessions.go` -- Session struct, CRUD, UpdateSessionTags, ForkSession
- `internal/store/bookmarks.go` -- Bookmark struct, CRUD
- `internal/store/workspaces.go` -- Workspace/Project CRUD, DeleteWorkspace, DeleteProject
- `internal/store/agent_projects.go` -- agent-project join table CRUD
- `internal/store/agents_hash.go` -- agent content hash (tags excluded from hash)
- `internal/store/prompt_templates.go` -- ListPromptTemplatesForAgent (cross-ref scan mismatch)
- `internal/api/agents.go` -- agent create/update handlers, tag passthrough
- `internal/api/sessions.go` -- session create/update/delete handlers
- `internal/api/types.go` -- request types, tag fields
- `internal/api/memories.go` -- memory tag filtering (Conduit-backed, not SQLite)
- `internal/agentvalidation/validation.go` -- agent tag validation
- `internal/service/chat_generate.go:L1080-1121` -- autoTags function
- `internal/service/store.go` -- store interfaces (UpdateSessionTags, etc.)
- `internal/plugin/builtin/adapter-nanite-native/plugin.go:L148-161` -- adapter tag sync
- `internal/plugin/builtin/adapter-claude/plugin.go:L450-453` -- Claude adapter tag rendering

**Sampled:**
- `internal/store/session_overrides.go` -- checked for tag references (none)
- `internal/store/execution_metrics.go` -- "autoTags" call type reference
- `internal/agent/override/merge.go` -- agent override Tags field

**Skipped:** Test files (not primary scope). Frontend tag rendering (out of scope for backend review).

## Methodology

**Categories applied:**
- FK integrity -- all entity deletion paths checked for orphan risk
- Tag CRUD completeness -- create, read, update, delete for each entity's tags
- Schema-code consistency -- column definitions vs. Go struct fields, INSERT/SELECT coverage
- Input validation -- tag format validation at trust boundaries
- Querying patterns -- N+1, full-table scan risk on tag lookups

**Categories deferred:**
- Concurrency correctness -- no goroutines or mutexes in the tag-related store code. `autoTags` runs in a goroutine but was reviewed for correctness, not concurrency.
- Standards and tooling -- covered by `whole-repo-tooling-and-tests-sweep` (2026-04-11).

**Tools run:** None (narrow subsystem scope; tooling deferred to existing sweep audits).

**Cross-audit grounding:** Read `docs/audits/2026-04-11-store-and-migrations/index.md`. Finding 01 (DeleteAgent FK disable) is cross-referenced but not re-flagged. The FK disable pattern is noted as context for finding 01 in this audit (missing `agent_projects` cleanup).

## Findings

### By severity

**Critical (0)**
- _none_

**High (2)**
- [01 -- DeleteAgent misses agent_projects cleanup](01-high-delete-agent-misses-agent-projects.md)
- [02 -- DeleteWorkspace and DeleteProject orphan child records](02-high-delete-workspace-project-orphans-children.md)

**Medium (3)**
- [03 -- Session tags not set during CreateSession](03-medium-session-tags-not-set-on-create.md)
- [04 -- Bookmark tags are write-only with no update or query-by-tag](04-medium-bookmark-tags-write-only.md)
- [05 -- No tag validation for session and bookmark tags](05-medium-no-tag-validation-sessions-bookmarks.md)

**Low (1)**
- [06 -- No indexes on tag columns](06-low-no-tag-indexes.md)

**Info (1)**
- [07 -- Tag model design observations and praise](07-info-tag-model-design-observations.md)

### By topic

**FK integrity / Entity deletion**
- [01 -- DeleteAgent misses agent_projects cleanup](01-high-delete-agent-misses-agent-projects.md)
- [02 -- DeleteWorkspace/DeleteProject orphan children](02-high-delete-workspace-project-orphans-children.md)

**Tag CRUD**
- [03 -- Session tags not written on create, lost on fork](03-medium-session-tags-not-set-on-create.md)
- [04 -- Bookmark tags dead feature](04-medium-bookmark-tags-write-only.md)

**Input validation**
- [05 -- Session/bookmark tags accept arbitrary strings](05-medium-no-tag-validation-sessions-bookmarks.md)

**Querying patterns**
- [06 -- No tag indexes](06-low-no-tag-indexes.md)

**Architecture**
- [07 -- Per-entity JSON array design is consistent and appropriate](07-info-tag-model-design-observations.md)

## Recommended next steps

1. **Fix finding 01.** Add `agent_projects` and `session_agent_overrides` to `DeleteAgent`'s cleanup list. One-line fix.
2. **Fix finding 02.** Add cascading child cleanup to `DeleteWorkspace` and `DeleteProject`. Consider whether sessions should be archived or deleted on workspace deletion.
3. **Fix finding 03.** Add `tags` column to `CreateSession`'s INSERT. Ensures `ForkSession` correctly preserves source tags and the returned struct is consistent.
4. **Decide on finding 04.** Either wire up bookmark tags end-to-end (API request type, update method, query filter) or remove the dead column.
5. **Add tag validation helper (finding 05)** for session tags, callable from `UpdateSessionTags`.
6. **Defer finding 06** until tag-based query features are implemented.

## Known issues skipped

- **DeleteAgent FK disable** (`store-and-migrations` audit finding 01) -- the `PRAGMA foreign_keys=OFF` bug in `DeleteAgent` is already tracked. This audit's finding 01 is a distinct, additional issue (missing table in cleanup list) that compounds with it.
- No other pre-existing known issues from `docs/beta-known-issues.md` or the reviewer-backend context apply to this scope.

## Noticed but out of scope

- **Memory service tags (Conduit-backed).** `internal/api/memories.go` implements tag filtering via the Conduit backend, not SQLite. The memory tag system is architecturally separate from the entity tags reviewed here. Tags on memories are `[]string` in Go (not JSON TEXT columns). A review of the Conduit tag implementation is a separate scope: `memory-tag-correctness`.
- **Plugin catalog tags.** `internal/plugin/catalog.go:L30` defines `Tags []string` on `CatalogEntry`. These are catalog metadata, not persisted in the store. Not in scope but noted for completeness.
- **Agent override merge includes tags.** `internal/agent/override/merge.go:L20` includes `Tags []string` in the override struct. The merge semantics for tags (append vs. replace) were not verified. Suggested scope: `agent-override-merge`.
