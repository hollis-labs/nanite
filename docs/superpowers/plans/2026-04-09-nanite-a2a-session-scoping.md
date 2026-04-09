# Nanite A2A Session Scoping + Handoff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend Nanite's A2A (agent-to-agent) messaging from unconstrained TEXT addressing to session-scoped `(session_id, agent_id)` addressing. Add an explicit handoff flow that mutates `session_agents.is_primary` in a single transaction. Expose everything as CLI + bidirectional MCP.

**Architecture:** The existing `a2a_messages` table gets new `from_session_id`, `from_agent_id`, `to_session_id`, `to_agent_id` columns (clean break — drop the unconstrained `from_agent`/`to_agent`). A reserved `"user"` sentinel identifies the human in a session. Handoff is built on the existing `session_agents` junction table — no new binding table — with a `session_handoffs` audit table for the approval workflow. MCP tools expose send/inbox/ack/resolve/catch-up as request/response, and `a2a_subscribe` as streaming for push delivery to subscribed agents.

**Tech Stack:** Go 1.26, SQLite (WAL), `mark3labs/mcp-go` for MCP transport, existing `internal/store/a2a.go` and `internal/api/a2a.go` as starting points (both will be rewritten).

**Spec:** `docs/superpowers/specs/2026-04-09-nanite-agentrc-consolidation-design.md` (section 2.4)

**Independent of:** the install/rollout plan (`2026-04-09-nanite-install-and-rollout.md`). No shared code paths. Both plans can execute in parallel if in separate worktrees.

---

## File Map

### New Files

| File | Responsibility |
|---|---|
| `internal/store/migrations/005_a2a_session_scoping.sql` | Schema change: drop old A2A columns, add new ones, create `session_handoffs` table |
| `internal/service/a2a/service.go` | Public `Service` type + `SendMessage`, `Inbox`, `Thread`, `Ack`, `Resolve`, `RecentForSession` |
| `internal/service/a2a/service_test.go` | Unit tests for basic send/inbox/thread/ack/resolve with validation |
| `internal/service/a2a/handoff.go` | `RequestHandoff`, `ApproveHandoff`, `RejectHandoff`, single-transaction binding update |
| `internal/service/a2a/handoff_test.go` | Unit tests for handoff edge cases (double handoff, reject, idempotent approve) |
| `internal/service/a2a/subscribe.go` | `SubscribeSessionAgent` — in-process pubsub for bidirectional MCP streaming |
| `internal/service/a2a/subscribe_test.go` | Unit tests for subscribe (delivery, drop + resubscribe, no replay) |
| `internal/service/a2a/validate.go` | Agent ID validation (DB UUID, `file-<slug>`, `"user"` sentinel) |
| `internal/service/a2a/validate_test.go` | Unit tests for validation |
| `cmd/nanite/a2a_cmd.go` | `cmdA2A` — CLI entry point for `nanite a2a send/inbox/thread/ack/resolve/handoff/catch-up` |
| `assets/framework/docs/nanite-a2a.md` | User-facing docs: CLI + MCP reference, `"user"` sentinel, handoff protocol |

### Modified Files

| File | Changes |
|---|---|
| `internal/store/a2a.go` | Rewrite: new column names, new queries, `SendMessage` returns validated row, `Inbox`/`Thread` take session+agent pairs |
| `internal/store/a2a_test.go` | Rewrite to match new schema |
| `internal/store/agents.go` | Reject attempts to create an agent with `id = "user"` or `slug = "user"` |
| `internal/store/agents_test.go` | Test the sentinel rejection |
| `internal/api/a2a.go` | Rewrite HTTP endpoints to use new addressing |
| `internal/mcp/self_tools_transport.go` | Add 10 A2A MCP tools: send, inbox, thread, ack, resolve, catch_up, subscribe, handoff_request, handoff_approve, handoff_reject |
| `cmd/nanite/main.go` | Add `"a2a"` case to command switch |

---

## Task 1: Schema migration

**Files:**
- Create: `internal/store/migrations/005_a2a_session_scoping.sql`

**Context:** Clean break on the A2A schema. Drop the unconstrained TEXT columns, add session-scoped columns with `NOT NULL DEFAULT ''`, create `session_handoffs` table, add indexes. Per the spec, there are no current A2A users, so no parallel writes or data migration is needed.

- [ ] **Step 1: Write the migration file**

```sql
-- internal/store/migrations/005_a2a_session_scoping.sql

-- Drop unconstrained columns (clean break, no current users).
ALTER TABLE a2a_messages DROP COLUMN from_agent;
ALTER TABLE a2a_messages DROP COLUMN to_agent;

-- Add session-scoped addressing.
ALTER TABLE a2a_messages ADD COLUMN from_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN from_agent_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN to_session_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN to_agent_id     TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_a2a_to_session_agent   ON a2a_messages(to_session_id, to_agent_id, status);
CREATE INDEX idx_a2a_from_session_agent ON a2a_messages(from_session_id, from_agent_id);

-- Handoff audit trail. The session_agents junction table already handles
-- the binding itself (via is_primary); this table only tracks the request
-- and approval lifecycle.
CREATE TABLE IF NOT EXISTS session_handoffs (
    id                    TEXT PRIMARY KEY,
    session_id            TEXT NOT NULL REFERENCES sessions(id),
    from_agent_id         TEXT,
    to_agent_id           TEXT NOT NULL,
    requested_by          TEXT NOT NULL,    -- "departing" | "incoming" | "user"
    status                TEXT NOT NULL DEFAULT 'pending'
                              CHECK(status IN ('pending','approved','rejected','completed')),
    requested_at          TEXT NOT NULL,
    approved_at           TEXT,
    approved_by_user      INTEGER NOT NULL DEFAULT 0,
    context_message_count INTEGER,
    notes                 TEXT
);

CREATE INDEX idx_handoffs_session ON session_handoffs(session_id, status);
```

- [ ] **Step 2: Verify migration runs on a fresh store**

```bash
cd ~/Projects-apps/nanite
rm -f /tmp/a2a-migration-test.db
go run ./cmd/nanite serve --db /tmp/a2a-migration-test.db &
SERVER_PID=$!
sleep 2
kill $SERVER_PID

sqlite3 /tmp/a2a-migration-test.db ".schema a2a_messages"
sqlite3 /tmp/a2a-migration-test.db ".schema session_handoffs"
rm /tmp/a2a-migration-test.db
```

Expected: schema shows new columns and tables, no errors from migration run.

**Note on SQLite ALTER TABLE DROP COLUMN:** SQLite 3.35+ supports `ALTER TABLE ... DROP COLUMN`. Verify the Nanite binary's bundled SQLite is ≥ 3.35:

```bash
cd ~/Projects-apps/nanite
grep -r "mattn/go-sqlite3\|modernc.org/sqlite" go.mod | head -3
```

If it's `mattn/go-sqlite3` < v1.14.16 or an older `modernc.org/sqlite`, the DROP COLUMN will fail. Fallback strategy: rename `a2a_messages` to `a2a_messages_old`, create fresh `a2a_messages` with the new schema, don't copy data (clean break), drop the old table. Update the migration file to do this if needed.

- [ ] **Step 3: Commit**

```bash
git add internal/store/migrations/005_a2a_session_scoping.sql
git commit -m "feat(store): 005 A2A session scoping migration"
```

---

## Task 2: `"user"` sentinel validation in store/agents.go

**Files:**
- Modify: `internal/store/agents.go`
- Modify: `internal/store/agents_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/store/agents_test.go (append)

func TestCreateAgent_RejectsUserSlug(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{
		Slug: "user",
		Name: "sneaky",
	}
	_, err := s.CreateAgent(profile)
	if err == nil {
		t.Fatal("expected error for slug=user, got nil")
	}
}

func TestCreateAgent_RejectsUserID(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{
		ID:   "user",
		Slug: "not-user",
		Name: "also sneaky",
	}
	_, err := s.CreateAgent(profile)
	if err == nil {
		t.Fatal("expected error for id=user, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/Projects-apps/nanite
go test ./internal/store/... -run "TestCreateAgent_RejectsUser" -v
```

- [ ] **Step 3: Add the rejection to `CreateAgent`**

Open `internal/store/agents.go`, find `CreateAgent` (or equivalent creation function). At the top:

```go
// CreateAgent inserts a new agent profile. Rejects the reserved "user"
// slug/ID, which is used as the A2A addressing sentinel for human users.
func (s *Store) CreateAgent(profile *AgentProfile) (*AgentProfile, error) {
	if profile.Slug == "user" {
		return nil, fmt.Errorf("agent slug %q is reserved (A2A user sentinel)", profile.Slug)
	}
	if profile.ID == "user" {
		return nil, fmt.Errorf("agent id %q is reserved (A2A user sentinel)", profile.ID)
	}
	// ... existing implementation
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/store/... -run "TestCreateAgent_RejectsUser" -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/store/agents.go internal/store/agents_test.go
git commit -m "feat(store): reject reserved 'user' slug/id in agent creation"
```

---

## Task 3: Agent ID validation package

**Files:**
- Create: `internal/service/a2a/validate.go`
- Create: `internal/service/a2a/validate_test.go`

**Context:** Centralizes the "what counts as a valid agent_id for A2A" logic. Used by `SendMessage`, `Ack`, `Resolve`, and handoff validation.

- [ ] **Step 1: Write failing test**

```go
// internal/service/a2a/validate_test.go
package a2a

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestValidateAgentID_User(t *testing.T) {
	// The "user" sentinel is always valid.
	err := ValidateAgentID(nil, "user")
	if err != nil {
		t.Errorf("user sentinel rejected: %v", err)
	}
}

func TestValidateAgentID_FileSlug(t *testing.T) {
	// A "file-<slug>" ID is valid if the slug matches a discoverable file agent.
	s := newTestStoreWithFileAgent(t, "backend")
	if err := ValidateAgentID(s, "file-backend"); err != nil {
		t.Errorf("file-backend rejected: %v", err)
	}
	if err := ValidateAgentID(s, "file-does-not-exist"); err == nil {
		t.Error("expected rejection for unknown file agent")
	}
}

func TestValidateAgentID_DBUUID(t *testing.T) {
	s := newTestStore(t)
	profile, _ := s.CreateAgent(&store.AgentProfile{Slug: "test", Name: "T"})
	if err := ValidateAgentID(s, profile.ID); err != nil {
		t.Errorf("DB agent rejected: %v", err)
	}
	if err := ValidateAgentID(s, "ffffffff-ffff-ffff-ffff-ffffffffffff"); err == nil {
		t.Error("expected rejection for unknown UUID")
	}
}

func TestValidateAgentID_Empty(t *testing.T) {
	if err := ValidateAgentID(nil, ""); err == nil {
		t.Error("expected rejection for empty agent id")
	}
}
```

**Note:** The test helper `newTestStore` should exist in the store package's test utilities. `newTestStoreWithFileAgent` may need to be created — see existing patterns in `internal/store/*_test.go`.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/a2a/... -run TestValidateAgentID -v
```

- [ ] **Step 3: Implement `ValidateAgentID`**

```go
// internal/service/a2a/validate.go
package a2a

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// UserSentinel is the reserved agent_id used to address the human user in a session.
const UserSentinel = "user"

// ValidateAgentID checks that an agent_id is one of:
//   - the "user" sentinel
//   - a "file-<slug>" where <slug> references a known file agent
//   - a DB UUID for a known AgentProfile
// Returns nil if valid, an error otherwise.
func ValidateAgentID(s *store.Store, agentID string) error {
	if agentID == "" {
		return fmt.Errorf("empty agent id")
	}
	if agentID == UserSentinel {
		return nil
	}
	if strings.HasPrefix(agentID, "file-") {
		slug := strings.TrimPrefix(agentID, "file-")
		if s == nil {
			return fmt.Errorf("cannot validate file agent without store")
		}
		// Look up the file agent via the store's file-agent discovery.
		// The existing store may expose this via GetAgentBySlug or GetFileAgent;
		// use whichever is available. If neither exists, fall back to
		// iterating the file agents list.
		if _, err := s.GetAgentByID(agentID); err != nil {
			return fmt.Errorf("file agent not found: %s", slug)
		}
		return nil
	}
	// Assume UUID — look up by ID.
	if s == nil {
		return fmt.Errorf("cannot validate UUID agent without store")
	}
	if _, err := s.GetAgentByID(agentID); err != nil {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	return nil
}
```

**Note:** The exact function for looking up agents by ID may be called `GetAgentByID`, `GetAgent`, or similar. Confirm by reading the existing `internal/store/agents.go`. Use the correct name in the implementation.

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/a2a/... -run TestValidateAgentID -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/a2a/validate.go internal/service/a2a/validate_test.go
git commit -m "feat(a2a): agent ID validation with user sentinel"
```

---

## Task 4: Rewrite `internal/store/a2a.go` for new schema

**Files:**
- Modify: `internal/store/a2a.go`
- Modify: `internal/store/a2a_test.go`

**Context:** The existing store layer uses `from_agent`/`to_agent` as unconstrained TEXT. After Task 1's migration, these columns are gone. This task rewrites the store layer to match.

- [ ] **Step 1: Read the existing file**

```bash
cd ~/Projects-apps/nanite
cat internal/store/a2a.go
```

Note:
- Exported struct (`A2AMessage`) fields
- Function names (e.g., `SendA2AMessage`, `GetA2AInbox`, `GetA2AThread`)
- Any methods on `*Store`

- [ ] **Step 2: Update the `A2AMessage` struct**

Replace the current struct with:

```go
// internal/store/a2a.go (replace struct definition)

type A2AMessage struct {
	ID            string  `json:"id"`
	FromSessionID string  `json:"from_session_id"`
	FromAgentID   string  `json:"from_agent_id"`
	ToSessionID   string  `json:"to_session_id"`
	ToAgentID     string  `json:"to_agent_id"`
	ThreadID      string  `json:"thread_id"`
	ReplyTo       string  `json:"reply_to"`
	Type          string  `json:"type"`
	Subject       string  `json:"subject"`
	Body          string  `json:"body"`
	Metadata      string  `json:"metadata"`
	Priority      int     `json:"priority"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	ReadAt        *string `json:"read_at"`
	ResolvedAt    *string `json:"resolved_at"`
}
```

- [ ] **Step 3: Rewrite `SendA2AMessage`**

```go
// SendA2AMessage inserts a new message. Caller is responsible for validation
// of agent IDs; this function only enforces the schema constraints.
func (s *Store) SendA2AMessage(msg *A2AMessage) (*A2AMessage, error) {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Status == "" {
		msg.Status = "unread"
	}
	if msg.Type == "" {
		msg.Type = "message"
	}
	if msg.Priority == 0 {
		msg.Priority = 2
	}
	_, err := s.db.Exec(`
		INSERT INTO a2a_messages (
			id, from_session_id, from_agent_id, to_session_id, to_agent_id,
			thread_id, reply_to, type, subject, body, metadata, priority, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, msg.ID, msg.FromSessionID, msg.FromAgentID, msg.ToSessionID, msg.ToAgentID,
		nullString(msg.ThreadID), nullString(msg.ReplyTo), msg.Type,
		nullString(msg.Subject), msg.Body, msg.Metadata, msg.Priority, msg.Status)
	if err != nil {
		return nil, fmt.Errorf("insert a2a message: %w", err)
	}
	return s.GetA2AMessage(msg.ID)
}

// nullString converts empty strings to SQL NULL via sql.NullString.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 4: Rewrite `GetA2AInbox`**

```go
// GetA2AInbox returns messages where to_session_id AND to_agent_id match.
// If status != "" filters by status.
func (s *Store) GetA2AInbox(sessionID, agentID, status string) ([]A2AMessage, error) {
	query := `
		SELECT id, from_session_id, from_agent_id, to_session_id, to_agent_id,
		       COALESCE(thread_id, ''), COALESCE(reply_to, ''), type,
		       COALESCE(subject, ''), body, metadata, priority, status,
		       created_at, read_at, resolved_at
		  FROM a2a_messages
		 WHERE to_session_id = ? AND to_agent_id = ?
	`
	args := []any{sessionID, agentID}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY priority DESC, created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}
	defer rows.Close()

	var out []A2AMessage
	for rows.Next() {
		var m A2AMessage
		if err := rows.Scan(
			&m.ID, &m.FromSessionID, &m.FromAgentID, &m.ToSessionID, &m.ToAgentID,
			&m.ThreadID, &m.ReplyTo, &m.Type, &m.Subject, &m.Body, &m.Metadata,
			&m.Priority, &m.Status, &m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
```

- [ ] **Step 5: Add new methods `GetA2AMessage`, `GetA2ARecent`, `AckA2AMessage`, `ResolveA2AMessage`**

```go
// GetA2AMessage fetches a single message by ID.
func (s *Store) GetA2AMessage(id string) (*A2AMessage, error) {
	var m A2AMessage
	err := s.db.QueryRow(`
		SELECT id, from_session_id, from_agent_id, to_session_id, to_agent_id,
		       COALESCE(thread_id, ''), COALESCE(reply_to, ''), type,
		       COALESCE(subject, ''), body, metadata, priority, status,
		       created_at, read_at, resolved_at
		  FROM a2a_messages WHERE id = ?
	`, id).Scan(
		&m.ID, &m.FromSessionID, &m.FromAgentID, &m.ToSessionID, &m.ToAgentID,
		&m.ThreadID, &m.ReplyTo, &m.Type, &m.Subject, &m.Body, &m.Metadata,
		&m.Priority, &m.Status, &m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetA2ARecent returns the last N messages for a session, across both sides
// of the conversation (either from or to the session). Used for handoff catch-up.
func (s *Store) GetA2ARecent(sessionID string, limit int) ([]A2AMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, from_session_id, from_agent_id, to_session_id, to_agent_id,
		       COALESCE(thread_id, ''), COALESCE(reply_to, ''), type,
		       COALESCE(subject, ''), body, metadata, priority, status,
		       created_at, read_at, resolved_at
		  FROM a2a_messages
		 WHERE from_session_id = ? OR to_session_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?
	`, sessionID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []A2AMessage
	for rows.Next() {
		var m A2AMessage
		if err := rows.Scan(
			&m.ID, &m.FromSessionID, &m.FromAgentID, &m.ToSessionID, &m.ToAgentID,
			&m.ThreadID, &m.ReplyTo, &m.Type, &m.Subject, &m.Body, &m.Metadata,
			&m.Priority, &m.Status, &m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	// Reverse to chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// AckA2AMessage sets status to 'read' and populates read_at.
func (s *Store) AckA2AMessage(id string) error {
	_, err := s.db.Exec(`UPDATE a2a_messages SET status = 'read', read_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// ResolveA2AMessage sets status to 'resolved' and populates resolved_at.
func (s *Store) ResolveA2AMessage(id string) error {
	_, err := s.db.Exec(`UPDATE a2a_messages SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 6: Rewrite `GetA2AThread`**

```go
// GetA2AThread returns all messages in a thread (by thread_id) in
// chronological order.
func (s *Store) GetA2AThread(threadID string) ([]A2AMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, from_session_id, from_agent_id, to_session_id, to_agent_id,
		       COALESCE(thread_id, ''), COALESCE(reply_to, ''), type,
		       COALESCE(subject, ''), body, metadata, priority, status,
		       created_at, read_at, resolved_at
		  FROM a2a_messages
		 WHERE thread_id = ?
		 ORDER BY created_at ASC
	`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []A2AMessage
	for rows.Next() {
		var m A2AMessage
		if err := rows.Scan(
			&m.ID, &m.FromSessionID, &m.FromAgentID, &m.ToSessionID, &m.ToAgentID,
			&m.ThreadID, &m.ReplyTo, &m.Type, &m.Subject, &m.Body, &m.Metadata,
			&m.Priority, &m.Status, &m.CreatedAt, &m.ReadAt, &m.ResolvedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
```

- [ ] **Step 7: Rewrite store tests**

```go
// internal/store/a2a_test.go (replace)
package store

import "testing"

func TestSendA2AMessage_NewSchema(t *testing.T) {
	s := newTestStore(t)
	msg := &A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   "file-backend",
		ToSessionID:   "sess-1",
		ToAgentID:     "user",
		Subject:       "hi",
		Body:          "hello",
	}
	out, err := s.SendA2AMessage(msg)
	if err != nil {
		t.Fatalf("SendA2AMessage: %v", err)
	}
	if out.ID == "" {
		t.Error("ID not populated")
	}
	if out.Status != "unread" {
		t.Errorf("Status = %q, want unread", out.Status)
	}
}

func TestGetA2AInbox_FiltersBySessionAndAgent(t *testing.T) {
	s := newTestStore(t)
	// Three messages: two to (sess-1, file-a), one to (sess-1, file-b).
	s.SendA2AMessage(&A2AMessage{ToSessionID: "sess-1", ToAgentID: "file-a", Body: "1", FromSessionID: "sess-1", FromAgentID: "user"})
	s.SendA2AMessage(&A2AMessage{ToSessionID: "sess-1", ToAgentID: "file-a", Body: "2", FromSessionID: "sess-1", FromAgentID: "user"})
	s.SendA2AMessage(&A2AMessage{ToSessionID: "sess-1", ToAgentID: "file-b", Body: "3", FromSessionID: "sess-1", FromAgentID: "user"})

	inbox, err := s.GetA2AInbox("sess-1", "file-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 {
		t.Errorf("inbox len = %d, want 2", len(inbox))
	}
}

func TestGetA2ARecent(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		s.SendA2AMessage(&A2AMessage{
			FromSessionID: "sess-1",
			FromAgentID:   "file-a",
			ToSessionID:   "sess-1",
			ToAgentID:     "user",
			Body:          fmt.Sprintf("msg-%d", i),
		})
	}
	recent, err := s.GetA2ARecent("sess-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 3 {
		t.Errorf("recent len = %d, want 3", len(recent))
	}
}
```

- [ ] **Step 8: Run store tests**

```bash
go test ./internal/store/... -run TestA2A -v
```

- [ ] **Step 9: Build the full binary to catch downstream compile errors**

```bash
go build ./...
```

Expected: errors from `internal/api/a2a.go` referencing the old struct fields. These are fixed in Task 9. For now, mark them with a build tag or TODO comment, or leave them broken until Task 9.

**Workaround:** Temporarily stub out `internal/api/a2a.go` by commenting out the endpoint handlers. This keeps the build green during Tasks 5-8:

```go
// internal/api/a2a.go (top of file)
//go:build !skip_a2a_api
// +build !skip_a2a_api
```

And invoke the build with `-tags skip_a2a_api` during intermediate tasks. Restore in Task 9.

Better alternative: fix `internal/api/a2a.go` at the same time in this task — rewrite each handler to use the new struct fields. Adds ~30 minutes of work to this task but keeps main buildable throughout.

Pick the second (fix `api/a2a.go` in this task) unless the handlers are extensive.

- [ ] **Step 10: Commit**

```bash
git add internal/store/a2a.go internal/store/a2a_test.go internal/api/a2a.go
git commit -m "refactor(store,api): A2A session-scoped addressing"
```

---

## Task 5: Service layer — basic send/inbox/thread/ack/resolve

**Files:**
- Create: `internal/service/a2a/service.go`
- Create: `internal/service/a2a/service_test.go`

**Context:** The service layer wraps the store layer with validation. All CLI and MCP surfaces call into this layer, never directly into the store.

- [ ] **Step 1: Write failing test**

```go
// internal/service/a2a/service_test.go
package a2a

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestService_SendMessage_RejectsUnknownTo(t *testing.T) {
	s := newTestStore(t)
	svc := NewService(s)

	msg := &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   "user",
		ToSessionID:   "sess-1",
		ToAgentID:     "file-nonexistent",
		Body:          "hi",
	}
	_, err := svc.SendMessage(context.Background(), msg)
	if err == nil {
		t.Error("expected rejection for unknown to_agent_id")
	}
}

func TestService_SendMessage_AcceptsUserSentinel(t *testing.T) {
	s := newTestStore(t)
	svc := NewService(s)

	msg := &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   "user",
		ToSessionID:   "sess-1",
		ToAgentID:     UserSentinel,
		Body:          "notification",
	}
	out, err := svc.SendMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if out.ToAgentID != UserSentinel {
		t.Errorf("ToAgentID = %q, want %q", out.ToAgentID, UserSentinel)
	}
}

func TestService_Inbox(t *testing.T) {
	s := newTestStore(t)
	svc := NewService(s)
	// Seed via store directly (skip validation for test setup).
	s.SendA2AMessage(&store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-backend",
		Body:          "question 1",
	})
	inbox, err := svc.Inbox(context.Background(), "sess-1", "file-backend", "")
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Errorf("inbox len = %d, want 1", len(inbox))
	}
}
```

**Note:** `newTestStore` must set up a backend file agent so `file-backend` validates as a known agent. Either add a helper that seeds a file agent, or accept the unknown-agent case in `ValidateAgentID` when running against a test store (not recommended — cleaner to seed).

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/a2a/... -run TestService_ -v
```

- [ ] **Step 3: Implement the Service**

```go
// internal/service/a2a/service.go
package a2a

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// Service is the A2A service layer — CLI and MCP surfaces call into this.
type Service struct {
	store *store.Store
	pub   *pubsub // from subscribe.go (Task 7)
}

// NewService constructs a Service backed by the given store.
func NewService(s *store.Store) *Service {
	return &Service{
		store: s,
		pub:   newPubsub(),
	}
}

// SendMessage validates the addresses and inserts the message. Emits to
// subscribers via the in-process pubsub after a successful insert.
func (svc *Service) SendMessage(ctx context.Context, msg *store.A2AMessage) (*store.A2AMessage, error) {
	if err := ValidateAgentID(svc.store, msg.FromAgentID); err != nil {
		return nil, fmt.Errorf("from_agent_id: %w", err)
	}
	if err := ValidateAgentID(svc.store, msg.ToAgentID); err != nil {
		return nil, fmt.Errorf("to_agent_id: %w", err)
	}
	if msg.FromSessionID == "" {
		return nil, fmt.Errorf("from_session_id required")
	}
	if msg.ToSessionID == "" {
		return nil, fmt.Errorf("to_session_id required")
	}
	if msg.Body == "" {
		return nil, fmt.Errorf("body required")
	}

	out, err := svc.store.SendA2AMessage(msg)
	if err != nil {
		return nil, err
	}

	// Publish to subscribers (best effort, non-blocking).
	if svc.pub != nil {
		svc.pub.publish(out)
	}
	return out, nil
}

// Inbox returns messages addressed to (sessionID, agentID). Optional status filter.
func (svc *Service) Inbox(ctx context.Context, sessionID, agentID, status string) ([]store.A2AMessage, error) {
	return svc.store.GetA2AInbox(sessionID, agentID, status)
}

// Thread returns all messages in a thread.
func (svc *Service) Thread(ctx context.Context, threadID string) ([]store.A2AMessage, error) {
	return svc.store.GetA2AThread(threadID)
}

// Ack marks a message as read.
func (svc *Service) Ack(ctx context.Context, sessionID, agentID, msgID string) error {
	// Validate the caller's binding exists; MVP is permissive (no impersonation check).
	if err := ValidateAgentID(svc.store, agentID); err != nil {
		return err
	}
	return svc.store.AckA2AMessage(msgID)
}

// Resolve marks a message as resolved.
func (svc *Service) Resolve(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(svc.store, agentID); err != nil {
		return err
	}
	return svc.store.ResolveA2AMessage(msgID)
}

// RecentForSession returns the last N messages for a session regardless of
// which agents participated — used for handoff catch-up.
func (svc *Service) RecentForSession(ctx context.Context, sessionID string, limit int) ([]store.A2AMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	return svc.store.GetA2ARecent(sessionID, limit)
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/a2a/... -run TestService_ -v
```

Note: the pubsub import is anticipated here; it'll be implemented in Task 7. Use a stub for now:

```go
// internal/service/a2a/subscribe.go (stub)
package a2a

import "github.com/hollis-labs/nanite/internal/store"

type pubsub struct{}

func newPubsub() *pubsub            { return &pubsub{} }
func (p *pubsub) publish(msg *store.A2AMessage) {}
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/a2a/service.go internal/service/a2a/service_test.go internal/service/a2a/subscribe.go
git commit -m "feat(a2a): service layer for send/inbox/thread/ack/resolve"
```

---

## Task 6: Handoff transaction

**Files:**
- Create: `internal/service/a2a/handoff.go`
- Create: `internal/service/a2a/handoff_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/service/a2a/handoff_test.go
package a2a

import (
	"context"
	"testing"
)

func TestHandoff_FullFlow(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "backend", "frontend")
	svc := NewService(s)

	// Create a session and bind backend as primary.
	sess := createTestSession(t, s)
	s.EnsureSessionAgent(sess.ID, "file-backend", "default", true)

	// Request handoff from backend to frontend.
	handoffID, err := svc.RequestHandoff(context.Background(), sess.ID, "file-backend", "file-frontend", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}
	if handoffID == "" {
		t.Error("empty handoff id")
	}

	// Approve.
	if err := svc.ApproveHandoff(context.Background(), handoffID); err != nil {
		t.Fatalf("ApproveHandoff: %v", err)
	}

	// Verify primary is now frontend.
	primary, err := s.GetSessionPrimaryAgent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionPrimaryAgent: %v", err)
	}
	if primary.AgentID != "file-frontend" {
		t.Errorf("primary = %q, want file-frontend", primary.AgentID)
	}
}

func TestHandoff_DoubleRequest(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "a", "b", "c")
	svc := NewService(s)
	sess := createTestSession(t, s)
	s.EnsureSessionAgent(sess.ID, "file-a", "default", true)

	h1, _ := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-b", "departing")
	h2, _ := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-c", "departing")

	// Approve h1 — h2 should become rejected automatically.
	if err := svc.ApproveHandoff(context.Background(), h1); err != nil {
		t.Fatal(err)
	}

	// Query h2 status.
	status, err := svc.getHandoffStatus(h2)
	if err != nil {
		t.Fatal(err)
	}
	if status != "rejected" {
		t.Errorf("h2 status = %q, want rejected", status)
	}
}

func TestHandoff_ApproveCompleted_Idempotent(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "a", "b")
	svc := NewService(s)
	sess := createTestSession(t, s)
	s.EnsureSessionAgent(sess.ID, "file-a", "default", true)

	h, _ := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-b", "departing")
	if err := svc.ApproveHandoff(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	// Second call should be no-op, not error.
	if err := svc.ApproveHandoff(context.Background(), h); err != nil {
		t.Errorf("second approve errored: %v", err)
	}
}

func TestHandoff_ApproveRejected_Errors(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "a", "b")
	svc := NewService(s)
	sess := createTestSession(t, s)
	s.EnsureSessionAgent(sess.ID, "file-a", "default", true)

	h, _ := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-b", "departing")
	if err := svc.RejectHandoff(context.Background(), h, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApproveHandoff(context.Background(), h); err == nil {
		t.Error("expected error approving rejected handoff")
	}
}
```

**Note:** The test helpers `newTestStoreWithFileAgents` and `createTestSession` need to exist or be added. They set up in-memory stores with file agents seeded and a test session row.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/a2a/... -run TestHandoff -v
```

- [ ] **Step 3: Implement handoff**

```go
// internal/service/a2a/handoff.go
package a2a

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RequestHandoff creates a pending handoff row. Returns the handoff ID.
// fromAgentID may be empty (e.g., orphaned session claiming a new primary).
func (svc *Service) RequestHandoff(ctx context.Context, sessionID, fromAgentID, toAgentID, requestedBy string) (string, error) {
	if toAgentID == "" {
		return "", fmt.Errorf("to_agent_id required")
	}
	if err := ValidateAgentID(svc.store, toAgentID); err != nil {
		return "", fmt.Errorf("to_agent_id: %w", err)
	}
	if requestedBy == "" {
		return "", fmt.Errorf("requested_by required")
	}

	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := svc.store.DB().Exec(`
		INSERT INTO session_handoffs (id, session_id, from_agent_id, to_agent_id, requested_by, status, requested_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)
	`, id, sessionID, nullableString(fromAgentID), toAgentID, requestedBy, now)
	if err != nil {
		return "", fmt.Errorf("insert handoff: %w", err)
	}
	return id, nil
}

// ApproveHandoff atomically:
//  1. flips the session's primary agent to the handoff target
//  2. marks the handoff as completed
//  3. auto-rejects any other pending handoffs for the same session
func (svc *Service) ApproveHandoff(ctx context.Context, handoffID string) error {
	tx, err := svc.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Read handoff.
	var sessionID, toAgentID, status string
	err = tx.QueryRowContext(ctx, `
		SELECT session_id, to_agent_id, status FROM session_handoffs WHERE id = ?
	`, handoffID).Scan(&sessionID, &toAgentID, &status)
	if err != nil {
		return fmt.Errorf("read handoff: %w", err)
	}
	if status == "completed" {
		return nil // idempotent
	}
	if status == "rejected" {
		return fmt.Errorf("handoff %s is rejected", handoffID)
	}

	// Flip primary.
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_agents SET is_primary = 0 WHERE session_id = ? AND is_primary = 1
	`, sessionID); err != nil {
		return fmt.Errorf("clear primary: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_agents (session_id, agent_id, mode, is_primary, joined_at)
		VALUES (?, ?, 'default', 1, CURRENT_TIMESTAMP)
		ON CONFLICT(session_id, agent_id) DO UPDATE SET is_primary = 1
	`, sessionID, toAgentID); err != nil {
		return fmt.Errorf("set new primary: %w", err)
	}

	// Mark handoff completed.
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_handoffs
		   SET status = 'completed', approved_at = CURRENT_TIMESTAMP, approved_by_user = 1
		 WHERE id = ?
	`, handoffID); err != nil {
		return fmt.Errorf("update handoff: %w", err)
	}

	// Auto-reject other pending handoffs for the same session.
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_handoffs
		   SET status = 'rejected',
		       notes = 'superseded by handoff ' || ?
		 WHERE session_id = ? AND status = 'pending' AND id != ?
	`, handoffID, sessionID, handoffID); err != nil {
		return fmt.Errorf("reject superseded handoffs: %w", err)
	}

	return tx.Commit()
}

// RejectHandoff marks a pending handoff as rejected with a reason.
func (svc *Service) RejectHandoff(ctx context.Context, handoffID, reason string) error {
	_, err := svc.store.DB().Exec(`
		UPDATE session_handoffs
		   SET status = 'rejected', notes = ?
		 WHERE id = ? AND status = 'pending'
	`, reason, handoffID)
	return err
}

// getHandoffStatus is a test helper that reads the current status of a handoff.
func (svc *Service) getHandoffStatus(handoffID string) (string, error) {
	var status string
	err := svc.store.DB().QueryRow(`SELECT status FROM session_handoffs WHERE id = ?`, handoffID).Scan(&status)
	return status, err
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

**Note:** `svc.store.DB()` assumes the store exposes its `*sql.DB`. If it doesn't, add a `DB() *sql.DB` method on `*store.Store` (or adjust to use the store's existing transaction helpers).

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/a2a/... -run TestHandoff -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/a2a/handoff.go internal/service/a2a/handoff_test.go
git commit -m "feat(a2a): handoff flow with single-transaction binding update"
```

---

## Task 7: Subscribe / pubsub for bidirectional MCP

**Files:**
- Replace: `internal/service/a2a/subscribe.go` (stub from Task 5)
- Create: `internal/service/a2a/subscribe_test.go`

**Context:** MCP subscribe is a streaming RPC: the client subscribes once, the server pushes new matching messages as they arrive. Implementation is an in-process pubsub indexed by `(session_id, agent_id)`.

- [ ] **Step 1: Write failing test**

```go
// internal/service/a2a/subscribe_test.go
package a2a

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestSubscribe_ReceivesNewMessage(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "a")
	svc := NewService(s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
	if err != nil {
		t.Fatal(err)
	}

	// Send a message matching the subscription.
	msg := &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   "user",
		ToSessionID:   "sess-1",
		ToAgentID:     "file-a",
		Body:          "hello",
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		svc.SendMessage(context.Background(), msg)
	}()

	select {
	case received := <-ch:
		if received.Body != "hello" {
			t.Errorf("Body = %q, want hello", received.Body)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for pushed message")
	}
}

func TestSubscribe_IgnoresNonMatching(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "a", "b")
	svc := NewService(s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
	if err != nil {
		t.Fatal(err)
	}

	// Send to a different agent.
	svc.SendMessage(context.Background(), &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   "user",
		ToSessionID:   "sess-1",
		ToAgentID:     "file-b",
		Body:          "not for us",
	})

	select {
	case received := <-ch:
		t.Errorf("unexpected push: %v", received)
	case <-time.After(100 * time.Millisecond):
		// Good — no delivery.
	}
}

func TestSubscribe_NoReplayOnResubscribe(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "a")
	svc := NewService(s)

	// Send before subscribing.
	svc.SendMessage(context.Background(), &store.A2AMessage{
		FromSessionID: "sess-1", FromAgentID: "user",
		ToSessionID: "sess-1", ToAgentID: "file-a",
		Body: "old",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
		t.Error("expected no replay of pre-subscription messages")
	case <-time.After(100 * time.Millisecond):
		// Good.
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/service/a2a/... -run TestSubscribe -v
```

- [ ] **Step 3: Implement pubsub**

```go
// internal/service/a2a/subscribe.go (replace the stub)
package a2a

import (
	"context"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// pubsub is an in-process broadcast system for A2A messages, indexed by
// (session_id, agent_id). Subscribers receive messages addressed to their
// exact key. Messages sent before a subscription are not replayed.
type pubsub struct {
	mu   sync.RWMutex
	subs map[string][]chan *store.A2AMessage // key: sessionID + ":" + agentID
}

func newPubsub() *pubsub {
	return &pubsub{subs: make(map[string][]chan *store.A2AMessage)}
}

func (p *pubsub) subscribe(ctx context.Context, sessionID, agentID string) <-chan *store.A2AMessage {
	key := sessionID + ":" + agentID
	ch := make(chan *store.A2AMessage, 16)

	p.mu.Lock()
	p.subs[key] = append(p.subs[key], ch)
	p.mu.Unlock()

	// Unsubscribe when context is cancelled.
	go func() {
		<-ctx.Done()
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, c := range p.subs[key] {
			if c == ch {
				p.subs[key] = append(p.subs[key][:i], p.subs[key][i+1:]...)
				break
			}
		}
		close(ch)
	}()

	return ch
}

func (p *pubsub) publish(msg *store.A2AMessage) {
	if msg == nil {
		return
	}
	key := msg.ToSessionID + ":" + msg.ToAgentID
	p.mu.RLock()
	subs := p.subs[key]
	p.mu.RUnlock()

	for _, ch := range subs {
		// Non-blocking send — if a subscriber can't keep up, drop the message.
		select {
		case ch <- msg:
		default:
		}
	}
}

// SubscribeSessionAgent returns a channel that receives new messages matching
// (sessionID, agentID). The channel is closed when ctx is cancelled.
func (svc *Service) SubscribeSessionAgent(ctx context.Context, sessionID, agentID string) (<-chan *store.A2AMessage, error) {
	if err := ValidateAgentID(svc.store, agentID); err != nil {
		return nil, err
	}
	return svc.pub.subscribe(ctx, sessionID, agentID), nil
}
```

- [ ] **Step 4: Run tests, verify pass**

```bash
go test ./internal/service/a2a/... -run TestSubscribe -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/a2a/subscribe.go internal/service/a2a/subscribe_test.go
git commit -m "feat(a2a): in-process pubsub for bidirectional MCP subscriptions"
```

---

## Task 8: Rewrite `internal/api/a2a.go`

**Files:**
- Modify: `internal/api/a2a.go`

**Context:** Already partially touched in Task 4's compile-fix workaround. This task ensures the HTTP endpoints match the new service layer and new addressing.

- [ ] **Step 1: Read existing HTTP routes**

```bash
grep -n "POST\|GET\|mux\.Handle\|r\.Method" ~/Projects-apps/nanite/internal/api/a2a.go
```

Understand the current route layout.

- [ ] **Step 2: Rewrite the handlers to use new schema**

Change request/response payloads to include `from_session_id`, `from_agent_id`, `to_session_id`, `to_agent_id` instead of `from_agent` / `to_agent`.

Add new endpoints:
- `POST /api/a2a/handoff/request` → `svc.RequestHandoff`
- `POST /api/a2a/handoff/approve/:id` → `svc.ApproveHandoff`
- `POST /api/a2a/handoff/reject/:id` → `svc.RejectHandoff`
- `GET /api/a2a/recent?session_id=X&limit=N` → `svc.RecentForSession`

Existing endpoints (updated to use new fields):
- `POST /api/a2a/send` → `svc.SendMessage`
- `GET /api/a2a/inbox?session_id=X&agent_id=Y[&status=Z]` → `svc.Inbox`
- `GET /api/a2a/thread/:id` → `svc.Thread`
- `POST /api/a2a/ack/:id` → `svc.Ack`
- `POST /api/a2a/resolve/:id` → `svc.Resolve`

Use the existing JSON marshaling / error handling patterns in other API files (e.g., `internal/api/agents.go` or similar).

- [ ] **Step 3: Smoke-test one endpoint**

```bash
cd ~/Projects-apps/nanite
go build ./...
# start server in background
./nanite serve --db /tmp/a2a-api-smoke.db &
SERVER_PID=$!
sleep 2
curl -X POST http://localhost:8090/api/a2a/send \
  -H 'Content-Type: application/json' \
  -d '{"from_session_id":"s1","from_agent_id":"user","to_session_id":"s1","to_agent_id":"user","body":"hi"}'
kill $SERVER_PID
rm /tmp/a2a-api-smoke.db
```

Expected: JSON response with a created message object.

- [ ] **Step 4: Commit**

```bash
git add internal/api/a2a.go
git commit -m "refactor(api): A2A HTTP endpoints use session-scoped addressing + handoff routes"
```

---

## Task 9: CLI command `nanite a2a ...`

**Files:**
- Create: `cmd/nanite/a2a_cmd.go`
- Modify: `cmd/nanite/main.go`

- [ ] **Step 1: Implement `cmdA2A`**

```go
// cmd/nanite/a2a_cmd.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/hollis-labs/nanite/internal/brand"
	a2asvc "github.com/hollis-labs/nanite/internal/service/a2a"
	"github.com/hollis-labs/nanite/internal/store"
)

func cmdA2A(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s a2a <send|inbox|thread|ack|resolve|catch-up|handoff>\n", brand.BinaryName)
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]

	dbPath := resolveDBPath()
	s, err := store.New(dbPath)
	if err != nil {
		dieErr("open db", err)
	}
	defer s.Close()
	svc := a2asvc.NewService(s)

	switch sub {
	case "send":
		a2aSend(svc, rest)
	case "inbox":
		a2aInbox(svc, rest)
	case "thread":
		a2aThread(svc, rest)
	case "ack":
		a2aAck(svc, rest)
	case "resolve":
		a2aResolve(svc, rest)
	case "catch-up":
		a2aCatchUp(svc, rest)
	case "handoff":
		a2aHandoff(svc, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown a2a subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func a2aSend(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a send", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	to := fs.String("to", "", "to agent id (or 'user')")
	from := fs.String("from-agent", "user", "from agent id")
	subject := fs.String("subject", "", "subject line")
	body := fs.String("body", "", "message body")
	msgType := fs.String("type", "message", "message type")
	fs.Parse(args)

	if *session == "" || *to == "" || *body == "" {
		fmt.Fprintln(os.Stderr, "required: --session, --to, --body")
		os.Exit(1)
	}
	msg := &store.A2AMessage{
		FromSessionID: *session,
		FromAgentID:   *from,
		ToSessionID:   *session,
		ToAgentID:     *to,
		Subject:       *subject,
		Body:          *body,
		Type:          *msgType,
	}
	out, err := svc.SendMessage(context.Background(), msg)
	if err != nil {
		dieErr("send", err)
	}
	fmt.Printf("sent: %s\n", out.ID)
}

func a2aInbox(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a inbox", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agent := fs.String("agent", "", "agent id")
	status := fs.String("status", "", "filter: unread|read|acknowledged|resolved")
	fs.Parse(args)

	inbox, err := svc.Inbox(context.Background(), *session, *agent, *status)
	if err != nil {
		dieErr("inbox", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(inbox)
}

func a2aThread(svc *a2asvc.Service, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: a2a thread <threadID>")
		os.Exit(1)
	}
	messages, err := svc.Thread(context.Background(), args[0])
	if err != nil {
		dieErr("thread", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(messages)
}

func a2aAck(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a ack", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agent := fs.String("agent", "", "agent id")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: a2a ack --session X --agent Y <msgID>")
		os.Exit(1)
	}
	if err := svc.Ack(context.Background(), *session, *agent, fs.Arg(0)); err != nil {
		dieErr("ack", err)
	}
	fmt.Println("acked")
}

func a2aResolve(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a resolve", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agent := fs.String("agent", "", "agent id")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: a2a resolve --session X --agent Y <msgID>")
		os.Exit(1)
	}
	if err := svc.Resolve(context.Background(), *session, *agent, fs.Arg(0)); err != nil {
		dieErr("resolve", err)
	}
	fmt.Println("resolved")
}

func a2aCatchUp(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a catch-up", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	last := fs.Int("last", 20, "number of recent messages")
	fs.Parse(args)

	messages, err := svc.RecentForSession(context.Background(), *session, *last)
	if err != nil {
		dieErr("catch-up", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(messages)
}

func a2aHandoff(svc *a2asvc.Service, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: a2a handoff <request|approve|reject>")
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "request":
		fs := flag.NewFlagSet("handoff request", flag.ExitOnError)
		session := fs.String("session", "", "session id")
		to := fs.String("to", "", "to agent id")
		from := fs.String("from", "", "from agent id (optional)")
		reqBy := fs.String("requested-by", "user", "departing|incoming|user")
		fs.Parse(rest)

		id, err := svc.RequestHandoff(context.Background(), *session, *from, *to, *reqBy)
		if err != nil {
			dieErr("handoff request", err)
		}
		fmt.Printf("handoff requested: %s\n", id)
	case "approve":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "usage: a2a handoff approve <handoffID>")
			os.Exit(1)
		}
		if err := svc.ApproveHandoff(context.Background(), rest[0]); err != nil {
			dieErr("handoff approve", err)
		}
		fmt.Println("approved")
	case "reject":
		fs := flag.NewFlagSet("handoff reject", flag.ExitOnError)
		reason := fs.String("reason", "", "rejection reason")
		fs.Parse(rest)
		if fs.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: a2a handoff reject --reason X <handoffID>")
			os.Exit(1)
		}
		if err := svc.RejectHandoff(context.Background(), fs.Arg(0), *reason); err != nil {
			dieErr("handoff reject", err)
		}
		fmt.Println("rejected")
	default:
		fmt.Fprintf(os.Stderr, "unknown handoff subcommand: %s\n", sub)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Wire into `main.go`**

```go
// cmd/nanite/main.go (inside switch in main())

	case "a2a":
		cmdA2A(os.Args[2:])
```

Update the usage line:
```go
	fmt.Fprintln(os.Stderr, "commands: serve, install, a2a, plugin, mcp, version")
```

- [ ] **Step 3: Build and smoke test**

```bash
cd ~/Projects-apps/nanite
go build ./cmd/nanite
./nanite a2a 2>&1
./nanite a2a send --help 2>&1 | head -20
```

- [ ] **Step 4: Commit**

```bash
git add cmd/nanite/a2a_cmd.go cmd/nanite/main.go
git commit -m "feat(cli): nanite a2a send/inbox/thread/ack/resolve/catch-up/handoff"
```

---

## Task 10: Register A2A MCP tools

**Files:**
- Modify: `internal/mcp/self_tools_transport.go`

**Context:** Add 10 A2A tools to `SelfToolsTransport`. The subscribe tool is the only streaming one; it needs special handling because `SelfToolsTransport`'s default shape is request/response. For MVP, implement subscribe as a "snapshot" tool that returns the current inbox + a subscription handle, and document that the full streaming subscribe is a follow-up.

Alternative: wire subscribe as a native mcp-go streaming handler in `internal/mcpserver/server.go` (bypassing the `SelfToolsTransport` abstraction for this one tool). Pick this if streaming support is essential for MVP.

- [ ] **Step 1: Choose a streaming approach**

Read `~/Projects-apps/nanite/internal/mcpserver/server.go` and check whether `mcp-go`'s server supports streaming tool results (look for `StreamableToolResult` or similar). If yes, use streaming for `nanite_a2a_subscribe`. If no, implement as a polling-friendly snapshot call and capture streaming as a follow-up task.

```bash
grep -n "Stream\|Subscribe\|streaming" ~/Projects-apps/nanite/internal/mcp/*.go 2>&1 | head -10
grep -n "Stream\|Subscribe" ~/Projects-apps/nanite/internal/mcpserver/server.go
```

- [ ] **Step 2: Add tool definitions to `ListTools()`**

Append to `SelfToolsTransport.ListTools()`:

```go
{
    Name:        "nanite_a2a_send",
    Description: "Send an A2A message addressed to (to_session_id, to_agent_id). Use 'user' for to_agent_id to reach the human in a session.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "from_session_id": map[string]any{"type": "string"},
            "from_agent_id":   map[string]any{"type": "string"},
            "to_session_id":   map[string]any{"type": "string"},
            "to_agent_id":     map[string]any{"type": "string"},
            "subject":         map[string]any{"type": "string"},
            "body":            map[string]any{"type": "string"},
            "type":            map[string]any{"type": "string", "enum": []string{"message", "help_request", "directive", "status_update", "handoff"}},
        },
        "required": []string{"from_session_id", "from_agent_id", "to_session_id", "to_agent_id", "body"},
    },
},
{
    Name:        "nanite_a2a_inbox",
    Description: "Read the A2A inbox for (session_id, agent_id). Optional status filter.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "session_id": map[string]any{"type": "string"},
            "agent_id":   map[string]any{"type": "string"},
            "status":     map[string]any{"type": "string", "enum": []string{"", "unread", "read", "acknowledged", "resolved"}},
        },
        "required": []string{"session_id", "agent_id"},
    },
},
{
    Name:        "nanite_a2a_thread",
    Description: "Get all messages in a thread by thread_id.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{"thread_id": map[string]any{"type": "string"}},
        "required": []string{"thread_id"},
    },
},
{
    Name:        "nanite_a2a_ack",
    Description: "Mark an A2A message as read.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "session_id": map[string]any{"type": "string"},
            "agent_id":   map[string]any{"type": "string"},
            "message_id": map[string]any{"type": "string"},
        },
        "required": []string{"session_id", "agent_id", "message_id"},
    },
},
{
    Name:        "nanite_a2a_resolve",
    Description: "Mark an A2A message as resolved.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "session_id": map[string]any{"type": "string"},
            "agent_id":   map[string]any{"type": "string"},
            "message_id": map[string]any{"type": "string"},
        },
        "required": []string{"session_id", "agent_id", "message_id"},
    },
},
{
    Name:        "nanite_a2a_catch_up",
    Description: "Get the last N messages for a session across both sides of the conversation. Used for handoff catch-up.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "session_id": map[string]any{"type": "string"},
            "limit":      map[string]any{"type": "integer", "default": 20},
        },
        "required": []string{"session_id"},
    },
},
{
    Name:        "nanite_a2a_handoff_request",
    Description: "Request a session handoff from one agent to another. Creates a pending row; user must approve.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "session_id":   map[string]any{"type": "string"},
            "from_agent_id": map[string]any{"type": "string"},
            "to_agent_id":   map[string]any{"type": "string"},
            "requested_by":  map[string]any{"type": "string", "enum": []string{"departing", "incoming", "user"}},
        },
        "required": []string{"session_id", "to_agent_id", "requested_by"},
    },
},
{
    Name:        "nanite_a2a_handoff_approve",
    Description: "Approve a pending handoff. Atomically rebinds the session's primary agent and marks the handoff complete.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{"handoff_id": map[string]any{"type": "string"}},
        "required": []string{"handoff_id"},
    },
},
{
    Name:        "nanite_a2a_handoff_reject",
    Description: "Reject a pending handoff with a reason.",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "handoff_id": map[string]any{"type": "string"},
            "reason":     map[string]any{"type": "string"},
        },
        "required": []string{"handoff_id"},
    },
},
```

- [ ] **Step 3: Add dispatch cases to `CallTool()`**

```go
case "nanite_a2a_send":
    return st.callA2ASend(args)
case "nanite_a2a_inbox":
    return st.callA2AInbox(args)
case "nanite_a2a_thread":
    return st.callA2AThread(args)
case "nanite_a2a_ack":
    return st.callA2AAck(args)
case "nanite_a2a_resolve":
    return st.callA2AResolve(args)
case "nanite_a2a_catch_up":
    return st.callA2ACatchUp(args)
case "nanite_a2a_handoff_request":
    return st.callA2AHandoffRequest(args)
case "nanite_a2a_handoff_approve":
    return st.callA2AHandoffApprove(args)
case "nanite_a2a_handoff_reject":
    return st.callA2AHandoffReject(args)
```

- [ ] **Step 4: Implement handler methods**

Append to `self_tools_transport.go`:

```go
import (
	// ...
	a2asvc "github.com/hollis-labs/nanite/internal/service/a2a"
)

func (st *SelfToolsTransport) a2aService() *a2asvc.Service {
	return a2asvc.NewService(st.store)
}

func (st *SelfToolsTransport) callA2ASend(args map[string]any) (*ToolResult, error) {
	msg := &store.A2AMessage{
		FromSessionID: stringArg(args, "from_session_id"),
		FromAgentID:   stringArg(args, "from_agent_id"),
		ToSessionID:   stringArg(args, "to_session_id"),
		ToAgentID:     stringArg(args, "to_agent_id"),
		Subject:       stringArg(args, "subject"),
		Body:          stringArg(args, "body"),
		Type:          stringArg(args, "type"),
	}
	out, err := st.a2aService().SendMessage(context.Background(), msg)
	if err != nil {
		return nil, err
	}
	return textResult(fmt.Sprintf("sent: %s", out.ID)), nil
}

func (st *SelfToolsTransport) callA2AInbox(args map[string]any) (*ToolResult, error) {
	inbox, err := st.a2aService().Inbox(
		context.Background(),
		stringArg(args, "session_id"),
		stringArg(args, "agent_id"),
		stringArg(args, "status"),
	)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(inbox)
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callA2AThread(args map[string]any) (*ToolResult, error) {
	messages, err := st.a2aService().Thread(context.Background(), stringArg(args, "thread_id"))
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(messages)
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callA2AAck(args map[string]any) (*ToolResult, error) {
	if err := st.a2aService().Ack(context.Background(), stringArg(args, "session_id"), stringArg(args, "agent_id"), stringArg(args, "message_id")); err != nil {
		return nil, err
	}
	return textResult("acked"), nil
}

func (st *SelfToolsTransport) callA2AResolve(args map[string]any) (*ToolResult, error) {
	if err := st.a2aService().Resolve(context.Background(), stringArg(args, "session_id"), stringArg(args, "agent_id"), stringArg(args, "message_id")); err != nil {
		return nil, err
	}
	return textResult("resolved"), nil
}

func (st *SelfToolsTransport) callA2ACatchUp(args map[string]any) (*ToolResult, error) {
	limit := 20
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	messages, err := st.a2aService().RecentForSession(context.Background(), stringArg(args, "session_id"), limit)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(messages)
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callA2AHandoffRequest(args map[string]any) (*ToolResult, error) {
	id, err := st.a2aService().RequestHandoff(
		context.Background(),
		stringArg(args, "session_id"),
		stringArg(args, "from_agent_id"),
		stringArg(args, "to_agent_id"),
		stringArg(args, "requested_by"),
	)
	if err != nil {
		return nil, err
	}
	return textResult("handoff requested: " + id), nil
}

func (st *SelfToolsTransport) callA2AHandoffApprove(args map[string]any) (*ToolResult, error) {
	if err := st.a2aService().ApproveHandoff(context.Background(), stringArg(args, "handoff_id")); err != nil {
		return nil, err
	}
	return textResult("approved"), nil
}

func (st *SelfToolsTransport) callA2AHandoffReject(args map[string]any) (*ToolResult, error) {
	if err := st.a2aService().RejectHandoff(context.Background(), stringArg(args, "handoff_id"), stringArg(args, "reason")); err != nil {
		return nil, err
	}
	return textResult("rejected"), nil
}

// stringArg extracts a string argument from a tool call's args map.
// Returns "" if missing or wrong type.
func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// textResult wraps a text response in the ToolResult shape.
func textResult(text string) *ToolResult {
	return &ToolResult{Content: []ToolContent{{Type: "text", Text: text}}}
}
```

**Note on `st.store`:** `SelfToolsTransport` already holds a `*store.Store` (see the field from Task 14 in Plan A or the existing struct definition). Use it directly.

**Subscribe MCP tool deferred:** Register `nanite_a2a_subscribe` as a follow-up task in the Future Work section of the spec — requires streaming support in `mcp-go` or a custom server-side handler. The MVP ships with 9 request/response tools above.

- [ ] **Step 5: Build and run tests**

```bash
cd ~/Projects-apps/nanite
go build ./...
go test ./internal/mcp/... -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/self_tools_transport.go
git commit -m "$(cat <<'EOF'
feat(mcp): A2A MCP tools (send, inbox, thread, ack, resolve, catch_up, handoff)

Exposes 9 request/response tools over SelfToolsTransport. Streaming
subscribe tool deferred to a follow-up task pending mcp-go streaming
support (or a custom server-side handler).
EOF
)"
```

---

## Task 11: Integration test — handoff round-trip

**Files:**
- Create: `internal/service/a2a/integration_test.go`

- [ ] **Step 1: Write the integration test**

```go
// internal/service/a2a/integration_test.go
package a2a

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestIntegration_HandoffFullFlow exercises:
// 1. two agents bound to a session (backend primary, frontend inactive)
// 2. user sends messages to backend
// 3. backend sends messages to user
// 4. handoff requested backend → frontend
// 5. approved
// 6. frontend is now primary
// 7. messages sent to (session, frontend) post-handoff arrive correctly
// 8. catch-up returns the full conversation including backend-era messages
func TestIntegration_HandoffFullFlow(t *testing.T) {
	s := newTestStoreWithFileAgents(t, "backend", "frontend")
	svc := NewService(s)
	ctx := context.Background()

	sess := createTestSession(t, s)
	s.EnsureSessionAgent(sess.ID, "file-backend", "default", true)
	s.EnsureSessionAgent(sess.ID, "file-frontend", "default", false)

	// User → backend.
	_, err := svc.SendMessage(ctx, &store.A2AMessage{
		FromSessionID: sess.ID, FromAgentID: "user",
		ToSessionID: sess.ID, ToAgentID: "file-backend",
		Body: "implement the thing",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Backend → user.
	_, err = svc.SendMessage(ctx, &store.A2AMessage{
		FromSessionID: sess.ID, FromAgentID: "file-backend",
		ToSessionID: sess.ID, ToAgentID: "user",
		Body: "done",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Backend requests handoff to frontend.
	handoffID, err := svc.RequestHandoff(ctx, sess.ID, "file-backend", "file-frontend", "departing")
	if err != nil {
		t.Fatal(err)
	}

	// Approve.
	if err := svc.ApproveHandoff(ctx, handoffID); err != nil {
		t.Fatal(err)
	}

	// Verify frontend is now primary.
	primary, _ := s.GetSessionPrimaryAgent(sess.ID)
	if primary.AgentID != "file-frontend" {
		t.Errorf("primary = %q, want file-frontend", primary.AgentID)
	}

	// User → frontend (post-handoff).
	_, err = svc.SendMessage(ctx, &store.A2AMessage{
		FromSessionID: sess.ID, FromAgentID: "user",
		ToSessionID: sess.ID, ToAgentID: "file-frontend",
		Body: "now do the UI",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Frontend inbox should have exactly one message.
	inbox, _ := svc.Inbox(ctx, sess.ID, "file-frontend", "")
	if len(inbox) != 1 {
		t.Errorf("frontend inbox len = %d, want 1", len(inbox))
	}

	// Catch-up returns all 3 messages in chronological order.
	recent, _ := svc.RecentForSession(ctx, sess.ID, 10)
	if len(recent) != 3 {
		t.Errorf("recent len = %d, want 3", len(recent))
	}
	if recent[0].Body != "implement the thing" {
		t.Errorf("recent[0].Body = %q", recent[0].Body)
	}
	if recent[2].Body != "now do the UI" {
		t.Errorf("recent[2].Body = %q", recent[2].Body)
	}
}
```

- [ ] **Step 2: Run**

```bash
cd ~/Projects-apps/nanite
go test ./internal/service/a2a/... -run TestIntegration -v
```

- [ ] **Step 3: Run the full a2a test suite**

```bash
go test ./internal/service/a2a/... -v
go test ./internal/store/... -run A2A -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/service/a2a/integration_test.go
git commit -m "test(a2a): integration test for full handoff round-trip"
```

---

## Task 12: User-facing documentation

**Files:**
- Create: `assets/framework/docs/nanite-a2a.md`

- [ ] **Step 1: Write the reference doc**

```markdown
# Nanite A2A (Agent-to-Agent Messaging)

Nanite provides a session-scoped messaging system that lets agents communicate with each other and with the human user during a chat session.

## Addressing

Messages are addressed to a `(session_id, agent_id)` pair. Agent IDs come in three forms:

- **DB UUID** — A UUID string assigned when an agent is created via the API.
- **`file-<slug>`** — A deterministic ID for file-based agents discovered from `.nanite/config.yaml`.
- **`"user"`** — The reserved sentinel for addressing the human user in a session.

The `"user"` slug is reserved: you cannot create an agent with slug `"user"`.

## CLI

```bash
# Send a message to an agent.
nanite a2a send --session <sid> --to file-backend --subject "question" --body "what file handles auth?"

# Send a message to the user.
nanite a2a send --session <sid> --to user --body "I finished the refactor."

# Read an agent's inbox.
nanite a2a inbox --session <sid> --agent file-backend

# Read the user's inbox (notifications to the human).
nanite a2a inbox --session <sid> --agent user

# Get a full thread.
nanite a2a thread <thread-id>

# Mark a message read.
nanite a2a ack --session <sid> --agent file-backend <message-id>

# Mark a message resolved.
nanite a2a resolve --session <sid> --agent file-backend <message-id>

# Get the last N messages in a session (both sides).
nanite a2a catch-up --session <sid> --last 20
```

## Handoff

Handoff transfers a session's primary agent from one to another. Useful for session resumption, role switching, or context-full bail-outs.

```bash
# Agent requests handoff.
nanite a2a handoff request --session <sid> --from file-backend --to file-frontend --requested-by departing

# User approves (this counts as user approval in CLI contexts).
nanite a2a handoff approve <handoff-id>

# Reject.
nanite a2a handoff reject --reason "not now" <handoff-id>
```

Approval is a single atomic transaction: the session's primary agent flips, the handoff row is marked complete, and any other pending handoffs for the same session are auto-rejected as superseded.

## MCP tools

All of the above are also exposed as MCP tools for agents running inside Nanite or external CLI agents (Claude Code, Gemini CLI, etc.) connected via MCP:

- `nanite_a2a_send`
- `nanite_a2a_inbox`
- `nanite_a2a_thread`
- `nanite_a2a_ack`
- `nanite_a2a_resolve`
- `nanite_a2a_catch_up`
- `nanite_a2a_handoff_request`
- `nanite_a2a_handoff_approve`
- `nanite_a2a_handoff_reject`

Streaming subscription (`nanite_a2a_subscribe`) is a follow-up — see the spec for details.

## Validation

`SendMessage` rejects:
- Empty addresses (from_session_id, from_agent_id, to_session_id, to_agent_id, body)
- Unknown agent IDs (checked against the store)
- Attempts to use `"user"` as a real agent slug or ID

The caller's `from_agent_id` is **trusted** in the current single-user desktop MVP. Multi-tenant impersonation checks are a documented follow-up.

## Storage

Messages live in the `a2a_messages` table. Handoff audit trail in `session_handoffs`. Session-to-agent primary binding is tracked in the existing `session_agents` junction table (no new binding table introduced for handoff).
```

- [ ] **Step 2: Commit**

```bash
git add assets/framework/docs/nanite-a2a.md
git commit -m "docs: nanite a2a CLI + MCP reference"
```

---

## Task 13: Phase 4b — A2A smoke test on Nanite itself

**Not a code task — an operational verification runbook.**

**Prerequisite:** Plan A Task 16 (dogfood install on Nanite) must be complete. Plan B (this plan) Tasks 1-12 complete.

- [ ] **Step 1: Build latest Nanite**

```bash
cd ~/Projects-apps/nanite
go build ./cmd/nanite
./nanite version
```

- [ ] **Step 2: Start a live chat session against a real DB**

```bash
# Use your default or a test db. The point is a real session.
./nanite serve --db ~/.nanite/nanite.db &
SERVER_PID=$!
sleep 2
```

Open Nanite's UI (if available) or use the API to create a session and bind an agent. Note the `session_id`.

- [ ] **Step 3: Send a user → agent message**

```bash
./nanite a2a send --session <session-id> --to file-backend --body "A2A smoke test 1"
```

- [ ] **Step 4: Read the agent's inbox**

```bash
./nanite a2a inbox --session <session-id> --agent file-backend
```

Expected: JSON array with one message.

- [ ] **Step 5: Send an agent → user message**

```bash
./nanite a2a send --session <session-id> --from-agent file-backend --to user --body "response from agent"
```

- [ ] **Step 6: Read the user's inbox**

```bash
./nanite a2a inbox --session <session-id> --agent user
```

Expected: JSON array with the agent's response.

- [ ] **Step 7: Request and approve a handoff**

```bash
HANDOFF_ID=$(./nanite a2a handoff request --session <session-id> --from file-backend --to file-frontend --requested-by departing | awk '{print $3}')
echo "handoff: $HANDOFF_ID"
./nanite a2a handoff approve $HANDOFF_ID
```

- [ ] **Step 8: Verify the binding flipped**

Query the database directly or hit an API:

```bash
sqlite3 ~/.nanite/nanite.db "SELECT agent_id, is_primary FROM session_agents WHERE session_id = '<session-id>';"
```

Expected: file-frontend has `is_primary=1`, file-backend has `is_primary=0`.

- [ ] **Step 9: Catch-up returns the full conversation**

```bash
./nanite a2a catch-up --session <session-id> --last 10
```

Expected: JSON array with both earlier messages (user → backend, backend → user) plus any post-handoff traffic, in chronological order.

- [ ] **Step 10: Kill the test server**

```bash
kill $SERVER_PID
```

- [ ] **Step 11: Document the smoke test result**

If everything worked: done. The MCP side is also verified by the integration tests in Task 11. If the CLI works and the integration tests pass, the MCP surface is correct by construction (same service layer).

If anything failed: capture the error, identify whether it's in the service layer (unit test gap), the store layer (SQL issue), or the CLI wrapper. Fix and re-run from Task 3 onward as needed.

---

## Done

After Task 13:
- Mark this plan's spec section (2.4) as Implemented in the spec document.
- Verify the nine deferred items in the spec's §6 Future Work — which (if any) are unblocked by A2A being live now?
- Specifically: streaming `nanite_a2a_subscribe` is the obvious next thing if agent responsiveness becomes a UX gap.
