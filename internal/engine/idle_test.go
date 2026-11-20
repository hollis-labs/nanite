package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/turn"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/provider/keepalive"
)

// busyProvider sends only keepalives for a while, then a reply: a provider
// whose model is writing something the stream does not pass on.
type busyProvider struct {
	llmcontracts.Provider
	busy, every time.Duration
}

func (p busyProvider) StreamChat(ctx context.Context, _ llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	out := make(chan llmtypes.StreamEvent)
	go func() {
		defer close(out)
		send := func(ev llmtypes.StreamEvent) bool {
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for end := time.Now().Add(p.busy); time.Now().Before(end); {
			time.Sleep(p.every)
			if !send(llmtypes.StreamEvent{Type: keepalive.Event}) {
				return
			}
		}
		_ = send(llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "done writing"}) && send(llmtypes.StreamEvent{Type: llmtypes.EventDone})
	}()
	return out, nil
}

// Keepalives keep a turn alive past its idle timeout and never reach the
// reply, the transcript or the events.
func TestKeepalivesOutlastTheIdleTimeout(t *testing.T) {
	e, db, id := setupEngine(t, busyProvider{Provider: fake.New(), busy: 400 * time.Millisecond, every: 30 * time.Millisecond}, "")
	e.cfg.IdleTimeout = 120 * time.Millisecond
	run, err := e.Send(t.Context(), id, Input{Text: "write it"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := run.Wait()
	if err != nil || got.Text != "done writing" {
		t.Fatalf("Wait = %+v, %v", got, err)
	}
	h, err := db.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range h {
		for _, b := range m.Blocks {
			if strings.Contains(b.Text, "keepalive") {
				t.Fatalf("a keepalive reached the transcript: %+v", b)
			}
		}
	}
}

// IdleTimeout sets how long a silent stream may last before the turn fails as
// stalled.
func TestIdleTimeoutIsConfigurable(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Response{Stall: true}), "")
	e.cfg.IdleTimeout = 100 * time.Millisecond
	start := time.Now()
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = run.Wait()
	if !errors.Is(err, turn.ErrTurnStalled) {
		t.Fatalf("Wait error = %v, want a stall", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the stall took %s; IdleTimeout was not used", elapsed)
	}
	if e.idleTimeout() != 100*time.Millisecond {
		t.Fatal("idleTimeout ignored the config")
	}
	e.cfg.IdleTimeout = 0
	if e.idleTimeout() != DefaultIdleTimeout {
		t.Fatal("zero IdleTimeout is not the default")
	}
}
