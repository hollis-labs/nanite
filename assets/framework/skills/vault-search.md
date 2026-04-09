# Vault Search (:vault-search)

Search Nanite knowledge vaults from any project context. Nanite stores research findings, notes, captured content, and reference material.

## When to use

- When looking for prior research or decisions
- When checking if something has already been investigated
- When gathering context before starting a new task
- When you need reference material from across the portfolio

## Input Format

```
/vault-search <query> [--type <type>] [--tag <tag>] [--limit <N>]
```

- **query** — Search terms (required)
- **--type** — Filter by item type: research, note, todo, reference
- **--tag** — Filter by tag
- **--limit** — Max results (default 10)

## Examples

- `/vault-search tool broker architecture`
- `/vault-search MCP integration --type research`
- `/vault-search otel compliance --tag audit --limit 5`

## Procedure

1. Parse the query and optional flags from the arguments.

2. Check if Nanite is available. Prefer CLI, fall back to HTTP API:
   - **CLI (preferred):** Run `nanite search "<query>"` with applicable flags.
   - **HTTP fallback:** If `nanite` is not in PATH, read port/key from `~/.config/nanite/config.json` and use the search API endpoint.
   - If neither works, report: "Nanite is not available. Check that the nanite CLI is installed or the app is running with API enabled."

3. Apply type and tag filters if provided. Limit results as specified.

4. For each result, display:
   - Title
   - Type and tags
   - Created/updated timestamp
   - A preview snippet (first 200 chars of content)
   - Item ID (for retrieval)

5. If zero results, say: "No vault items found matching query."

## Output

```
=== VAULT SEARCH: "<query>" ===
<N> results found [filtered by: type=<type>, tag=<tag>]

1. <Title>
   Type: <type> | Tags: <tag1>, <tag2>
   Updated: <timestamp>
   Preview: <first 200 chars>...
   ID: <item_id>

=== END SEARCH ===
```

## Invariants

- Read-only — never modify vault items
- Prefer `nanite` CLI over HTTP API
- Always show item IDs so the user can retrieve full content via `nanite get <id>`
