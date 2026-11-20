package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// fanOut is one assistant step calling subtask once per goal, in that order.
func fanOut(variant string, goals ...string) fake.Response {
	var out fake.Response
	for i, goal := range goals {
		call := subtaskCall(variant, goal, false)
		call.Events[0].ToolUse.ID = fmt.Sprintf("fan-%d", i)
		out.Events = append(out.Events, call.Events[0])
	}
	out.Events = append(out.Events, llmtypes.StreamEvent{Type: llmtypes.EventDone})
	return out
}

// gatedProvider holds each child's first request until `want` children are
// in flight at once (or a timeout passes), and records the most in flight.
// A child may also be delayed after the gate, to finish out of call order.
type gatedProvider struct {
	*routedProvider
	want  int
	delay map[string]time.Duration

	mu       sync.Mutex
	inFlight int
	most     int
	seen     map[string]bool
	all      chan struct{}
}

func newGated(inner *routedProvider, want int) *gatedProvider {
	return &gatedProvider{routedProvider: inner, want: want, seen: map[string]bool{}, all: make(chan struct{})}
}

func (p *gatedProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	name, _ := p.route(req)
	if name == "" {
		return p.routedProvider.StreamChat(ctx, req)
	}
	p.mu.Lock()
	first := !p.seen[name]
	p.seen[name] = true
	if first {
		p.inFlight++
		p.most = max(p.most, p.inFlight)
		if p.inFlight == p.want {
			close(p.all)
		}
	}
	p.mu.Unlock()
	if first {
		select {
		case <-p.all:
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
		}
		if d := p.delay[name]; d > 0 {
			time.Sleep(d)
		}
		defer func() {
			p.mu.Lock()
			p.inFlight--
			p.mu.Unlock()
		}()
	}
	return p.routedProvider.StreamChat(ctx, req)
}

func (p *gatedProvider) peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.most
}

func subtaskResults(t *testing.T, results []llmtypes.ContentBlock) []parentView {
	t.Helper()
	out := make([]parentView, len(results))
	for i, r := range results {
		if !strings.HasPrefix(r.Content, "{") {
			out[i] = parentView{Status: "refused", Reason: r.Content}
			continue
		}
		if err := json.Unmarshal([]byte(r.Content), &out[i]); err != nil {
			t.Fatalf("result %d is not JSON: %s", i, r.Content)
		}
	}
	return out
}

func TestSubtasksInOneStepRunTogetherAndReturnInCallOrder(t *testing.T) {
	goals := []string{"alpha", "beta", "gamma"}
	children := map[string]*fake.Provider{}
	for _, g := range goals {
		children[g] = fake.New(returnCall("ok", g))
	}
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", goals...), fake.Text("parent done")), children: children}, 3)
	// The first call finishes last and the last first.
	p.delay = map[string]time.Duration{"alpha": 300 * time.Millisecond, "beta": 150 * time.Millisecond}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "parent done" {
		t.Fatalf("parent %+v, %v", result, err)
	}
	if got := p.peak(); got != 3 {
		t.Fatalf("three subtasks in one step did not overlap: at most %d ran at once", got)
	}
	views := subtaskResults(t, toolResults(t, db, id))
	if len(views) != 3 {
		t.Fatalf("results %+v", views)
	}
	for i, g := range goals {
		if views[i].Status != "ok" || string(views[i].Result) != `"`+g+`"` {
			t.Fatalf("result %d is %+v, want %s's", i, views[i], g)
		}
	}
}

func TestSubtaskOverTheLimitIsRefusedClearly(t *testing.T) {
	goals := []string{"one", "two", "three", "four", "five"}
	children := map[string]*fake.Provider{}
	for _, g := range goals {
		children[g] = fake.New(returnCall("ok", g))
	}
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", goals...), fake.Text("parent done")), children: children}, 4)
	// Five tool calls may run side by side; the subtask limit is what stops the fifth.
	e, db, id := setupEngine(t, p, "runtime:\n  tool_concurrency: 8\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	var refused, ok int
	for _, r := range results {
		switch {
		case r.IsError && strings.Contains(r.Content, "4 subtasks are already running") && strings.Contains(r.Content, "did not start"):
			refused++
		case !r.IsError:
			ok++
		default:
			t.Fatalf("unexpected result %+v", r)
		}
	}
	if refused != 1 || ok != 4 {
		t.Fatalf("want 4 run and 1 refused, got %d and %d: %+v", ok, refused, results)
	}
	if p.peak() != 4 {
		t.Fatalf("at most %d ran at once, want 4", p.peak())
	}
}

func TestConfiguredLimitAppliesAcrossTheAgent(t *testing.T) {
	children := map[string]*fake.Provider{"one": fake.New(returnCall("ok", "one")), "two": fake.New(returnCall("ok", "two"))}
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", "one", "two"), fake.Text("parent done")), children: children}, 1)
	e, db, id := setupEngine(t, p, "")
	e.children = subagent.NewLimit(1)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var refused int
	for _, r := range toolResults(t, db, id) {
		if r.IsError && strings.Contains(r.Content, "1 subtasks are already running") {
			refused++
		}
	}
	if refused != 1 {
		t.Fatalf("a limit of 1 refused %d of two subtasks", refused)
	}
}

func TestTurnTokenBudgetIsSharedWithSubtasks(t *testing.T) {
	spend := func(r fake.Response, tokens int) fake.Response {
		r.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: tokens}}}, r.Events...)
		return r
	}
	children := map[string]*fake.Provider{}
	for _, g := range []string{"left", "right"} {
		// Each child alone stays under its own (absent) budget; only the
		// shared one stops it.
		children[g] = fake.New(spend(toolResponse("unknown"), 400), spend(toolResponse("unknown"), 400), spend(toolResponse("unknown"), 400), fake.Text("never"))
	}
	p := &routedProvider{root: fake.New(spend(fanOut("worker", "left", "right"), 100), fake.Text("parent never")), children: children}
	e, db, id := setupEngine(t, p, "")
	e.cfg.TurnTokenBudget = 1000
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != events.StopTokenBudget || result.Text == "parent never" {
		t.Fatalf("the root kept going past the shared budget: %+v", result)
	}
	views := subtaskResults(t, toolResults(t, db, id))
	if len(views) != 2 {
		t.Fatalf("results %+v", views)
	}
	for i, v := range views {
		if v.Status != "limit" || v.Reason != events.StopTokenBudget {
			t.Fatalf("child %d ended %+v, want limit token_budget", i, v)
		}
	}
	// 100 for the root, then the two children stop as soon as the shared total
	// reaches 1000: neither can spend its full 1,200 alone.
	if total := result.Usage.InputTokens; total >= 100+2*1200 || total < 1000 {
		t.Fatalf("turn spent %d tokens", total)
	}
}

func TestSubtaskDoesNotStartOnASpentBudget(t *testing.T) {
	spent := fanOut("worker", "late")
	spent.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 50}}}, spent.Events...)
	p := &routedProvider{root: fake.New(spent, fake.Text("parent never")), children: map[string]*fake.Provider{"late": fake.New(returnCall("ok", "late"))}}
	e, db, id := setupEngine(t, p, "")
	e.cfg.TurnTokenBudget = 50
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "shared token budget") {
		t.Fatalf("results %+v", results)
	}
	conversations, err := db.Conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conversations {
		if c.ParentID == id {
			t.Fatalf("a child conversation was created: %+v", c)
		}
	}
}

func TestCancellingTheParentCancelsItsSubtasks(t *testing.T) {
	// Each child stalls until its context ends: only cancellation stops it.
	children := map[string]*fake.Provider{"slow-a": fake.New(fake.Response{Stall: true}), "slow-b": fake.New(fake.Response{Stall: true})}
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", "slow-a", "slow-b"), fake.Text("parent never")), children: children}, 2)
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range run.Events() {
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for p.peak() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.peak() < 2 {
		t.Fatal("the subtasks never started")
	}
	var kids []string
	conversations, err := db.Conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conversations {
		if c.ParentID == id {
			kids = append(kids, c.ID)
		}
	}
	if len(kids) != 2 {
		t.Fatalf("children %v", kids)
	}
	run.Cancel()
	waited := make(chan struct{})
	go func() { _, _ = run.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("canceling the parent did not end its subtasks")
	}
	// Every child run has ended and released its slot.
	for _, child := range kids {
		e.mu.Lock()
		running := e.active[child] != nil
		e.mu.Unlock()
		if running {
			t.Fatalf("child %s is still running after its parent was cancelled", child)
		}
	}
	for range subagent.DefaultLimit {
		release, err := e.children.Acquire()
		if err != nil {
			t.Fatalf("a cancelled child kept its slot: %v", err)
		}
		defer release()
	}
}
