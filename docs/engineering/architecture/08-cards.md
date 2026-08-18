# Cards

Structured, interactive UI content agents use to present rich data to a user instead of raw prose — tables, approvals, diffs, forms. Formerly called "the envelope system" as a subsystem name; **Envelope now stays scoped narrowly to the wire/transport wrapper**, Cards is the rendered UI system built on top of it. See `GLOSSARY.md`.

## Purpose, stated plainly

This is meant to let the harness inject rich, structured, typed/validated data for a user to act on — without costing the agent turns to construct UI, and without bloating its own context with data that isn't conversationally useful. The canonical example: an agent triggers a deterministic data fetch (e.g. `torque_fetch_latest`), the harness renders the result as an interactive table the user can act on directly, and the structured response posts back via API — the agent's own context stays light regardless of how much data the table holds. Comparable in spirit to Microsoft Teams' Adaptive Cards; this implementation is considered a genuinely good one, worth continuing to invest in.

## Primitives vs. concrete cards — compose, don't multiply types

A real primitive set exists and is the right foundation: `info-card`, `list-card`, `metric-card`, `progress-card`, `confirmation-card`, `table-card`, `timeline-card`, `diff-card`, plus `document-viewer`/`report-card`/`error-report`/`approval-card`/`proposal-card`. **Standing principle going forward: build concrete, feature-specific cards by composing this primitive set** (using the existing `props` discriminator mechanism, already used for `approval-card`/`proposal-card`) rather than minting a new top-level manifest entry per feature. `todo-list`, `plan-review`, and `subagent-spawn-approval` are being rebuilt this way rather than staying separate types.

**Interactive tables with row-level actions** are being built as a first-class, provider-agnostic primitive extension to `table-card` (schema-validated per-row/per-column actions, the same response-routing pattern `approval-card` already uses) — previously proven feasible as a one-off plugin implementation, now being generalized so any plugin can build on the pattern.

## The context-replay gap — the real engineering lever here

Tool-auto-emitted card data is already excluded from the *current* turn's tool-result content the agent sees. But the full data still gets appended into the assistant's own persisted response text, which *is* replayed into every subsequent turn's conversation history — none of the compaction stages target this specifically (they operate on tool_result blocks, not card data inside assistant messages). **This is the concrete mechanism that actually makes the harness-injects-rich-data-agent-context-stays-light vision hold at session scale**, not just for one turn. Real, scoped task, not a redesign.

## Cut

`question-form` (a pre-Cards-system legacy attempt — already special-cased in the core turn loop unlike every other type, itself evidence it doesn't fit the uniform model). The four backend-only messaging card types (`message-request`/`reply`/`notification`/`handoff` — unfinished scaffolding, redundant with `agent_messages.kind`'s own validation). The giphy/oembed/support-ticket plugins and their card types (`kb-result`, `giphy-modal`, `resolution-capture`, `ticket-form`, `ticket-confirmation`) — confirmed demos.

## Naming cleanup

"Volon" (an intermediate brand-history name) is eradicated from the codebase entirely, including the `volon-envelope` fence tag. `fragments-envelope` is also dropped. Backend and frontend both recognize `nanite-envelope` only.

## A known, live bug in CLI-agent boot content

CLI-launched agents are currently told to consult files for "the current card type list" that don't exist in this repo, and separately given a stale, incomplete list directly in their boot prompt. Fix as part of the CLI boot-directory content work (see [Agent Launching](02-agent-launching.md)) — don't hardcode a static type list into planted content again; source it from the manifest the codegen check already validates against.
