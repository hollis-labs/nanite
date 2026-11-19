# Changelog

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
