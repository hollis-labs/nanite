# Reflex Authoring Guide — RETIRED

**Status:** retired. `internal/promptrouter` (the package this doc described authoring conventions for) no longer exists — deleted in full by `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`. This page is kept as a short pointer for anyone who lands here from an old link, commit message, or CW ticket; it is not maintained content.

## Authoring a new dispatch-to-agent reflex today

Reflexes are DB-authoritative. There is no YAML file to write and no Go literal to append to. Use the `agent_reflexes` CRUD API:

```
POST /api/agents/{id}/reflexes
{
  "name": "my-reflex",
  "trigger_kind": "predicate",
  "trigger_spec": "{\"kind\":\"user_regex_window\",\"window\":1,\"pattern\":\"(?i)(phrase one|phrase two)\"}",
  "action_kind": "dispatch_to_agent",
  "action_spec": "{\"agent_slug\":\"worker\",\"confidence\":0.5,\"reason\":\"why this fires\"}",
  "priority": 30
}
```

- `trigger_spec`'s `user_regex_window` predicate (`internal/agent/reflexes/evaluator.go`) is the phrase-match equivalent of the old `promptrouter.Triggers.UserPhraseAnyOf` — a case-insensitive regex over the current turn's raw user text. `scope_tier`/`execution_pattern` predicates (string-equality, `op` ∈ `{=, !=}`) are the equivalent of the old `ScopeTierHint`/`ExecutionPatternHint` guards; combine with an `AND` node the same way `internal/agent/reflexes/seeds.go`'s migrated entries do.
- `action_spec` for `dispatch_to_agent` requires `agent_slug` (non-empty); `confidence`/`reason` are optional, audit-only fields — see `internal/store/agent_reflexes.go`'s `ReflexActionDispatchToAgent` doc comment for the full shape.
- `priority` (higher wins) determines evaluation order among all `dispatch_to_agent` rows active for an agent — see `internal/service/chat_reflex_dispatch.go`'s header comment for the exact "first fire wins" semantics.
- Class-bound (operator-authored, applies to every agent of a class) vs. agent-specific reflexes both go through the same CRUD path; see `internal/agent/reflexes/seeds.go` for worked examples of both shapes.

## For historical reference

`internal/agent/reflexes/seeds.go`'s "migrated from internal/promptrouter" section carries forward the design rationale (why each phrase list, why each priority, why each tier-hint translation) for the 6 entries migrated from the old catalog — read that file's comments if you're trying to understand why a particular migrated phrase/priority/target combination was chosen.

See `docs/engineering/architecture/03-steering.md` for the architecture this authoring convention serves.
