# Getting Nanite running

A runbook for a fresh database with API auth ON: build, serve, define an agent,
run one API agent and one CLI agent, each through a turn. `scripts/smoke-agents.sh`
does the same sequence unattended.

`nanite agent setup` creates each agent; its output supplies the session request.

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

## 4. Author a definition

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

## 6. Set up an agent, create a session, send a turn

After the server starts and records its tool catalog, setup installs the
pinned definition, creates host settings and records tool grants. Use the same
database as the server. Give each runtime a distinct slug:

```sh
api() { curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
SETUP=$(./nanite agent setup agent.md --runtime api --provider anthropic \
  --model '<model id>' --slug api-agent --tools all --reason 'local validation')
SID=$(api -X POST localhost:$NANITE_PORT/api/agent/v1/sessions \
  -d "$(echo "$SETUP" | jq -c '.session_request.body')" | jq -r .session_view_id)
api -X POST localhost:$NANITE_PORT/api/agent/v1/sessions/$SID/turns \
  -d '{"content":[{"kind":"text","text":"What is 17 times 3? Reply with the number only."}],"delivery":"at_idle"}'
api localhost:$NANITE_PORT/api/agent/v1/sessions/$SID/messages | jq '.items[]|{role,content}'
```

Poll messages until the assistant replies `51`. For Claude or Codex, use the
same session and turn commands with one of these setup outputs:

```sh
SETUP=$(./nanite agent setup agent.md --runtime cli --provider claude \
  --model haiku --slug claude-agent --tools all --reason 'local validation')
SETUP=$(./nanite agent setup agent.md --runtime cli --provider codex \
  --slug codex-agent --tools all --reason 'local validation')
```

CLI agents use their CLI login, without an API-key preflight. Omitting `--model`
lets the CLI select its default. Setup creates a private work directory beside
the database; use `--directories /absolute/work,/absolute/shared` to choose the
work root and additional directories. Tools are explicitly granted by `--tools`;
`all` means the available catalog, not a policy bypass.

## 7. Stop and resume

Cancel a running turn with `POST /api/agent/v1/sessions/{id}/turns/{turnId}/cancel`;
the session answers the next turn. A session keeps its history: after the server
restarts, send another turn to the same `session_view_id`. A CLI session resumes
from the provider's session, or from Nanite's recovery pack when the provider
cannot resume. The smoke script exercises restart recovery and cancellation for both runtimes.

## Work root, listing and stopping

A CLI session runs in the first host directory unless the create request names
`"work_root": "/abs/path"`. The path must be absolute, exist, and sit inside one
of the host's `directories`; otherwise create answers `422` with
`error.cause` `work_root_invalid` (or `work_root_unsupported` when the host is
not a CLI host). The work root is also the first directory the process is
given.

```sh
nanite agent list [--all]        # sessions on the running server (--all includes ended)
nanite agent stop <session>      # same as POST /api/agent/v1/sessions/{id}/stop
```

Stop cancels a running or queued turn, closes the agent process and marks the
session ended. The response says `stopped` (false on a repeat call, which
changes nothing) and `turn_canceled`. An ended session refuses new turns with
`409 invalid_state`. Both commands call the running server, so point them at it
with `NANITE_API_URL` or `NANITE_PORT`.

## Known refusals

| Step | Symptom | Cause |
|---|---|---|
| session create, `runtime=cli` | `422`, `error.cause` `host_directories_missing` | the CLI host settings have no `directories` |
| session create, `runtime=cli` | `422`, `error.cause` `cli_host_unsupported_field` | the host sets `mcp_servers`, `harness_profile`, a non-empty `native_loop`, `recall_*`, `protocol` or `transport`, or a relative directory |
| session create, `runtime=cli` | `422`, `error.cause` `read_only_unenforceable` | the definition asks for a read-only permission profile, which a CLI process cannot enforce |
| session create, `runtime=cli` | `422`, `error.cause` `provider_not_launchable` | no provider, or one Nanite cannot launch |
| session create | `422`, `error.cause` `work_root_invalid` / `work_root_unsupported` | `work_root` is relative, missing, not a directory, outside the host directories, or the host is not a CLI host |
| turn | `409 invalid_state` | the session was stopped; create a new session |
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
