# Harness profiles

The chat loop's limits (how long it may sit idle, how many iterations it may
take, how many tool failures end it, how large a tool-result preview is) are
not constants. Each run resolves them from a named **harness profile**, layered
with the model, the agent and the launch, and records where every value came
from.

This is the host-side realization of the agent-fabric launch profile: the
shared `agentcontracts.Limits` shape carries the limits every host can honor,
and a `harness` block carries the knobs only a host that owns the tool loop can.

## Where a profile lives

A profile is YAML. The built-ins (`default`, `conservative`, `dev`) are embedded
in the binary; nothing is seeded through the database. User profiles are files
named `<name>.yaml` in the profiles directory, `harness.profiles_dir` in the app
config or the `profiles` directory under the config directory. A file may not
reuse a built-in's name, its `name:` must match its file name, and an unknown
key is an error, so a misspelled knob fails instead of being ignored. Files are
re-read when they change; editing a profile applies to the next run.

```yaml
name: tighter
extends: default          # optional; values start from the parent
limits:                   # the shared contract shape
  idle_timeout_ms: 300000
harness:                  # host-only knobs
  hard_ceiling: 100
models:                   # optional, path.Match patterns over the model id
  "claude-opus-*":
    harness: { preview_max_bytes: 64000 }
```

`default` states nothing, so a run without a selection behaves as the harness
did before profiles existed. A parity test pins that.

## What a profile can state

Enforced by the chat loop: `hooks.write_claim_guard`, `limits.idle_timeout_ms`,
`harness.subagent_idle_timeout_ms`, `hard_ceiling`, `consecutive_fail_cap`,
`runaway_fail_cap`, `per_tool_cap`, `max_concurrent_tools`,
`compact_preview_bytes`, `preview_pct`, `preview_min_bytes`, `preview_max_bytes`.

`max_concurrent_tools` bounds how many of a turn's concurrent-safe tool calls
run at once (default 8, clamped to 1..64; the env override is
`NANITE_HARNESS_MAX_CONCURRENT_TOOLS`). Results keep their plan order. A call
still queued when the turn is canceled is not started and gets a canceled
result for its own tool call id; calls already running finish as they would have.

The tool-output ceiling: `limits.tool_output_bytes` and the shaping knobs
`tool_output_pct`, `tool_output_min_bytes`, `tool_output_max_bytes`,
`tool_output_remaining_share` and `tool_output_remaining_floor_bytes`.

Carried and recorded but not yet enforced, and listed as such on the resolved
profile: the rest of `limits` (`max_duration_ms`, `cost_budget`, `token_budget`,
`max_turns`, `max_retries`).

## The tool-output ceiling

The ceiling is the cumulative bytes of tool output a turn may deliver at full
preview size. Once a turn passes it, later results step down to the compact
preview. It is a function of the model, not a constant:

- **Base.** `limits.tool_output_bytes` when stated, in any layer, replaces the
  scaled value. Otherwise it is `tool_output_pct` of the model's window in bytes
  (window tokens times four), clamped to `[tool_output_min_bytes,
  tool_output_max_bytes]`. The minimum is the value the harness used before it
  scaled, so a window of 200K tokens or less, or a model with no window
  information, gets exactly that. `0` means no cumulative ceiling.
- **Remaining context.** Every iteration, after the context budget is enforced,
  the ceiling is also limited to `tool_output_remaining_share` of the bytes still
  free below the loop's own context ceiling, never below
  `tool_output_remaining_floor_bytes`. A nearly full context therefore gets a
  ceiling below the minimum. An explicit value gets this limit too.
- **Per-result cap.** A single result is bounded by the preview budget whatever
  the ceiling is, and by the same remaining-context cap, so one result cannot
  overrun a nearly full context. The remaining-context cap still applies when
  the cumulative ceiling is disabled with `0`; only the cumulative step-down is
  turned off.
- **Legacy names.** `NANITE_TOOL_TURN_CEILING_BYTES` is an environment layer
  alias for `NANITE_HARNESS_TOOL_OUTPUT_BYTES`, which wins when both are set, and
  the `tool_turn_ceiling_bytes` extended user setting is an app-settings layer.
  Both keep their `0` meaning.

## Selecting a profile

A session selects a profile and may add per-launch overrides through its
metadata: `harness_profile` and `harness_overrides` (the same `limits` /
`harness` shape). Session creation accepts them as `harness_profile` and
`harness_overrides` and refuses an unknown profile or an invalid override then,
instead of failing every turn. The default for a session that selects nothing is
`harness.profile` in the app config, or `NANITE_HARNESS_PROFILE`, or `default`.

An unknown or invalid profile ends the turn with the reason. It is never
replaced by a default.

## How a value is decided

Lowest to highest:

1. computed defaults, which are the harness's compiled-in values, scaled by the
   model where a knob scales with it,
2. app settings, meaning the existing `user_settings` values,
3. the selected profile and its `extends` chain, base first,
4. per-model blocks of that chain whose pattern matches the model, the most
   specific pattern last,
5. per-agent overrides, the agent's stored constraints,
6. per-launch overrides from the session,
7. environment overrides, `NANITE_HARNESS_<KNOB>`,
8. host maximum clamps, applied to every value last.

A per-model block on a base profile outranks a child profile's plain value,
because model blocks are applied after the whole chain's plain values. A profile
that states a knob outranks the user setting for it; one that does not
leaves the setting in force. A clamp is recorded on the value it changed, with
what was asked for, and the value keeps the source of the layer that asked.

Every resolved value carries its source (`computed`, `app-settings`,
`profile:<name>`, `profile:<name>/model:<pattern>`, `agent`, `launch`,
`env:<VAR>`), and the profile carries a digest over its whole definition, so a
run can be attributed to exactly the profile content it used.

## The write-claim guard

`hooks.write_claim_guard` sets how strictly the guard acts: `off`, `warn`,
`ask` or `deny`. It is `deny` by default and `warn` in the `dev` profile, and any
layer, including the environment (`NANITE_HARNESS_WRITE_CLAIM_GUARD`), can
override either.

The guard is a go-hooks `Stop` implementation. When a reply is about to be
finalized in an API-driven run, it looks for a completed-write claim (a phrase
such as "wrote", "created", "saved" or "the write succeeded") in the same or an
adjacent paragraph as an id-shaped token (a ULID, a UUID, a tracker id or a long
hex digest). Fenced code, negated, conditional, future and interrogative
sentences are not claims, and neither is an id with no write phrase or a phrase
with no id. CLI-launched sessions are skipped, since their tools run outside this
loop.

A claim is unbacked when no write-capable tool call succeeded in the turn and
the cited id is not one an earlier successful write returned in the same session.
A tool is write-capable unless it is known not to be: request_tools,
tool_describe, tool_list, tool_validate, whoami, the result-cache tools and the
scratchpad are never writes, and neither is a tool its server or the name
heuristic marks read-only. An unknown tool counts as a write, so the guard errs
toward silence. An id that only a read showed does not back a write claim, and a
failed write does not back a success claim.

- `deny` sends the reply back once with a correction message and clears it from
  the client. If the retried reply still carries the claim, the loop does not
  block again: the reply is finalized with a visible footer naming the ids.
  The worst case is one extra model call.
- `warn` and `ask` never block. Both emit a status event; `ask` is a distinct
  recorded decision that a later release may turn into an operator prompt, and
  behaves like `warn` today.

Every decision that found a claim writes an `event_log` row (type
`write_claim_guard`) with the mode, the action taken, the reason
(`unbacked_write_claim`, `write_tool_succeeded_this_turn`,
`claim_ids_grounded_in_a_prior_write_result`), the ids and the tools that ran, so
the false-positive rate can be tuned from real traffic. Successful write results
log the ids they returned (type `write_result_ids`), which is what lets a later
turn recap earlier work.

## Recording and diagnostics

Each turn's `execution_metrics` row records the profile name, the profile
digest, and the effective values with the layer that supplied each (`profile_name`,
`profile_digest`, `effective_limits_json`), so a result can be attributed to the
exact profile content it ran under. They are returned by
`GET /api/sessions/{id}/metrics` and, in developer mode, attached to the turn's
inspector snapshot. Rows written before the columns existed, and utility calls,
carry none.

`GET /api/sessions/{id}/harness-profile` resolves what the session's next turn
would run under, from the session's selection, its agent's constraints, the user
settings and the model (`?model=` overrides it). `nanite profile list` and
`nanite profile show <name> [--model M] [--json]` resolve a profile from the
files and the current environment, without a server; they cannot show the
session, agent and user-settings layers, which the endpoint does.

## dev is a profile, not a build mode

`dev` is an ordinary named profile chosen the same way as any other. It is
independent of the `devmode` build tag, which only disables plugin signature
verification and must never ship. Tying a runtime profile to a build tag would
make shipped and development builds behave differently in ways nobody selected.

## Verify

```bash
# the profile files a checkout ships, and the resolution order in code
ls internal/harnessprofile/builtin
nanite profile show conservative --model claude-opus-5
grep -n 'w.apply(\|w.clamp()' internal/harnessprofile/resolve.go
# the tests that pin precedence, sources, clamps and parity with today's defaults
go test ./internal/harnessprofile ./internal/service -run 'Harness|Precedence|Clamp'
```
