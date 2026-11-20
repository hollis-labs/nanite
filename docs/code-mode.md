# Code Mode: delegating with subtasks

Code Mode keeps an agent's conversation small. Instead of running twenty
tool calls itself and carrying every result, the agent hands a focused goal
to a child conversation with the `subtask` tool. The child does the tool
work. The parent gets back one short, checked result, not the tool traffic.

This page is for people who write agents and hosts, and for models that use
the tool. The engine-level details, such as event shapes and storage, are in
[architecture.md](architecture.md#subtasks-and-background-jobs).

## When to delegate

Delegate when the work would flood the conversation:

- it takes many tool calls, such as a change across several files;
- it means searching or reading broadly;
- it produces large output and you need only a summary or a few fields.

Do a single quick lookup yourself. A subtask costs a model call to start and
returns only what you ask for, so it suits work you can describe as a goal
with an expected answer.

Every agent's base prompt carries a short version of this guidance, so a
model with the `subtask` tool knows when to use it.

## The subtask tool

```text
subtask({goal, return_spec, variant, inputs?, context?, background?})
```

| Field | Required | What it is |
|---|---|---|
| `goal` | yes | What the subtask must achieve. |
| `return_spec` | yes | What to return: prose, or a JSON Schema object the result is checked against. |
| `variant` | yes | Which kind of subtask runs it. There is no default; the model chooses. |
| `inputs` | no | Values the child needs verbatim, such as paths or ids. |
| `context` | no | How much of the parent conversation the child starts with (see [What the child starts with](#what-the-child-starts-with)). |
| `background` | no | Run in the background and get a job id. Offered only to a root conversation that also has `background_status`. |

The tool is in the `subtasks` built-in group. A host that leaves that group
out gives its agent no `subtask` tool.

## Variants

A variant is a kind of subtask: a role prompt, the tools it may use, an
optional model and its budgets. Four are built in:

| Variant | Use it for | Tools |
|---|---|---|
| `code` | Tool work: run commands, edit files, report the outcome. | The child set (below). |
| `research` | Broad reading and searching; returns findings with their sources. | The child set. |
| `worker` | General work on a focused goal. | The child set. |
| `planner` | Gathering context and returning a plan. | The read-only tools of the child set, and no MCP tools. |

The child set is everything the agent has except `ask_user` (a child can't
ask the person directly), and except `subtask` unless the variant is a lead.
Every child also gets `return_result`, which nothing removes;
`fetch_tool_result` and `search_tool_result`, which an allow list need not
name but an explicit deny removes; and the parent history tools
`parent_history_search` and `parent_history_get`. A variant's allow and deny
lists narrow the rest, the history tools included.

Each built-in prompt states the same contract: return only what the return
spec asks for, never paste raw tool output, return `partial` or `error` with
what is missing when the goal can't be met, and end by calling
`return_result`. `code` also names the files it changed and what it ran to
check them. `research` gives each finding's source. `planner` names what each
step touches and the questions it could not settle.

### Writing your own

An agent adds variants as `variants/<name>.md` in its directory. Nanite reads
them again at the start of every turn. The name starts with a lower-case
letter, uses lower-case letters, digits and dashes, and is at most 40
characters. It can't reuse a built-in name or `chat`. The YAML frontmatter is optional
and the Markdown body is the role prompt. `nanite init` writes a commented
example, `variants/reviewer.md`:

```markdown
---
description: Reviews a change or a file and returns findings; changes nothing.
read_only: true
tools:
  deny: ["mcp__*"]
budgets:
  steps: 20
  wall: 10m
last_n: 4
---
You are a reviewer subtask. Read the work named in the goal and check it
for mistakes ...
```

| Field | What it does |
|---|---|
| `description` | Shown to the model in the `subtask` tool's list of variants. Write it so the model can choose. |
| `tools.allow` | When set, the child is offered only tools matching one of these names or globs (`mcp__notes__*`). |
| `tools.deny` | Removes tools matching any of these. |
| `read_only` | Offers only read-only tools. |
| `provider`, `model` | Runs the child on another provider and model. `model` alone runs on the provider of the `subtask` use in `providers.yaml` (else `chat`), which must serve that model. `provider` needs `model`. |
| `budgets.steps` | Most model steps the child may take. It only lowers the agent's `max_steps`. |
| `budgets.tokens` | Most tokens the child may use: input, output and cached. |
| `budgets.wall` | A time limit such as `10m`. It also stops a step in progress. |
| `budgets.cost` | Accepted and checked, but not yet enforced. |
| `last_n` | How many parent messages the child sees by default: 1 to 50; 0 or unset means 6. To start a child with no messages, pass `context.last_n: 0` on the call. |
| `result_cap` | The result size cap in tokens (see [What comes back](#what-comes-back)). |
| `can_spawn` | Lets this variant start subtasks one level down (see [Leads](#leads-one-more-level)). |

A variant only narrows. It can never give a child a tool the agent doesn't
have, and every call the child makes still passes the agent's permissions.
`read_only` and `can_spawn` can't be combined. Unknown frontmatter keys are
errors.

Run `nanite validate` to check your variants. It reports each bad file by
name, and warns when a `provider` isn't defined in the directory, since an
embedding host may supply it. During a turn, a bad file is skipped with a
warning and the other variants still load.

An MCP server marks some tools read-only, but that is the server's own
claim. So the built-in `planner` denies every `mcp__*` tool. An authored
variant can allow the MCP tools you trust by name.

## What the child starts with

The child is a separate conversation. It starts with its variant's role
prompt and one message: the goal, the inputs, the return spec, then a copy
of part of the parent conversation.

- **Recent messages.** The last `context.last_n` parent messages, else the
  variant's `last_n`, else 6. `context.full: true` copies them all. The call
  that started the subtask is never copied.
- **State.** `context.include` copies parent state, up to 8 items: `plan`,
  `todos`, `handoff`, or `scratchpad:<key>`.
- **Size.** Everything copied is redacted, then shortened, and the whole copy
  fits 32,000 characters. When something doesn't fit, the child is told how
  much was left out.
- **The rest of the history.** The child can search and read the rest of the
  parent conversation with `parent_history_search` and `parent_history_get`.

A variant's tool rules limit what a child may call, not what it may read. A
child with the history tools can read anything in its parent conversation,
including the output of tools its own variant denies. Deny
`parent_history_*` in the variant when that matters.

## What comes back

The child ends by calling `return_result({status, result, artifacts, notes})`
with status `ok`, `partial` or `error`. Only what it passes there reaches the
parent. When `return_spec` is a JSON Schema, the result is checked against
it; a result that fails gets one chance to repair, then is accepted as
`partial`.

The parent receives JSON with `status`, `result`, `artifacts`, `notes`,
`reason`, `cost`, `child_id`, `variant` and `steps`, plus the overflow fields
below when the result was stored. The status tells the parent how far to
trust the result:

| Status | Meaning |
|---|---|
| `ok` | The goal is met. |
| `partial` | Only part of it is. Also given when the child stopped without calling `return_result` (reason `no_return_result`); the result is then the end of its last output. |
| `limit` | The child stopped at a limit. The reason is one of `max_steps`, `token_budget`, `wall_time`, `max_tokens` (the model's output limit) or `model_context_window_exceeded`. |
| `error` | The child failed or reported that the goal can't be done. |
| `cancelled` | The run was cancelled. |
| `denied` | A person refused an approval the child asked for, and the child then ended `ok` or `partial`. Only the engine sets this; a child can't claim it. |

Anything other than `ok` is incomplete work, and `reason` says why.

The whole result is capped: the variant's `result_cap`, else the host's
`agent.Options.SubtaskResultCap`, else the default. Every cap is clamped to
what one tool result shows in full, about 5,900 tokens, so the effective
default is about 5,900 tokens and a larger setting has no further effect.
A return that doesn't fit is stored, scrubbed of secrets, for 30 days. The
parent then gets `result: null` and the overflow fields: `result_ref`, a
`tool_result://` pointer it reads with `fetch_tool_result` (also appended to
`artifacts`); `result_bytes`, the stored size; `result_expires_at`; and
`preview`, the start of the result.

## Approvals

A subtask runs under its root conversation's permissions, including any
grants already given there. The root is the conversation the person is
talking to. Policy rules see the root's conversation and role, plus
`subtask_variant`, `subtask_conversation` and `subtask_depth`, so a rule can
treat subtasks differently.

When a child needs approval that no rule or grant covers, the ask goes to the
root conversation, labelled with the subtask's variant and goal. Only that
child waits. MCP questions (elicitations) from a child go the same way. One
ask is open at a time per root conversation, so prompts never overlap.

Grants flow down only. A grant in the root covers its subtasks. A grant given
on a subtask's ask covers that subtask and its own subtasks, never the root
or a sibling, and lasts only while that subtask runs. How far an allow can
reach depends on the host:

- chatrunner's chat cards for a subtask's ask take a once answer only;
- the REPL's "always" covers that subtask and its own subtasks.

A refusal reaches the child as a tool result with the gate's reason. When a
person refused, and the child then ends `ok` or `partial`, its status becomes
`denied`; a `limit`, `error` or `cancelled` ending stays as it is. A refusal
that no person gave, such as a default gate, an expiry or a policy, is not
`denied`. A background subtask that asks between turns posts a notice to the
root and is asked on the root's next turn. If no root turn comes to ask it on
within `agent.Options.SubtaskAskWait` (30 minutes by default), the ask is
refused with reason `unanswered`; that is not a person's refusal, so it never
makes the subtask `denied`.

## Leads: one more level

Subtasks run one level deep. A variant with `can_spawn: true` is a lead: it
may start subtasks of its own, one level further, and no deeper. A lead's
children get a tool only when both the lead and their own variant allow it,
so a lead can't hand out a tool it lacks. A lead with an allow list must
include `subtask` itself.

A lead suits an "internal council": several independent subtasks on the same
question, merged into one answer. This lead, from
[`examples/variants/council.md`](../examples/variants/council.md), works with
the starter's `reviewer` variant:

```markdown
---
description: Runs three independent reviews of the same work and returns one merged verdict.
can_spawn: true
tools:
  allow: [subtask, read_file, list_dir, find_files, search_files, parent_history_search, parent_history_get]
budgets:
  steps: 12
  wall: 30m
last_n: 6
---
You lead a council of three reviewers. Start three subtasks with variant
"reviewer", each with the same goal and inputs, and give each a different
angle in its goal: correctness, safety, and clarity. ...
```

A lead's token budget includes the children it has finished, and its time
limit bounds them too. The lead and its three reviewers take 4 subtask
slots, the whole default limit, so a council can't run alongside other
subtasks unless the host raises `agent.Options.SubtaskLimit`. The council
prompt says what to do when a reviewer is refused over the limit.

## Several subtasks at once

> **To check once B5 (CW-20261119-0161) merges.** This section describes
> fan-out as B5 specifies it. Until B5 lands, several `subtask` calls in one
> step run one after another.

Several `subtask` calls in one step run side by side, bounded by the host's
`runtime.tool_concurrency` (default 4). They run before the step's other
calls, and the results come back in call order, each capped as above.

At most `agent.Options.SubtaskLimit` subtasks (default 4, at most 8) run at
once across the whole agent: every conversation, background subtasks and a
lead's own children count against it. A subtask over the limit is refused,
not queued: the model is told how many are running and to wait or start
fewer.

`agent.Options.TurnTokenBudget` is opt-in. When a host sets it, one root turn
shares that token ceiling with every foreground subtask it starts, at any
depth; background subtasks draw only on their variant's own budget. A turn or
child that reaches it ends with `limit` (`token_budget`), and no new
foreground subtask starts once the budget is spent. A cost (USD) budget is
not enforced.

Cancelling the parent turn cancels every foreground subtask it started.

## Background subtasks

A root conversation can pass `background: true` to get a job id at once and
keep working. Children can't. `background_status` reports the same capped
result a foreground subtask returns, and a notice arrives when the subtask
finishes. `background_cancel` stops it. Its token usage is reported with the
notice, not counted into whichever turn happens to be running.

## Not yet

- `budgets.cost` is accepted but not enforced, because usage doesn't carry
  a price yet.
- The agent-wide result cap, the subtask limit and the turn token budget
  can't be set in `host.yaml` yet; a host sets them with
  `agent.Options.SubtaskResultCap`, `agent.Options.SubtaskLimit` and
  `agent.Options.TurnTokenBudget`.
- A child can't yet search the parent's tool catalog for tools it wasn't
  given (CM-4), and there is no long-lived companion subtask (CM-7).
