package agent

import (
	"context"

	"github.com/hollis-labs/nanite/internal/engine"
)

// HistoryHit is one message a history search found: its conversation, its
// number there (from 1), role, time, and a redacted snippet around the
// first match.
type HistoryHit = engine.HistoryHit

// SearchHistory finds up to k messages (default 5, at most 20) matching
// query in this conversation's full transcript, including messages
// compaction has summarized, best match first. The query is plain words; a
// message matches any of them. Snippets are redacted. Only a root
// conversation can be searched, and only itself: searching across
// conversations belongs to the shared transcript service.
func (c *Conversation) SearchHistory(ctx context.Context, query string, k int) ([]HistoryHit, error) {
	return c.agent.engine.SearchHistory(ctx, c.id, query, k)
}

// HistoryMessages renders this root conversation's messages numbered from
// through to (at most 20 at a time, and 32,000 characters), redacted and
// with long blocks shortened, as the history_get tool returns them.
func (c *Conversation) HistoryMessages(ctx context.Context, from, to int) (string, error) {
	return c.agent.engine.HistoryMessages(ctx, c.id, from, to)
}
