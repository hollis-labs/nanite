# Tesseract v0.10 migration

Nanite consumes the immutable `github.com/hollis-labs/tesseract` `v0.10.0`
release. The tag resolves to commit
`90dbe0cac065e6ed21305a5e13e5f16543c69206`; do not add a `replace` directive,
branch pin, or pseudo-version.

## What v0.10.0 changed for Nanite

v0.10.0 is public-preview hardening. Its breaking changes are concentrated in
the `tesseract serve` HTTP daemon — loopback-by-default bind, token required on
reads, `admin` scope enforced, unknown JSON fields rejected, 10 MiB body cap.
**Nanite runs neither of those surfaces**: it uses the embedded
`*tesseract.Tesseract` and, optionally, the `tesseract mcp` stdio server. None
of the daemon changes reach it.

One change did reach Nanite, and it is invisible to the compiler:

- **`context_plan`'s assembly budget was renamed.** `budget_items` →
  `max_items`, `budget_tokens` → `max_tokens_estimate`, with the token default
  raised 4000 → 8000 to match `context_pack` and `POST /v1/context/packet`.
  Tesseract **refuses** the retired names rather than ignoring them, precisely
  because an ignored name would have silently doubled the caller's token
  budget. `internal/contextbroker/source_tesseract.go` sent both old names and
  was updated; `TestTesseractSourceUsesContextPlanExecuteContract` asserts the
  new spelling.

  `budget_tokens` still exists on `tesseract_recall`, `tesseract_history` and
  `tesseract_get`, where it is a different knob — the response serialization
  ceiling, not an assembly budget. It is unchanged there. Do not "fix" those
  call sites to match.

No MCP tool was renamed in v0.10.0, so persisted agent configuration and
operator allowlists carrying the names in the table below remain correct.
Nothing in v0.10.0 is a data migration: existing stores are tightened in place
on the next open, and backups taken by earlier versions remain restorable
(v0.10.0 writes the new directory-based v2 backup format, which — unlike v1 —
actually contains the memory and knowledge tables).

The OpenTelemetry rename in v0.10.0 (`FE_OTEL_REDACT_PROMPTS` →
`HOLLIS_OTEL_REDACT_PROMPTS`, `fe.*` span names → `hollis.*`) does not affect
Nanite, which references neither spelling.

## Runtime contract

The embedded root is `*tesseract.Tesseract`, opened with explicit stable
`DBPath` and `RecordsDir` values and closed during Nanite shutdown. Nanite uses
Tesseract's public memory package for ranking, paging, projection, hydration,
and reinforcement; its SQLite schema is not an integration API.

Memory writes require one of these typed namespace shapes:

```text
user/{user_id}/memory/{type}
user/{user_id}/project/{project_id}/memory/{type}
user/{user_id}/session/{session_id}/memory/{type}
```

Nanite defaults ordinary memories to `notes` and captured tool lessons to
`learnings`. The flat `user/{id}/memory` form remains a cross-type recall
prefix and must not be used for writes.

Recall defaults to `payload_mode=summary`. A missing body under `keys` or
`summary` is withheld, not empty. Hydrate selected revisions by ID. Scores are
nullable: chronological and lexical relevance have no score, while a semantic
score may be zero or negative. Cursor reads preserve the returned manifest and
must reuse the same ordering inputs. A recall candidate is not an access
signal; call touch only for summary-only results that actually informed work.

The current MCP names are:

| Operation | Tool |
|---|---|
| Cross-domain current read | `tesseract_get` |
| Revision history | `tesseract_history` |
| Ranked recall | `tesseract_recall` |
| Hydrate revision | `tesseract_get_revision` |
| Deprecate revision | `tesseract_deprecate` |
| Reinforce used results | `tesseract_touch` |
| Plan or execute a context fetch | `context_plan` (`execute=true` fetches) |
| Memory write | `memory_write` |
| Knowledge write | `knowledge_write` |

There are no retired-name aliases. Update persisted agent configuration and
operator-managed prompts/allowlists to these names.

## External MCP configuration

The optional managed process is configured under `tesseract`; Nanite launches
the released `tesseract mcp` stdio server and makes it the sole store owner for
that process. The default server name is `tesseract` and the trust tier is
`plugin_stdio`. `NANITE_TESSERACT_TOKEN` overrides the YAML token. Tesseract
does not expose an HTTP MCP endpoint; use Nanite's generic persisted MCP
server configuration if an operator-supplied bridge is intentionally present.

```yaml
tesseract:
  command: /absolute/path/to/tesseract
  # token: prefer NANITE_TESSERACT_TOKEN
  trust_tier: plugin_stdio
  server_name: tesseract
  # Add OPENAI_API_KEY here only when the child needs embedding access.
  env_allowlist: [PATH, HOME, XDG_DATA_HOME, XDG_STATE_HOME, XDG_CACHE_HOME,
                  XDG_CONFIG_HOME, TESSERACT_DB_PATH, TESSERACT_WORKSPACE]
```

When `command` is absent, Nanite uses its embedded `*tesseract.Tesseract`.
When it is present, Nanite does not open a second embedded handle or decay
worker against the same XDG store. API surfaces that depend on Nanite's local
memory facade are unavailable in external-only mode; agents use the discovered
Tesseract MCP tools and Context Broker source instead.

## Data and process cutover

The executable and process name is `tesseract`. Remove `CONTEXTD_ROOT`; it is
ignored. Use `tesseract path` to inspect the resolved XDG layout. Defaults use:

```text
database: ~/.local/share/tesseract/workspaces/default/main.db
records:  ~/.local/state/tesseract/records
config:   ~/.config/tesseract
cache:    ~/.cache/tesseract
```

`TESSERACT_DB_PATH`, `TESSERACT_WORKSPACE`, and the standard XDG environment
variables affect resolution.

Before the first upgraded start:

1. Stop every old writer and all `tesseract` and Nanite processes.
2. Back up the complete old database, records, state, and configuration.
3. Confirm both old and new resolved paths.
4. Start Nanite once. If the new database does not exist and
   `~/.conduit/data/index/context.db` does, Nanite copies the legacy database,
   SQLite sidecars, and records into the resolved Tesseract locations. The old
   tree is retained for rollback. A journal resumes an interrupted copy, and
   the main DB is published last so a partial copy cannot activate silently.
5. If the destination DB already exists, Nanite leaves it authoritative. If an
   unrelated destination records directory exists without a migration journal,
   migration fails closed and embedded memory remains disabled. If both a
   destination DB and migration journal exist after an interrupted activation,
   Nanite verifies the DB, sidecars, and records against the legacy source;
   any mismatch fails closed instead of treating the destination as current.
6. A hand-made `~/.tesseract` tree is not auto-migrated. Move it explicitly
   while all writers are stopped.
7. Run `tesseract migrate-namespaces` and
   `tesseract migrate-knowledge-kinds` plan-first when the corpus requires it,
   review the plan, then use its guarded apply command.
8. Verify representative current, history, recall, hydrate, and touch flows
   before bringing up additional clients.

Rollback preserves the old `~/.conduit` source: stop new writers and point the
old binary back at that preserved tree. Writes accepted after cutover are not
automatically copied backward, so reconcile them before rollback.
