# Tool Naming Convention

**Status:** Active  
**Established:** 2026-04-26 (CW-20260426-0012 — D-naming sweep)  
**Precondition:** D-mcp internalization wrap layer (ADR-002) landed first. The uniform
agent-facing surface (no `mcp__server__` prefix) is the substrate this convention builds on.

---

## Baseline rule: `<concept>_<verb>`

Every tool name should reflect what the tool **does** (its concept and action), not where it
lives. The MCP server name is invisible to the agent — it is preserved only in audit metadata
via `Manager.ToolAttribution`.

```
GOOD: memory_write      — concept=memory, verb=write
GOOD: task_create       — concept=task, verb=create
GOOD: context_search    — concept=context, verb=search

BAD:  vanta_memory_write  — "vanta" is a provider, not a concept
BAD:  mcp__mux__memory_write — legacy form, eliminated by ADR-002
```

The verb slot should be a common action word:

| Verb | Meaning |
|------|---------|
| `create` | Create a new resource |
| `get` | Fetch a single resource by ID |
| `list` | Enumerate resources (plural noun preferred: `tasks_list`) |
| `update` | Mutate fields on an existing resource |
| `delete` | Remove a resource |
| `search` | Query by content or filter |
| `enqueue` | Kick off an async job |
| `write` | Store a value |
| `read` / `view` | Read a value |
| `transition` | Move a resource through a state machine |
| `health` | Liveness / readiness probe |

---

## When to add a provider prefix: collision only

The provider prefix (`<provider>_<concept>_<verb>`) is a **disambiguator**, not a default.
Use it **only** when two owned MCP servers publish the same concept and the bare slot would
be contested.

```
# No collision: only Engine has "sprint" today → use bare concept
GOOD: sprint_create     ← single owner, no contest

# Real collision: Engine, Hadron, AND Cerberus all publish "health"
# Bare "health" can't be assigned to one server without ambiguity
GOOD: engine_health
GOOD: hadron_health
GOOD: cerberus_health
BAD:  health            ← ambiguous when three servers own it
```

### Known real collisions (as of 2026-04-26)

| Bare concept | Colliding servers | Resolution |
|---|---|---|
| `health` | engine, hadron, cerberus | Keep `engine_health`, `hadron_health`, `cerberus_health` |
| `status` | cerberus (ambiguously generic) | Keep `cerberus_status` |
| `start` / `stop` / `restart` | cerberus (too generic to go bare) | Keep `cerberus_start`, etc. |
| `logs` | cerberus (generic verb) | Keep `cerberus_logs` |

### Known non-collision groups that keep their prefix anyway

These tool groups use a prefix not because of a live collision but because the prefix is the
disambiguating concept itself — stripping it would lose meaning:

| Group | Why the prefix stays |
|---|---|
| `clockwork_task_*`, `clockwork_sprint_*` | Clockwork is a specific scheduling system; bare `task_*` would be ambiguous vs. Engine tasks |
| `engine_task_*`, `engine_sprint_*` | Engine (Fragments Engine) task system is distinct from Clockwork's. Both in play simultaneously on agents with full tool access |
| `hadron_run_*`, `hadron_blueprint_*` | Hadron owns "run" as a CI/CD primitive; other future CI systems could have `run_*` |

---

## The `nanite_*` reserved namespace

`nanite_` is exclusively for first-party **self-tools** — tools that control the Nanite harness
itself (scratchpad, messaging, skill management, subagent spawning, UI navigation). MCP servers
must not publish tools whose bare name falls in this namespace. The registration layer
(`Manager.assignUniformNameLocked`) force-prefixes any violating tool with its server name.

Do not add MCP-origin tools to this namespace. If you need a harness-internal tool, add it
to `internal/mcp/self_tools.go`.

---

## Builtin server groups

Builtin servers (`dev`, `general`, `code`, `self`, `memory`) register tools without any
server prefix because their concepts are universal:

| Tool | Server | Concept |
|---|---|---|
| `dev_read`, `dev_write`, `dev_edit`, `dev_grep`, `dev_glob`, `dev_bash` | dev | Developer file/shell ops |
| `web_fetch`, `json_parse`, `datetime`, `hash`, `math_eval`, `think` | general | General utilities |
| `base64_encode`, `base64_decode`, `url_encode`, `url_decode` | general | Encoding utilities |
| `nanite_code_execute` | code | Sandboxed code execution (self-tool namespace) |

These do not need a prefix because they are unique concepts with no collision risk in the
current tool set. New builtins should follow the same pattern: bare concept name unless
a collision is identified at registration time.

---

## Collision policy (runtime)

The `Manager.assignUniformNameLocked` registration path handles collisions automatically:

1. Default: bare tool name (`memory_write` from mux → `memory_write`).
2. Reserved-namespace defense: if the bare name is `nanite_*`, force-prefix with server.
3. Collision: if the bare slot is already taken by a different server, both the incumbent
   and the newcomer are rewritten to `<server>_<tool>`. A `WARN` log and
   `DiscoveryWarning{Reason:"uniform_name_collision_disambiguated"}` are emitted.
4. Hard collision (disambiguated slot also taken): tool dropped with
   `DiscoveryWarning{Reason:"uniform_name_collision"}`.

Monitor the `uniform_name_collision*` discovery warnings in your environment when onboarding
a new MCP server.

---

## Hard rename policy (no compat shims)

Per `feedback_no_compat_shims` and the Phase 5 pre-launch latitude: when a tool is renamed,
the rename is a **clean break** — no alias, no redirect. There are no external consumers
to preserve; the uniform agent-facing surface and the `ExecuteToolOnServer` internal path
are both first-party code.

Decision captured in `docs/tool-naming-audit.md` (Decisions section).

---

## How to name a new tool

1. **Identify the concept.** What resource or domain does the tool touch?
   `task`, `memory`, `context`, `sprint`, `blueprint`, `run`, `workspace`, `schedule`.

2. **Pick a verb** from the table above.

3. **Check for collision.** If another owned MCP publishes the same `<concept>_<verb>`,
   prefix your server name: `<server>_<concept>_<verb>`.

4. **Avoid bare generics.** `start`, `stop`, `status`, `health`, `get`, `list` on their
   own are not tool names — they must be qualified with a concept or server prefix.

5. **Do not use** `mcp__`, `nanite_` (unless it's a self-tool), or the full legacy
   `mcp__server__tool` form.

---

## Reference

- `internal/mcp/naming.go` — `UniformToolName`, `DisambiguatedToolName`, `IsReservedSelfToolName`
- `internal/mcp/manager.go` — `assignUniformNameLocked` (collision policy)
- `docs/decisions/ADR-002-mcp-internalization-wrap-layer.md` — uniform surface spec
- `docs/tool-naming-audit.md` — per-tool verdict table
