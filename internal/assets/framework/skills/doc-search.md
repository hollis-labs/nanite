# Doc Search (:doc-search)

Search project documentation stored in Tesseract without interpreting it.

## Input

```text
/doc-search [query] [--project X] [--type Y] [--status Z] [--limit N]
```

Default `limit` is 5. Supported document types match `doc-note`. Status is optional; omitting it uses current revision scope and excludes deprecated records.

## Procedure

Call `mcp__tesseract__tesseract_recall` with JSON-encoded array strings:

```json
{
  "namespaces": "[\"user/{USER}/knowledge/{PROJECT}\"]",
  "domains": "[\"knowledge\"]",
  "facet_kinds": "[\"note\"]",
  "payload_mode": "summary",
  "limit": 5
}
```

When there is query text, add `query`, `ranking: relevance`, and `search_mode: hybrid`. Without query text use `ranking: chronological`. Add JSON-encoded `tags` for `project:{PROJECT}` and `doc-type:{TYPE}` filters, and `statuses` only when explicitly requested. With no project, recall under the current user's `user/{USER}/knowledge` prefix.

Parse the outer `{results, facets, manifest}` envelope. For each result display its status, namespace/key, kind, summary, and `revision_id`. A missing body under `summary` means withheld, never empty. If the full body is needed, hydrate that revision with `mcp__tesseract__tesseract_get_revision`; this deliberate read already reinforces it once.

`manifest` includes totals, returned count, byte/token estimates, truncation details, and nullable `next_cursor`. A cursor is opaque and query-bound: continue only with identical namespaces, ranking, search mode, revision scope, query, and filters. Projection and page size may change. Summary/keys pages cap at 500; full pages cap at 100.

Call `mcp__tesseract__tesseract_touch` only for summary-only hits that actually informed work. Recall itself does not reinforce; do not touch every candidate or double-reinforce hydrated hits.

If there are no results, return `No documents found matching query.` Otherwise return only the formatted results plus a pagination notice when `next_cursor` is non-null.

## Invariants

- Read only; never write, update, or deprecate.
- Use `tesseract_recall`, not a domain-specific retired read.
- Treat optional scores as comparable only within one response.
- On cursor validation failure, restart the read; never manufacture or edit a cursor.
