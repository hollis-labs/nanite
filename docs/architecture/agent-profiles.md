# Operator agent profile edits and history

Retained operator profiles live in `agent_profiles`. Native agent API views
use verified file definitions and a separate lifecycle; editing a retained
profile does not rewrite or grant authority to a native definition.

`GET /api/agents` returns an array. `GET /api/agents/{id}` returns an object
with an `agent` member. Neither response is an update request. Send a bare
object containing supported editable fields to `PUT /api/agents/{id}`:

```json
{"description":"Updated description","revision":"current opaque revision"}
```

Omitted fields survive. An empty string explicitly clears an optional string
or assignment; `name`, `slug` and `system_prompt` must stay non-empty. Supplied
nulls, duplicate or unsupported keys, wrapped bodies, arrays, empty edits and
trailing JSON are refused. JSON-bearing strings use their documented array or
object shapes. `UpdateAgentRequest`, in `internal/api/types.go`, defines the
editable wire fields. Identity, source and import provenance are not writable
request fields.

The optional `revision` precondition covers the complete stored row, including
assignments, rather than the prompt content hash. A stale precondition returns
409. Even without a caller-supplied token, the service checks the revision read
before applying a partial edit, so a concurrent mutation requires a fresh read
instead of overwriting fields from a stale profile.

Database triggers record row inserts and updates transactionally in
`agent_profile_revisions`, including imports, boot sync, direct store writers,
self-tools, builders and registry registration. A failed history insert aborts
the row write. A configuration transaction applies assignments before its
profile write and commits profile, history and accompanying seeds together.
History records individual row writes; a command that also changes a trust
tier may produce another snapshot. The snapshot contains profile data and
assignments, not capability children or grant tables.

Existing rows receive a pre-edit baseline when first mutated after migration;
there is no reconstruction of writes before history recording was installed.
History survives profile deletion, but the restore route requires an existing
target and cannot recreate a deleted agent. No pruning policy is applied by
these routes. Migration snapshot expressions enumerate the persisted profile
fields; a future schema change must also reconcile the snapshot projection.

`GET /api/agents/{id}/revisions?limit=50&offset=0` lists snapshots newest first.
The limit is 1–100, and offset is non-negative. Offset pages can shift if new
writes arrive between requests. Each item includes its opaque ID, sequence,
operation, timestamp and profile. The sequence orders writes sharing a
timestamp.

Restore an item with
`POST /api/agents/{id}/revisions/{revisionId}/restore`, sending the current
revision as the bare request body shown above. The result carries the agent,
`restore_scope: "partial_profile_and_assignments"`, `restored_from` and
`grants_restored: false`.

`AgentConfigService.RestoreRevision` restores editable configuration and
role/consumer/model plus protocol/transport assignments. It preserves the
current ID, provenance, plugin ownership, registry metadata, Tether identity
and trust tier. System, plugin and externally owned targets are refused.
Missing historical assignment references are refused rather than recreated.
Restoration never replays role-tool seeds, tool/dispatch/skill grants,
procedures, schedules or other children. Approved grant changes use the
separate capability APIs. Ordinary profile updates also leave current tool
grants alone; a retained role declaration cannot re-grant a revoked tool.
The edit/restore transaction also records the independent legacy-backfill
marker when absent, preventing a later boot from deriving grants from those
declarations.

A successful partial restore is a new revision marked `restore_partial`.
Profile, assignments and that history annotation roll back together if any
stage fails. The stored prompt hash/version is recomputed for the new write;
restoration does not rewind identity or the current revision token.
