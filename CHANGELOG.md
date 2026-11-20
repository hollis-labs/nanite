# Changelog

## Unreleased

Breaking (next minor version): the subagent tools are replaced by `subtask`.

- New `subtask` tool, `subtask({goal, return_spec, variant, inputs?,
  context?, background?})`. `variant` is required; the model chooses and
  there is no default. Results carry `status` (`ok`, `partial`, `limit`,
  `error` or `cancelled`), the child's text, steps and token usage.
- Variants: built-in `code`, `research`, `worker` and `planner` (read-only,
  and denying `mcp__*` tools, since MCP `readOnlyHint` is not trusted),
  plus authored `variants/<name>.md` in the agent directory, re-read every
  turn. Frontmatter sets tool allow and deny patterns, `read_only`, `model`,
  `budgets` (steps, tokens, wall, cost), `last_n`, `result_cap` and
  `can_spawn`, plus an optional `provider` with `model`, resolved like a
  `providers.yaml` use (`model` alone stays on the subagent provider). A
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
  ID. `context.full` copies all of them. `context.include` copies `plan`,
  `todos`, `handoff` and `scratchpad:<key>`. Everything copied is redacted
  before it is shortened, and the whole seed fits 32,000 characters, keeping
  the newest messages and saying how many were left out.
- Children get `parent_history_search` (full-text search over the parent's
  messages; snippets are cut from the redacted message, never from the
  index) and `parent_history_get` (messages by number, 20 at a time), in the
  `subtasks` group; root conversations never get them. The store adds an
  FTS5 index, `messages_fts`, kept in step with messages by insert, update
  and delete triggers (migration 7 builds it for existing messages).
- A child's `fetch_tool_result` and `search_tool_result` also read its
  parent's and lead's cached results, so pointers in the copied context work.
- `agent.Message` gains `Seq`, the message's position in its conversation,
  set on messages read back from history (additive).
- Removed `subagent_spawn`, `subagent_status` and `subagent_cancel`, with no
  alias. `background_status` and `background_cancel` now also serve
  background subtasks. The built-in group `BuiltinSubagents` ("subagents") is
  now `BuiltinSubtasks` ("subtasks").
- `SubagentStarted` replaces `Role` with `Variant`, `Goal` and `Background`;
  `SubagentFinished` gains `Variant` and `Status`. The `nanite.subagent`
  activity carries `variant` (and `status` when done) instead of `role`.
- `return_spec` and `context` are recorded and passed to the child; result
  validation and forked context come in later releases.

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
  chat, compaction summaries and subagents. Hosts can add providers with
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
