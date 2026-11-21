package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// QueueNotice durably queues a notice once for key, scoped to this conversation.
// It returns true only after a turn carrying the notice completed successfully.
// Reusing a key preserves the original body. Unlike PostNotice, this does not
// announce or wake a turn; a durable bridge owns retries and wake scheduling.
func (c *Conversation) QueueNotice(ctx context.Context, key, kind, body string) (bool, error) {
	if key == "" {
		return false, errors.New("nanite: notice key is required")
	}
	sum := sha256.Sum256([]byte(c.id + "\x00" + key))
	return c.agent.engine.QueueNotice(ctx, c.id, "keyed-"+hex.EncodeToString(sum[:]), kind, body)
}

// NoticeHandled reports whether a turn carrying the notice queued for key
// completed successfully. Unlike QueueNotice it never queues or requeues the
// notice, so a bridge can check before deciding whether to deliver.
func (c *Conversation) NoticeHandled(ctx context.Context, key string) (bool, error) {
	sum := sha256.Sum256([]byte(c.id + "\x00" + key))
	return c.agent.engine.NoticeHandled(ctx, c.id, "keyed-"+hex.EncodeToString(sum[:]))
}

// WithdrawQueuedNotice removes a queued notice if no turn is active. Completed
// receipts are retained. A bridge must retain its own tombstone to avoid requeueing.
func (c *Conversation) WithdrawQueuedNotice(ctx context.Context, key string) error {
	sum := sha256.Sum256([]byte(c.id + "\x00" + key))
	return c.agent.engine.WithdrawQueuedNotice(ctx, c.id, "keyed-"+hex.EncodeToString(sum[:]))
}
