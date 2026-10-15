# Getting Nanite running

A runbook for a fresh database with API auth ON: build, serve, define an agent,
run one API agent and one CLI agent, each through a turn. `scripts/smoke-agents.sh`
does the same sequence unattended.

Status (CW-20261014-0163, scaffold): the API-agent path below was run end to
end on `integ/nanite-usable`. The CLI-agent path needs `runtime=cli` in the
definition flow (B2, CW-20261014-0161) and is marked **[after B2]**. Today it
is refused; see "Known refusals". `nanite agent setup` (A2) is not on this
branch, so agents are set up with the definition and host-settings steps that
it will wrap.

## 0. Prerequisites

- Go (see `go.mod`), `jq`, `curl`.
- For the CLI agent: a logged-in `claude` CLI (`claude --version`, run once
  interactively to log in).
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
./nanite agent install agent.md        # prints {"id","revision","digest"}
```

## 5. Serve with auth ON

```sh
./nanite serve -port "$NANITE_PORT" &            # no --insecure-no-auth
TOKEN=$(cat "$(./nanite auth token-path)")        # operator token, never printed here
curl -s -o /dev/null -w '%{http_code}\n' localhost:$NANITE_PORT/api/agent-host-settings   # 401
```

Every call below sends `Authorization: Bearer $TOKEN` (the `api` function in step 6).

## 6. Host settings, session, turn

Host settings pick the runtime, provider and model for a pinned definition.
The pin from step 4 is `{id, revision, digest}` on the host-settings route and
`{definition_id, revision, semantic_digest}` on the session route.

```sh
api() { curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
PIN=$(./nanite agent install agent.md)
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

The reply is the assistant message (`51`). Host settings are
`{"runtime":"cli","provider":"claude","model":"haiku","native_loop":{}}` for the
CLI agent **[after B2]**; everything else is identical.

## 7. Stop and resume **[verify]**

Cancel a running turn with `POST /api/agent/v1/sessions/{id}/turns/{turnId}/cancel`.
A session keeps its history; send another turn to the same `session_view_id`
after the server restarts. Not yet exercised by the smoke script.

## Known refusals

| Step | Symptom | Cause |
|---|---|---|
| session create, `runtime=cli` | `422 unsupported_selection`, "definition semantics unsupported by native chat" | native sessions accept only `runtime=api` until B2; the message hides the cause |
| host settings create | `400 invalid host settings request` | the body has an unknown field (e.g. `source`); the server assigns it |
| session create | `404 sql: no rows` | stale `host_settings.revision`; use the revision returned by create |

## Smoke test

```sh
scripts/smoke-agents.sh                                   # API half runs when SMOKE_API_MODEL is set
SMOKE_API_MODEL=<model id> scripts/smoke-agents.sh
SMOKE_CLI=1 SMOKE_API_MODEL=<model id> scripts/smoke-agents.sh   # [after B2]
```
