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
