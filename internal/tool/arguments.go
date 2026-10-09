package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	toolresult "github.com/hollis-labs/substrate/agent/toolresult"
)

// DefaultArgumentBudgetBytes bounds the body persisted per tool call. It is
// the same preview budget idiom results use (toolresult.Preview), sized for audit:
// enough to see which file or query a call named, not to archive file contents.
const DefaultArgumentBudgetBytes = 4 * 1024

// ArgumentRecord is one persisted tool-call argument document.
type ArgumentRecord struct {
	ID            string
	SessionID     string
	ToolCallID    string
	ToolName      string
	CreatedAt     time.Time
	ByteSize      int // redacted document size before the budget
	SHA256        string
	WasTruncated  bool
	RedactedCount int
	Body          string
}

// PersistArguments redacts a tool call's arguments, bounds them with the result
// preview pipeline, and stores them for audit. Redaction happens before
// anything is hashed, previewed or written, so the raw values never reach the
// database. Failure to persist must not fail the tool call; callers log it.
func (c *ResultCache) PersistArguments(sessionID, toolCallID, toolName string, args map[string]any) (ArgumentRecord, error) {
	rec := ArgumentRecord{SessionID: sessionID, ToolCallID: toolCallID, ToolName: toolName}
	if c == nil || c.db == nil {
		return rec, fmt.Errorf("argument persist: no database")
	}
	normalized, err := normalizeArgs(args)
	if err != nil {
		return rec, fmt.Errorf("argument persist: normalize: %w", err)
	}
	if normalized == nil {
		normalized = map[string]any{}
	}
	redactor := c.argRedactor
	if redactor == nil {
		redactor = NamePatternRedactor{}
	}
	redacted, count := redactor.Redact(toolName, normalized)
	doc, err := json.Marshal(redacted)
	if err != nil {
		return rec, fmt.Errorf("argument persist: encode: %w", err)
	}
	sum := sha256.Sum256(doc)
	body := string(doc)
	budget := c.argBudgetBytes
	rec.ByteSize, rec.SHA256, rec.RedactedCount = len(doc), hex.EncodeToString(sum[:]), count
	if len(doc) > budget {
		body, _ = toolresult.Preview(body, budget)
		rec.WasTruncated = true
	}
	rec.Body = body

	rec.ID = newULID()
	rec.CreatedAt = time.Now().UTC()
	expiresAt := rec.CreatedAt.Add(time.Duration(c.cacheTTLSeconds) * time.Second)
	trunc := 0
	if rec.WasTruncated {
		trunc = 1
	}
	if _, err := c.db.Exec(
		`INSERT INTO tool_call_arguments (id, session_id, tool_call_id, tool_name, created_at, expires_at, byte_size, sha256, was_truncated, redacted_count, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, sessionID, toolCallID, toolName, rec.CreatedAt.Format(time.RFC3339), expiresAt.Format(time.RFC3339),
		rec.ByteSize, rec.SHA256, trunc, count, rec.Body); err != nil {
		return rec, fmt.Errorf("argument persist: %w", err)
	}
	return rec, nil
}

// ListArguments returns a session's persisted arguments oldest first.
func (c *ResultCache) ListArguments(sessionID string) ([]ArgumentRecord, error) {
	rows, err := c.db.Query(
		`SELECT id, tool_call_id, tool_name, created_at, byte_size, sha256, was_truncated, redacted_count, body
		 FROM tool_call_arguments WHERE session_id = ? ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("argument list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ArgumentRecord
	for rows.Next() {
		r := ArgumentRecord{SessionID: sessionID}
		var created string
		var trunc int
		if err := rows.Scan(&r.ID, &r.ToolCallID, &r.ToolName, &created, &r.ByteSize, &r.SHA256, &trunc, &r.RedactedCount, &r.Body); err != nil {
			return nil, fmt.Errorf("argument list: %w", err)
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, created)
		r.WasTruncated = trunc != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
