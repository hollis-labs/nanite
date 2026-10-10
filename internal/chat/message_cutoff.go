package chat

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/store"
)

// ErrWorkingHistoryBoundary refuses an accepted turn whose exact user message
// is absent from working history (cleared, compacted or outside its window).
var ErrWorkingHistoryBoundary = errors.New("accepted turn working-history boundary unavailable")

type workingHistoryThroughKey struct{}

type WorkingHistoryTurn struct{ UserMessageID, OutputMessageID string }
type workingHistoryBoundary struct {
	messageID    string
	predecessors []WorkingHistoryTurn
}

// WithWorkingHistoryThrough carries an exact admitted message ID, never an
// actor/permission or persisted session setting. Empty explicitly clears it.
func WithWorkingHistoryThrough(ctx context.Context, messageID string) context.Context {
	return context.WithValue(ctx, workingHistoryThroughKey{}, workingHistoryBoundary{messageID: messageID})
}

// CopyWorkingHistoryThrough preserves only the accepted message marker across
// HTTP detachment, not arbitrary caller identity or credentials.
func CopyWorkingHistoryThrough(from, into context.Context) context.Context {
	boundary, _ := from.Value(workingHistoryThroughKey{}).(workingHistoryBoundary)
	return context.WithValue(into, workingHistoryThroughKey{}, boundary)
}

func WorkingHistoryMessageID(ctx context.Context) string {
	boundary, _ := ctx.Value(workingHistoryThroughKey{}).(workingHistoryBoundary)
	return boundary.messageID
}

// WithWorkingHistoryPredecessors retains exact host-tracked predecessor
// outputs even when they commit after this queued question was admitted.
func WithWorkingHistoryPredecessors(ctx context.Context, predecessors []WorkingHistoryTurn) context.Context {
	boundary, _ := ctx.Value(workingHistoryThroughKey{}).(workingHistoryBoundary)
	boundary.predecessors = append([]WorkingHistoryTurn(nil), predecessors...)
	return context.WithValue(ctx, workingHistoryThroughKey{}, boundary)
}

func HasWorkingHistoryThrough(ctx context.Context) bool {
	return WorkingHistoryMessageID(ctx) != ""
}

func workingHistoryThrough(ctx context.Context, messages []store.Message) ([]store.Message, error) {
	boundary, _ := ctx.Value(workingHistoryThroughKey{}).(workingHistoryBoundary)
	id := boundary.messageID
	if id == "" {
		return messages, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, message := range messages {
		if message.ID == id {
			if message.Role != "user" || message.IsCompacted {
				return nil, ErrWorkingHistoryBoundary
			}
			rows := make(map[string]store.Message, len(messages))
			for _, row := range messages {
				rows[row.ID] = row
			}
			outputs := map[string]store.Message{}
			outputIDs := map[string]bool{}
			for _, prior := range boundary.predecessors {
				row, found := rows[prior.OutputMessageID]
				user, userFound := rows[prior.UserMessageID]
				if !found || !userFound || row.Role != "assistant" || row.IsCompacted || user.Role != "user" || user.IsCompacted || row.SessionID != message.SessionID || user.SessionID != message.SessionID || outputs[user.ID].ID != "" || outputIDs[row.ID] {
					return nil, ErrWorkingHistoryBoundary
				}
				outputs[prior.UserMessageID] = row
				outputIDs[row.ID] = true
			}
			out := make([]store.Message, 0, i+1+len(outputs))
			for _, row := range messages[:i] {
				if row.SessionID != message.SessionID {
					return nil, ErrWorkingHistoryBoundary
				}
				if !outputIDs[row.ID] {
					out = append(out, row)
				}
				if output, found := outputs[row.ID]; found {
					out = append(out, output)
					delete(outputs, row.ID)
				}
			}
			if len(outputs) != 0 {
				return nil, ErrWorkingHistoryBoundary
			}
			return append(out, message), nil
		}
	}
	return nil, ErrWorkingHistoryBoundary
}
