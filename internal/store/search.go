package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// SearchMessages finds up to k of a conversation's messages matching query,
// best match first. The query is plain words; any message containing any of
// them matches, and messages with more and rarer matches rank higher. It
// returns whole messages: a caller must redact before showing any of them,
// so the index's own text is never returned.
func (s *Store) SearchMessages(ctx context.Context, conversation, query string, k int) ([]Message, error) {
	match := matchExpression(query)
	if match == "" {
		return nil, fmt.Errorf("search query has no words")
	}
	if k <= 0 {
		return nil, nil
	}
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT m.id,m.turn_id,m.role,m.blocks_json,m.created_at,m.seq
 FROM messages_fts JOIN messages m ON m.rowid = messages_fts.rowid AND m.conversation_id = messages_fts.conversation_id
 WHERE messages_fts MATCH ? AND m.conversation_id = ?
 ORDER BY bm25(messages_fts), m.seq DESC LIMIT ?`, match, conversation, k)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MessageSeqs returns the numbers of a conversation's messages with the given
// IDs, by ID; an unknown ID is left out.
func (s *Store) MessageSeqs(ctx context.Context, conversation string, ids ...string) (map[string]int, error) {
	out := make(map[string]int, len(ids))
	for _, id := range ids {
		var seq int
		err := s.db.Reader().QueryRowContext(ctx, `SELECT seq FROM messages WHERE conversation_id=? AND id=?`, conversation, id).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = seq
	}
	return out, nil
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

// LastMessages returns a conversation's last n messages, oldest first.
func (s *Store) LastMessages(ctx context.Context, conversation string, n int) ([]Message, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,turn_id,role,blocks_json,created_at,seq FROM
 (SELECT * FROM messages WHERE conversation_id=? ORDER BY seq DESC LIMIT ?) ORDER BY seq`, conversation, n)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// QueryWords splits a search query into the words SearchMessages looks for.
func QueryWords(query string) []string {
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) > 32 {
		words = words[:32]
	}
	return words
}

// matchExpression turns plain words into an FTS5 query: each word quoted, so
// no input is read as FTS syntax, and joined with OR.
func matchExpression(query string) string {
	words := QueryWords(query)
	for i, w := range words {
		words[i] = `"` + w + `"`
	}
	return strings.Join(words, " OR ")
}
