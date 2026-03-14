# vault-search

Search Nanite knowledge vaults from any project context. Nanite stores research findings, notes, captured content, and reference material.

## Usage
`/vault-search <query> [--type <type>] [--tag <tag>] [--limit <N>]`

**query**: Search terms (required)
**--type**: Filter by item type (e.g., "research", "note", "task", "reference")
**--tag**: Filter by tag
**--limit**: Max results (default 10)

## Examples
- `/vault-search tool broker architecture`
- `/vault-search MCP integration --type research`
- `/vault-search otel compliance --tag audit --limit 5`

## Instructions

1. Parse the query and optional flags from the arguments.

2. Check if Nanite is running:
   - Use `cerberus_status` to check the nanite service
   - If not running, report: "Nanite is not running. Start it with `cerberus_start` or check `cerberus_health`."

3. Search Nanite using the `/nanite` skill or the Nanite API directly:
   - Use the search endpoint with the query terms
   - Apply type and tag filters if provided
   - Limit results as specified

4. For each result, display:
   - Title
   - Type and tags
   - Created/updated timestamp
   - A preview snippet (first 200 chars of content)
   - Item ID (for retrieval)

5. If results reference other Fragments Engine projects, note the cross-project connections.

## Display Format

```
=== VAULT SEARCH: "<query>" ===
<N> results found [filtered by: type=<type>, tag=<tag>]

1. <Title>
   Type: <type> | Tags: <tag1>, <tag2>
   Updated: <timestamp>
   Preview: <first 200 chars>...
   ID: <item_id>

2. <Title>
   ...

Cross-project references:
  - Result #1 references: cortex, volon
  - Result #3 references: hadron

=== END SEARCH ===
```

## When to Use
- When looking for prior research or decisions
- When checking if something has already been investigated
- When gathering context before starting a new task
- When you need reference material from across the portfolio
