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

Enforced by the chat loop: `limits.idle_timeout_ms`,
`harness.subagent_idle_timeout_ms`, `hard_ceiling`, `consecutive_fail_cap`,
`runaway_fail_cap`, `per_tool_cap`, `compact_preview_bytes`, `preview_pct`,
`preview_min_bytes`, `preview_max_bytes`.

Carried and recorded but not yet enforced, and listed as such on the resolved
profile: the rest of `limits` (`max_duration_ms`, `cost_budget`, `token_budget`,
`max_turns`, `max_retries`, `tool_output_bytes`). The tool-output ceiling reads
`tool_output_bytes` once it consumes it.

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

A profile that states a knob outranks the user setting for it; one that does not
leaves the setting in force. A clamp is recorded on the value it changed, with
what was asked for, and the value keeps the source of the layer that asked.

Every resolved value carries its source (`computed`, `app-settings`,
`profile:<name>`, `profile:<name>/model:<pattern>`, `agent`, `launch`,
`env:<VAR>`), and the profile carries a digest over its whole definition, so a
run can be attributed to exactly the profile content it used.

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
