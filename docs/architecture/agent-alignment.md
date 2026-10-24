# Agent alignment: one directory, one launch model, live or pinned

How a Nanite agent is authored, what a launch materializes from it, and when an
edit takes effect. The same agent runs on Nanite's native loop against an API
provider or on top of a CLI runtime, and behaves the same either way: same
workspace, same instructions, same skills, same tool grants, same wake. The
model is shared with Tether so an agent authored once can be launched by either.

This document describes the model and says, for each part, what runs today. The
directory loader (`nanite agent load`, `nanite agent run`, `POST
/api/agent-reload`), `agent.md` and `host.yaml`, the `live`/`pinned` modes and the
definition-field handling are built; the shared launch pipeline and the
endpoint-backed mode are not, and where a part is still a proposal it says so.
`agent-runtime.md`, `native-agent-api.md` and `native-authority.md` describe the
runtimes.

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

The file form of the existing host settings document (`NativeHostSettings`,
version `"1"`). Fields that exist today are validated by that struct; fields
marked **proposed** are new and would need a schema change before the loader
could accept them.

| Field | Status | Meaning |
|---|---|---|
| `version` | exists | `"1"` |
| `runtime` | exists | `api` (Nanite's native loop) or `cli` (a CLI runtime driven by Nanite) |
| `provider`, `model` | exists | for `api`, a registered API provider and model; for `cli`, the runtime (`claude`, `codex`, `opencode`, `agy`) and the model flag it accepts |
| `directories` | exists | filesystem the agent may use; the first entry is its work root. Required for `cli`, which refuses a relative entry. Proposed: the same meaning for `api` (see Launch materialization); today an `api` host ignores it |
| `native_loop`, `recall_limit`, `recall_timeout_ms` | exists | `api` only |
| `harness_profile`, `mcp_servers`, `debug` | exists; `host.yaml` carries the first two | `harness_profile` is a native-loop knob. Today a `cli` host refuses a non-empty `harness_profile`. `mcp_servers` names registry servers; a `cli` host carries them only on `claude` (granted tools allowed per tool, see agent-runtime.md) and refuses them on other providers |
| `protocol`, `transport` | exists | select an ACP CLI transport. `Validate` rejects them on an `api` host, and a `cli` host refuses them today |
| (all of the above on a `cli` host) | exists | a non-empty `harness_profile`, `native_loop`, `recall_*`, `protocol` or `transport`, or `mcp_servers` on a provider other than `claude`, is refused with cause `cli_host_unsupported_field` |
| `mode` | exists | `live` or `pinned` (below). Absent means the default rule, evaluated when the directory is loaded |
| `agent_dir` | exists | the agent directory a load read the row from, recorded by the loader (not authored): the directory a reload re-reads |
| skill selection hints (trigger phrases, priority) | **proposed** | host-side hints keyed by skill name (see Skills) |

Authority over the filesystem comes from this file, not from a request. A
session create request may name a `work_root`, but only inside these
directories (`nanite` validates it; see `getting-running.md`).

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
  under, and a later turn after the host changed or was disabled fails with
  `host_revision_changed` rather than silently using the new one. This holds for
  a CLI session (checked at boot) and an API session (checked at the start of each
  turn).
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

An edit to the agent directory reaches new sessions in one of two ways.

- **`pinned`.** The host settings name an exact definition pin. Only an explicit
  operator action changes it (install a new revision, update the host settings).
  This is the behavior of a database-only deployment, and it is what runs today.
- **`live`.** The host names the directory (`agent_dir`). A **reload** reads it,
  validates it, installs the result as an immutable revision (the same bytes
  give the same digest and the same pin) and records a new host revision that
  points at it. New sessions then start against the new pin exactly as
  `pinned` does. No restart and no file watcher: a reload is an event, not a
  poll.

**What a reload does to running sessions.** Today a defined CLI session records
the host revision it was admitted under, and every later turn is checked against
the host's *current* revision. If the host moved, the turn fails with
`host_revision_changed`. So the process keeps running what it started with, but
its next turn is refused. If a reload edited the one shared host row, creating
session B would break session A's next turn. `live` therefore must not move a
shared row: a reload records a **new host row per loaded revision** (named
from the agent and the pin), leaves the row A was admitted under untouched, and
A keeps working until it ends. The cost is one host row per distinct state of the
directory. This is the built behavior; the alternative, that any reload ends
the older sessions' ability to take another turn, was not chosen.

**Who may reload.** A reload writes host settings, which is a *maintain*
operation. Creating a session is *operate*. A session create must therefore
never perform a reload as a side effect of the caller's request. A reload is
run by an operator (a maintain-scope call or `nanite agent load`), or by the
server on a schedule or trigger the operator configured. It runs with that
authority, takes its content only from the directory, and produces a revision
that carries no authority (below). The caller of a later session create only
selects among revisions that already exist.

**The agent must not be able to edit its own definition.** An agent's
`directories` are its filesystem. For a `live` agent the loader refuses a
directory that is inside, or contains, any entry of its own `host.yaml`
`directories` (`agentdir.CheckOwnDirectories`, symlinks resolved). A `pinned`
agent is exempt: nothing republishes it without an explicit operator load, which
is what lets `nanite agent run` keep a `.nanite` inside the project the agent
works in. (The boot root is not checked: the loader does not know it.)
Otherwise an agent could rewrite its `agent.md` and a reload would hand the new
text its old grants.

**Authority and a changed pin.** Tool authority is recorded against a host
revision and a definition digest. A reload therefore yields a revision with no
authority, and a launch against it is refused with a stated cause until an
operator grants again. Grants are never carried forward automatically and
there is no standing carry-forward setting. A development workflow that wants
the same tools on the new revision uses an explicit operator action (grant the
same tool set, with a reason), which is recorded as an ordinary new authority
with its own audit entry. That keeps every grant tied to a decision made about
the text it applies to.

### Chosen names

The names the design left open, as built:

- `agent_dir`: the host setting that records the agent directory a load read.
- `NANITE_AGENT_MODE`: the application default mode (`live` or `pinned`).
- `NANITE_RESOURCE_ENV`: the service's Cerberus `env` label.
- `NANITE_RESOURCE_ADMIN`: the service's Cerberus `admin` label.

### Choosing the default

One rule, evaluated in order, first match wins:

1. `mode` in `host.yaml`.
2. The application setting for the default mode (`NANITE_AGENT_MODE`).
3. Otherwise **`pinned`, unless the target is positively labeled development:
   the Cerberus resource labels of the service Nanite runs as read `env: dev`
   and `admin` is not `owner`.** Then the default is `live`.

A production label, an `admin: owner` label and a missing label all give
`pinned`. Today every labeled resource is `env: dev`, so a labeled Nanite
defaults to `live` and an unlabeled one to `pinned`; no `prod` label exists
anywhere yet, so the production branch has not been exercised. Nanite reads the
labels from the environment, `NANITE_RESOURCE_ENV` and `NANITE_RESOURCE_ADMIN`,
to be populated from the manifest's `env` and `admin`; nothing populates them
yet, so until something does an unlabeled server defaults to `pinned`
(`service.ResolveAgentMode`). `agent load` and the reload endpoint read the same
variables, and a load prints the mode and which step of the rule chose it. One deviation: an
attended `agent load`/`agent run` treats the step 3 fallback (pinned only because
nothing says dev) as "publish", so the edit-and-run loop works on an unlabeled
machine; an explicit `mode`, the application setting and a dev label still apply,
and the unattended reload applies the rule as written.

Other signals can only tighten this rule, never loosen it: the fabric
authentication mode and saved posture profiles, once they exist. A host name
such as the `HOLLIS_ENV` location name is not an environment signal. An agent or
session carries no environment label of its own; the label belongs to the target
service, and giving agents one is a separate decision.

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
artifact claims nothing about running it. (Install refuses nothing that parses and verifies, skills included.) Launch
is where a field is applied, recorded or refused, per runtime.

Requests are not grants. A definition may ask for tools and skills; the
operator approves, and the agent view shows each request as pending or granted.

| Field | Target | Today (main, `agentpolicy.ValidateExecution`) |
|---|---|---|
| `behavior.purpose`, body, `instructions`, `sops`, `completion` | apply: rendered into the instruction content in order | accepted and applied |
| `behavior.hooks` | apply where the runtime has hooks, otherwise refuse; an unknown name or a runtime without hooks refuses with the name | refused at launch, naming the hook: Nanite ships no hook registry, so every name is unknown |
| `capabilities` | record: advertised services for discovery | recorded |
| `requirements.requires` | apply as a check; an unmet entry refuses and names it | checked against the host vocabulary (`agentpolicy.HostCapabilities`, empty today), so any entry is refused and named |
| `requirements.uses` | record; an unmet entry is shown, not refused | recorded; an unmet entry is reported as `unmet` |
| `requirements.tools` | record as a request: shown as pending until the operator grants; never a grant | recorded; the agent view and the load response show each request as `pending` or `granted` against the revision's authority |
| `requirements.skills` | apply: pinned tree verified, requested skills granted at setup, tree delivered through the shared planting layer; a runtime with no skill location refuses with a cause | install accepts it: the directory loader packs `skills/<name>/` into a canonical tree (`agentpolicy.EncodeSkillTree`) and the pin must equal that tree's digest, verified at install. Launch reports it `unmet` and does not deliver it: a definition's skill request has no grant yet (below) |
| `requirements.resources` | apply: pinned files materialized into the boot root | applied: planted under `agent/` in the CLI boot root, or the API session workspace |
| `harness_profile.steering` | apply: appended after the instructions, in order | applied, after the completion line, in order |
| `harness_profile.context.sources` | apply: planted as reference files and listed in the instructions | applied: planted under `agent/` and listed |
| recorded |
| `harness_profile.permissions.profile` | apply `default` and `read-only`; refuse the rest. A CLI refuses `read-only` (it cannot enforce it) | `default` and `read-only` applied; any other profile refused. A CLI host refuses `read-only` with `read_only_unenforceable` |
| `harness_profile.approvals`, `escalation` | refuse: ignoring them would silently weaken oversight | refused at launch |
| `continuity.mode: ephemeral` | apply | applied |
| `continuity.mode: durable` | refuse; the error points to durable-agent enrollment | refused at launch |
| `continuity.memory_policy`, `recovery_strategy` | record on ephemeral, refuse on durable | recorded on ephemeral; durable is refused as a whole |
| `extensions` (native-policy, reflex-policy) | apply; unknown optional extensions are kept and confer nothing; an unknown mandatory one refuses | applied |
| `title`, `presentation`, `provenance` | record | recorded |

The middle column is the rule, the right column is what `main` does.
`agentpolicy.ExecutionReport` returns the disposition of every field a
definition declares (`applied`, `recorded`, `unmet`, `refused`), and a load
prints it; `unmet` is a field the launch goes ahead without and says so.
`ValidateExecution` fails exactly when the report has a `refused` entry.

**Skills are the one `apply` row not yet delivered.** Nanite grants a skill to an
agent through `agent_known_skills`, which only issuer-owned authority may write,
and a native authority has no skill grants. Delivering a directory's skills
therefore needs a decision on who may grant a skill to a definition's request and
how the grant ties to the tree digest. Until it is made, the tree is stored and
pinned, shown as a pending request, and not planted.

**Contract for granting a requested skill (not built).** Skills are granted
through Tachyon, operator-approved, like tool requests. Nanite will expose
`PUT /api/native-authority/{authority_id}/skills/{skill}` with
`{"digest": "sha256:...", "reason": "..."}` (and `DELETE` to revoke), modeled on
the tool-grant route `PUT .../tools/{tool}`. It is admin class, the same scope rule as the tool-grant routes
(`agents:manage` when CW-20261024-0025 lands), never an agent
credential, and the granting credential is recorded from authentication, not the
body. It succeeds only when the authority is live and current, the authority's
definition requests a skill of that name, and the digest equals the pinned tree
digest and the stored tree decodes; otherwise 422 (`skill_not_requested`,
`skill_digest_mismatch`, `skill_tree_missing`) or 409 (revoked, stale, a different
digest already granted). The grant belongs to the authority, so it is tied to one
host revision and definition pin and is **not carried across revisions**; a
re-approval is an explicit act. Each grant and revoke is an audit row in
`native_authority_audit`. A repeat of the same grant is a no-op with
`already_granted: true`. Until it exists, requested skills stay recorded and
pending and none is delivered.

Where the files go: `requirements.resources` and `harness_profile.context.sources`
are planted under `agent/` in the session root, which is the boot root for a CLI
runtime and `<workspace>/agent/` for an API session (the instructions then name
the session root).

Tool and skill approval at setup takes the same shape as tool approval: a list
of names or `*`, with a recorded reason, in one transaction with the rest of
setup.
