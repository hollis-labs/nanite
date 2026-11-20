# Embed Nanite in a Go application

Run the [example host](../examples/embed-host/main.go) from the repository root.

```sh
go run ./examples/embed-host --offline
```

The command copies its bundled [authored agent](../examples/embed-host/agentdir/agent.md)
and [host profile](../examples/embed-host/agentdir/host.yaml) to a temporary
directory and opens it with `agent.Open`. The offline provider makes scripted
model requests and tool calls. Omit `--offline` to use the authored provider
with `NANITE_GATEWAY_URL`, `NANITE_GATEWAY_KEY_REF`, and optional
`NANITE_GATEWAY_AUTH` set for your gateway. Use `--dir` to supply an existing
authored directory and retain its conversations.

1. Open an agent and create a conversation. Keep that conversation across turns.
2. Give your application an owner with `a.Slots().Owner("demo")`. The example
   uses `Replace` to set the workspace and selection before each send.
3. Register tools with `a.Tools().SetGroup("demo", tool)`. Replacing a group
   removes the previous group's tools from the next turn. The first turn offers
   `notes_search` and the second offers `files_list`.
4. Use `conversation.Send` and consume `run.Events()`. Handle question events
   with `run.Answer`, then call `run.Wait` for the result and error. A host that
   wants to approve permission requests implements `agent.Gate`. The default
   gate denies requests that need approval.
5. Use `writer.Clear()` and `a.Tools().RemoveGroup("demo")` to remove the host
   contributions before the third turn. Call `a.Close()` at shutdown.

`SlotsAssembled` reports each slot's estimated token count, whether its content
changed since the previous turn, and each entry's disposition. A changed slot
with zero tokens was cleared. The example also prints the names it observes
in the provider request, including Nanite's built-ins. The system and permissions token
counts vary with the lengths of the workspace paths.

```text
Turn 1 slots:
  universal: 625 tokens, changed yes
    engine/rules: ship
  system: 72 tokens, changed yes
    engine/environment: ship
  agent: 19 tokens, changed yes
    file/instructions: ship
  permissions: 79 tokens, changed yes
    engine/mode: ship
  workspace: 1 tokens, changed yes
    demo/view: ship
  context: 4 tokens, changed yes
    demo/selection: ship
Tools offered: ask_user, fetch_tool_result, find_files, list_dir, notes_search, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subtask, todo_list, todo_write
Tool called: notes_search
The notes mention a planning meeting.
Turn 2 slots:
  universal: 625 tokens, changed no
    engine/rules: ship
  system: 72 tokens, changed no
    engine/environment: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 1 tokens, changed yes
    demo/view: ship
  context: 4 tokens, changed yes
    demo/selection: ship
Tools offered: ask_user, fetch_tool_result, files_list, find_files, list_dir, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subtask, todo_list, todo_write
Tool called: files_list
The workspace contains agenda.md.
Turn 3 slots:
  universal: 625 tokens, changed no
    engine/rules: ship
  system: 72 tokens, changed no
    engine/environment: ship
  agent: 19 tokens, changed no
    file/instructions: ship
  permissions: 79 tokens, changed no
    engine/mode: ship
  workspace: 0 tokens, changed yes
  context: 0 tokens, changed yes
Tools offered: ask_user, fetch_tool_result, find_files, list_dir, read_file, scratchpad_read, scratchpad_write, search_files, search_tool_result, skill_get, skill_list, subtask, todo_list, todo_write
The workspace context is clear.
```

## Building a provider yourself

`agent.Open` builds the providers that `host.yaml` and `providers.yaml`
select, so most hosts never build one. A host that wants a provider outside
an agent (a live eval, a workbench, a one-off request), or wants to choose it
explicitly through `agent.Options.Provider`, uses
`github.com/hollis-labs/nanite/agent/providers`:

```go
p, err := providers.New(providers.Config{
    Name:    providers.OpenAI, // or providers.Anthropic
    Gateway: agent.Gateway{KeyRef: "keychain://nanite/provider/openai"}, // a reference, never the key
    Options: providers.Options{Redactor: scope}, // the scope your logs are scrubbed with
})
// A named provider, as providers.yaml defines one:
q, err := providers.NewNamed(spec, providers.Options{Redactor: scope})
```

A built-in provider looks for its key at `Gateway.KeyRef`, then at
`keychain://nanite/provider/<name>`, then, only with
`NANITE_PROVIDER_KEY_ENV=1`, in `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`.
`Gateway.BaseURL`, `Headers` and `Auth` route it through a gateway, as for
`agent.Open`; with a third-party gateway, make sure `KeyRef` resolves, or the
provider's own key is sent there. Keys are resolved on each request and
added to `Options.Redactor`: pass the scope you scrub logs and traces with,
since with none the key goes to a scope nothing reads. A key that cannot be
resolved fails the request, not `New`.

A named provider with `auth: warden` sends a short-lived Warden token instead
of a key. Build one `Warden` from the agent's `identity.yaml` and pass it to
every such provider, so they share one token cache and one parent credential:

```go
id, ok, err := authored.LoadIdentity(dir) // ok is false without identity.yaml
w, err := providers.NewWarden(id, providers.WardenOptions{
    Redactor:      scope,           // learns every token and parent minted
    OnAuthWaiting: showGrantRequest, // a missing grant is filed and reported here
})
q, err := providers.NewNamed(spec, providers.Options{Redactor: scope, Warden: w})
```

`NewNamed` refuses `auth: warden` without `Options.Warden`. The Warden mints
under the same `<credential_file>.lock` as a running agent and the Warden
client, so a Workbench can share an agent's credential file with it.

## Steering

`Options.Steering` turns on modes, work slots, reflexes and a classifier for
root turns (libs `agent/steer`). Pass what is in front of the person in
`Input.Situation`, and set the mode only from the person's own choice with
`Conversation.SetMode`. See [steering.md](steering.md).

With chatrunner, `Options.Situation` supplies the situation for each turn,
and `Options.OnSteered` sees each `Steered` event, including the reflex
actions for the host, which never go on the wire. With a front desk, a message
is steered before intake, a halted message is not planned, and
`DeskOptions.Mode` defaults to the conversation's mode.

## Slot budgets

Every slot except the agent's instructions has a token budget (estimated at
about 4 bytes a token). `agent.SlotBudget(name)` reports it, so a host can
size what it writes. `Options.SlotBudgets` overrides it per slot; the
Universal slot, which holds the base prompt, keeps its own.

An entry that does not fit what is left of its slot is never dropped
without a trace. In order, the default selector:
1. ships the entry's pointer if it has one and the pointer fits;
2. otherwise trims the entry to fit, keeping the start of the content and
   adding a marker (`[trimmed to fit the <slot> slot budget of N tokens]`);
3. leaves the entry out only when fewer than 32 tokens of room are left.

Every trim and omission is logged as a warning. It is also reported in
`SlotsAssembled` (`SlotTrim` and `SlotSkip`), in the `nanite.slots`
activity, and in the trace's `trimmed` list.

## Choosing the built-in tools

The application decides what its embedded agent can do. An agent whose
`host.yaml` says `kind: embedded` gets **no built-in tools** unless the host
names them in `Options.Builtins`; it has the tools the host registers and its
MCP tools. A stable or task agent, and any agent run by the terminal `nanite`
command, gets every built-in tool by default.

```go
a, err := agent.Open(ctx, agent.Options{
    Dir:      dir,
    Builtins: []agent.BuiltinGroup{agent.BuiltinFilesRead, agent.BuiltinSkills, agent.BuiltinNotes},
})
```

| Group | Tools |
|---|---|
| `BuiltinFilesRead` | `read_file`, `list_dir`, `find_files`, `search_files` |
| `BuiltinFilesWrite` | `write_file`, `edit_file` |
| `BuiltinShell` | `bash` |
| `BuiltinBackground` | `background_start`, `background_status`, `background_cancel` |
| `BuiltinSubtasks` | `subtask`, plus `parent_history_search` and `parent_history_get`, which only a subtask's child conversation is offered. Every child also gets `return_result`, which ends its subtask; it belongs to no group and is never offered to a root conversation. Background subtasks are offered only together with `BuiltinBackground`, whose `background_status` and `background_cancel` poll and cancel them. The built-in `planner` variant denies `mcp__*` tools. `Options.SubtaskResultCap` sets the cap on a subtask's whole result in tokens (zero means the effective default of about 5,900; a variant's `result_cap` overrides it; either is clamped to the 23KB a tool result shows in full). These three subtask settings can also come from `host.yaml` (`runtime.subtask_result_cap`, `runtime.subtask_limit`, `runtime.turn_token_budget`), read when the agent opens; a non-zero Option wins over `host.yaml`, and a `host.yaml` `subtask_limit` above 8 is refused. `Options.SubtaskLimit` sets how many subtasks run at once across the agent, separately for leads and for other subtasks (zero means 4, at most 8; a foreground subtask over the limit waits for a slot, a background one is refused), and `Options.TurnTokenBudget` a token ceiling a root turn shares with its foreground subtasks, enforced mid-step (zero means none). Consecutive `subtask` and read-only calls in one step run side by side; calls otherwise run in model order. See "Subtasks and background jobs" in `architecture.md`. |
| `BuiltinSkills` | `skill_list`, `skill_get`, and the skill listing in the prompt |
| `BuiltinNotes` | `todo_list`, `todo_write`, `scratchpad_read`, `scratchpad_write` |
| `BuiltinQuestions` | `ask_user` |
| `BuiltinTools` | the agent's tool packages (`tool__<name>`, `tool__<name>__<tool>`) and `tool_validate`; see "Tool packages" in [agent-directory.md](agent-directory.md) |

Leaving `Builtins` nil gives the default for the agent's kind, as above.
Pass `agent.AllBuiltins()` to give an embedded agent everything. An empty
slice offers none for any kind. A
withheld tool is not sent to the model at all, which is different from a
permission rule: a denied tool is still described to the model and refused
when called. The two tools that page through a long tool result are always
offered. Tools the host registers with `a.Tools().SetGroup` and MCP tools are
not affected.

## Panics in host code

A panic in code the host gives Nanite does not take the host process down.
Nanite contains it at each boundary and turns it into an error carrying only
a reference, `internal error (ref pnc_...)`:

| Where it panics | What happens |
|---|---|
| A host tool's handler | The call gets an error tool result; the turn continues. |
| A slot source or the slot selector | The turn fails before any model request. |
| The approval gate or the PDP | The call is denied. |
| The event sink | The event is dropped; the turn continues. |
| A provider stream | The turn fails and is recorded as failed. |
| A background job | The job is marked failed. |

The next turn works as usual. The record of each panic, with its value and
stack passed through the agent's redactor, goes to `Options.Logger` and to
`Options.PanicReporter` when set. `agent.PanicRef(err)` returns the reference
from an error. Nanite never replaces the process-wide default guard of
`libs/core/panics`; that is the host's to set.

## Where an application keeps its agents

Nanite expects nothing beyond a directory path, so this is a convention for
hosts to share.

- One setting in the application's config, `agents.dir`, names a directory.
  The default is `agents/` beside the config file.
- Each agent is a subdirectory, and the directory name is the agent's name.
- The authored files (`agent.md`, `host.yaml`, `permissions.yaml`, `skills/`,
  `context/`) live there. They can be checked in or shipped read-only.
- The agent's `host.yaml` says `kind: embedded`, `lifetime: application`, and
  an `application_id` naming the application.
- Run-time state need not sit beside the authored files. Pass
  `Options.DataDir`, one directory per agent under the application's own
  state root. When it is empty, state goes in `.nanite/` inside the agent
  directory, as the terminal does.
- The working directory is where the file tools resolve relative paths
  first (then the agent directory) and where bash and background jobs run.
  It is `Options.WorkDir`, an existing absolute directory, or the process's
  working directory at `Open` when that is empty. Nanite never changes the
  process's working directory, so several agents in one process can each
  work in their own `WorkDir`. MCP stdio servers are the exception: they
  start in the process's working directory, so give them absolute paths.

Only one process can hold an agent's state directory open. If the
application keeps state in its own `DataDir`, `nanite -C agents/<name>` from a
terminal still works while the application runs: it reads the same authored
files and keeps separate conversations in `.nanite/` inside the agent
directory. If the application leaves `DataDir` empty, the two share one state
directory and the second to start is refused.

## Hosting an agent behind the chat handler

`github.com/hollis-labs/nanite/chatrunner` runs an agent behind the
conversation handler in `github.com/hollis-labs/libs/agent/chat`. It
implements `chat.Runner`, so the application gets the conversation routes,
replay, leases and approvals from the library and Nanite does the turns.

```go
r, err := chatrunner.Open(ctx, chatrunner.Options{
    Agent: agent.Options{Dir: "agents/desk", DataDir: stateDir},
    Tools: []chatrunner.Tool{{
        Name: "notes_save", Description: "Save a note.",
        InputSchema: schema, Level: chatrunner.LevelWrite,
        Run: func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) { ... },
    }},
})
svc, err := chat.NewService(chat.Config{Runner: r, ...})
```

`Options.Agent` is the same `agent.Options` as above, so `Builtins` chooses
Nanite's own tools as usual. Leave `Gate` nil: approvals are answered on the
chat wire. `r.Agent()` gives the agent for slots, skills and notices, and
`r.Close()` releases the directory.

**Notices and host work.** Set `Options.Observe` to the chat Service's
`Observe`. A notice posted while a turn runs reaches the wire on that turn as
a `nanite.notice` activity (id, kind, body). With `Observe`, one posted while
no turn runs is published as a short run of its own, each conversation's
notices in order and independently of other conversations. With
`NoticeTurns` it starts a notice turn instead, whose user message is
`agent.NoticeTurnText` and which opens with the `nanite.notice` activity. The
turn starts only when the Service adopts it, so it never holds the
conversation before it is published; if a turn began meanwhile, the notice
turn only announces the notice, so the wire shows "A notice arrived." with no
model reply, and it still counts toward the limit. A chained notice never
starts one (a chained ask relayed between turns waits for a user turn or its
`SubtaskAskWait`), and at most three notice turns follow one another before a
user turn. `r.Dispatch(ctx, chatID, starts)` starts subtasks
for the host without a model call and publishes the dispatch as a run on the
wire, so the subtasks' activity and relayed approvals are seen and answered
like a turn's; it returns the outcomes once every foreground start has ended.
A background start's finish reaches the wire as a `nanite.notice`. Without
`Observe`, notices wait for the conversation's next turn and `Dispatch` fails.

**Front desk.** `Options.Desk` puts the front desk (libs `agent/desk`)
between the owner and the chat agent. It needs `Observe`, and dispatching
plans needs the `subtasks` and `background` builtin groups.

- *Intake.* Before each user turn, `DeskOptions.Intake` screens the message
  and, when it is worth it, plans it (`desk.Model` over a small model, or
  the rules). `PlanWithModel` plans with the agent's own provider, the
  `desk` use in `providers.yaml` (falling back to chat), with
  `PlannerInstructions` added to its prompt; `agent.Agent.Complete` is the
  one-request call behind it. Planning is bounded by `IntakeTimeout` (15s)
  and never blocks the message. A trivial plan changes nothing. In `suggest` mode the turn
  opens with a `nanite.desk.plan` activity (id, disposition, topic, card,
  plan) and the chat agent is told a plan was suggested; the host calls
  `r.Desk().AcceptPlan(ctx, chatID, id, edited)` or `DeclinePlan`, and
  `OnCorrection` sees the answer. In `auto` mode a confident plan is
  dispatched before the turn and the chat agent is told not to redo it.
  `OnPlan` sees every intake result, shadow ones included.
- *Running a plan.* Items run as their dependencies finish. Research and
  analysis run as background `research` subtasks, council and delegate as
  `worker` (`Variants` overrides); `Delegate` hands delegate items to their
  owning seat instead, and the host reports the outcome with
  `r.Desk().Complete`. An answer item is left to the chat agent. A question
  item goes to the owner at once, and the owner's next message after it has
  gone out answers it (the desk cannot tell an answer from a new subject).
  A later step's goal carries the results of the steps it depends on. Steps
  that come due while a turn runs start when it ends; a step that fails to
  start three times is marked failed and the owner is told. Desk notices are
  handled per conversation, so a slow `Delegate` holds up only its own chat.
- *Ledger.* Every plan is a topic in `r.Desk().Ledger()`: its work, results
  and the questions waiting on the owner. The desk saves the ledger it made
  and restores it on `Open`. A ledger you pass in `DeskOptions.Ledger` is
  yours to save and restore (it marshals to JSON); the desk only uses it.
- *Delivery.* With a desk, a notice no running turn carried does not go to
  the wire as it arrives. Each one (a desk job's result, a relayed ask,
  a host notice, a shell job) is an item for the chat's delivery editor,
  which applies `DeskOptions.Policy` (default `desk.DefaultPolicy()`): now,
  merge with more on the topic, hold while a question on the topic is open,
  digest, or file. What is due goes out as one `nanite.notice` of kind
  `desk` whose body is the composed message. A turn the owner sees, or
  `r.Desk().SetOwnerTyping(chatID, true)`, holds everything but critical
  items and questions until it ends. `Held` counts what waits and `Flush`
  sends it now. A desk job's result always goes through the editor, even
  when a running turn announced that the job ended.
- *Restarts.* The desk saves each chat's state as the conversation's host
  state (`agent.Conversation.SetHostState`, key `desk`) whenever it changes:
  its plans and how far each step got, the jobs running them, suggested
  plans not yet answered, and the delivery editor's queue. `Open` restores
  it before notices flow and then runs `r.Desk().Recover`. That looks up
  each job a step was waiting on. A job that finished meanwhile has its
  result applied, as its notice would have. A job Nanite stopping ended
  (`Close`, or a process exit: the job's reason is
  `agent.JobReasonShutdown` or `agent.JobReasonProcessExit`) is started
  again, counting as one of the step's three attempts. Then ready steps
  start and what the editor holds goes out when due. A job's finish is
  applied once, whether its notice or `Recover` sees it first, and an item
  the editor already released is not sent again. A release is saved before
  it is published, so a crash in that moment loses that one message rather
  than sending it twice. A step saved as running before its job was
  recorded goes back to planned on restart, without counting an attempt.
  Suggested plans nobody answers expire after 24 hours (at most 20 per
  chat). State saved by a newer Nanite is neither read nor overwritten.
  `Close` saves everything before it stops the agent. The owner's typing
  state is not saved.

**Conversations.** Each chat conversation is one Nanite conversation, created
on its first turn. Its ID is `chatrunner.ConversationID(chatID)` and is the
`runner_ref` on the chat conversation record. The runner records which chat
each Nanite conversation serves (host state `chat`), so a restarted runner can
find its interrupted turns.

**Interrupted turns.** A turn the previous process left unfinished (it was
killed mid-turn) is kept until the owner decides. **Hosts upgrading should
call `Runner.RecoverTurns(ctx)` once the chat Service starts**; without it
the owner is not offered the turn, and it waits out its hold.

- `Runner.RecoverTurns(ctx)` handles all of them. Call it once the chat
  Service the runner serves is running; `Open` cannot, because the Service is
  built on the runner and resuming publishes through `Options.Observe`. With
  `Options.ResumeInterrupted` it resumes each. Otherwise it offers each to
  the owner as a `nanite.interrupted` activity (`turn`, and `actions`:
  `resume`, `discard`) on a run of its own.
- `Runner.Resume(ctx, chatID)` continues the turn from its stored message as a
  new run published through `Options.Observe`, with no new user message (the
  owner's message is already in the chat). The front desk does not plan it
  again. The first `Resume` claims the turn; another before the resumed run
  ends fails with "no interrupted turn" and publishes nothing. `Resume`
  returns once `Observe` accepts the run, which waits while another run holds
  the chat.
- `Runner.Discard(ctx, chatID)` abandons it. A new message from the owner, or
  `Record`, also discards it: the owner has moved on.
- `Runner.Interrupted(ctx, chatID)` and `Runner.InterruptedChats(ctx)` report
  them. A conversation is found by the chat the runner records for it on
  first use (host state `chat`) or by front-desk state; one last used by an
  older version with neither is not listed.
- **The hold.** An interrupted turn waits `Options.InterruptedHold` (default
  15 minutes; negative waits indefinitely), counted from when the runner
  first found it; the start is saved, so a restart does not reset the clock.
  When it runs out the runner discards the turn.
- Every discard says so on the wire: a `nanite.interrupted` activity with
  `disposition: discarded` and `reason` `new message`, `timeout` or
  `requested`, so an offer card can close.

While a turn is interrupted, the front desk's dispatches on that chat wait,
as for any held conversation, and the owner gets one desk item saying work
is waiting (once the Service is known to run: after `RecoverTurns` or an
owner's turn). The work starts when the resumed turn ends, the turn is
discarded, or the hold runs out. A notice turn on that chat only announces
its notice.

**The turn's context.** `Turn.Context` goes into one slot entry
(`SlotUserContext` unless `Options.ContextSlot` names another) for that turn
only. A JSON string is placed as its text; any other JSON is placed as
written. Nanite still assembles everything else.

**Host tools.** A tool has a level, and the level decides whether a call asks
first. Replace the whole set between turns with `r.SetTools(...)`. A tool's
`Run` function can read the turn with `chatrunner.TurnFromContext(ctx)`.

| Level | In `default` mode |
|---|---|
| `LevelRead` | Runs without asking. |
| `LevelAct` | Runs without asking. |
| `LevelWrite` | Asks. An answer for the conversation, or a standing grant, covers later calls. |
| `LevelConfirm` | Asks every time, including in `yolo` mode. No answer covers a later call. |

An empty or unknown level is `LevelWrite`. `Options.Ask` is called before
each host-tool call and can make that one call ask, whatever the level; it
cannot make a call looser. `Options.Granted` reports a standing grant the
application stored through `chat.Grants`; a covered call runs without asking.
The agent's `permissions.yaml` and mode still apply to every tool.

**Approvals.** A call that asks becomes `approval.request` and the turn waits
for the answer the Service delivers. Its `descriptor` carries `description`,
`standing` (false for a `LevelConfirm` tool), `tool` and `input`. The scopes
`conversation` and `always` both stand for the rest of the conversation in
Nanite; keeping `always` beyond it is the application's `Granted` function.

**Subtask approvals.** A subtask asks through its root conversation. Its
call is decided under the root's rules and grants, so a tool the conversation
already allowed runs in the subtask without asking. Anything still needing an
answer arrives on the root's turn as one `approval.request` with ID
`<child>:<call>`, `call_id` set to the root's `subtask` tool call (so the card
can be shown with it), a `descriptor.description` of `subtask "<goal>" wants
<tool>: … (this call only)`, `descriptor.standing` false, `grant_scope`
`once` and a `descriptor.subtask` object (`child_id`, `variant`, `goal`,
`depth`, `call_id`). It takes only a once answer: a standing grant would be
kept against the root chat and reach the root and the subtask's siblings,
and the chat wire has no scope for one subtask. A controller's denial ends
the subtask with status `denied`; an expiry does not count as one.

**Events.** Each model step is a `step.start` / `step.finish` pair around one
assistant message that holds the step's text, reasoning, tool calls and tool
results. Activity kinds are `nanite.slots` (each slot's tokens and whether it
changed, plus `cuts`: entries that did not ship whole, with `disposition`
`pointer`, `trim` or `skip` and the reason), `nanite.subagent` (a subtask's
child conversation, with `variant`, `parent_id` and `depth`, and `status`
when done; state `progress` carries `step`, `tool` and `status_line` while
it runs, and a background subtask's start and progress carry `background`) and
`nanite.compact` (`value.state` is `start`, `done` or `failed`). `run.finish`
has reason `turn_limit` at the step limit and `length` at the model's output
limit. A cancelled turn ends in `run.abort`, a failed one in `run.error` with
code `turn_failed`. The `ask_user` tool is declined.

## Warden auth

An agent directory with `identity.yaml` (see
[agent-directory.md](agent-directory.md#identity-and-warden-auth)) gets
short-lived Warden tokens for providers with `auth: warden` and for MCP
servers named in its `mcp` section. Nothing else is needed from the host.
`ProviderSpec` entries in `Options.Providers` take `Auth: authored.AuthWarden`
with `Audience` and `Scopes` the same way, and use the directory's identity.

When the agent has no grant for an audience, Nanite files a grant request
and calls `Options.OnAuthWaiting` with an `agent.AuthWaiting` (request id,
status, tier, audience and scopes; nothing secret). Show it to the owner,
for example as an activity of kind `agent.ActivityAuthWaiting`
(`nanite.auth.waiting`), so they can approve it. The turn that needed the
token fails with an error naming the request; a later turn mints once the
request is approved.

`AuthWaiting.Conversation` names the conversation whose turn needed the
grant and `AuthWaiting.For` what needed it (`provider <name>` or
`MCP server <name>`); the grant request's purpose names both too, so the
approval card says what it is for. `Agent.AuthRequest` looks a request up,
and `Agent.AuthDecided` tells the agent a request was decided so the next
token request checks it at once.

The chatrunner does all of this for a chat: it publishes a
`nanite.auth.waiting` activity on the chat once per request (request id,
status, tier, audience, scopes and `for`), ends the turn with a run error
that says it is waiting for that request, and watches the request (every
`Options.AuthPoll`, default 30s, doubling up to five minutes; the watch is
kept in host state, so a restart goes on watching). When the request is
decided, each chat that waited gets a notice of kind `auth`
(`chatrunner.NoticeKindAuth`): on approval it says to continue, and with
`Options.NoticeTurns` it starts a notice turn that retries; on a denial or
expiry it says so. A host that is pushed decisions (from Gatecrasher or
Warden) calls `Runner.AuthDecided(ctx, requestID, status)` itself;
`Options.AuthStatus` replaces the lookup the polling uses.

`agent.ErrReenroll` (matched with `errors.Is`) means the parent credential
can no longer be used safely; see the agent directory doc for recovery.

Out of scope for now: launcher token exchange for subtasks or launched
agents (each gets the agent's own tokens), signing a verified run token,
and accepting Warden tokens on Nanite's own endpoints.
