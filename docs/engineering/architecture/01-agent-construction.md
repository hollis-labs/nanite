# Agent Construction

## The composition model

An Agent is built from three layers, resolved with closest-wins override — the same shape as RBAC/IAM's Role+Binding+cascading-scope pattern (AWS IAM, Kubernetes RBAC/RoleBinding, GCP IAM), chosen deliberately over the flatter "fully-specified Agent object" most agent frameworks use, because the flat pattern doesn't solve reuse-across-context cleanly.

```
roles                        -- reusable persona/behavior template
  id, name, system_prompt, default tool/skill/permission hints

agents                       -- the composition/assembly record (not renamed —
                                 keeps every existing agent_id FK's current meaning)
  id, slug, role_id (FK -> roles), consumer_id (FK -> consumers, nullable),
  model_id (FK -> models), instance_mode, runtime_kind, class, enabled

<scope references>           -- not a new facet catalog like role — references to
                                 entities that already exist for other reasons
  agent_projects (agent_id, project_id)
  <future scope dims follow the same pattern>  -- data sources, datasets, etc.
```

Cascading override, closest wins: **role → agent/composition → task/invocation.** A role supplies the broadest defaults; the agent composition can override per scope; a specific task/dispatch can override again at runtime. Not special-cased per field — `class` (advisor/process/template/harness), tool/skill/permission grants, and model selection all follow this same three-tier cascade.

**Scope is not a facet catalog.** Unlike role, there's no new "scope" table to define — scope is a reference to something that already exists independently (a project, eventually a data source). A label like "SME" is identity, not structure — it belongs in a role's `name`/`system_prompt`.

## Relational references replace free-text strings

```
known_tools                  -- live-synced catalog. status=unavailable, not deleted,
                                 when a server disconnects

agent_tools                  -- join, replaces tools:/toolPermissions:/roleTools: entirely
  agent_id FK -> agents, tool_id FK -> known_tools

agent_dispatch_allowlist     -- separate concept from agent_tools: which tools this
                                 agent may authorize a subagent it dispatches to use

agent_skills                 -- same FK pattern, against the real skills catalog

models                       -- real schema (provider_id FK, context_window, pricing,
                                 is_enabled) — its models.dev refresh target needs fixing
                                 (currently feeds an in-memory overlay, not this table)
```

A default-tools baseline (so a new agent can't accidentally ship without the tool-discovery escape hatch — `request_tools`/`tool_list`/`tool_describe`) is a flag on `known_tools` marking certain tools as always-included, not a per-creation-flow default that can be silently dropped.

## Ownership and instancing

`agents.consumer_id` (FK to a minimal `consumers` table, nullable = operator-owned) tags external ownership — Loom is the first real row. `agents.instance_mode` (singleton / fresh-per-wake / concurrent) makes explicit what used to be implicit, hardcoded per-`lifecycle_class` behavior.

## Reflexes at construction time

`agent_reflexes` already supports what's needed structurally: `agent_id IS NULL` = class-bound/global reflex, a specific `agent_id` = per-agent reflex. Gap being closed: a field distinguishing "required, cannot opt out" from "default-on, agent may opt out."

## What's cut

- **External-format agent import** (importing `.claude/agents/`, codex/gemini/opencode config formats as Nanite agents) — no replacement. Nanite agents are defined in Nanite's own schema.
- **Files as agent storage**, except builtin/seed content. Builtin/seed profiles become one-time seed data — inserted once, never re-overwritten from the compiled file on every boot (this closed a real bug where a GUI customization to a builtin agent was silently reverted on restart).
- **`agent_known_skills`/`roleSkills:`-as-currently-used** — a dead end structurally identical to `roleTools:`. Superseded by the FK-based `agent_skills` join.

Plugin-provided agents stay as a real source (`registers.agent_profiles[]` in a plugin manifest), must conform to this schema, no legacy grandfathering. Wiring that registration path up is real, scoped work — see `TASKS.md`.
