# Getting Nanite running

A runbook for a fresh database with API auth ON: build, serve, define an agent,
run one API agent and one CLI agent, each through a turn. `scripts/smoke-agents.sh`
does the same sequence unattended.

Host settings and sessions are created with the definition and host-settings
steps below.

## 0. Prerequisites

- Go (see `go.mod`), `jq`, `curl`.
- For the CLI agent: a logged-in provider CLI. `claude` by default; `codex`,
  `opencode` and `agy` also launch (`SMOKE_CLI_PROVIDER`). Run it once
  interactively to log in.
- For the API agent: a provider key (below).

## 1. Build

```sh
go build -o nanite ./cmd/nanite
```

## 2. Pick a scratch database

Every offline command needs to know the database. For a throwaway run:

```sh
export NANITE_DB_PATH=$(mktemp -d)/nanite.db
export NANITE_PORT=8090
```

## 3. Provider key

Keys are references, not stored values (`docs/provider-and-agent-configuration.md`).
Resolution order: the provider's `key_ref`, the keychain entry
`keychain://nanite/provider/<id>`, then, only when `NANITE_PROVIDER_KEY_ENV=1`,
the environment (`ANTHROPIC_API_KEY`, ...). Either save one, or use the env:

```sh
pbpaste | ./nanite provider set-key anthropic      # reads stdin, never an argument
# or, for this shell only:
export NANITE_PROVIDER_KEY_ENV=1 ANTHROPIC_API_KEY=...
./nanite provider status anthropic                 # exits 1 with a reason if unusable
```

A key already in your keychain wins over the environment.

## 4. Install a definition

```sh
cat > agent.md <<'DEF'
---
schema_version: "2"
definition_id: def:getting-running
revision: "1"
name: getting-running
description: Runbook agent.
behavior:
  purpose: Answer with the number only.
requirements: {}
harness_profile:
  context: {}
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Answer arithmetic questions with the number only.
DEF
./nanite agent install agent.md        # prints the pin: id, revision, digest (and name, identity_digest)
```

## 5. Serve with auth ON

```sh
./nanite serve -port "$NANITE_PORT" &            # no --insecure-no-auth
TOKEN_FILE=$(./nanite auth token-path)
until [ -s "$TOKEN_FILE" ]; do sleep 0.5; done    # the server writes the token at startup
TOKEN=$(cat "$TOKEN_FILE")                        # operator token, never printed here
until curl -s -o /dev/null localhost:$NANITE_PORT/api/agent-host-settings; do sleep 0.5; done
curl -s -o /dev/null -w '%{http_code}\n' localhost:$NANITE_PORT/api/agent-host-settings   # 401
```

Every call below sends `Authorization: Bearer $TOKEN` (the `api` function in step 6).

## 6. Host settings, session, turn

Host settings pick the runtime, provider and model for a pinned definition.
The pin from step 4 is `{id, revision, digest}` on the host-settings route and
`{definition_id, revision, semantic_digest}` on the session route.

```sh
api() { curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
PIN=$(./nanite agent install agent.md | jq -c '{id, revision, digest}')
# API agent
HOST=$(api -X POST localhost:$NANITE_PORT/api/agent-host-settings -d "{
  \"title\":\"api agent\",\"slug\":\"api-agent\",\"definition_ref\":$PIN,\"enabled\":true,
  \"settings\":{\"version\":\"1\",\"runtime\":\"api\",\"provider\":\"anthropic\",\"model\":\"<model id>\",\"native_loop\":{}}}")
SREF=$(echo "$PIN" | jq -c '{definition_id:.id,revision,semantic_digest:.digest}')
SID=$(api -X POST localhost:$NANITE_PORT/api/agent/v1/sessions -d "{
  \"definition_ref\":$SREF,
  \"host_settings\":{\"id\":\"$(echo "$HOST"|jq -r .id)\",\"revision\":\"$(echo "$HOST"|jq -r .revision)\"}}" | jq -r .session_view_id)
api -X POST localhost:$NANITE_PORT/api/agent/v1/sessions/$SID/turns \
  -d '{"content":[{"kind":"text","text":"What is 17 times 3? Reply with the number only."}],"delivery":"at_idle"}'
api localhost:$NANITE_PORT/api/agent/v1/sessions/$SID/messages | jq '.items[]|{role,content}'
```

The reply is the assistant message (`51`).

For a CLI agent only the settings differ. A CLI host needs `directories`; the
first is the agent's work root and the rest are added to its reach. Provider and
model are both required:

```sh
mkdir -p /tmp/cli-work
HOST=$(api -X POST localhost:$NANITE_PORT/api/agent-host-settings -d "{
  \"title\":\"cli agent\",\"slug\":\"cli-agent\",\"definition_ref\":$PIN,\"enabled\":true,
  \"settings\":{\"version\":\"1\",\"runtime\":\"cli\",\"provider\":\"claude\",\"model\":\"haiku\",
    \"directories\":[\"/tmp/cli-work\"],\"native_loop\":{}}}")
```

Create the session and send the turn exactly as above. `codex`, `opencode` and
`agy` take their own model ids. Nanite's MCP tools reach claude and codex; agy
gets them only when the permission mode is yolo, and opencode not yet.

## 7. Stop and resume

Cancel a running turn with `POST /api/agent/v1/sessions/{id}/turns/{turnId}/cancel`;
the session answers the next turn. A session keeps its history: after the server
restarts, send another turn to the same `session_view_id`. A CLI session resumes
from the provider's session, or from Nanite's recovery pack when the provider
cannot resume. The smoke script exercises both for the CLI agent.

## Known refusals

| Step | Symptom | Cause |
|---|---|---|
| session create, `runtime=cli` | `422`, `error.cause` `host_directories_missing` | the CLI host settings have no `directories` |
| session create, `runtime=cli` | `422`, `error.cause` `cli_host_unsupported_field` | the host sets `mcp_servers`, `harness_profile`, a non-empty `native_loop`, `recall_*`, `protocol` or `transport`, or a relative directory |
| session create, `runtime=cli` | `422`, `error.cause` `read_only_unenforceable` | the definition asks for a read-only permission profile, which a CLI process cannot enforce |
| session create, `runtime=cli` | `422`, `error.cause` `provider_not_launchable` | no provider, or one Nanite cannot launch |
| turn, `runtime=cli` | `host_revision_changed` | the host settings were edited after the session was created; create a new session |
| host settings create | `400 invalid host settings request` | the body has an unknown field (e.g. `source`); the server assigns it |
| session create | `404 sql: no rows` | stale `host_settings.revision`; use the revision returned by create |

## Smoke test

```sh
scripts/smoke-agents.sh                                   # API half runs when SMOKE_API_MODEL is set
SMOKE_API_MODEL=<model id> scripts/smoke-agents.sh
SMOKE_CLI=1 SMOKE_API_MODEL=<model id> scripts/smoke-agents.sh
SMOKE_CLI=1 SMOKE_CLI_PROVIDER=codex SMOKE_CLI_MODEL=<model id> scripts/smoke-agents.sh
```
