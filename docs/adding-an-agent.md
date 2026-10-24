# Adding an agent

Nanite separates three things: immutable intrinsic definitions, mutable host
execution settings, and verified actor authority. Installing content does not
create an actor or grant tools and skills.

## The one-step path

```sh
nanite agent setup <flat.md | archetype-id> --provider anthropic --model <model> --tools all \
  [--runtime api|cli] \
  [--slug S] [--title T] [--resources resources.json] [--reason "why"] [-db path]
```

`setup` installs the definition, creates its host settings (slug from the
definition id unless `--slug`), reviews those settings and grants the tools,
all in one transaction. It prints the definition ref, host settings id and
revision, the authority id, the granted tools, and a ready
`POST /api/agent/v1/sessions` body.

Setup checks the definition, execution policy and runtime before creating the
agent. API agents require a provider and model and a resolvable API key; a key
refusal names the remedy without printing the credential. CLI agents use their
provider CLI's login and accept an optional model:

```sh
nanite agent setup agent.md --runtime cli --provider claude --model haiku --tools all
nanite agent setup agent.md --runtime cli --provider codex --slug codex-agent --tools all
```

CLI setup uses session creation's admission checks: supported provider runtime,
default permission profile, absolute directories and support for passing a model.
The CLI executable and its login must be available to the user running Nanite;
setup does not authenticate with the provider or execute a trial turn. Setup also
checks that every named tool exists. `--tools all` grants every tool the catalog lists as
available; it is a grant recorded like any other, not a bypass. The catalog is
filled when the server starts, so start it once on a new database before
asking for more than the always-included tools. Dotted names (`dev.read`) are
read as `dev_read`; `nanite authority tools` lists the names.

Host directories default to a private workspace, `agent-workspaces/<slug>` beside the
database (created for you); `--directories a,b` names others.

The first argument is an authored file or the id (or name) of an embedded
archetype such as `def:nanite/chat`; an unknown one lists the ids. A definition
the native runtime would refuse at launch (for example durable continuity) is
refused up front, so the printed session body is always admissible.

Running it again with the same inputs changes nothing and reports the existing
ids (`"created": false`). Re-running is additive: `tools` is always the
complete set now granted, `tools_added` is what this call added, and a narrower
`--tools` never revokes (use `authority ungrant`). A slug already used by different settings (another
definition revision, provider, model or runtime) is refused; pass `--slug`.
The semantic digest ignores identity, so definitions are told apart by id and
revision, and `agent validate` also prints `identity_digest`.

## The same setup over HTTP

A remote caller (a Tether agent, Tachyon) makes the same single transaction with
one request instead of driving the pieces in sequence:

```sh
curl -X POST $NANITE/api/agent-setup -H "Authorization: Bearer $TOKEN" -d '{
  "archetype": "def:nanite/chat",          // or "artifact": "<authored definition text>"
  "provider": "claude", "runtime": "cli",  // api needs "model" and a usable provider key
  "tools": ["*"],                          // or ["dev_read", ...]; "*" grants every available tool
  "reason": "why these tools",             // required, at most 500 characters
  "slug": "...", "title": "...",           // optional
  "directories": ["/abs/path"],            // optional, absolute; default agent-workspaces/<slug>
  "resources": [{"uri": "...", "content": "..."}]   // optional, for artifact
}'
```

The body fields and the response are those of `nanite agent setup`: `201` with
`"created": true` the first time, `200` and `"created": false` on an identical
re-run, `409` when the slug is taken by different settings, `400` for a request
that cannot work (refused before anything is written), `422` for a tool that
cannot be granted. The response carries `session_request`, the body for
`POST /api/agent/v1/sessions`.

Read agents back with `GET /api/native-agents` and `GET /api/native-agents/{slug-or-id}`:
host settings (definition pin and digest, runtime, provider, model,
directories, `enabled`), the active `authority_id`, the granted `tools`, and a
`state` of `ready`, `no_authority` (host settings changed since the last
review, so nothing is granted for the new revision) or `disabled`.

Changing an agent uses the existing routes: grant or withdraw a tool with
`PUT`/`DELETE /api/native-authority/{authority_id}/tools/{tool}`, withdraw all
authority with `POST /api/native-authority/{authority_id}/revoke`, retire by
`PUT /api/agent-host-settings/{id}` with `"enabled": false`, change host
settings the same way (send the current `revision`).

Credential: the setup and view routes are admin-class and refuse agent
credentials. Client credentials cannot hold `admin` (`nanite auth create`
refuses it), so today the caller must present the operator credential
(`nanite auth token-path`). The accountable actor recorded on the authority is
that credential's id, not anything in the body. Giving Tether and Tachyon their
own narrower credential for these routes would need a scope decision and is not
done here; until then treat the operator token as the key to granting tools.

The steps below are what `setup` does, for when you need one on its own.

## The same setup over MCP

`POST /api/admin/mcp` is a streamable-HTTP MCP endpoint (stateless, JSON
responses) so Tether's MCP gateway and Tachyon register Nanite as a tool server
instead of shelling out. Each tool calls the HTTP route below in-process with
the principal of the request that reached the endpoint, so the handlers' own
validation and reason-required rules apply. An error comes back as an MCP tool
error whose text starts `HTTP <status>:`; any non-2xx status, redirects
included, is an error. The inner call does not pass the auth middleware's
per-route scope rules or the `/api/admin` Origin allowlist a second time (the
outer request did); the endpoint instead re-checks for a non-agent admin
principal itself. Today only the operator holds admin, so this changes nothing.
Ids must be a single path segment (`.`, `..` and `/` are refused).

| Tool | Route |
|---|---|
| `agent_setup`, `agent_list`, `agent_get` | `POST /api/agent-setup`, `GET /api/native-agents[/{id}]` |
| `host_settings_list`, `host_settings_create`, `host_settings_update` | `/api/agent-host-settings` |
| `host_settings_retire` | not a route: a disable-only write (`enabled=false`, a required `reason`, no model/definition re-validation, retried on a moved revision). Live sessions and grants are untouched; use `session_stop` and `authority_revoke` |
| `authority_list`, `authority_get`, `authority_revoke` | `/api/native-authority[/{id}[/revoke]]` |
| `tool_grant`, `tool_ungrant` | `PUT`/`DELETE /api/native-authority/{id}/tools/{tool}` |
| `session_start`, `session_list`, `session_get`, `session_history`, `session_stop` | `/api/agent/v1/sessions...` |
| `turn_send`, `turn_get`, `turn_cancel` | `/api/agent/v1/sessions/{id}/turns...` |

Every mutating tool writes an auth-audit row naming the tool and its target (`mcp:tool_grant <authority> <tool>`), since the middleware only sees `POST /api/admin/mcp`; `host_settings_retire` also records its reason there. `agent_list`, `host_settings_list` and `authority_list` take `limit` (default 50) and `offset`; `authority_list` needs `host_settings_id`. `session_list` and `session_history` page with `limit`/`cursor`/`older_cursor`; any
response over 256 KiB is refused rather than cut. The endpoint sits under
`/api/admin`, so it needs the admin scope: the operator token. Agent credentials are refused, and so is any client credential without
admin. That credential stays admin for now; narrower credentials come later.
Directories and `work_root` are absolute paths on the machine Nanite runs on.

The `agent_create` and `agent_update` self-tools are gone: they only ever
returned the immutable-profile error. `agent_list` among the per-session self
tools still lists legacy profiles, read-only.

## Author and install intrinsic content

Definitions use agentdef v2 frontmatter, a stable definition ID, an authored
revision and a Markdown body. The SDK validates the complete schema. References
carry exact URI/digest pins; installation checks every referenced resource byte.
The semantic definition digest is distinct from each artifact byte digest.

Nine fresh flat archetypes are embedded under `internal/agentdefs/`. They are
newly authored content, not conversions of historical profiles. The original
`def:nanite-default` revision 1 artifact is preserved unchanged so an explicit
historical General Chat keep receipt remains verifiable.

```sh
nanite agent author --role role.md concrete.md > flat.md
nanite agent validate flat.md
nanite agent install --resources resources.json flat.md
```

`install` writes the database directly, so it needs to know which one: pass
`-db <path>`, or set `NANITE_DB_PATH=<path>` (or `NANITE_WORKSPACE=<name>`).
With none of them it refuses and prints the suggested default. The same
`-db` is accepted by every offline or client subcommand (`agent`, `auth`,
`authority`, `mcp`, `skill`, `plugin`, `profile`). For a server started with
`serve -db X`, the CLI finds its operator token (`nanite auth token-path`)
and credentials from the same `-db X` or `NANITE_DB_PATH=X`: the credential
directory sits beside the database whenever one is named explicitly. The
`nanite auth` commands talk to the server's address, `NANITE_API_URL` or
`NANITE_PORT`, so point those at a non-default port too.

`author` also accepts a standalone concrete definition without `--role`.
Flattening keeps concrete identity, requirements, permissions, continuity and
extensions. Behavioral prompt fields and native class may inherit. A nonnil
instruction/SOP list, including an empty list, overrides the whole role field.
Hooks stay concrete; required behavior that cannot be represented refuses.
Host provider/model, enrollment and grants never inherit from a role template.

`validate` checks schema and semantic pin; it does not claim referenced byte
availability. The install resource manifest is an explicit JSON array:

```json
[{"uri":"resource:instructions","path":"./instructions.md"}]
```

Installation writes the immutable artifact and referenced resources in one
transaction. Missing, changed, duplicate or unreferenced resources refuse.
Reusing an ID/revision with different bytes returns a conflict. Author a new
revision when content changes. Old profile sync/import and mutable profile
CRUD are retired, with no converter or boot-time grant replay.

Equivalent authenticated host endpoints are `POST /api/agent-definitions/author`
(`concrete`, optional `role`) and `POST /api/agent-definitions` (`artifact`,
`resources` entries containing `uri` and `content`). GET returns installed pins
and their immutable artifacts. Authoring alone does not install resources.

## Configure host execution inputs

Admin > Agents manages installed definitions and host settings separately.
`POST /api/agent-host-settings` creates a local host document; PUT and DELETE
require its current revision. The host assigns local IDs and revisions. A local
UUID is not an actor address.

Host settings contain provider/model, API or CLI runtime and transport,
harness-profile inputs, numeric recall limits/timeouts, filesystem/MCP execution
inputs and debug settings. Native provider/model selection is checked against
actual host policy. Caller source/plugin ownership fields are not accepted.
Workspace custody, sandbox and plugin credential enforcement remain host-owned.

Host title/enabled/settings edits do not mutate intrinsic content. Changing the
selected pin requires another already installed definition. Revision conflicts
refuse before effects. Retired IDs/slugs cannot be recreated through host setup.

Native `POST /api/agent/v1/sessions` requires `definition_ref` with API spellings
`definition_id`, `revision`, `semantic_digest`. Optional `host_settings` contains
`id` and `revision`; admission checks the matching pin, the enabled host runtime (`api` or
`cli`), the authorized model (API) or the CLI provider's launchability, and the
current host revision. The accepted view stores the typed
host snapshot. A later host edit does not change a previously accepted `api`
view. A `cli` view records the host revision it was accepted under, and its next
turn after any host edit fails with `host_revision_changed`; create a new
session to use the edited host. Unsupported required semantics refuse before session rows.

## Native and reflex extensions

The optional negotiated extensions are:

- `com.hollislabs.nanite/native-policy`, version `1`, area `harness_profile`:
  intrinsic class, completion/wake policy, write-claim guard, and auto-recall
  enabled/confidence behavior. Numeric recall limits and host loop settings
  belong in host settings. A host guard cannot be weakened by intrinsic policy.
- `com.hollislabs.nanite/reflex-policy`, version `1`, area `behavior`: a pinned
  declarative bundle resource. Rules are validated, use the published evaluator,
  and request session-owned reminder/halt effects. Failed collection/evaluation
  stops the turn. A halt wins over softer requests and stops provider dispatch.

Unknown required namespaces, versions, fields and unsupported mandatory actions
refuse. A definition tool preference is not a tool grant. Native views refuse
force-tool preferences until a verified actor grant port is adopted. Mutable DB
reflex rules, plugin seed imports, procedures, knowledge seeds and context
resolver authoring do not have fresh runtime equivalents.

## Authority and historical boundaries

Verified actors are stored separately from host settings, with issuer binding
receipts and actor-keyed operational state/grants. No enrollment writer is
adopted yet. Supplying a URI, local UUID, slug, definition declaration, metadata
claim or an old profile's identity does not create authority. Actor-dependent
launch, durable creation, fabric operations and grant issuance explicitly refuse
when genuine issuer support is unavailable. Native General Chat remains a local
cognitive view with no borrowed profile grants. Its session-local cached-result
navigation is a host capability, not external tool authority.

Historical profiles and their full old relation graph remain intact. Ordinary
readers do not fall back to them. Explicit historical export and audited
retirement use named historical lookup methods and independent receipts.
Protected-profile retirement and suppression are separate source capabilities;
this change performs no old-row deletion, live data operation, backfill or
identity conversion.

Between-turn artifact refresh remains typed unavailable for active bindings.
Nanite retains the old binding and bytes; it does not directly overwrite active
boot artifacts or claim an inactive root based on timing. CLI/provider or fabric
adoption is not implied by installing a definition or saving host settings.

## Session lifecycle

`POST /api/agent/v1/sessions` accepts `work_root` for CLI hosts (absolute, inside
a host directory). `POST /api/agent/v1/sessions/{id}/stop` ends a session and its
runtime and is idempotent; `nanite agent list` and `nanite agent stop <session>`
call the same routes. See `docs/getting-running.md`.

## Loading an agent from a directory

An agent can be authored as a directory and published with `nanite agent load`:

```
my-agent/
  agent.md      agentdef v2 (behavior, requirements, harness policy, continuity)
  host.yaml     runtime, provider, model, directories (absolute; the first is the work root)
  context/...   files that agent.md pins as resources (relative paths, or file:<path>)
```

```sh
nanite agent load my-agent             # or run it from the directory; ./.nanite is also searched
nanite agent load --dry-run            # show the diff, write nothing
nanite agent load --grant-same-as <previous-slug> --reason "same tools"
```

`host.yaml` accepts `version`, `runtime`, `provider`, `model`, `directories`,
`harness_profile`, `mcp_servers`, plus `slug`, `title` and `mode` (`live` or
`pinned`); unknown keys are refused. A load installs the definition and records a **new**
host-settings row named from the agent and a hash of the definition and host
settings, so loading the same state twice changes nothing, and an edit never
touches the row an older session was admitted under (see
`architecture/agent-alignment.md`). The output is the diff from the previous
revision and the body to start a session against the new row.

- The new row has **no tool authority**. Grant tools as usual, or pass
  `--grant-same-as` to re-grant exactly the tools of an earlier row; nothing else
  carries forward.
- Editing `agent.md` needs a new `revision:` label (a revision names one set of
  bytes); the error says so.
- `mode: pinned` refuses a second revision unless `--force`. With no `mode`, the
  default rule decides (`NANITE_AGENT_MODE`, then the target's labels in
  `NANITE_RESOURCE_ENV`/`NANITE_RESOURCE_ADMIN`): `live` only for a target
  labeled `env: dev` and not `admin: owner`, else `pinned`. The load output says
  which mode applied and why. Set `mode: live` to republish on every load. An
  operator's own `agent load`/`agent run` does not treat the bare fallback as
  `pinned` (it publishes), so editing and running again keeps working; the
  reload endpoint, which is unattended, applies the full rule.
- A `live` agent's directory may not sit inside, or contain, one of its own
  `directories`: the agent could rewrite its definition. A `pinned` one may.
- Skills in `skills/` that `agent.md` requests are packed, digest-pinned and
  stored; they are not delivered to the session yet (the load says so). A
  definition that declares more than the native runtime can launch is refused, as
  in `agent setup`. Each field's disposition is in the load output (`fields`).
- The load writes the database directly, like `setup`; a running server uses the
  new row for its next session without a restart. A running server reloads a
  directory with `POST /api/agent-reload` `{"host": "<slug or id>"}` (maintain
  scope; `force`, `dry_run`, and, with an admin credential and a `reason`,
  `grant_same_as`). It re-reads the directory recorded on that host row
  (`agent_dir`), publishes a new row when the state changed, and returns the body
  to start a session on it; sessions already running keep their row, and an API
  session's later turns fail with `host_revision_changed` if its own row is
  edited or disabled.

### Running it from the project

```sh
cd ~/dev/my-project
nanite agent run                    # load ./.nanite (or ./agent.md), start, attach
nanite agent run -p "fix the build" # one prompt, print the reply, exit
echo "summarize" | nanite agent run -p -
nanite agent run --session <id>     # resume (no reload)
```

`run` does an `agent load` first (same flags: `--force`, `--grant-same-as`,
`--reason`), then starts a session on the loaded revision and host row. For a
`runtime: cli` agent the current directory is the work root and must sit inside
`directories` in `host.yaml`; an API agent gets it as work root too when a
`directories` entry contains it, and otherwise works from its first directory. Edit the directory and run
again to pick the change up; the server is not restarted and running sessions
keep their revision. A fresh revision has no tool grants, so `run` prints the
`agent load --grant-same-as` command to give it the previous row's tools.
Starts the server if it is not running, unless `--no-autostart`.
