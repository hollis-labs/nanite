package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// MessageHit is one message found by SearchMessages.
type MessageHit struct {
	Seq       int
	Role      string
	CreatedAt time.Time
	// Snippet is the matching part of the message, with matches in [ and ].
	Snippet string
}

// SearchMessages finds up to k of a conversation's messages matching query,
// best match first. The query is plain words; any message containing any of
// them matches, and messages with more and rarer matches rank higher.
func (s *Store) SearchMessages(ctx context.Context, conversation, query string, k int) ([]MessageHit, error) {
	match := matchExpression(query)
	if match == "" {
		return nil, fmt.Errorf("search query has no words")
	}
	if k <= 0 {
		return nil, nil
	}
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT m.seq, m.role, m.created_at, snippet(messages_fts, 0, '[', ']', '…', 24)
 FROM messages_fts JOIN messages m ON m.rowid = messages_fts.rowid
 WHERE messages_fts MATCH ? AND messages_fts.conversation_id = ?
 ORDER BY bm25(messages_fts), m.seq DESC LIMIT ?`, match, conversation, k)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
	var out []MessageHit
	for rows.Next() {
		var hit MessageHit
		var created string
		if err := rows.Scan(&hit.Seq, &hit.Role, &created, &hit.Snippet); err != nil {
			return nil, err
		}
		if hit.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// MessagesBetween returns a conversation's messages with from <= seq <= to,
// oldest first.
func (s *Store) MessagesBetween(ctx context.Context, conversation string, from, to int) ([]Message, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,turn_id,role,blocks_json,created_at,seq FROM messages
 WHERE conversation_id=? AND seq BETWEEN ? AND ? ORDER BY seq`, conversation, from, to)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// matchExpression turns plain words into an FTS5 query: each word quoted, so
// no input is read as FTS syntax, and joined with OR.
func matchExpression(query string) string {
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) > 32 {
		words = words[:32]
	}
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " OR ")
}
