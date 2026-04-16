# Doc Search (:doc-search)

Search project documentation stored in Vanta Conduit. Returns results without interpretation.

## When to use

- When the user types `:doc-search` or `/doc-search` with a query
- When any agent needs to retrieve documentation context
- Example: `/doc-search "how does the runner work"` — semantic search across all projects
- Example: `/doc-search --project engine --type architecture` — filtered structured query
- Example: `/doc-search --project nexus "broker pattern"` — scoped semantic search

## Input Format

```
/doc-search [query] [--project X] [--type Y] [--status Z] [--limit N]
```

**query** — Natural language question or keywords. Triggers semantic search via embeddings.

**--project** — Scope to a single project namespace (e.g., `engine`, `vanta-conduit`). Optional.

**--type** — Filter by document type: architecture, decision, api, data, procedure, constraint, goal, note, summary. Optional.

**--status** — Filter by lifecycle: draft, reviewed, canonical, deprecated. Default: all non-deprecated.

**--limit** — Max results. Default: 5.

## Procedure

Parse the user's input for query text and optional filters.

### If query text is provided → Semantic Search

Use `mcp__vanta__context_rag_query` with:
- query: the search text
- namespace: `{project}/docs` if --project given, otherwise `*/docs`
- limit: from --limit or default 5

### If only filters, no query text → Structured Query

Use `mcp__vanta__context_typed_view` with:
- namespaces: `{project}/docs` if --project given, otherwise `*/docs`
- types: mapped from --type to Conduit type (same mapping as doc-note)
- status: from --status if given
- limit: from --limit or default 5

### Format Results

For each result, display:

```
[{status}] {namespace}/{key}  ({record_type})
  {first 200 chars of content}...
```

If no results, say: "No documents found matching query."

## Output

Display the formatted results list. No interpretation, no summary, no recommendations. The caller decides what to do with the results.

## Invariants

- This skill reads only. It never writes, updates, or deletes.
- Results are returned as-is from Vanta Conduit. No filtering by the skill beyond what was requested.
- If `context_rag_query` returns an error or empty results, fall back to `context_search` with keyword matching and note: "Semantic search unavailable, using keyword fallback."
- If zero results after fallback, say: "No documents found matching query."
- Default excludes deprecated records unless --status explicitly includes them.
