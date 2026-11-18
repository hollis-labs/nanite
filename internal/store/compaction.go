package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	core "github.com/hollis-labs/libs/core/store"
)

type Compaction struct {
	TurnID           string
	ThroughMessageID string
	Summary          string
	Before, After    int
}

func (j *Journal) RecordCompaction(id string, at time.Time, details Compaction) error {
	return j.sideband("nanite.compaction", id, at, details)
}

func projectCompaction(ctx context.Context, tx core.Tx, id, conversation string, details Compaction) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO compactions(id,conversation_id,turn_id,through_message_id,summary)
 VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, id, conversation, details.TurnID, details.ThroughMessageID, details.Summary)
	return err
}

// ModelHistory returns the latest summary and the messages beyond its coverage.
// parent preserves the raw transcript chain even when every message is covered.
func (s *Store) ModelHistory(ctx context.Context, conversation string) (summary string, messages []Message, parent string, err error) {
	var through string
	err = s.db.Reader().QueryRowContext(ctx, `SELECT summary,through_message_id FROM compactions
 WHERE conversation_id=? ORDER BY seq DESC LIMIT 1`, conversation).Scan(&summary, &through)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, "", err
	}
	messages, err = s.historyAfter(ctx, conversation, through)
	if err != nil {
		return "", nil, "", err
	}
	parent = through
	if len(messages) > 0 {
		parent = messages[len(messages)-1].ID
	}
	return summary, messages, parent, nil
}
