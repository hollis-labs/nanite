package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/agent"
)

// ActivityChatReset tells the owner that a chat now continues in a new
// Nanite conversation. Its value names the previous and the new
// conversation and the reason: `integrity` when the previous one failed
// integrity checks, otherwise `requested`.
const ActivityChatReset = "nanite.chat_reset"

// generationsHostState is the agent-wide host state recording how many times
// each chat was reset. A chat at generation 0 is served by
// ConversationID(chatID); generation n by ConversationID(chatID)+"-g<n>".
const generationsHostState = "chat_generations"

// ErrResetInterrupted refuses ResetChat while the chat's conversation has an
// interrupted turn: discard it (or resume it) first.
var ErrResetInterrupted = errors.New("chatrunner: the chat has an interrupted turn; discard it before resetting the chat")

// loadGenerations reads which conversation serves each reset chat.
func (r *Runner) loadGenerations(ctx context.Context) error {
	raw, err := r.agent.HostState(ctx, generationsHostState)
	if err != nil {
		return fmt.Errorf("chatrunner: load chat generations: %w", err)
	}
	generations := map[string]int{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &generations); err != nil {
			return fmt.Errorf("chatrunner: load chat generations: %w", err)
		}
	}
	r.mu.Lock()
	r.generations = generations
	r.mu.Unlock()
	return nil
}

// naniteID is the Nanite conversation that serves a chat now.
func (r *Runner) naniteID(chatID string) string {
	r.mu.Lock()
	n := r.generations[chatID]
	r.mu.Unlock()
	return generationID(chatID, n)
}

func generationID(chatID string, n int) string {
	if n == 0 {
		return ConversationID(chatID)
	}
	return fmt.Sprintf("%s-g%d", ConversationID(chatID), n)
}

// ConversationOf is the Nanite conversation that serves a chat now:
// ConversationID(chatID) until ResetChat moves the chat to a new one.
func (r *Runner) ConversationOf(chatID string) string { return r.naniteID(chatID) }

// ResetChat moves a chat to a new, empty Nanite conversation, for a chat
// whose conversation failed integrity checks and takes no new turns. The
// previous conversation is kept as it is on disk, for inspection and
// `nanite rebuild-index`; it no longer serves the chat. The move is saved,
// so a restart keeps it, and it is announced on the chat as a
// nanite.chat_reset activity. It refuses while a run, a Resume or another
// reset holds the chat (agent.ErrBusy), and while the chat has an
// interrupted turn Discard can still close (ErrResetInterrupted); a turn
// whose transcript refuses the discard (a torn tail) stays with the old
// conversation. It returns the new conversation's ID.
func (r *Runner) ResetChat(ctx context.Context, chatID string) (string, error) {
	// Claim the chat in the same step that finds it idle: a message arriving
	// meanwhile is refused (beginRun checks the claim and that its
	// conversation is still the chat's), so no run is left on the old one.
	r.mu.Lock()
	switch previous := generationID(chatID, r.generations[chatID]); {
	case r.closed:
		r.mu.Unlock()
		return "", errors.New("chatrunner: closed")
	case r.resuming[chatID] || r.resetting[chatID] || r.active[previous] != nil:
		r.mu.Unlock()
		return "", agent.ErrBusy
	}
	r.resetting[chatID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.resetting, chatID)
		r.mu.Unlock()
	}()
	previous := r.naniteID(chatID)
	reason := "requested"
	if c, err := r.agent.Conversation(ctx, previous); err == nil {
		err := c.Verify(ctx)
		var refused *agent.IntegrityError
		switch {
		case errors.As(err, &refused):
			reason = "integrity"
			// An interrupted turn the transcript will not let Discard close
			// (a torn tail) is left behind with the old conversation.
			if refused.Interrupted {
				return "", ErrResetInterrupted
			}
		case err != nil:
			return "", err
		case c.Interrupted() != nil:
			return "", ErrResetInterrupted
		}
	} else if !errors.Is(err, agent.ErrNoConversation) {
		return "", err
	}

	r.mu.Lock()
	generations := make(map[string]int, len(r.generations)+1)
	for k, v := range r.generations {
		generations[k] = v
	}
	generations[chatID]++
	r.mu.Unlock()
	raw, err := json.Marshal(generations)
	if err != nil {
		return "", err
	}
	if err := r.agent.SetHostState(ctx, generationsHostState, string(raw)); err != nil {
		return "", fmt.Errorf("chatrunner: save chat generation: %w", err)
	}
	r.mu.Lock()
	r.generations = generations
	delete(r.chats, previous)
	r.mu.Unlock()
	r.endHold(chatID)
	c, err := r.conversation(ctx, chatID)
	if err != nil {
		return "", err
	}
	r.announce(chatID, activity(ActivityChatReset, map[string]any{"previous_conversation": previous, "conversation": c.ID(), "reason": reason}))
	return c.ID(), nil
}

// olderGeneration reports whether naniteID served chatID before a reset.
func (r *Runner) olderGeneration(chatID, naniteID string) bool {
	r.mu.Lock()
	n := r.generations[chatID]
	r.mu.Unlock()
	for g := 0; g < n; g++ {
		if generationID(chatID, g) == naniteID {
			return true
		}
	}
	return false
}
