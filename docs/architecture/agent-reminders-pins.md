# Agent Reminders + Pin — Architecture (J11, CW-20260426-0009)

## Overview

Two built-in agent tools that give the Chat agent deterministic control over future
context injection:

- **`nanite_set_reminder`** — set a time or turn-count based reminder that fires by
  injecting a `<system-reminder>` block into the next applicable turn's context.
- **`nanite_pin`** / **`nanite_unpin`** — pin content into `SlotUserContext` so it
  persists across turns (and optionally across sessions).

## When Agents Should Reach for These

### Use `nanite_set_reminder` when:
- You want to be reminded of something N turns from now ("review the plan after 5 turns").
- You set a deferred task and want to inject a hint at a specific future time.
- You want to ensure context isn't lost after compaction by pre-scheduling a recall.

### Use `nanite_pin` when:
- There is a decision, constraint, or reference snippet the model should keep in mind
  throughout the session — without relying on the user or compaction-survial of the
  message history.
- You want a piece of content to survive across multiple sessions (use `cross_session` scope).

### Do NOT use these for:
- Calendar events or cross-system notifications (reminders are session-local).
- LLM-judge triggered content — triggers are always deterministic in v1.
- Large context blocks — both ride in the 2000-token `SlotUserContext` budget.

## Reminder Tool

### Tool: `nanite_set_reminder`

```json
{
  "text": "Don't forget to file a follow-up ticket",
  "trigger": {"type": "turn_count", "n": 5}
}
```

```json
{
  "text": "Review status",
  "trigger": {"type": "time", "at": "2026-04-28T09:00:00Z"}
}
```

### Trigger types (v1)

| Type | Shape | Description |
|---|---|---|
| `time` | `{"type":"time","at":"<RFC3339>"}` | Fires when wall clock >= `at` |
| `turn_count` | `{"type":"turn_count","n":5}` | Fires N turns after the reminder is set |

Keyword-mention triggers are deferred to a follow-up
(`followups_j11_keyword_mention_trigger`) — they require classifier integration.

### Firing mechanism

The deterministic trigger engine (`internal/reminders/Engine`) evaluates unfired
reminders on each turn via `EvalTurn(sessionID, currentTurn)`. When a trigger fires:

1. `Engine.EvalTurn` returns the fired `[]store.Reminder`.
2. `reminders.FormatInjection(fired)` renders them as `<system-reminder>` blocks.
3. The block is appended to `SlotUserContext` so it arrives in the next turn's context.
4. The inspector (`RecordReminders`) records both set + fired for I1 visibility.

There is no UI toast in v1. Reminder display is:
- `<system-reminder>` injection into context (Anthropic pattern).
- I1 dev-mode inspector — Meta tab → Reminders section.

## Pin Tool

### Tool: `nanite_pin`

```json
{
  "content": "Key constraint: no breaking changes to the JSON API",
  "scope": "session"
}
```

### Scope hierarchy

| Scope | Lifetime | Storage |
|---|---|---|
| `turn` | Cleared after current turn | In-memory only (not persisted to DB) |
| `session` | Cleared at session end | `pinned_content` table, `session_id` set |
| `cross_session` | Until explicit unpin | `pinned_content` table, `session_id` NULL |

Default scope: **`session`**.

Cross-session pins survive HandoffStash compaction by riding in `SlotUserContext`
(a non-compactable pinned slot per J10 contract).

### Budget

Pinned content shares the 2000-token `SlotUserContext` budget with:
- User-authored session context prompt
- Included documents (pointer or full)

Oldest pinned items are listed first (by `created_at ASC`) and truncate first when
the slot approaches budget. Keep pins thin and pointer-style.

### Tool: `nanite_unpin`

```json
{"pin_id": "pin-1234567890"}
```

Remove a pin by ID to free its budget. The UI Pins tab also provides an unpin button.

## Database schema (migration 039)

```sql
CREATE TABLE reminders (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    text        TEXT NOT NULL DEFAULT '',
    trigger_json TEXT NOT NULL DEFAULT '{}',
    fired_at    TEXT,
    created_at  TEXT NOT NULL DEFAULT (...),
    updated_at  TEXT NOT NULL DEFAULT (...)
);

CREATE TABLE pinned_content (
    id          TEXT PRIMARY KEY,
    session_id  TEXT,           -- NULL for cross_session scope
    scope       TEXT NOT NULL DEFAULT 'session',
    content     TEXT NOT NULL DEFAULT '',
    agent_id    TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (...),
    updated_at  TEXT NOT NULL DEFAULT (...)
);
```

## UI Surface

### Bottom drawer — Pins tab (4th tab)

`ui/src/components/drawers/BottomChatDrawer.tsx`

- Lists all active pins (session + cross_session) with content, scope label, agent attribution.
- Unpin button per item (calls `DELETE /api/pins/{id}`).
- Auto-refreshes every 5s (pins can appear mid-turn).

### I1 Inspector — Meta tab Reminders section

`ui/src/components/settings/inspector/InspectorPanel.tsx`

Shows per-turn reminder activity (dev-mode only):
- **Fired this turn** — reminders whose trigger fired; shown in green.
- **Set this turn** — reminders created by the agent this turn; shown with trigger JSON.

## API Endpoints

```
GET  /api/sessions/{id}/pins   — list pins for a session
DELETE /api/pins/{id}          — unpin (user-triggered from UI)
```

Reminder CRUD is agent-only (no user-facing API for creating reminders in v1).

## Integration Points

- `internal/reminders/Engine` — deterministic trigger evaluation.
- `internal/chat/context_client.go` `buildUserContextSlot()` — assembles pinned content
  into `SlotUserContext` alongside session context prompt and included documents.
- `internal/mcp/self_tools_reminders_pins.go` — tool handlers wired into `CallTool`.
- `internal/inspector/` — `RemindersRecord` field on `TurnSnapshot`, `RecordReminders` method.

## Known limitations (v1)

- No UI toast when a reminder fires — display is context injection only (I1 inspector
  shows it in dev-mode). Captured as `followups_j11_reminder_toast_ui`.
- Keyword-mention trigger type deferred. Captured as
  `followups_j11_keyword_mention_trigger`.
- Turn-scoped pins are acknowledged by the tool but not persisted to DB. They are
  ephemeral and cleared when the turn ends. No in-memory engine tracks them beyond
  the tool result acknowledgement in v1.
- The reminder engine is wired at the MCP tool level but the per-turn `EvalTurn` call
  must be integrated into the chat service's per-turn loop by the Phase 10 closeout.
  Currently the engine is available for direct use; the service wiring is a Phase 10
  follow-up (`followups_j11_service_loop_integration`).
