# Tool Enrichment — operator notes

Per-tool `Hints` (Preconditions / AntiPatterns / ChainsWith / OutputShape)
surfaced to the LLM via a compact "## Tool Overrides" markdown block appended
to the per-turn system prompt. Lets the broker add targeted guidance for
specific tools without modifying tool descriptions themselves.

Ticket: `CW-20260420-0006` (Agent Platform P1 ToolSurface).

## Where things live

- **Hints type + codec + composer + Enricher interface:** upstream in
  `github.com/hollis-labs/go-toolbroker/broker` (v0.1.0+). `broker.Hints`,
  `broker.Enricher`, `broker.NopEnricher`, `broker.ComposeOverrideBlock`,
  `broker.MarshalHints` / `broker.UnmarshalHints`.
- **Storage schema (nanite-owned):** `tool_enrichments` table added by
  `internal/store/migrations/022_tool_enrichments.sql`. Columns:
  `tool_name TEXT PRIMARY KEY`, `hints_json TEXT`, `updated_at TEXT`.
- **Store CRUD (nanite-owned):** `(*store.Store).GetToolEnrichment` /
  `UpsertToolEnrichment` / `ListToolEnrichments` / `DeleteToolEnrichment`
  in `internal/store/tool_enrichments.go`.
- **SQLite-backed Enricher (nanite-owned):** `toolclient.NewStoreEnricher`
  implements `broker.Enricher` over `*store.Store`.
- **Wiring:** `toolclient.New` constructs a `storeEnricher` automatically
  when a store is available. `SelectToolsAsProvider` returns
  `*SelectResult{Tools, OverrideBlock}`. `service.SelectForAgent` propagates
  `OverrideBlock` into `service.ToolSelection`. `chat_generate.go`'s
  `composeExtraSystemPrefix` appends it after `nativeToolGuide`.

## How to add an enrichment

Today, enrichment records are written via the store's `UpsertToolEnrichment`
method. Expected authors: admin tooling, CLI scripts, or (planned) the
Tool Broker Enrichment Pipeline probe agent (`CW-20260419-0024`).

Manual insertion via a one-off Go script:

```go
hints := broker.Hints{
    OutputShape:   "Paginated array of {id, name, status}",
    AntiPatterns:  []string{"Do not confuse id with name"},
    Preconditions: []string{"Call list_* first to get a real id"},
    ChainsWith:    []string{"my_tool_get"},
}
hintsJSON, _ := broker.MarshalHints(hints)
err := s.UpsertToolEnrichment(store.ToolEnrichment{
    ToolName:  "my_tool",
    HintsJSON: hintsJSON,
    UpdatedAt: time.Now().UTC(),
})
```

Direct SQL insertion (for operators comfortable with sqlite3):

```sql
INSERT INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (
    'my_tool',
    '{"output_shape":"Paginated array","anti_patterns":["do not X"]}',
    '2026-04-20T00:00:00Z'
) ON CONFLICT(tool_name) DO UPDATE SET
    hints_json = excluded.hints_json,
    updated_at = excluded.updated_at;
```

Field reference (all optional, JSON snake_case):

- `preconditions` (array of strings) — assertions the agent should verify
  before invoking the tool.
- `anti_patterns` (array of strings) — ways the agent commonly misuses the
  tool that the broker wants to prevent.
- `chains_with` (array of strings) — other tools this one is commonly
  paired with.
- `output_shape` (string) — one-line description of what the tool returns.

## How to verify a block reaches the LLM

The service-layer integration test
`TestOverrideBlockReachesPrefix_EndToEnd` in
`internal/service/chat_generate_test.go` exercises the full pipeline
(store → storeEnricher → ComposeOverrideBlock → SelectResult →
composeExtraSystemPrefix) against a real SQLite store with a synthetic
`example_tool` enrichment.

In a running dev instance, the block is part of the `extraSystemPrefix`
string in `generateResponse` and is forwarded to the provider as the leading
portion of the system prompt. Add a temporary `slog.Info` just before the
provider call to log it, or check the provider's request-side logs.

## Limits (v1)

- Override block line targets ~40 tokens per enriched tool. With 10
  enriched tools in a turn, expect ~400 tokens of system-prompt overhead.
  The composer does not enforce a hard budget today.
- Enrichment records replace in place (`ON CONFLICT DO UPDATE`). No
  version-hash / revision history yet — the probe agent (CW-20260419-0024)
  will add revision tracking.
- No admin UI. Maintenance is via SQL or programmatic store access.
- Progressive discovery mode does not inject override blocks for summary
  entries (the LLM doesn't see the real tools yet). Overrides apply on
  subsequent turns once `request_tools` expands specific definitions.

## External app tool names

Do not insert enrichment records for specific external-app tools in
migrations or in nanite source. External app tools (MCP-provided or
plugin-owned) are dynamic — their names arrive at runtime and may not
exist in a given nanite instance. Populate enrichment via admin tooling
or the probe agent, per-deployment. Test fixtures use synthetic names
(`example_tool`, `test_tool_a`).
