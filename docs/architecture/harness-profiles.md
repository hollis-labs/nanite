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
- **Within one iteration.** Every result an iteration delivers is taken out of
  the remaining context before the next is sized, so the results of one
  multi-tool iteration are bounded together, not each against the same
  pre-iteration figure. The next iteration replaces the estimate with a
  measurement, and an iteration with no measurement leaves the remaining context
  unknown rather than stale.
- **Per-result cap.** A single result is bounded by the preview budget whatever
  the ceiling is, and by the same remaining-context cap, so one result cannot
  overrun a nearly full context. The step-down compact preview is limited the
  same way. The remaining-context cap still applies when
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
such as "wrote", "created", "saved" or "the write succeeded") and the id-shaped
tokens it cites (a ULID, a UUID, a tracker id or a long hex digest). Text is
normalized first: fullwidth and compatibility forms are folded, zero-width and
format characters are dropped, and letters from other scripts that render like
Latin ones, and Unicode dashes, are mapped to ASCII, so an invented id cannot be
hidden with lookalikes. Ids match in any case and are compared in a canonical
one. A token must look like an id, not a word or a number: a ULID-length token
needs digits, and a hex digest needs both a digit and a letter.

An id is taken from the claim's own paragraph. A claim that names its id ("the
returned ID above") may take one from an adjacent paragraph; a claim that does
not may not, so unrelated text next to an id is not paired with it. Every
claiming paragraph counts, so a grounded first claim cannot hide a fabricated
second one. Fenced code, and negated, conditional, future and interrogative
clauses, are not claims, and neither is an id with no write phrase or a phrase
with no id. CLI-launched sessions are skipped, since their tools run outside this
loop.

A claim is unbacked when any id it cites is not one a successful write-capable
tool result returned, in this turn or an earlier turn of the same session. A
successful write in the turn does not license citing other ids, and an id copied
from the user's message does not ground a claim. The scan is clause-scoped: a
negation or modal ("should", "will", "may") disarms only the clause it sits in,
so a write verb earlier in the sentence still counts.

A tool must be recognizable as a writer to ground an id. In order: the discovery,
cache, scratch and computing tools (request_tools, tool_describe, whoami, the
result-cache tools, scratchpad, think, math_eval, datetime and the like) never
count, because a tool that only computes echoes whatever the model gave it,
including an invented id; a tool its server or the name heuristic marks
read-only does not count; a tool marked destructive does; and otherwise a write
verb in its name (write, create, update, transition, send, ingest, deploy, ...)
does. A tool with none of these is treated as not writing. An id that only a read
showed does not back a write claim, and a failed or denied write does not back a
success claim.

Prose written in an iteration that also called tools cannot be sent back: it is
already on screen and the tools have run. It is checked once the turn is over,
against the turn's final facts. A claim there that no write of the turn or the
session backs is logged with action `narration_flagged`; under `deny` the reply
gets a visible footer naming the ids, otherwise a status event is emitted. It is
never blocked or retried.

- `deny` sends the reply back once with a correction message and clears it from
  the client. If the retried reply still carries the claim, the loop does not
  block again: the reply is finalized with a visible footer naming the ids.
  The worst case is one extra model call.
- `warn` and `ask` never block. Both emit a status event; `ask` is a distinct
  recorded decision and behaves like `warn`.

Every decision that found a claim writes an `event_log` row (type
`write_claim_guard`) with the mode, the action taken, the reason
(`unbacked_write_claim`, or one of `write_tool_succeeded_this_turn` and
`claim_ids_grounded_in_a_prior_write_result` when every cited id was grounded),
the ids and the tools that ran, so
the false-positive rate can be tuned from real traffic. Successful write results
log the ids they returned (type `write_result_ids`), which is what lets a later
turn recap earlier work. Reading them back is limited to a session's newest 500
rows and is served in order by an index on the session, event type and id, so a
long session does not slow the check; it runs only when a claim is otherwise
unbacked.

## CLI-launched sessions

A CLI-launched agent's Nanite-tool results are cached and bounded by the
self-tool proxy, sized against a model. Nanite does not choose a CLI's model and
cannot observe it (the CLI wrapper does not report it), so `sessions.model` for
these sessions holds the wrapper's pseudo-model, never the model actually run,
and Nanite does not write a guess there. The proxy sizes against, in order: a
real model on the session; the model declared for that kind of CLI in
`harness.cli_models` in the app config (`claude`, `codex`, `copilot`, `opencode`,
`pi`); otherwise the floor. Only a large-window model changes the result, since a
200K window already gets the floor from the formula. Two mistakes are
unambiguous and stop startup with the problem named: a key that is not a known
CLI kind, and a value that is empty or a CLI wrapper pseudo-model such as
`claude-cli`. A model name the built-in registry does not know is not refused: a
newer model may only be known to the models.dev catalog, which has not synced
when the config is read. It is resolved when it is used, and one that still does
not resolve is sized at the floor with a single warning naming the key and value.
API sessions are unaffected. This is declared, not observed:
reading the model from the CLI's own init event would need the wrapper to expose
it.

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
independent of the `devmode` build tag. Tying a runtime profile to a build tag would
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
