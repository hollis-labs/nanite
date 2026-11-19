package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

func (e *Engine) Notices() <-chan store.Notice { return e.notices }

func (e *Engine) PostNotice(ctx context.Context, conversation, kind, body string) error {
	notice := store.Notice{ID: store.NewID(time.Now()), ConversationID: conversation, Kind: kind, Body: e.scrub.Text(body)}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("nanite: agent is closed")
	}
	if err := e.cfg.Store.AddNotice(ctx, notice); err != nil {
		return err
	}
	e.sendNoticeLocked(notice)
	return nil
}

func (e *Engine) publishNotice(notice store.Notice) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.sendNoticeLocked(notice)
	}
}

func (e *Engine) sendNoticeLocked(notice store.Notice) {
	// The channel wakes idle hosts. SQLite is the pending delivery queue.
	select {
	case e.notices <- notice:
	default:
	}
}

func deliveredNoticeIDs(report slots.Report, pending []store.Notice) []string {
	if len(pending) == 0 {
		return nil
	}
	shipped := make(map[string]bool)
	for _, d := range report[slots.Session].Decisions {
		if d.Owner == "engine" && d.Disposition == slots.Ship {
			shipped[d.Key] = true
		}
	}
	var delivered []string
	for _, n := range pending {
		if shipped["notice-"+n.ID] {
			delivered = append(delivered, n.ID)
		}
	}
	return delivered
}
