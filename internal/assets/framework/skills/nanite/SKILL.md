---
name: nanite
description: Push items to the NANITE inbox, search NANITE items, or retrieve a specific item. Use this when you want to save research findings, notes, tasks, or any captured content to NANITE for review.
argument-hint: "push <title> | search <query> | inbox [query] | get <id> | vaults"
allowed-tools: Bash(nanite *), Bash(curl *)
---

# NANITE

Interact with the user's NANITE task and note manager. **Prefer the `nanite` CLI** (primary). Fall back to the local HTTP API only when the CLI is not in PATH.

## Live Configuration

!`nanite vaults 2>/dev/null || echo "STATUS: NANITE not in PATH or not configured"`

---

## Task

$ARGUMENTS

Parse the arguments to determine the operation:
- Starts with `push` → Push to inbox
- Starts with `search` → Search items
- Starts with `inbox` → List inbox
- Starts with `get` → Get item by ID
- Starts with `vaults` → List vaults

Execute with the `nanite` CLI using the vault information shown above.

---

## Operations Reference

All CLI output follows the envelope:
```json
{ "ok": true,  "data": { ... } }   // success
{ "ok": false, "error": "reason" } // error
```

---

### Push to inbox — `nanite push`

Use this to save captured content (research summaries, tasks, ideas, notes) so the user can review and triage it later. Items go to the inbox by default.

```bash
nanite push "Short descriptive title" \
  --source my-agent \
  --notes "<p>Rich content here. HTML is supported.</p>" \
  --type note \
  --priority B \
  --tags research,ai-generated \
  --contexts reading \
  --projects my-project
```

To push directly to a vault (skip inbox triage):
```bash
nanite push "Meeting notes" --vault <vault-id> --type note
```

**Flag reference:**
| Flag | Default | Notes |
|------|---------|-------|
| `--source` | `cli` | Identifies the sender; shown as `api_source` on the item |
| `--vault` | inbox | Vault ID to push to; omit to route to inbox |
| `--notes` | — | HTML string; wrap in `<p>`, use `<ul>`, `<strong>`, etc. |
| `--type` | `todo` | `todo` or `note` |
| `--priority` | — | `A`, `B`, or `C` |
| `--tags` | — | Comma-separated |
| `--contexts` | — | Comma-separated |
| `--projects` | — | Comma-separated |
| `--due` | — | ISO date: `2026-03-01` |
| `--inbox` | true | Force inbox routing even when `--vault` is set |

---

### Search items — `nanite search`

Full-text search across all non-inbox items in the active (or specified) vault.

```bash
nanite search "query terms"
nanite search "query" --type note --tags research,ai --page 0 --page-size 20
nanite search "query" --vault <vault-id>
```

**Flag reference:**
| Flag | Default | Notes |
|------|---------|-------|
| `--vault` | active | Vault ID to search |
| `--type` | `all` | `todo`, `note`, or `all` |
| `--tags` | — | Comma-separated |
| `--contexts` | — | Comma-separated |
| `--projects` | — | Comma-separated |
| `--page` | `0` | Zero-based page index |
| `--page-size` | `50` | Results per page |

---

### List inbox — `nanite inbox`

List unprocessed inbox items, optionally filtered by keyword.

```bash
nanite inbox
nanite inbox "refactor"
```

---

### Get item by ID — `nanite get`

Retrieve a single item with all its fields.

```bash
nanite get 42
nanite get 42 --vault inbox
nanite get 42 --vault <vault-id>
```

---

### List vaults — `nanite vaults`

List all registered vaults and the active vault ID.

```bash
nanite vaults
```

---

## Tips for agents

- **Always set `--source`** to your agent's name so the user can see which agent sent each item.
- For long research outputs, put the summary in the title and full content in `--notes` as formatted HTML.
- Prefer `--type note` for reference material and `--type todo` (default) for action items.
- Batch-push by calling `nanite push` once per item.
- After pushing, report the created item's `id` so the user can find it quickly.
- Use `nanite vaults` first if you need to push to a specific vault rather than the inbox.

---

## HTTP API Reference (fallback)

Use these **only** if `nanite` CLI is not in PATH. Check first: `which nanite 2>/dev/null`.

If falling back, read PORT and API_KEY from `~/.config/nanite/config.json` (`apiPort`, `apiKey`). If this file doesn't exist, report: "Nanite CLI not found and no API config at ~/.config/nanite/config.json."

**Push to inbox:**
```bash
curl -s -X POST "http://127.0.0.1:{PORT}/api/v1/inbox" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: {API_KEY}" \
  -H "X-Agent-Source: {your-agent-name}" \
  -d '{"title": "Title", "notes_md": "<p>Content</p>", "type": "note"}'
```

**Search:**
```bash
curl -s "http://127.0.0.1:{PORT}/api/v1/search?q={query}&type=all" \
  -H "X-API-Key: {API_KEY}"
```

**List inbox:**
```bash
curl -s "http://127.0.0.1:{PORT}/api/v1/inbox" -H "X-API-Key: {API_KEY}"
```

**Get by ID:**
```bash
curl -s "http://127.0.0.1:{PORT}/api/v1/items/{id}" -H "X-API-Key: {API_KEY}"
```
