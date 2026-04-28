# Envelope Pipeline

Named and documented as of Phase 3 (arch-seq 2026-04-26). The pipeline shipped
in earlier phases; this doc maps the live code paths to the Stage 1 / Stage 2 /
Stage 3 vocabulary from the three-role harness spec
(`docs/superpowers/specs/2026-04-21-agent-platform-harness-design.md`,
"Response Middleware Pipeline" subsection).

## Overview

```
LLM/Worker output (streamed)
  → Stage 1: Deterministic detection
      (fenced block regex — no LLM call)
  → Stage 2: Envelope construction + validation
      (parse JSON, validate type registry, assign instance IDs)
  → Stage 3: Narrow-LLM TLDR call [FUTURE — not in v1]
  → Frontend receives envelope(s) + clean text
```

The Chat agent context **never contains raw worker output** — only envelopes
(or clean text when no envelope block was found).

## Stage 1 — Deterministic detection

**Where:** `internal/chat/envelope.go:154` — `var envelopePattern`

```go
var envelopePattern = regexp.MustCompile(
    "(?s)```(?:volon-envelope|nanite-envelope)\\s*\n(.*?)```")
```

Fenced code blocks tagged `nanite-envelope` (or the legacy `volon-envelope`
alias) are extracted from the full response string. No LLM call; purely
regex-based. The match captures the JSON payload inside the fence.

## Stage 2 — Envelope construction and validation

**Where:** `internal/chat/envelope.go:159` — `ParseEnvelopes`; called from
`internal/service/chat_generate.go:990`.

Steps, in order:
1. For each fenced match, unmarshal the JSON payload into `chat.Envelope`.
   Invalid JSON → `EnvelopeError{Reason: "invalid_json"}` (fatal; block is
   dropped from the message).
2. `ValidateEnvelope` checks `kind`, `version ≥ 1`, and — if `type` is set —
   that the type is registered (`registeredTypes` map, populated at startup
   from `config/envelopes.yaml` via `InitCoreTypes`; extended at runtime by
   plugin calls to `RegisterEnvelopeType`).
3. For `question-form` envelopes without an `id`, a `store.EnvelopeInstance`
   row is created in the DB and the assigned ID is written back into the struct
   before the envelope JSON is persisted (chat_generate.go:1020–1036).
4. The `envelope_data` plugin filter runs on each envelope's `Data` map
   (chat_generate.go:1046–1055), giving plugins a chance to enrich or
   transform card data before persistence.
5. Validated envelopes are serialised to `envelopeJSON` and stored alongside
   the assistant message; the frontend deserialises them on load.

**Envelope struct (runtime shape):** `internal/chat/envelope.go:72`

Key fields:
- `kind` / `version` / `type` — required; type validated against registry.
- `target` — optional drawer ID for declarative panel routing (J8 v1,
  CW-20260426-0006). When set, the frontend opens the named drawer and
  renders the card into it without requiring an explicit `panel_open` tool
  call. Known v1 values: `"bottom_chat_drawer"`, `"work"`, `"workflows"`.
  Plugin-shipped panel IDs are also accepted (subject to H1 trust on the
  Stage 2 emit side).
- `mode` — optional mode/status signal (J8 v1, CW-20260426-0006). When set,
  the FE resolves the mode against a preset map (`ui/src/lib/panel-modes.ts`)
  and opens the associated panel set with `source='agent'`. v1 vocabulary:
  `planning` → opens `[work, workflows]`. Unknown modes are silent FE
  no-ops (forward-compat). Independent of `target` — both can be set on the
  same envelope.
- `data` — freeform payload; shape is envelope-type-specific.

See `docs/architecture/agent-panels.md` for the full agent-controlled panels
v1 contract (panel_open / panel_close / signal_mode tools, 4-state dismiss
machine, plugin trust gate, mode preset map).

## Stage 3 — Narrow-LLM TLDR (future)

Not implemented. The original spec described an optional narrow LLM call to
produce a TLDR summary of the worker result, included alongside the envelope
so the Chat agent can narrate it. This remains aspirational; the
implementation cost was deferred from B4 scope per arch-seq Phase 3 re-scope
(2026-04-26). When it lands it will slot between Stage 2 output and the Chat
agent's receive path without changing Stage 1/2.

## Type registry

**Manifest:** `config/envelopes.yaml` — lists all core types with optional
`component` / `export` for frontend codegen. Backend reads this at startup;
frontend codegen (`scripts/generate-plugin-imports.mjs`) generates
`ui/src/generated/plugin-envelopes.ts` from it.

**Runtime registration:** `chat.RegisterEnvelopeType` / `UnregisterEnvelopeType`
(`internal/chat/envelope.go:27,37`) — called by the plugin host during
load/unload. Plugin types declared in `plugins/*/plugin.yaml` under
`registers.envelopes` are registered here.

**Schema:** `config/envelopes.schema.json` — validates the manifest structure
(not runtime envelope instances).

## Response path

Envelope responses (user completing a card) flow via
`POST /api/envelopes/{id}/respond`. See `docs/envelopes.md` for the full
response lifecycle (ResponseV1 schema, handlers, 409 dedup, trust boundary).

## Emit-side helpers

- `chat.BuildKBEnvelope` (`envelope.go:112`) — constructs a `kb-result`
  envelope from a `search_kb` tool result. Called from
  `chat_generate.go:1780`.
- `commands.go:133,304` — slash-command implementations that produce
  envelope blocks which `ParseEnvelopes` extracts downstream.
- `internal/mcpserver/handlers.go:29` — PTY output path; same
  `ParseEnvelopes` extraction for streamed PTY content.
