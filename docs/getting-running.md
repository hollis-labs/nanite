# Getting Nanite running

A runbook for a fresh database with API auth ON: build, serve, define an agent,
run one API agent and one CLI agent, each through a turn. `scripts/smoke-agents.sh`
does the same sequence unattended.

`nanite agent setup` creates each agent; its output supplies the session request.

## 0. Prerequisites

- Go (see `go.mod`), `jq`, `curl`.
- For the CLI agent: a logged-in provider CLI. `claude` by default; `codex`,
  `opencode` and `agy` also launch (`SMOKE_CLI_PROVIDER`). Run it once
  interactively to log in. `agy` runs under a private HOME with its own
  `.gemini` (`~/.nanite/agy-gemini/<agent>/`) that links your real login files; your
  real `~/.gemini` is not written (see `docs/architecture/agent-runtime.md`).
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

## Give an agent a Tether address (optional)

Enrollment gives an agent a durable `msg://agent/agent-mux/agt_...` address that
other agents can mail. It is explicit, optional and off the setup path: Nanite
runs without Tether, and `agent setup` never contacts it. Design and behavior:
`docs/architecture/tether-durable-actor.md`.

Live enrollment needs a **scoped `tether.registry` credential (`registry.write`
only) that you issue** on the Tether side. Nanite never issues or rotates one.
It is a different credential from the delivery bridge's messages/read token
(`NANITE_TETHER_TOKEN_FILE`) and is never read from it.

```sh
export NANITE_TETHER_ADDR=http://127.0.0.1:PORT      # the Tether daemon (also enables the bridge)
install -m 600 /dev/null ~/.nanite/tether-registry-token
pbpaste > ~/.nanite/tether-registry-token             # the token, from the clipboard; never an argument
export NANITE_TETHER_REGISTRY_TOKEN_FILE=~/.nanite/tether-registry-token
```

Set both variables in the environment of the process that enrolls: `nanite
serve` for the HTTP route and MCP tool, your shell for the CLI. The file must be
mode 0600 (any group or world permission is refused), is read on each call and
is never logged or stored in the database.

```sh
./nanite agent enroll <host-settings-id> --reason "takes part in messaging"
# or, with an admin credential: POST /api/native-agents/<id>/enroll {"reason": "..."}
```

The answer carries the `urn`. Repeating the call returns the same URN
(`already_enrolled`), and if a call is interrupted, repeat it: it resumes. A
Tether that is down answers `tether_unreachable` (retry later); a missing,
insecure or absent token or address answers a typed error and writes nothing.
Enrollment needs the `admin` scope; `agents:manage` alone is refused.

Once enrolled, a session started from the agent's host settings becomes the
actor's current session (the bridge then delivers its mail), and ending the
session releases it.

To retire the actor, retire the agent: `host_settings_retire` (MCP) or
`POST /api/agent-host-settings/{id}/retire`. For an enrolled agent this needs an
**admin** credential (it changes Tether's registry); `agents:manage` and
`maintain` are refused with `enrolled_retire_requires_admin`, and agents that
were never enrolled retire as before. The actor is archived and made unroutable
at once, then deregistered in Tether. If Tether is down the response shows
`actor.deregister_pending: true`; `nanite serve` retries in the background
(shortly after start, then every 15 minutes, backing off from 15 minutes to 2
hours per agent) and the next retire call retries too.

Rules to know:

- A retired address is never reused. Enrolling a retired agent again answers
  `409`; to give the agent a new address, create a new host settings row (a new
  id), which gets a new key and URN.
- Deleting an enrolled agent's host settings is refused (`409`,
  `enrolled_host_cannot_be_deleted`): retire it first.
- `PUT ... enabled=false` is a reversible disable that leaves the actor as it
  is; setting `enabled=true` again restores the host with the same URN. Only
  retire ends an actor.

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
`409 invalid_state`. Both commands call the running server, not a database: `-db` does not choose
the server. Point them at it with `--url`, `NANITE_API_URL` or `NANITE_PORT`;
`list` prints the server it asked and `stop` prints it on stderr. `list` follows
every page. Stop and `work_root` need the operate scope, the same as sending a
turn or `DELETE /api/sessions/{id}`; an agent credential cannot reach them.

## Known refusals

| Step | Symptom | Cause |
|---|---|---|
| session create, `runtime=cli` | `422`, `error.cause` `host_directories_missing` | the CLI host settings have no `directories` |
| session create, `runtime=cli` | `422`, `error.cause` `cli_host_unsupported_field` | the host sets `mcp_servers` on a provider other than `claude` or `agy` (or names a server that is missing, disabled, non-stdio or called `nanite`), `harness_profile`, a non-empty `native_loop`, `recall_*`, `protocol` or `transport`, or a relative directory |
| session create, `runtime=cli` | `422`, `error.cause` `read_only_unenforceable` | the definition asks for a read-only permission profile, which a CLI process cannot enforce |
| session create, `runtime=cli` | `422`, `error.cause` `provider_not_launchable` | no provider, or one Nanite cannot launch |
| session create, `runtime=api` | `422`, `error.cause` `host_directory_invalid` | the host lists a relative directory |
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
