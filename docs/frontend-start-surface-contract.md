# Frontend Start Surface Contract

**Status:** Shipped in Nanite  
**Scope:** UI-facing backend terms, enums, and read-only inspection contracts
for chats, durable agents, recipes, sessions, and runtime state.

## Terms

| Term | Contract |
|---|---|
| Session | A `sessions` row. It owns transcript persistence, provider/model selection, current mode pointer, and UI thread state. |
| Chat session | A session used through `POST /api/messages`. It may be API-backed or CLI-backed. |
| Runtime session | A live or historical `agent_runtime` row created by runtime boot for CLI-backed sessions. API chat sessions have no runtime row. |
| Durable agent profile | An `agent_profiles` row used as persona/template/policy source. Profiles are not runtime instances. |
| Durable agent instance | A `durable_agent_instances` row with immutable profile, lifecycle class, provider, model, runtime kind, launch source, and work root captured at creation. |
| Recipe | Versioned declarative setup input. Recipes compile to durable-agent instance fields, wake defaults, and launch policy. |
| Launch plan/policy | Backend-computed answer to "what would this durable agent start as?" |
| Runtime kind | Backend runtime substrate such as `api`, `streaming-stdio`, or `subprocess`. |

## Public Enums

The frontend should use `GET /api/start-surface/capabilities` as the bootstrap
contract. It returns the canonical enum lists and option sets used by Start and
admin surfaces.

Important enums include:

- lifecycle class: `advisor`, `process`, `template`, `harness`
- durable status: `sleeping`, `starting`, `active`, `paused`, `stopped`, `start_requested`, `stop_requested`, `resume_requested`, `failed`, `archived`
- launch source: `api_chat`, `cli_harness`, `boot_profile`, `durable_advisor`, `process_tick`, `task_template_run`
- runtime kind: `api`, `streaming-stdio`, `subprocess`, `jsonrpc-stdio`, `serve-http`, `pty`, `pty-debug`
- wake reason: `manual`, `lifecycle_start`, `lifecycle_resume`, `process_tick`, `scheduled_wake`, `external_message`
- session policy: `reuse_latest_or_create`, `fresh_per_wake`, `fresh_one_shot`, `reuse_managed`

## Start Surface Data

The capabilities response contains:

- enum lists
- recipe catalog rows
- durable-agent instances
- agent profiles
- provider and model options
- runtime kind options with automation and product-support flags
- boot profile options
- work-root hints

## Session Details

Use `GET /api/sessions/{id}/details` for the read-only details panel.

It returns:

- the session row
- current mode when set
- primary agent profile when available
- durable attachments
- current durable instance when attached
- latest runtime state when a runtime row exists
- inferred boot source
- immutable start fields
- checkpoint placeholder state
- additive observability fields such as activity, halt, usage, and recent durable events

Sessions without durable attachments or runtime rows are valid and should
render as API/default chat with runtime state `none`.

## Immutability

After a session has messages, these public start fields are immutable through
`PUT /api/sessions/{id}`:

- provider
- model

Runtime kind, boot profile, recipe source, lifecycle class, and work root are
captured on durable-agent or runtime paths and should be changed by
fork/restart/new-session flows rather than in-place mutation.

## Recipes

Each recipe row includes operator input definitions under `inputs`. The
frontend renders from that schema rather than hard-coding recipe forms.

Dry-run:

```text
POST /api/durable-agent-recipes/{id}/dry-run
```

Apply:

```text
POST /api/durable-agent-recipes/{id}/apply
```

Configured recipe catalogs load from app config:

```yaml
recipes:
  catalog_paths:
    - /path/to/recipes
```

Configured recipes override built-ins by matching `id`. Duplicate IDs across
configured sources are rejected at startup.
