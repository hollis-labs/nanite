# Changelog

## Unreleased

- A subagent that stops at its step, token or context limit now finishes
  with status `limit` and the stop reason instead of `succeeded`. The
  `subagent_spawn` and `subagent_status` results are flagged as errors, and
  `SubagentFinished` carries the reason in `Limit`. The jobs table gains a
  `reason` column (migration 00006).
- Compaction checks its progress. A pass that fails or frees less than 10% of
  the context stops further passes for the rest of the turn, emits
  `CompactionFailed`, and the turn continues. A turn makes at most three
  passes. An empty summary is a failure, not a compaction.
- Every Claude 5.x model, and undated 4.x aliases, now get the interleaved
  thinking beta when reasoning is enabled.
- chatrunner maps `model_context_window_exceeded` to the `context_exceeded`
  finish reason and `refusal` to `refusal`.
- `Complete` returns an error wrapping `failure.ErrEmptyResponse` on every
  adapter when the model returns no text. The OpenAI Chat Completions path
  used to return an empty string.

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
