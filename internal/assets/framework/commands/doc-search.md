Search project documentation in Tesseract. Takes arguments: [query] [--project X] [--type Y] [--status Z] [--limit N].

Use `mcp__tesseract__tesseract_recall` with:
- `namespaces`: a JSON-encoded array containing `user/{user}/knowledge/{project}` or the user's knowledge prefix when no project is supplied
- `domains`: `["knowledge"]`
- `facet_kinds`: `["note"]`
- `tags`: JSON-encoded project/type filters when supplied
- `statuses`: JSON-encoded status filters when supplied; otherwise omit it so deprecated entries stay excluded
- `payload_mode`: `summary`
- `limit`: 5 by default; maximum 500 for summary projection
- `query`, `ranking: relevance`, and `search_mode: hybrid` when query text is supplied; otherwise use `ranking: chronological`

Read the response envelope at `results`, `facets`, and `manifest`. A missing body in summary projection means withheld, not empty. Show each result's status, namespace/key, kind, summary, and revision_id. If `manifest.next_cursor` is non-null, mention that more results are available; reuse it only with identical ordering inputs. Hydrate a selected result with `mcp__tesseract__tesseract_get_revision`. Touch only summary-only revision ids that actually shaped work; a hydrated revision is already reinforced once.

If no results: "No documents found matching query."
No interpretation or synthesis — just the results and pagination notice.
