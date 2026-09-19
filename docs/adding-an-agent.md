# Adding an agent

How an agent profile gets into Nanite, and which of the several paths that look
equivalent actually are. Most of the sharp edges here are not bugs — they are
two subsystems with deliberately different trust models meeting in one create
call — but they are invisible until an agent sits in a session with no tools.

## Create through the API, not the import path

Two endpoints appear to do the same thing. They do not.

| Call | Records `source` | Class | Editable | Can hold skill grants |
|---|---|---|---|---|
| `POST /api/agents` | `user` (default) | `managed` | yes | yes |
| `POST /api/agents/install` | `import` | `external` | **no** | **no** |

`Classify`, in `internal/agent/source_class.go`, maps a profile's `source` onto
its `ManageClass`. It recognises `""`, `api`, `cli`, `managed_file`, `nanite`,
`project` and `user` as operator-owned; anything else falls through to
`external`, which is read-only **in place** — an external profile refuses
update, delete and skill-grant alike, and `CopyToManaged` only offers a copy
under a different slug.

So the `.md` install path is for importing somebody else's definition, not for
authoring your own. Authoring goes through `POST /api/agents`, where
`AgentConfigService.Create` forces `source = "user"` regardless of what the
caller sent.

`.md` frontmatter is also the smaller surface: it carries no `roleSkills` and no
`canExecute` (only `permissionMode: yolo`, which sets that flag as a side
effect). Both are first-class fields on the API request.

## Tools: a grant, not a declaration

`filterToolsByAgentTools`, in `internal/service/tool.go`, filters the tool list
offered to the model against the `agent_tools` table, and `ToolClient.CallTool`
checks the same table again at execution time. **Zero grants means zero tools**
— the empty set is a deny, not a pass — so an agent with no grants reaches the
model holding only the `always_included` escape hatches.

Grants come from `role_tools` on the create request. `AgentConfigService.Create`
hands it to `seedRoleToolsFromIngest`, which writes both an `agent_known_tools`
row and an `agent_tools` grant per name.

Two things follow from how that seeder resolves names:

- **A name absent from `known_tools` is skipped, silently but for a warning.**
  The create still succeeds; the agent is simply short a tool.
- **`known_tools` learns MCP tools only at boot.** `SyncKnownTools` runs once
  from the service container against the live catalog. Registering an MCP
  server through the API connects it immediately, but its tools cannot be
  granted until a restart has put them in the catalog.

That second point is the usual cause of an agent that looks correctly
configured and has no tools: the server was added and the agent created in the
same session, so every name was skipped.

### Tool names change when two servers collide

Registering the same MCP server twice — the normal way to run one backend under
two configurations — makes every tool name ambiguous. The manager disambiguates
by prefixing **both** registrations with their server name, logging
`tool-name collision — disambiguated both servers` as it goes.

A tool that was `get_incident` under one registration becomes
`helix_get_incident` and `helix-desk_get_incident`. Grants must use the
prefixed spelling, and so must any tool name written into a system prompt.

The prefix is worth having rather than working around: it is what binds an
agent to one registration's configuration, so an agent granted the `helix_`
spelling cannot reach the other server's credentials.

## Skills: a catalog entry is not a grant

`role_skills` seeds `agent_known_skills` through `seedRoleSkillsFromIngest`,
one pinned row per slug with `reason='role_seed'`. That row makes the skill
**discoverable** — it renders in the agent's catalog block as name plus
description, and `skill_get` can be pointed at it.

It does not make the skill **executable**. The seeded row carries no
`ApprovedContentHash`, and `internal/skill/gate.go` treats an unapproved row
exactly as it treats a missing one: `GrantRequiredError`, on the stated
principle that a skill is never an ambient capability.

Approval is a separate, explicit act:

```
POST /api/agents/{id}/skills/{slug}/grant   {"granted_by": "you@example.com"}
```

`granted_by` is required and the refusal does not say so.

The grant binds to the skill's **current content hash**. Re-installing a skill
mints a new hash and every approval against the old one goes stale, surfacing
as `ReapprovalRequiredError` rather than silently executing content nobody
approved. Editing a skill therefore means re-approving it everywhere it was
granted — the cost is real and it is the point.

Two consequences worth stating plainly:

- **Granting a skill does not grant the tool that reads it.** `skill_get` and
  `skill_list` are ordinary tools and must be in `role_tools`. They are not in
  `always_included`, which holds only `request_tools`, `tool_describe` and
  `tool_list`.
- **Seeding never overwrites an existing row.** The seeder runs again on every
  update, and `InsertAgentKnownSkill` is `INSERT OR REPLACE`, so re-seeding a
  granted slug would blank its approval columns — revoking a skill because
  somebody edited a description. `seedRoleSkillsFromIngest` skips a slug that
  already has a row for that reason.

## Executable agents and subagent targets

`subagent_spawn` refuses a role whose profile has `can_execute = false`, unless
the slug is in `textOnlyRoleSlugs` (`internal/subagent/service.go`) — a
deliberately tiny list for roles that produce text through a dispatch surface
needing no tool path.

So any agent meant to be spawned as a worker needs `can_execute: true` on the
create request. It is not a blanket permission: what the worker may actually
call is still bounded by its own `agent_tools` grants.

Spawning is gated a second time, by approval. The predicate is
`subagent_approval_required && !developer_mode`, evaluated per spawn, and a
gated spawn parks in `requested` until a human responds. `developer_mode` is
settable through `PUT /api/settings`; `subagent_approval_required` is not
exposed there.

## Cards need no tool

An agent renders a card by emitting a fenced block, which `ParseEnvelopes`
(`internal/chat/envelope.go`) lifts out of the reply text:

~~~
```nanite-envelope
{"kind":"envelope","version":1,"type":"info-card","data":{"title":"…","body":"…"}}
```
~~~

The block is stripped from the prose and validated against the registered type
schema. Only malformed JSON is fatal; a schema miss still renders. `card_show`
covers the same v1 types as a tool call, and an MCP server can emit the same
marker in a tool result — that last path is the one to reach for when rendering
must not depend on the model remembering.

## Materialize, then boot — mint identity before either

A CLI session goes through two phases, in order: **materialize** (a
`Layout`'s `Setup`/`Populate` writes the boot dir — CLAUDE.md, skills,
`.mcp.json`, provider settings) then **spawn** (`agent.Boot` starts the
actual process). This is not incidental ordering — it's the reason the sharp
edge below is easy to hit and easy to miss.

`Populate` (`claudeLayout.Populate`, `bootdir_claude.go`) is deliberately
synchronous, local, and takes no `context.Context` — "a synchronous
filesystem write with no cancellation point today," by its own doc comment.
It is not the place to call another service. Neither is `agent.Boot` itself:
it's the one shared entrypoint every CLI session boots through, for every
agent profile in the system, not just the one you're building — a bug or a
slow/unreachable dependency added there degrades every other agent, not just
yours.

**The pattern that avoids this:** mint any external identity (a Tether URN,
anything else that needs registering with another system) **once, upstream
of boot entirely** — e.g. at agent-creation time, not per-session and not
per-launch. By the time materialization runs, the value already exists as
plain data (on the `AgentProfile` row, or wherever it was minted to); the
plant step just reads it and writes it out. No client, no network call, no
new failure mode in the hot path — because there's nothing left to fetch.

Concretely: `SetupParams` already carries both `AgentProfile` (whose
`Settings` JSON can hold an already-minted identity) and `SessionID` (already
resolved before boot dir setup starts). `internal/runtime/agent/tether_identity.go`
(`tetherIdentityFile`) reads `settings.tether_urn` — set once, at
registration time, via `tether_registry_register` — builds the matching
session identity locally via `internal/a2a.NewSessionAddress(params.SessionID).URN()`
(Nanite's own local `msg://` scheme, no daemon call), and returns `nil` when
`tether_urn` is absent. `claudePlantSpec` (`bootdir_claude.go`) adds the
result to its `files` map like any other planted file — `.sandbox/tether-identity.md`
next to `CLAUDE.md`. An agent that was never registered gets nothing extra:
the gate is on data presence, not a hardcoded agent list.

This is not Nanite-specific: Torque follows the same materialize-then-boot
sequencing (plant the boot dir, then spawn). It does **not**, however, share
Nanite's `internal/a2a` package or URN scheme — there is no cross-app
identity library today, only the same two-phase *pattern*, independently
implemented. Don't assume shared code because the architecture matches.

## A worked create

```bash
curl -X POST localhost:8099/api/agents -H 'Content-Type: application/json' -d '{
  "name": "Service Desk", "slug": "service-desk",
  "system_prompt": "...",
  "can_execute": true,
  "mcp_servers": "[\"helix-desk\"]",
  "role_tools": "[\"helix-desk_search_incidents\",\"skill_get\",\"skill_list\",\"card_show\"]",
  "role_skills": "[\"helix-queue-triage\"]"
}'
```

Note that the JSON-array fields are **strings containing JSON**, not arrays.

Then approve each seeded skill, and confirm what the agent actually holds —
a create that skipped every tool name still returns `201`:

```sql
SELECT p.slug,
       (SELECT COUNT(*) FROM agent_tools t WHERE t.agent_id = p.id)        AS grants,
       (SELECT COUNT(*) FROM agent_known_skills k WHERE k.agent_id = p.id) AS skills
  FROM agent_profiles p WHERE p.slug = 'service-desk';
```

A `grants` of zero is the answer to almost every "why does my agent ignore its
tools" question.

## Order of operations

1. Register MCP servers.
2. **Restart**, so `SyncKnownTools` puts their tools in `known_tools`.
3. Install skills.
4. Create agents with `role_tools` (prefixed spellings) and `role_skills`.
5. Approve each skill per agent.
6. Verify grants and approvals before driving a turn.

Steps 2 and 5 are the two that are silently skippable, and each fails as an
agent that looks configured and behaves as though it is not.
