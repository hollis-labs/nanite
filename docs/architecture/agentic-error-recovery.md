# Agentic Error Recovery

> Status: placeholder — full doc lands with E2 (CW-… sprint SP-20260428-0002).
> Sections below are populated incrementally as each layer of the
> "self-healing tool surface" lens lands.

## Layer 1 — Discovery: `nanite_tool_describe`

Landed in CW-20260429-0005 (A1).

`nanite_tool_describe(name)` returns the contract for any first-party
self-tool: description, input schema, golden examples, related tools/skills.
Agents call it when they're unsure about a tool's input shape so they don't
burn round-trips on schema-discovery via repeated failures.

### Golden examples convention

Per-tool examples live at `internal/mcp/examples/<tool_name>.json`. Each file
is a JSON array; each element has the shape:

```json
{
  "title": "Short human-readable scenario name",
  "args": { "...": "the args the agent would pass to the tool" },
  "result": { "...": "optional — the shape of a representative success" },
  "notes": "Optional gotchas or chaining hints"
}
```

The runtime embeds these via `//go:embed` in `internal/mcp/self_tools_describe.go`,
so binaries are self-contained — no filesystem dependency at runtime.

Coverage is 1-per-tool best-effort at v1; a full audit pass that adds a
second example per tool covering the second-most-common usage is tracked
separately (E1 in the same sprint).

### Cross-references

`describeRelations` in `internal/mcp/self_tools_describe.go` is the curated
related-tools / related-skills map. It is hand-written rather than derived
from prefixes so the surface stays useful — auto-derivation devolves into
"every tool with the same prefix" noise quickly.

### Unknown-name handling

When the requested name isn't on the self-tool surface, the handler returns
a structured `tool_not_found` error with the three closest matches by
case-insensitive Levenshtein distance. The agent reads those, picks one,
and re-describes in a second round-trip — no guessing.

## Layer 2 — Validation feedback (TODO, B1)

When E2 lands, this section will document how envelope-validation failures
are surfaced back to the agent with actionable remediation hints rather
than raw schema-validator output.

## Layer 3 — Recall integration (TODO, D1)

When D1 lands, this section will document how prior tool-failure outcomes
are surfaced into the per-turn grounding block so the agent learns from
its own past mistakes within a session.
