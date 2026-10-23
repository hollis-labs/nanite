# Agent alignment: one directory, one launch model, live or pinned

How a Nanite agent is authored, what a launch materializes from it, and when an
edit takes effect. The same agent runs on Nanite's native loop against an API
provider or on top of a CLI runtime, and behaves the same either way: same
workspace, same instructions, same skills, same tool grants, same wake. The
model is shared with Tether so an agent authored once can be launched by either.

This document is the target. `agent-runtime.md`, `native-agent-api.md` and
`native-authority.md` describe the parts that exist.

## The agent directory

The unit of authoring is a directory. It is the source; database rows, catalog
entries and planted boot directories are projections of it.

```
<agent>/
  agent.md                  agentdef v2: behavior, requirements, harness policy, continuity
  host.yaml                 host profile: where and how it runs (below)
  skills/<name>/SKILL.md    one folder per skill; the whole folder is the skill
  context/...               files that agent.md lists as pinned resources
```

| Part | Authored or derived | Owns |
|---|---|---|
| `agent.md` | authored | what the agent is, independent of any host (agentdef v2: instructions, requirements, harness policy, continuity). The parser refuses host knobs by design. |
| `host.yaml` | authored | how this host runs it: `runtime`, `provider`, `model`, `directories`, `mode`, plus runtime-specific fields |
| `skills/`, `context/` | authored | content that `agent.md` references with a pinned digest; the digest covers the whole packaged tree |
| definition pin | derived | `definition_id`, `revision`, semantic digest, computed from the bytes by the shared agentdef digester |
| host settings row | derived | the revisioned projection of `host.yaml` |
| tool authority | **neither** | granted by the operator through the native-authority routes or setup; never authored in the directory and never implied by a pin |
| boot root | derived | per-session, private, removed at session end |

### `host.yaml`

It is the file form of the existing host settings document, version `"1"`, so
nothing new is invented for the database: the file is parsed into the same
validated struct.

| Field | Meaning |
|---|---|
| `runtime` | `api` (Nanite's native loop) or `cli` (a CLI runtime driven by Nanite) |
| `provider`, `model` | for `api`, a registered API provider and model; for `cli`, the runtime (`claude`, `codex`, `opencode`, `agy`) and the model flag it accepts |
| `directories` | the filesystem the agent may use; the first entry is its work root. Required for `cli`; the same meaning for `api` (see Launch materialization) |
| `mode` | `live` or `pinned` (below). Absent means the default rule |
| `native_loop`, `recall_*` | `api` only. A non-empty value on a `cli` host is refused with a stated cause |
| `mcp_servers` | extra MCP servers for the session; refused until planting supports them |

A relative directory is refused for `cli`. Authority over the filesystem comes from this
file, not from a request.

## Execution modes

One definition, three ways to run it. Which one is a property of the host
profile, not of the definition.

| Mode | `runtime` | Who runs the loop | Notes |
|---|---|---|---|
| Native loop | `api` | Nanite | the model is called through a Nanite provider; tools run in-process |
| CLI runtime | `cli` | the provider CLI | Nanite plants the boot root and drives the process; the CLI reaches Nanite's tools through the planted Nanite MCP server |
| Endpoint-backed CLI | `cli` plus an endpoint | the provider CLI | the CLI is pointed at an Anthropic- or OpenAI-compatible Nanite endpoint, so the model call also goes through Nanite. Reserved; no field is defined until it is built |

### What is identical in every mode

- **Work root.** `directories[0]` is where the agent works; the rest are extra
  roots. Nothing defaults to the server's working directory.
- **Instructions.** Rendered once from `agent.md`; each mode consumes the same
  rendering.
- **Skills.** The same `SKILL.md` trees, from the same source.
- **Tool authority.** One grant set per host-settings revision, enforced at
  Nanite's tool boundary. A CLI does not widen it: its tools arrive through the
  Nanite MCP server, which asks the same authority.
- **Host revision pinning.** A session records the host revision it started
  under; a later turn after the host changed fails rather than silently using
  the new one.
- **Wake and messaging, session events.** The same session record and event
  stream.

### What differs, and why it is allowed to

- The loop. Loop knobs (`native_loop`, `recall_*`) mean nothing to a CLI.
- Permission enforcement. A CLI process cannot be held to a read-only posture,
  so a definition that asks for one is refused for `cli` rather than weakened.
- Resume. A CLI resumes its own provider session where the provider allows it;
  the native loop resumes from Nanite's record.
- The model flag, which only some CLIs take.

## Launch materialization

Every Nanite-launched session, in every mode, begins by materializing a
**boot root** from the directory's projection. This is the step the CLI path
already performs and the native loop does not.

1. Resolve the pinned definition and host settings; verify the digest and the
   host revision.
2. Compile a launch with the shared harness launch pipeline (compile, prepare,
   plant). Nanite supplies the neutral inputs; the shared planting layer owns
   the provider-specific rendering. Nanite does not carry its own per-provider
   renderers.
3. Materialize into a fresh private root (mode 0700, owned by this session,
   removed when it ends):

| Neutral content | Native loop reads it as | CLI runtime gets it as |
|---|---|---|
| instructions | the system prompt, from the rendered file | the provider's instruction file (`CLAUDE.md`, `AGENTS.md`, ...) |
| skills | the skill tool over the same trees | the provider's native skill location |
| context resources | files the loop can read | files in the root |
| Nanite tool access | in-process | the planted MCP descriptor |
| writable roots | the tool sandbox for file tools | the provider's allow-list and `--add-dir` |

4. Start the loop: in-process for `api`, the CLI process for `cli`.

Planting into a root that a provider may already be reading is not done; a
change to what a running session sees requires a fresh session. This follows
from the planter's custody rule (a root belongs to one session from creation).

## Live and pinned

An edit to the agent directory reaches a session in one of two ways.

- **`pinned`.** The host settings name an exact definition pin. Only an explicit
  operator action changes it (install a new revision, update the host settings).
  This is the behavior of a database-only deployment.
- **`live`.** The host settings name the directory. When a session is created,
  Nanite reads the directory, validates it, installs the result as an immutable
  revision (the same bytes give the same digest and the same pin), updates the
  host settings to that pin, and then creates the session against it exactly as
  `pinned` does. No restart is involved. There is no file watcher: the
  directory is read at session creation.

In both modes a session keeps the pin it started with; an edit never changes a
running session. Applying an edit to a running session means starting a new one
(an explicit reboot). A session's recorded pin is verified against the digest
before launch in both modes.

**Authority and a changed pin.** Tool authority is recorded against a host
revision and a definition digest. A semantic edit therefore leaves the new
revision with no authority, and a launch against it is refused with a stated
cause until an operator grants again. `live` does not widen that on its own. A
deployment may set the host to carry the previous tool set forward onto the new
revision; that carry-forward is recorded as a new authority with its own audit
entry and is the operator's declared policy, not an inference from the edit.

### Choosing the default

The effective mode is the first that applies:

1. `mode` in `host.yaml`.
2. The user or application setting for the default mode.
3. The environment of the target: **`live` unless the target carries a
   production label.** The signal is the Cerberus resource label set (`env`,
   `owner`, `admin`) of the service Nanite runs as. A target labeled
   `env: prod`, or with `admin: owner`, defaults to `pinned`. A target that
   carries no label is treated as not development and defaults to `pinned`.

Other signals can only tighten this rule, never loosen it: the fabric
authentication mode and saved posture profiles, once they exist. A host name
such as the `HOLLIS_ENV` location name is not an environment signal.

An agent or session carries no environment label of its own; the label belongs
to the target service. Giving agents a label is a separate decision.

## Skills

`SKILL.md` folders (the Agent Skills format) are the one source format. The
definition pins the whole folder tree by digest. Fields a catalog adds beyond
`SKILL.md` front matter, such as trigger phrases or a priority, are not part of
the skill: they are host-side selection hints that live in `host.yaml` or the
catalog, keyed by skill name, so a skill authored for one host loads unchanged
in another.

## Sequencing

The pieces are independent enough to build in this order:

1. Load agents from a directory (parse `agent.md` and `host.yaml`, project
   idempotently into definitions and host settings, `live` and `pinned`).
2. Move planting onto the shared harness layer for every provider, closing the
   gaps where a provider has no MCP or skills support.
3. Give API agents a boot root and a work root (the launch materialization
   above) so they run like CLI agents.
4. Accept what a definition declares for skills, tools, hooks and context
   instead of refusing it, and grant skills at setup.
5. A command to run an agent from the current directory.

The endpoint-backed mode and Tether's cutover to the directory are consumers of
this model and wait for it.

## Definition fields: accept, record or refuse

An agentdef v2 file declares more than a host can run. For each field the
host does exactly one of three things, and the choice follows one rule:

- **Apply**: the field changes what runs.
- **Record**: the field is stored, digest-covered and shown to the operator, and
  changes nothing at run time. Allowed only where ignoring the field cannot
  weaken a safety or isolation property.
- **Refuse**: install or launch fails with a stated cause. Used where ignoring
  the field would silently weaken a guarantee the author asked for, or where the
  host has no meaning to give it yet.

Installing a definition and running it are separate decisions. Install accepts
anything that parses, validates and has verifiable pins, because storing an
artifact claims nothing about running it. Launch is where a field is applied,
recorded or refused, per runtime.

Requests are not grants. A definition may ask for tools and skills; the
operator approves, and the agent view shows each request as pending or granted.

| Field | Decision | Notes |
|---|---|---|
| `behavior.purpose`, body, `instructions`, `sops`, `completion` | apply | rendered into the instruction content in order |
| `behavior.hooks` | apply where the runtime has hooks, otherwise refuse | each name resolves in the host's hook registry; an unknown name or a runtime without hooks refuses with the name |
| `capabilities` | record | advertised services for discovery; no execution effect |
| `requirements.requires` | apply as a check | an unmet entry refuses and names it |
| `requirements.uses` | record | an unmet entry is shown, not refused |
| `requirements.tools` | record as a request | shown as pending until the operator grants; never a grant. A name the host cannot resolve stays pending with that reason |
| `requirements.skills` | apply | the pinned tree is verified against its digest, requested skills are granted by the operator at setup, and the tree is delivered through the shared planting layer. A runtime with no skill location refuses with a stated cause rather than dropping the skill |
| `requirements.resources` | apply | pinned files are materialized into the boot root and readable by the agent |
| `harness_profile.steering` | apply | appended after the instructions, in order, same mechanism |
| `harness_profile.context.sources` | apply | planted as reference files and listed in the instructions. "Knowledge on boot" means these files plus the host's recall settings, for both runtimes |
| `harness_profile.context.policy` | record | an opaque pinned document until a policy format exists |
| `harness_profile.permissions.profile` | apply `default` and `read-only`; refuse the rest | a CLI refuses `read-only` because it cannot enforce it. Another name is added only through a host binding table from profile name to permission mode |
| `harness_profile.approvals`, `escalation` | refuse | ignoring an approval or escalation policy would silently weaken oversight |
| `continuity.mode: ephemeral` | apply | |
| `continuity.mode: durable` | refuse | the error points to durable-agent enrollment, which is separate from a definition |
| `continuity.memory_policy`, `recovery_strategy` | record on ephemeral, refuse on durable | an ephemeral session has no cross-session memory and recovers from Nanite's own pack |
| `extensions` (native-policy, reflex-policy) | apply | unchanged. Unknown optional extensions are kept and confer nothing; an unknown mandatory one refuses |
| `title`, `presentation`, `provenance` | record | |

Tool and skill approval at setup takes the same shape as tool approval: a list
of names or `*`, with a recorded reason, in one transaction with the rest of
setup.
