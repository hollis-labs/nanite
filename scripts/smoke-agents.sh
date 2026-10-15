#!/usr/bin/env bash
# Smoke test for "Nanite is running": one API agent and one CLI agent each
# complete a turn on a scratch database with API auth ON (CW-20261014-0163).
#
#   scripts/smoke-agents.sh                 # API half (needs a provider key), CLI half skipped
#   SMOKE_API_PROVIDER=anthropic SMOKE_API_MODEL=<model> ANTHROPIC_API_KEY=... scripts/smoke-agents.sh
#   SMOKE_CLI=1 scripts/smoke-agents.sh     # also the CLI half (needs B2 and a logged-in claude)
#
# Key resolution is the real one (provider key_ref, then the keychain, then the
# environment when NANITE_PROVIDER_KEY_ENV=1 as set below), so a key already in
# your keychain is used before ANTHROPIC_API_KEY. Everything else lives in a temp dir that is removed on exit. The script never
# prints a key or a token. It uses `serve` without --insecure-no-auth.
set -euo pipefail
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

root=$(cd "$(dirname "$0")/.." && pwd)
port=${SMOKE_PORT:-18090}
api_provider=${SMOKE_API_PROVIDER:-anthropic}
api_model=${SMOKE_API_MODEL:-}
work=$(mktemp -d "${TMPDIR:-/tmp}/nanite-smoke.XXXXXX")
db=$work/smoke.db
base=http://127.0.0.1:$port
bin=${NANITE_BIN:-$work/nanite}
pass=0; fail=0; skip=0
srv=

say()  { printf '%s\n' "$*"; }
ok()   { pass=$((pass+1)); say "PASS  $*"; }
bad()  { fail=$((fail+1)); say "FAIL  $*"; }
skp()  { skip=$((skip+1)); say "SKIP  $*"; }
cleanup() { [ -n "$srv" ] && kill "$srv" 2>/dev/null || true; if [ -n "${SMOKE_KEEP:-}" ]; then say "kept $work"; else rm -rf "$work"; fi; }
trap cleanup EXIT

[ -x "$bin" ] || (cd "$root" && go build -o "$bin" ./cmd/nanite)
export NANITE_DB_PATH=$db NANITE_API_URL=$base NANITE_PORT=$port
# The environment is only read for provider keys when this is set.
export NANITE_PROVIDER_KEY_ENV=1

# 1. definition (offline install straight into the scratch DB)
cat >"$work/def.md" <<'DEF'
---
schema_version: "2"
definition_id: def:smoke-agent
revision: "1"
name: smoke-agent
description: Smoke test agent.
behavior:
  purpose: Do exactly what is asked, briefly.
requirements: {}
harness_profile:
  context: {}
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Follow the user's instruction exactly and briefly.
DEF
# install prints more than the pin (name, identity_digest); the routes take {id, revision, digest}.
pin=$("$bin" agent install -db "$db" "$work/def.md" | jq -c '{id, revision, digest}') && ok "definition installed" || { bad "definition install"; exit 1; }

# 2. serve with auth ON
start_server() {
  "$bin" serve -db "$db" -port "$port" -dev >>"$work/serve.log" 2>&1 &
  srv=$!
  for _ in $(seq 1 60); do curl -fs "$base/api/health" >/dev/null 2>&1 && break; sleep 0.5; done
}
start_server
curl -s -o /dev/null -w '%{http_code}' "$base/api/agent-host-settings" | grep -q '^401$' \
  && ok "unauthenticated request refused (auth is ON)" || bad "expected 401 without a token"
tok=$(cat "$("$bin" auth token-path -db "$db")")
auth=(-H "Authorization: Bearer $tok")
# SMOKE_YOLO=1 puts the permission engine in yolo mode (reset by a restart, so
# set_yolo runs again after each start). Only for exercising yolo-gated launches.
set_yolo() {
  [ -n "${SMOKE_YOLO:-}" ] || return 0
  curl -s -o /dev/null "${auth[@]}" -H 'Content-Type: application/json' -X PUT "$base/api/permissions/mode" -d '{"mode":"yolo"}'
}
set_yolo

# wait_reply SESSION PATTERN LABEL: poll history for an assistant message matching PATTERN
wait_reply() {
  local sid=$1 pat=$2 label=$3 h
  for _ in $(seq 1 90); do
    sleep 2
    h=$(curl -s "${auth[@]}" "$base/api/agent/v1/sessions/$sid/messages")
    # Only an assistant message counts.
    printf '%s' "$h" | jq -e --arg p "$pat" '[.items[] | select(.role=="assistant") | .content] | any(test($p))' >/dev/null 2>&1 && { ok "$label"; return 0; }
  done
  bad "$label: no matching reply within 180s; last assistant message: $(printf '%s' "$h" | jq -r '[.items[] | select(.role=="assistant") | .content] | last // "none" | .[0:240]' 2>/dev/null)"; return 1
}

# resume_check SESSION: restart the server, then ask something only the earlier
# turn can answer. The CLI's own session (provider_session_id) must carry it.
resume_check() {
  local sid=$1
  kill "$srv" 2>/dev/null || true; wait "$srv" 2>/dev/null || true
  start_server
  set_yolo
  curl -s "${auth[@]}" -H 'Content-Type: application/json' -X POST "$base/api/agent/v1/sessions/$sid/turns" \
    -d '{"content":[{"kind":"text","text":"Add 1 to your previous answer. Reply with the number only."}],"delivery":"at_idle"}' >/dev/null
  wait_reply "$sid" 52 "cli: resumed after a server restart with context"
}

# send_turn SESSION TEXT: post a turn, print the response body
send_turn() {
  curl -s "${auth[@]}" -H 'Content-Type: application/json' -X POST "$base/api/agent/v1/sessions/$1/turns" \
    -d "$(jq -nc --arg t "$2" '{content:[{kind:"text",text:$t}],delivery:"at_idle"}')"
}

# stop_check SESSION: cancel a running turn, then expect the session to answer the next one.
stop_check() {
  local sid=$1 turn tid code
  turn=$(send_turn "$sid" "Count from 1 to 400 as a comma separated list, nothing else.")
  tid=$(printf '%s' "$turn" | jq -r '.stream_url' | sed -n 's#.*/turns/\([^/]*\)/.*#\1#p')
  sleep 4
  code=$(curl -s -o /dev/null -w '%{http_code}' "${auth[@]}" -X POST "$base/api/agent/v1/sessions/$sid/turns/$tid/cancel")
  case "$code" in 2??) ok "cli: running turn cancelled ($code)";; *) bad "cli: cancel returned $code";; esac
  sleep 3
  send_turn "$sid" "Reply with the single word BANANA." >/dev/null
  wait_reply "$sid" BANANA "cli: session answers after a cancel"
}

# mcp_check SESSION: the Nanite MCP server must be visible to the CLI.
mcp_check() {
  local sid=$1
  send_turn "$sid" "Call your MCP tool named agent_list (it may be prefixed with the server name nanite). Reply with TOOL_OK if the call ran, TOOL_MISSING if you have no such tool, or TOOL_REFUSED if it was refused, then one short reason." >/dev/null
  wait_reply "$sid" TOOL_OK "cli: Nanite MCP tool callable" || true
}

# one agent: host settings -> session -> turn -> reply
run_agent() { # name settings-json
  local name=$1 settings=$2 host hid hrev sid sref turn
  host=$(curl -s "${auth[@]}" -H 'Content-Type: application/json' -X POST "$base/api/agent-host-settings" \
    -d "{\"title\":\"smoke $name\",\"slug\":\"smoke-$name\",\"definition_ref\":$pin,\"enabled\":true,\"settings\":$settings}")
  hid=$(printf '%s' "$host" | jq -r '.id // empty' 2>/dev/null || true)
  hrev=$(printf '%s' "$host" | jq -r '.revision // empty' 2>/dev/null || true)
  # The session route names the same pin definition_id / semantic_digest.
  sref=$(printf '%s' "$pin" | jq -c '{definition_id: .id, revision, semantic_digest: .digest}')
  [ -n "$hid" ] && ok "$name: host settings created" || { bad "$name: host settings: $(printf '%s' "$host" | head -c 300)"; return; }
  sid=$(curl -s "${auth[@]}" -H 'Content-Type: application/json' -X POST "$base/api/agent/v1/sessions" \
    -d "{\"definition_ref\":$sref,\"host_settings\":{\"id\":\"$hid\",\"revision\":\"$hrev\"}}")
  case "$sid" in *session_view_id*) ok "$name: session created";; *) bad "$name: session: $(printf '%s' "$sid" | head -c 300)"; return;; esac
  sid=$(printf '%s' "$sid" | jq -r '.session_view_id')
  turn=$(curl -s "${auth[@]}" -H 'Content-Type: application/json' -X POST "$base/api/agent/v1/sessions/$sid/turns" \
    -d '{"content":[{"kind":"text","text":"What is 17 times 3? Reply with the number only."}],"delivery":"at_idle"}')
  say "      turn accepted: $(printf '%s' "$turn" | head -c 120)"
  wait_reply "$sid" 51 "$name: turn completed with a reply" || return
  if [ "$name" = cli ]; then
    resume_check "$sid"
    stop_check "$sid"
    [ -n "${SMOKE_MCP:-}" ] && mcp_check "$sid"
  fi
}

# 3. API agent
if [ -n "$api_model" ] && "$bin" provider status "$api_provider" >/dev/null 2>&1; then
  run_agent api "{\"version\":\"1\",\"runtime\":\"api\",\"provider\":\"$api_provider\",\"model\":\"$api_model\",\"native_loop\":{}}"
else
  skp "api agent: set SMOKE_API_MODEL and provide a key for '$api_provider' (e.g. ANTHROPIC_API_KEY)"
fi

# 4. CLI agent (runtime=cli lands with B2, CW-20261014-0161)
if [ -n "${SMOKE_CLI:-}" ]; then
  # A CLI agent's work root is the first host directory.
  mkdir -p "$work/cli-work"
  cli_model=${SMOKE_CLI_MODEL-haiku}
  run_agent cli "{\"version\":\"1\",\"runtime\":\"cli\",\"provider\":\"${SMOKE_CLI_PROVIDER:-claude}\",\"model\":\"$cli_model\",\"directories\":[\"$work/cli-work\"],\"native_loop\":{}}"
else
  skp "cli agent: set SMOKE_CLI=1 once runtime=cli (B2) is merged and claude is logged in"
fi

say "---- pass=$pass fail=$fail skip=$skip"
[ "$fail" -eq 0 ]
