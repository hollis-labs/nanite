package chatrunner

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// noticePublishWait bounds how long one notice waits for the chat
// conversation's running turn to end before it is published.
const noticePublishWait = 2 * time.Minute

// listen announces notices on the wire. A notice a running turn carried is
// already on the wire with that turn; one posted while no turn ran is
// published as a run of its own, or starts a notice turn (NoticeTurns).
func (r *Runner) listen(ctx context.Context, notices <-chan agent.NoticePosted) {
	defer close(r.noticesDone)
	for n := range notices {
		if n.RunID != "" || r.opts.Observe == nil {
			continue
		}
		chatID := r.chatID(n.ConversationID)
		if chatID == "" {
			continue // not a conversation this runner serves
		}
		publishCtx, cancel := context.WithTimeout(ctx, noticePublishWait)
		err := r.publishNotice(publishCtx, chatID, n)
		cancel()
		if err != nil && ctx.Err() == nil {
			// The notice still waits in the store for the next turn.
			logger := r.opts.Agent.Logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Warn("chatrunner: publish notice", "conversation", chatID, "kind", n.Kind, "error", err.Error())
		}
	}
}

func (r *Runner) publishNotice(ctx context.Context, chatID string, n agent.NoticePosted) error {
	if r.opts.NoticeTurns {
		turn := chat.Turn{ConversationID: chatID}
		x, err := r.begin(ctx, turn, func(c *agent.Conversation, runCtx context.Context) (*agent.Run, error) {
			return c.Send(runCtx, agent.Input{Text: noticeText(n), Source: agent.SourceNotice})
		})
		if err == nil {
			x.notice = &n
			if err := r.opts.Observe(ctx, chatID, turn, x); err != nil {
				x.Cancel()
				_, _ = x.inner.Wait()
				r.release(x)
				return err
			}
			return nil
		}
		if !errors.Is(err, agent.ErrBusy) {
			return err
		}
		// A turn began meanwhile; announce the notice on its own instead.
	}
	return r.opts.Observe(ctx, chatID, chat.Turn{ConversationID: chatID}, &noticeRun{notice: n})
}

// noticeText is the user message of a notice turn.
func noticeText(n agent.NoticePosted) string {
	return fmt.Sprintf("[notice %s] %s\n\nTell me about this notice.", n.Kind, n.Body)
}

// noticeRun is a run that only announces a notice.
type noticeRun struct{ notice agent.NoticePosted }

func (n *noticeRun) Events() iter.Seq2[chatstream.Event, error] {
	return func(yield func(chatstream.Event, error) bool) {
		if !yield(noticeActivity(n.notice), nil) {
			return
		}
		yield(chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: string(chatstream.FinishStop)}, nil)
	}
}

func (n *noticeRun) Cancel() {}

func (n *noticeRun) Answer(context.Context, string, chat.Decision) error {
	return errors.New("chatrunner: a notice asks for no approval")
}
