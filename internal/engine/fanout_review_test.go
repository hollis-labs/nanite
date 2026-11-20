package engine

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// spendAfter streams usage for "late" only once "early" has returned its result.
type spendAfter struct {
	*routedProvider
	e *Engine
}

func (p *spendAfter) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if name, _ := p.route(req); name == "late" {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			p.e.mu.Lock()
			returned := false
			for _, s := range p.e.subtasks {
				if s.returned != nil {
					returned = true
				}
			}
			p.e.mu.Unlock()
			if returned {
				break
			}
			time.Sleep(50 * time.Microsecond)
		}
	}
	return p.routedProvider.StreamChat(ctx, req)
}

// A sibling crosses the shared budget just after a child returned ok: the
// returned result stands.
func TestReturnedResultSurvivesTheBudgetCancel(t *testing.T) {
	relabelled := 0
	const n = 40
	for range n {
		late := toolResponse("unknown")
		late.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 1000}}}, late.Events...)
		inner := &routedProvider{root: fake.New(fanOut("worker", "early", "late"), fake.Text("root")),
			children: map[string]*fake.Provider{"early": fake.New(returnCall("ok", "done")), "late": fake.New(late, fake.Text("never"))}}
		p := &spendAfter{routedProvider: inner}
		e, db, id := setupEngine(t, p, "")
		p.e = e
		e.cfg.TurnTokenBudget = 500
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = run.Wait()
		v := subtaskResults(t, toolResults(t, db, id))[0]
		if v.Status != "ok" {
			relabelled++
			t.Logf("early returned ok but ended %s (%s), result %s", v.Status, v.Reason, v.Result)
		}
	}
	if relabelled > 0 {
		t.Fatalf("%d of %d returned-ok results were relabelled by the budget cancel", relabelled, n)
	}
}

// Canceling the parent ends a foreground subtask that is waiting for a
// slot, leaks no slot, and creates no child conversation.
func TestCancelWhileWaitingForASlot(t *testing.T) {
	p := &routedProvider{root: fake.New(fanOut("worker", "queued"), fake.Text("never")), children: map[string]*fake.Provider{"queued": fake.New(returnCall("ok", "q"))}}
	e, db, id := setupEngine(t, p, "")
	e.children = subagent.NewLimit(1)
	hold, err := e.children.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range run.Events() {
		}
	}()
	time.Sleep(300 * time.Millisecond)
	run.Cancel()
	done := make(chan struct{})
	go func() { _, _ = run.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end a subtask waiting for a slot")
	}
	hold()
	r, err := e.children.TryAcquire()
	if err != nil {
		t.Fatalf("slot leaked: %v", err)
	}
	r()
	conversations, _ := db.Conversations(t.Context())
	for _, c := range conversations {
		if c.ParentID == id {
			t.Fatalf("a child conversation was created while waiting: %+v", c)
		}
	}
}

// With a limit of one, a subtask waiting for the slot does not hold up the
// running subtask's relayed ask.
func TestAWaitingSubtaskDoesNotBlockARelayedAsk(t *testing.T) {
	p := &routedProvider{root: fake.New(fanOut("worker", "a", "b"), fake.Text("parent done")),
		children: map[string]*fake.Provider{"a": fake.New(callAs("deploy", "ca"), returnCall("ok", "a")), "b": fake.New(callAs("deploy", "cb"), returnCall("ok", "b"))}}
	e, db, id := setupEngine(t, p, "")
	e.children = subagent.NewLimit(1)
	ran := askedTool(t, e)
	e.cfg.Gate = &recordingGate{decide: func(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
		time.Sleep(100 * time.Millisecond)
		return permission.PermissionDecision{Allow: true, Human: true}
	}}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan events.Result)
	go func() { r, _ := run.Wait(); done <- r }()
	select {
	case r := <-done:
		if r.Text != "parent done" || ran.Load() != 2 {
			t.Fatalf("root %+v ran %d", r, ran.Load())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a subtask waiting for a slot blocked the running one's relayed ask")
	}
	for _, v := range subtaskResults(t, toolResults(t, db, id)) {
		if v.Status != "ok" {
			t.Fatalf("%+v", v)
		}
	}
}
