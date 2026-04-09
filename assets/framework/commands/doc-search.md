Search project documentation in Vanta Conduit. Takes arguments: [query] [--project X] [--type Y] [--status Z] [--limit N].

Parse $ARGUMENTS for:
- query text (anything not prefixed with --)
- --project: scope to one project namespace
- --type: filter by doc type (architecture, decision, api, data, procedure, constraint, goal, note, summary)
- --status: filter by lifecycle (draft, reviewed, canonical, deprecated). Default: all non-deprecated
- --limit: max results. Default: 5

If query text is provided, use mcp__cortex__context_rag_query for semantic search.
If only filters (no query), use mcp__cortex__context_typed_view for structured query.

Namespace pattern: {project}/docs (or */docs if no --project)

Type mapping: architecture→system/map, decision→decision/adr, api→contract/api, data→contract/data, procedure→runbook, constraint→strategy/constraints, goal→strategy/goal, note→note/volatile, summary→brief/summary

For each result display:
[{status}] {namespace}/{key}  ({record_type})
  {first 200 chars of content}...

If no results: "No documents found matching query."
No interpretation or summary — just the results.