# Tesseract v0.9 migration

Nanite consumes the immutable `github.com/hollis-labs/tesseract` `v0.9.0`
release. The tag resolves to commit
`764c5bca270a75653076615a92fa697c07bfe91c`; do not add a `replace` directive,
branch pin, or pseudo-version.

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
v0.9 does not expose an HTTP MCP endpoint; use Nanite's generic persisted MCP
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
