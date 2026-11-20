# Changelog

## Unreleased

- evals: `Scenario.Stubs` declares stub host tools (name, description,
  schema, read-only, canned results matched by input fields or served in
  order) offered in every arm; `Result.StubCalls` counts the task turn's
  calls. Stubs are checked with the agent's host-tool rules before any run.
- New public package `agent/providers`: `New(Config{Name, Gateway,
  Options})` builds a built-in provider (anthropic, openai) from its name
  and an `agent.Gateway` (key reference, optional endpoint, headers, auth);
  `NewNamed(authored.ProviderSpec, Options)` builds a named provider as
  providers.yaml defines one. Resolved keys go to `Options.Redactor`. `nanite-eval` and the embed-host example use
  it instead of internal packages.
- `agent.Options.WorkDir`: the working directory the file tools resolve
  relative paths against first and bash runs in (an existing absolute
  directory; empty is the process's working directory at `Open`). MCP stdio
  servers still start in the process's working directory.
- evals: **breaking** — `Run` never changes the process's working directory;
  each run works in its copy through `WorkDir`, so `Run` is safe to call
  concurrently. `Config.Dir` is the base directory for the run copies.
  New `Config.Parallel` (and `nanite-eval -parallel`, 1–8) runs that many at
  once, sharing the dollar cap; results keep scenario and arm order. A
  request that does not fit beside others' reservations now waits for them
  instead of being refused; a failed run cancels the runs in flight.
- New public package `agent/authored`: the rules for the Nanite files in an
  agent directory, for editors and CI. `ParseVariant`, `LoadVariants`,
  `CheckVariantName`, `BuiltinVariants()`, `Variant` (JSON-tagged) and
  `MaxLastN` for `variants/*.md`; `ParseProviders`, `LoadProviders`,
  `Providers`, `Selection`, `Uses()` and the `Use*` constants,
  `ProviderSpec` with the `Wire*` and `Auth*` constants, and
  `BuiltinProviders()` for `providers.yaml`. The engine loads both through
  it (the engine's variant embeds `authored.Variant`), so there is one set of
  rules. Both loaders read through an `os.Root`, so a symlink cannot reach
  outside the agent directory.
- chatrunner front desk survives a restart. Each chat's plans, step
  states, job links, suggested plans and delivery queue are saved as the
  conversation's host state on every change, and the ledger the desk made
  agent-wide. `Open` restores them and runs the new `Desk.Recover`. A job
  that finished meanwhile is applied, a job a restart stopped is started
  again (it counts toward the three attempts), and held deliveries go out
  once. `Close` saves before it stops the agent.
- `agent.Conversation.HostState` / `SetHostState`, `agent.Agent.HostState` /
  `SetHostState` and `ConversationHostStates`: host values that outlive a
  restart and that the model never sees (stored with the conversation
  settings, under a `host.` key prefix; no migration).
- A background job `Close` cancels now has the reason `shutdown` (a job
  `Cancel` was asked to stop keeps a plain cancellation), and a job
  the next start finds still running (status `lost`) has `process exit`
  (`agent.JobReasonShutdown`, `agent.JobReasonProcessExit`), so a host can
  tell a restart from a cancellation or a failure.
- Fix: a turn sent the moment the previous turn's terminal event arrives
  waits for that turn to release the conversation instead of failing busy.
- Steering (libs `agent/steer`), off unless `agent.Options.Steering` is set.
  A root turn gets the conversation's mode (a preset: instructions and output
  style in the `mode` slot), the work slots in front with their instructions
  and top-of-mind tools (in `user_context`, before the message), reflexes
  (`remind` adds a reminder; `halt` answers without calling the model; other
  action kinds go to the host), and classifier suggestions that are never
  applied unless the policy sets a facet to `auto`. A notice turn gets the
  mode and work slots only. See `docs/steering.md`.
- `Conversation.SetMode`, `Mode` and `AcceptSuggestion`. Only the person sets
  a mode; there is no model tool for it, and it is kept across restarts
  (store migration 8, `conversation_settings`).
- `agent.Input.Situation`: what is in front of the person, for steering.
- `Conversation.Steer` steers a person's message ahead of its turn, so a host
  sees a reflex halt first; the turn takes the result in `Input.Steer`.
  chatrunner steers before front-desk intake and does not plan a halted
  message; `DeskOptions.Mode` defaults to the conversation's mode.
- chatrunner `Options.Situation` (the situation for each turn) and
  `Options.OnSteered` (each `Steered` event, with the reflex actions that
  never go on the wire).
- New event `Steered`, sent by chatrunner as the `nanite.steer` activity.
- Dependencies: libs `agent/steer` v0.1.0, through the Substrate BOM (which
  also moves libs `harness/core` forward).
- Trace records of steered turns fill `situation`, `classified`, `why` and
  `fired`, with no prompt content.
- Reflex cooldowns are kept per conversation across restarts;
  `Steering.StateFill` adds host facts for predicate reflexes.
- providers.yaml: a `desk` use for the front desk's planner, falling back
  to chat. `agent.Agent.Complete` makes one model request for a use
  outside any conversation, and chatrunner `DeskOptions.PlanWithModel`
  plans with it. A desk job's result whose lookup fails is read up to three
  times before its item is failed.

- chatrunner: the front desk (`Options.Desk`, libs `agent/desk`). Intake
  plans each user message (off, shadow, suggest or auto); suggested plans
  open the turn as a `nanite.desk.plan` activity and run on
  `Desk.AcceptPlan`; dispatched plans run as background subtasks, wave by
  wave as dependencies finish, with earlier results in later goals, and are
  tracked as topics in a ledger. Every notice no running turn carried goes
  through a delivery editor that paces it into composed `desk` notices:
  now, merge, hold while the owner answers, digest or file.

## 0.6.0

Code Mode: an agent can hand work to a `subtask` running a chosen variant,
with forked parent context, a validated result contract, inherited approvals,
side-by-side fan-out under shared budgets, progress events and trace links,
plus base-prompt guidance, docs (`docs/code-mode.md`) and evals. Schema
validation is now bounded, compaction counts tool schemas, per-turn
context no longer breaks the prompt cache, and no slot entry is dropped
silently. Hosts can start subtasks themselves (`Conversation.Dispatch`), and
notices reach the chat wire.

Breaking (no aliases or compatibility layer):

- `subagent_spawn`, `subagent_status` and `subagent_cancel` are removed;
  use `subtask`. The builtin group `subagents` is now `subtasks`.
- `providers.yaml`: the use `subagent` is renamed `subtask`.
- `subtask`, `return_result`, `parent_history_search` and
  `parent_history_get` are reserved tool names.
- Tool calls in a step run in model order; only consecutive read-only or
  `subtask` calls run side by side.
- Subtask limits: `agent.Options.SubtaskLimit` (or `runtime.subtask_limit`)
  sets the worker and lead pools; above 8 is refused. A foreground subtask
  over the limit waits for a slot.
- Events: `SubagentStarted` replaces `Role` with `Variant`, `Goal` and
  `Background`; `SubagentStarted`/`SubagentFinished` gain `ParentID` and
  `Depth`; the `nanite.subagent` activity carries `variant`, `parent_id`
  and `depth` instead of `role`.
- Trace records (libs `agent/trace` v0.2.0): `usage` is the turn's own
  requests only, without its subtasks'.
- Dependencies: libs `agent/trace` v0.2.0 and `agent/agentdir` v0.2.0.
- Schemas: unsafe schemas are refused with `ErrUnsafeSchema`; tool input
  nested deeper than 64 levels is refused; tool schemas over 256KB are
  refused.
- OpenAI usage: `InputTokens` excludes cached and cache-written tokens.
- Per-turn context (`user_context`, including chatrunner's turn context)
  moves from the system prompt into the turn, as a `nanite.turn_context`
  block stored with the user's message, so it now appears in history.
- Notices: `Agent.Notices()` and the `agent.Notice` alias are removed; use
  `Agent.SubscribeNotices(ctx)`. A notice turn uses
  `Input.Source = agent.SourceNotice`; an unknown `Input.Source` is refused.
- chatrunner: a second run on a conversation that already has one active is
  refused before it starts, instead of replacing the active run's entry.

- Per-turn context no longer breaks the prompt cache. Slots now have a
  placement. `user_context` (including chatrunner's turn context) is
  placed in the turn: it goes in a `nanite.turn_context` text block in front
  of the user's message and is stored with it, so the system blocks and
  earlier messages stay byte-identical across turns. Other slots stay in
  the system prefix. `agent.Options.SlotPlacement` moves slots, and
  `agent.TurnContextBlock` names the block for hosts that show history.
  A turn's context is now part of that message's history instead of being
  dropped after the turn.
- No slot entry is dropped silently. An entry over what is left of its
  slot's budget is replaced by its pointer, or else trimmed to fit with a
  marker (new `SlotTrim`). It is left out only when no room is left. Trims
  and omissions are logged, reported in `SlotsAssembled` and the
  `nanite.slots` activity (`cuts`), and listed in the trace.
  `Options.SlotBudgets` overrides budgets per slot (except Universal), and
  `agent.SlotBudget` reports them.
- Compaction counts everything a request carries. Tool schemas fill the
  Tools slot of the context window, and the provider's reported input for
  the last request is checked against the budget too. Before, a host with
  hundreds of tools could grow requests past the window without compaction
  ever firing. New `agent.Options.CompactAt` sets the budget in tokens.
  When the fixed slots and tool schemas alone fill the budget, compaction
  is skipped with a warning, since it cannot help.
- OpenAI usage: `InputTokens` now excludes cached and cache-written tokens,
  which is its meaning on the Anthropic wire and everywhere Nanite sums
  usage. Cached tokens were previously counted twice in turn usage, the
  turn token budget, traces and chatstream's uncached input.
- Host dispatch: `Conversation.Dispatch` starts subtasks for the host without
  a model call. It keeps the agent's gates: it needs the `subtask` tool (and
  the background tools for a background start), and each start is evaluated
  as a `subtask` call (a deny refuses it, an ask goes to the gate, and an
  approval covers that start only, never the model's own calls). It takes
  the same path as a model's `subtask` call (variants,
  budgets, slot limits, the turn budget, forked context, the result contract,
  relayed approvals attributed `dispatched`, progress and trace links). It
  runs as a run holding the conversation; `Result.Dispatched` has one outcome
  per start, in order. Its trace record has source `host` and lists each start
  in `tool_calls[]`. `agent.DispatchStart` is a plain struct with the
  `subtask` call's fields.
  `Conversation.Job` reads a background job, and `Conversation.PostNotice`
  posts a notice.
- Breaking, notices: `Agent.Notices()` (a lossy channel of 64) is replaced by
  `Agent.SubscribeNotices(ctx)`: each subscriber gets every notice in order,
  up to 1,024 unread; past that the oldest is dropped and counted in
  `Dropped`. A posted notice is also reported as a `NoticePosted` event on the
  conversation's running turn, if any. A turn started because of a notice
  uses `Input.Source = agent.SourceNotice` with empty text; its user message is
  `agent.NoticeTurnText` and the notice reaches the model in the session slot
  only (trace source `reflex`). Notices caused by a notice turn's work are
  `Chained`. The `agent.Notice` alias is removed.
- chatrunner: notices reach the wire as a `nanite.notice` activity, on the
  running turn or, with `Options.Observe` set to the chat Service's `Observe`,
  as a run of their own, per conversation; `Options.NoticeTurns` makes such a
  notice start a notice turn instead, adopted by the Service before it starts,
  never for a chained notice, and at most three in a row before a user turn. `Runner.Dispatch` publishes a host dispatch on the wire, so
  its relayed approvals are answered there. A relayed card from a dispatched
  subtask says it was started by the host.

- Code Mode evals (experimental): six scenarios (code search, research from a
  local corpus, a tool-heavy chore, a council review through a lead variant,
  a task needing earlier conversation history, and a list longer than a small
  result cap), each run with direct tool use and with `subtask`, plus arms
  that vary `last_n` and the result cap. Metrics come from the trace records
  (parent context tokens, total tokens, estimated cost, wall time, approvals,
  subtasks and the variants they ran, compactions). The scripted run is a
  harness check that runs in CI (`go test ./evals`); `go run ./cmd/nanite-eval
  -live` runs against a chosen model under a required token cap and a dollar
  cap that every request reserves against. See `docs/evals/code-mode.md`.
- Breaking, tool call order: a step's calls now run in model order, and
  only consecutive calls that may run side by side (read-only tools and
  `subtask`) run together, bounded by `runtime.tool_concurrency`. Before,
  every read-only call ran ahead of the step's other calls whatever their
  order. No option restores the old order.
- Breaking, fan-out: several consecutive `subtask` calls in one step run side
  by side, with results in call order. `subtask` is concurrency-safe but not
  read-only.
- Breaking, subtask limits: two agent-wide limits of `agent.Options.SubtaskLimit`
  slots each (default 4, at most 8), one for leads (`can_spawn` variants)
  and one for every other subtask, replace the single fixed limit of four.
  A foreground subtask over its limit now waits for a slot while its turn
  lasts instead of being refused; a background one is still refused at
  once, and the refusal text names the limit.
- New `agent.Options.TurnTokenBudget`: a token ceiling a root turn shares
  with its foreground subtasks at any depth, compaction summaries included
  (estimated; a failed summary call is not charged). Reaching it stops
  every running subtask mid-step with
  `limit` (`token_budget`) and the root at its next step; no foreground
  subtask starts on a spent budget; a subtask that has already returned its
  result keeps it. Background subtasks keep only their
  variant's budget; a resumed turn starts a fresh budget. A cost budget is
  not enforced: usage carries no price yet.
- Dependencies: libs `agent/agentdir` v0.2.0 and `agent/trace` v0.2.0
  (the trace record gains `ParentTurnID`, `ParentCallID`, `Depth` and
  `Subtasks`, visible through `agent.TraceRecord`), with Substrate repinned
  to the BOM that lists both.
- `host.yaml` gains `runtime.subtask_limit`, `runtime.turn_token_budget`
  and `runtime.subtask_result_cap` (libs `agent/agentdir` v0.2.0), read when
  the agent opens. A non-zero `agent.Options` value wins, then `host.yaml`,
  then the default. A `subtask_limit` above 8 is refused at open and by
  `nanite validate`, not clamped. The effective default result cap is about
  5,900 tokens (6,000 clamped to what one tool result shows in full).
- Code Mode guidance: the base prompt gains a short section on when to
  delegate with `subtask`, how to choose a variant, that a subtask sees only
  what it is passed and cannot ask the person, and how to read its status
  (only `ok` is finished work). The built-in variant prompts state their contract more fully:
  `code` names the files it changed and what it checked, `research` gives a
  source for each finding, `planner` names what each step touches.
- `nanite init` writes a commented example variant, `variants/reviewer.md`.
  An example lead variant is at `examples/variants/council.md`.
- New guide: `docs/code-mode.md`.
- New `subtask` tool, `subtask({goal, return_spec, variant, inputs?,
  context?, background?})`. `variant` is required; the model chooses and
  there is no default.
- Result contract: a child ends by calling the new `return_result({status,
  result, artifacts[], notes})`. A JSON Schema `return_spec` is validated
  (a malformed one is refused up front), with one repair, then `partial`
  with the validation error. The parent gets `{status, result, artifacts,
  notes, reason, cost{tokens_in, tokens_out}, child_id, variant, steps}`;
  status is `ok`, `partial`, `limit`, `error`, `cancelled` or `denied`
  (set only by the engine, when a person refused an approval the subtask
  asked for; `return_result` refuses `denied`). A child that
  never calls `return_result` gives `partial` (`no_return_result`) with the
  last 2KB of its output. The whole result is capped for every status
  (variant `result_cap`, else the new `agent.Options.SubtaskResultCap`, else
  6,000 tokens, clamped to the 23KB a tool result shows in full, so about
  5,900 in effect), with
  at most 50 artifacts. A larger return is scrubbed and stored for 30 days,
  and the parent gets a `result_ref` pointer for `fetch_tool_result` with
  `result_expires_at`. `return_result` must be the only call in its step,
  and is now a reserved tool name. A child gets `fetch_tool_result` and
  `search_tool_result` even when its variant's allow list leaves them out;
  an explicit deny removes them.
- Bounded schema validation, so no schema or value from a host, an MCP
  server or the model can crash, hang or exhaust the process. Nanite now
  validates with a vendored copy of jsonschema-go v0.4.3
  (`internal/third_party/jsonschema`, MIT, with upstream's tests and the
  JSON-Schema-Test-Suite subset; see its README) patched so `Validate` takes
  a context and a work budget: it stops past 200,000 units of work, charged
  in proportion to what each keyword does (schema-value visits, enum/const
  nodes compared, uniqueItems item sizes, string lengths, required names,
  instance keys, and regular-expression matching by input length times
  program size), checks the context every 64 units, and is cut off after
  2 seconds. Error text stays bounded while a failure still names its leaf
  path: wraps add only their prefix, discarded branches build no text, and
  long messages keep their start and end. A pattern compiling to more than
  20,000 instructions is refused. Validation that ran out of budget or time
  is not sent to argument repair. Cheap static checks still refuse, with
  `ErrUnsafeSchema`: a cycle among in-place keywords (`$ref`, `$dynamicRef`,
  `allOf`, `anyOf`, `oneOf`, `not`, `if`/`then`/`else`, `dependentSchemas`,
  draft-07 schema `dependencies`); more than 64 of them in a row on one
  value; more than 10,000 such runs from one schema; patterns compiling to
  more than 100,000 instructions together; a schema over 256KB (16KB for one
  the model writes); and JSON nesting past 256 levels. A value (tool input,
  `return_result`, `ask_user` answers) nesting more than 64 levels is
  refused before validation and is not sent to argument repair; validation
  error text is cut to 4KB. An MCP tool with an unsafe schema is left out of
  the catalog with a warning naming the server and tool; a host tool keeps
  its entry and each call fails with `invalid tool schema`. Resolved schemas
  are cached by content in an LRU bounded at 64MB, sized from measured
  per-entry memory.
- Schemas the model writes (`return_spec`, `ask_user`) are held to a strict
  subset: at most 16KB, no `$id`/`$anchor`/`$dynamicRef`/`$recursiveRef`,
  keywords spelled exactly, `$ref` only to `#/$defs/<name>` or
  `#/definitions/<name>`, and no
  reference cycle that never moves into a value, which jsonschema-go would
  otherwise overflow the stack on. Tool input schemas are unchanged.
- `background_status` for a subtask embeds its result as JSON under
  `result`. Expired cached and stored tool results are purged when the
  agent opens.
- Variants: built-in `code`, `research`, `worker` and `planner` (read-only,
  and denying `mcp__*` tools, since MCP `readOnlyHint` is not trusted),
  plus authored `variants/<name>.md` in the agent directory, re-read every
  turn. Frontmatter sets tool allow and deny patterns, `read_only`, `model`,
  `budgets` (steps, tokens, wall, cost), `last_n`, `result_cap` and
  `can_spawn`, plus an optional `provider` with `model`, resolved like a
  `providers.yaml` use (`model` alone stays on the `subtask` use's provider). A
  variant only narrows tools. `nanite validate` checks them, rejects
  `read_only` with `can_spawn`, and checks that a variant's provider exists.
- Depth is one level; a `can_spawn` variant (a lead) may go one level
  further. A lead's children get a tool only when the lead allows it too.
- Budgets only narrow: `budgets.steps` lowers `max_steps`, never raises it.
  The token budget counts input, output and cache tokens, and a lead's
  includes its finished children. The wall budget is a deadline on the child
  and stops a step in progress. New stop reasons `token_budget` and
  `wall_time`, both reported as limits.
- A child's token usage rolls into the parent turn: `Usage` gains `Subtask`,
  the child conversation id, on the rolled-up event.
- Background subtasks: only a root chat starts them, and the `background`
  field is offered only alongside `background_status`. Their notice and
  `background_status` use the subtask statuses; a cancelled one is an error.
  The notice carries the child's token usage, and a parent turn still running
  at completion gets a `Usage` event with `Subtask` set and the new
  `Background` flag. Background usage is not part of that turn's total;
  chatrunner forwards it as `nanite.subagent` activity (state `usage`)
  instead of a usage delta.
- Forked context: a child's first message copies the parent's last N
  messages (`context.last_n`, else the variant's `last_n`, else 6; at most
  50), leaving out the in-flight `subtask` call and never starting on a tool
  result without its call; calls and results pair by position, not by call
  ID. `context.full` copies all of them. `context.include` copies up to 8
  of `plan`, `todos`, `handoff` and `scratchpad:<key>`. Everything copied is
  redacted before it is shortened, and the whole seed fits 32,000
  characters: include items take at most half, and the newest messages fill
  the rest, saying how many items and messages were left out.
- Children get `parent_history_search` (full-text search over the parent's
  messages; snippets are cut from the redacted message, never from the
  index) and `parent_history_get` (messages by number, 20 at a time), in the
  `subtasks` group; root conversations never get them. The store adds an
  FTS5 index, `messages_fts`, kept in step with messages by insert, update
  and delete triggers (migration 7 builds it for existing messages).
- A child's `fetch_tool_result` and `search_tool_result` also read its
  parent's and then the root's cached results (a depth-2 child: the lead's,
  then the root's), so pointers in the copied context work. Never a
  sibling's.
- `agent.Message` gains `Seq`, the message's position in its conversation,
  set on messages read back from history (additive).
- Removed `subagent_spawn`, `subagent_status` and `subagent_cancel`, with no
  alias. `background_status` and `background_cancel` now also serve
  background subtasks. The built-in group `BuiltinSubagents` ("subagents") is
  now `BuiltinSubtasks` ("subtasks").
- The `providers.yaml` use `subagent` is renamed `subtask`, with no
  fallback: a file that still says `subagent` is refused like any unknown
  use.
- `SubagentStarted` replaces `Role` with `Variant`, `Goal` and `Background`;
  `SubagentFinished` gains `Variant` and `Status`. The `nanite.subagent`
  activity carries `variant` (and `status` when done) instead of `role`.
- Subtask progress: a running subtask's parent run gets `SubtaskProgress`
  events (ID, parent ID, depth, variant, step, tool, status line,
  background, and the final status of a background subtask), rate-limited
  per subtask (500ms, 5s for a background subtask) and never shown to the
  model. Steps are the turn's own step count. A foreground subtask waiting
  for a slot first reports "waiting for a subtask slot"; one that then never
  starts gets a last progress event with `Status` `cancelled`, `refused` or
  `error`. A depth-2 subtask's start,
  progress and finish reach the root's run through its lead. chatrunner
  sends progress as `nanite.subagent` activity with state `progress`.
- Breaking: `SubagentStarted` and `SubagentFinished` gain `ParentID` and
  `Depth`, and the `nanite.subagent` activity carries `parent_id` and
  `depth` in every state (start also `background`).
- Trace records use libs `agent/trace` v0.2.0: a subtask's record sets
  `parent_turn_id`, `parent_call_id` and `depth`, and every record lists the
  subtasks its turn started in `subtasks` (conversation and tool call IDs,
  variant, status including `running` and `denied`, background, steps,
  duration, and usage of the subtask's whole tree). Breaking: a record's
  `usage` is now the turn's own requests only, without its subtasks'; the
  run's `Result.Usage` and `Usage` events are unchanged.
- Subtask approvals. A subtask is now evaluated with its root conversation's
  role and conversation (scope), and the PDP request ID is `<child>:<call>`;
  the request context's `subtask_variant` carries the variant for rules that
  treat subtasks differently, with `subtask_conversation` and
  `subtask_depth`. Grants flow down: one given in the root covers its
  subtasks; one given on a subtask's relayed ask covers that subtask and its
  own subtasks only. An ask they do not cover, and an MCP elicitation, is
  relayed to the root: the gate gets a request with the root's
  `ConversationID`, ID `<child>:<call>` and the new
  `PermissionRequest.Subtask` (`agent.SubtaskOrigin`, with a scrubbed goal
  and the root's subtask call ID), and `Question` gains `Subtask`. One ask is
  open at a time per root conversation. `PermissionDecision` gains `Human`;
  a refusal a person gave ends the subtask with status `denied`. A
  background subtask's ask waits for the root's next turn, posts a notice
  that it is waiting, and ends `unanswered` after `Options.SubtaskAskWait`
  (default 30 minutes); the notice is withdrawn if the ask ends before a
  turn shows it. chatrunner shows a relayed ask as one `approval.request`
  attached to the root's subtask call, with `descriptor.subtask`, and takes
  only a once answer on it (`standing` false), so no standing grant against
  the root chat can reach the root or a sibling; before, a subtask's ask was
  denied. The REPL names the subtask when it asks. A subtask's title is
  stored with its goal scrubbed.

- Every turn writes one trace record (`libs/agent/trace`, schema version 1),
  however it ends. It holds identity and source, provider and model, slots
  and trimmed entries, tools offered, tool calls with permission and error
  kind, compactions, usage and outcome. The CLI appends records to
  `.nanite/traces/YYYY-MM-DD.jsonl`, hosts receive them through
  `agent.Options.TraceSink` (or `TraceFiles`), and the turn span carries
  the summary as attributes.
- Named providers. `providers.yaml` in the agent directory defines providers
  by wire (`anthropic`, `openai`, `openai-chat`, `openai-responses`), base
  URL, key reference, auth and headers, so any OpenAI-compatible server or a
  model gateway can be used. Its `use` section picks a provider and model for
  chat, compaction summaries and subtasks. Hosts can add providers with
  `agent.Options.Providers`.
- Every agent now gets a base prompt in the Universal slot, replacing the
  one-line rule. It frames tool results as the record of what happened,
  reporting actions only from their results, persistence, permissions and
  checking work. `nanite init` writes a shorter `agent.md` that no longer
  repeats it. See "The base prompt" in `docs/architecture.md`.
- A subagent that stops at its step, token or context limit now finishes
  with status `limit` and the stop reason instead of `succeeded`. The
  `subagent_spawn` and `subagent_status` results are flagged as errors, and
  `SubagentFinished` carries the reason in `Limit`. The jobs table gains a
  `reason` column (migration 00006).
- Compaction checks its progress. A pass that fails or frees nothing emits
  `CompactionFailed` and stops compaction for the rest of the turn, and the
  turn continues. A pass that frees less than 10% stops later passes. A turn
  makes at most three passes. An empty summary is a failure, not a
  compaction.
- Every Claude 5.x model, and undated 4.x aliases, now get the interleaved
  thinking beta when reasoning is enabled.
- chatrunner maps `model_context_window_exceeded` to the `context_exceeded`
  finish reason and `refusal` to `refusal`.
- `Complete` returns an error wrapping `failure.ErrEmptyResponse` on every
  adapter when the model returns no text. The OpenAI Chat Completions path
  used to return an empty string.
- Contain panics in code Nanite does not own and in every goroutine it
  starts, with `libs/core/panics`. A panicking host tool yields an error tool
  result; a panicking slot source, selector or provider stream fails the
  turn; a panicking gate or PDP denies the call; a panicking event sink is
  ignored; a panicking background job is marked failed. Errors carry only a
  `pnc_` reference. `Options.PanicReporter` receives the redacted record.
  The CLI exits with status 70 (`EX_SOFTWARE`) and the reference when a
  run ends in a panic, whether contained in the turn or not.

## 0.5.0

This release is a hard break. Initialize a new authored agent directory with
`nanite init`. The former server API, fleet deployment bundle, legacy config,
and legacy databases have no compatibility layer.

- Add an embeddable `agent` package and a local CLI with interactive chat,
  one-shot prompts, conversation management, and interrupted-turn recovery.
- Assemble fixed-order context slots with host contributions. Reload authored
  settings, MCP servers, and skills between turns.
- Run tools through permission rules and optional host approval. Support
  worker and planner subagents, background jobs, questions, and notices.
- Persist conversations to JSONL transcripts with a rebuildable SQLite index.
- Add the `chatrunner` package, which runs an agent behind the
  `libs/agent/chat` conversation handler with host tools and approvals.
- Send each tool's input schema to Anthropic in full. A schema without
  `properties` now gets an empty one, and keywords such as `$defs` and
  `additionalProperties` are kept. Top-level `anyOf`, `oneOf` and `allOf`,
  which the API refuses, are folded into `properties`.
