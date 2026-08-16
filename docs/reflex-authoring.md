# Reflex Authoring Guide

**Package:** `internal/reflex`
**Content layer:** `internal/reflex/catalog.go` (built-ins), `~/.nanite/reflexes/*.yaml` (user overrides)
**Consumed by:** playbook runtime dispatcher (`internal/reflex/dispatcher.go`)
**Reference:** `docs/agent-reflex-catalog.md` — canonical schema, v1 reflex set, test cases

> **Not to be confused with:** `internal/agent/driftguard` (formerly `internal/agent/reflexes`), the unrelated FU-30 session-drift monitor. See the note in `docs/agent-reflex-catalog.md` — disambiguated in CW-20260816-0062.

---

## What is a reflex?

A reflex is a deterministic, phrase-match rule that maps user input to an agent behavior selection. The reflex matcher runs before `dispatch.AssignRole` on every `nanite_execute_task` call. A matched reflex overrides the M1 classifier's ScopeTier and ExecutionPattern hints and selects a specific agent profile slug.

Reflexes are the fast first-pass of the E1 playbook system. They use no LLM calls — pure substring matching.

---

## Authoring user overrides

Create YAML files in `~/.nanite/reflexes/`. Each file defines one reflex using the schema below.

```yaml
id: my-reflex-id
triggers:
  user_phrase_any_of:
    - "phrase one"
    - "phrase two"
  scope_tier_hint: medium   # optional guard (trivial|small|medium|large|open)
  execution_pattern_hint: "" # optional guard (inline|subagent|background)
resolves_to:
  pattern: worker            # pattern slug from docs/agent-pattern-catalog.md
  role: worker               # dispatch role (chat|worker|planner)
  profile: worker            # agent profile slug to spawn
side_effects:
  mode_signal: execute       # mode signal emitted to the session bus (optional)
  dispatch_via: executeTask  # how to run the agent (executeTask|executeBackground)
priority: 55                 # must be >= 50 to reliably beat built-in reflexes
```

### Field reference

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Unique slug. Convention: `<pattern>-<keyword>`. |
| `triggers.user_phrase_any_of` | list of strings | yes | Lower-case substrings. Match is case-insensitive. Any one entry fires the reflex. |
| `triggers.scope_tier_hint` | string | no | When set, reflex only fires when M1 tier is this value or broader. |
| `triggers.execution_pattern_hint` | string | no | When set, reflex only fires when M1 pattern exactly matches. |
| `resolves_to.pattern` | string | yes | Pattern slug from `docs/agent-pattern-catalog.md`. |
| `resolves_to.role` | string | yes | One of: `chat`, `worker`, `planner`. |
| `resolves_to.profile` | string | no | Agent profile slug to spawn. Empty uses AssignRole's default slug. |
| `side_effects.mode_signal` | string | no | One of: `planning`, `research`, `review`, `document`, `execute`. |
| `side_effects.dispatch_via` | string | no | `executeTask` (sync) or `executeBackground` (async). |
| `priority` | int | no | Default 0. Range 0–100. Use >= 50 to beat all built-in reflexes (max builtin: 25). |

### Priority rules

- Higher priority wins. First registered in descending-priority order wins on ties.
- Built-in reflexes range from priority 10 (worker-execute, strategist-mention) to 25 (background-long-task).
- **Set `priority >= 50` for user overrides to reliably win.**
- At equal priority, the entry that appears first in the sorted merged slice wins.

---

## Scope tier hint semantics

`scope_tier_hint` is a **minimum** tier guard. A reflex with `scope_tier_hint: medium` fires when the M1 classifier returns `medium`, `large`, or `open` — not when it returns `trivial` or `small`.

Tier ordering (smallest to largest): `trivial < small < medium < large < open`.

---

## Execution pattern hint semantics

`execution_pattern_hint` is an **exact match** guard. A reflex with `execution_pattern_hint: background` fires ONLY when M1 returns `PatternBackground`. This is different from scope tier (which is "at least").

---

## DispatchVia → Mode mapping

| `dispatch_via` value | Mode forwarded to dispatch | AssignRole effect |
|---|---|---|
| `executeTask` | `sync` | Worker or Planner, synchronous |
| `executeBackground` | `async` | Worker, asynchronous (PatternBackground path) |
| (omitted) | uses AssignRole default | no override |

---

## Loader behavior

On every startup, the harness:
1. Loads built-in reflexes from `reflex.BuiltinReflexes()`.
2. Globs `~/.nanite/reflexes/*.yaml` and parses each file.
3. Merges both sets, sorted descending by priority.
4. Wires the merged set into `SelfToolsTransport.ReflexSet`.

Invalid YAML files are logged to stderr and skipped — one bad file does not block startup.

---

## Integration path (for developers)

```
nanite_execute_task tool call
    │
    ▼
callExecuteTask (internal/mcp/self_tools_dispatch.go)
    │
    ├── classify.Classify(message) → m1Tier, m1Pattern
    ├── reflex.Match(message, m1Tier, m1Pattern, ReflexSet) → ReflexMatch, bool
    │       (phrase-match loop over merged reflex set)
    │
    ├── on match: build dispatch.ReflexHints; log to playbook_match_log
    │
    ▼
dispatch.ExecuteTask(ctx, spawner, wrapper, ExecuteTaskArgs{ReflexHints: ...})
    │
    ├── apply ReflexHints overrides to tier/pattern/slug/mode
    ├── dispatch.AssignRole(tier, pattern) → RoleAssignment
    │
    ▼
spawner.Spawn(...)
```

The dispatcher (`internal/reflex/dispatcher.go`) also exposes `AssignRoleWithReflex` for callers that want a single-call integration without going through `ExecuteTask`.

---

## Testing user overrides locally

```bash
# Create a test override in the user reflexes dir.
mkdir -p ~/.nanite/reflexes
cat > ~/.nanite/reflexes/my-override.yaml <<'EOF'
id: my-override
triggers:
  user_phrase_any_of:
    - "my custom phrase"
resolves_to:
  pattern: planner
  role: planner
  profile: planner
side_effects:
  mode_signal: planning
  dispatch_via: executeTask
priority: 60
EOF

# Run the Go tests to confirm matcher picks it up.
go test ./internal/reflex/... -v -run TestLoader
```

---

## See also

- `docs/agent-reflex-catalog.md` — canonical v1 reflex set with rationale
- `docs/agent-pattern-catalog.md` — pattern slug definitions
- `internal/reflex/catalog.go` — Go literal built-in set
- `internal/reflex/matcher.go` — matcher implementation
- `internal/reflex/dispatcher.go` — dispatch integration + MergeReflexes
- `internal/reflex/loader.go` — YAML loader
- `internal/store/migrations/032_playbook_match_log.sql` — match log table
