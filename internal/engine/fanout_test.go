package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/tooluse"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
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

func TestSubtaskOverTheLimitWaitsForASlot(t *testing.T) {
	goals := []string{"one", "two", "three", "four", "five"}
	children := map[string]*fake.Provider{}
	for _, g := range goals {
		children[g] = fake.New(returnCall("ok", g))
	}
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", goals...), fake.Text("parent done")), children: children}, 4)
	// Five tool calls may run side by side; the subtask limit is what holds the fifth back.
	e, db, id := setupEngine(t, p, "runtime:\n  tool_concurrency: 8\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	views := subtaskResults(t, toolResults(t, db, id))
	for i, v := range views {
		if v.Status != "ok" || string(v.Result) != `"`+goals[i]+`"` {
			t.Fatalf("result %d: %+v", i, v)
		}
	}
	if len(views) != 5 || p.peak() != 4 {
		t.Fatalf("%d results, at most %d ran at once; want 5 and 4", len(views), p.peak())
	}
}

func TestConfiguredLimitAppliesAcrossTheAgent(t *testing.T) {
	children := map[string]*fake.Provider{"one": fake.New(returnCall("ok", "one")), "two": fake.New(returnCall("ok", "two"))}
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", "one", "two"), fake.Text("parent done")), children: children}, 2)
	e, db, id := setupEngine(t, p, "")
	e.children = subagent.NewLimit(1)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	for i, v := range subtaskResults(t, toolResults(t, db, id)) {
		if v.Status != "ok" {
			t.Fatalf("result %d: %+v", i, v)
		}
	}
	if p.peak() != 1 {
		t.Fatalf("a limit of 1 let %d run at once", p.peak())
	}
}

func TestBackgroundSubtaskOverTheLimitIsRefused(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "later", true), fake.Text("parent done")), children: map[string]*fake.Provider{"later": fake.New(returnCall("ok", "later"))}}
	e, db, id := setupEngine(t, p, "")
	e.children = subagent.NewLimit(1)
	hold, err := e.children.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "1 subtask is already running") || !strings.Contains(results[0].Content, "did not start") {
		t.Fatalf("a background subtask over the limit was not refused clearly: %+v", results)
	}
}

func TestCallsRunInModelOrder(t *testing.T) {
	// The model writes, then delegates: the subtask must see the write.
	step := fanOut("worker", "reader")
	mark := llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "mark-0", Name: "mark", Input: map[string]any{}}}
	step.Events = append([]llmtypes.StreamEvent{mark}, step.Events...)
	var mu sync.Mutex
	var order []string
	note := func(what string) {
		mu.Lock()
		order = append(order, what)
		mu.Unlock()
	}
	p := &orderProvider{routedProvider: &routedProvider{root: fake.New(step, fake.Text("parent done")),
		children: map[string]*fake.Provider{"reader": fake.New(returnCall("ok", "read"))}}, note: note}
	e, _, id := setupEngine(t, p, "")
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: mark\n      behavior: allow\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "mark", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) {
			note("mark")
			return tools.Result{Text: "written"}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[0] != "mark" || order[1] != "reader" {
		t.Fatalf("calls ran as %v; the write the model made first must run first", order)
	}
}

func TestOnlyConsecutiveConcurrentCallsRunTogether(t *testing.T) {
	jobs := []tooluse.Job{{Ready: true, Concurrent: true}, {Ready: true, Concurrent: true}, {Ready: true}, {}, {Ready: true, Concurrent: true}, {Ready: true}}
	var segments [][]int
	runSegments(jobs, func(seg []tooluse.Job) {
		var ready []int
		for i, j := range seg {
			if j.Ready {
				ready = append(ready, i)
			}
		}
		segments = append(segments, ready)
	})
	want := [][]int{{0, 1}, {2}, {4}, {5}}
	if fmt.Sprint(segments) != fmt.Sprint(want) {
		t.Fatalf("segments %v, want %v", segments, want)
	}
}

// orderProvider notes when each child first asks its model.
type orderProvider struct {
	*routedProvider
	note func(string)
	seen sync.Map
}

func (p *orderProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if name, _ := p.route(req); name != "" {
		if _, loaded := p.seen.LoadOrStore(name, true); !loaded {
			p.note(name)
		}
	}
	return p.routedProvider.StreamChat(ctx, req)
}

func TestCouncilDoesNotStarveAtTheDefaultLimit(t *testing.T) {
	leads := []string{"l0", "l1", "l2", "l3"}
	children := map[string]*fake.Provider{}
	for i, l := range leads {
		w := fmt.Sprintf("w%d", i)
		children[l] = fake.New(subtaskCall("worker", w, false), returnCall("ok", l))
		children[w] = fake.New(returnCall("ok", w))
	}
	p := newGated(&routedProvider{root: fake.New(fanOut("lead", leads...), fake.Text("root done")), children: children}, 4)
	e, db, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var result events.Result
	go func() { result, err = run.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		run.Cancel()
		t.Fatal("a council of four leads deadlocked at the default limit")
	}
	if err != nil || result.Text != "root done" {
		t.Fatalf("root %+v, %v", result, err)
	}
	for i, v := range subtaskResults(t, toolResults(t, db, id)) {
		if v.Status != "ok" {
			t.Fatalf("lead %d: %+v", i, v)
		}
	}
	conversations, err := db.Conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conversations {
		if c.ParentID != id {
			continue
		}
		for _, r := range toolResults(t, db, c.ID) {
			if r.IsError {
				t.Fatalf("lead %s's own subtask failed: %s", c.ID, r.Content)
			}
		}
	}
}

func TestCouncilWithALimitAndASharedBudget(t *testing.T) {
	spend := func(r fake.Response, tokens int) fake.Response {
		r.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: tokens}}}, r.Events...)
		return r
	}
	children := map[string]*fake.Provider{}
	for _, l := range []string{"la", "lb"} {
		children[l] = fake.New(fanOut("worker", l+"-1", l+"-2"), fake.Text("lead never"))
		for _, w := range []string{l + "-1", l + "-2"} {
			children[w] = fake.New(spend(toolResponse("unknown"), 300), spend(toolResponse("unknown"), 300), fake.Text("worker never"))
		}
	}
	p := &routedProvider{root: fake.New(fanOut("lead", "la", "lb"), fake.Text("root never")), children: children}
	e, db, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead.\n")
	// One worker and one lead at a time, 1,000 tokens for the whole tree.
	e.children, e.leads = subagent.NewLimit(1), subagent.NewLimit(1)
	e.cfg.TurnTokenBudget = 1000
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var result events.Result
	go func() { result, err = run.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a council under a limit of 1 and a shared budget did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != events.StopTokenBudget {
		t.Fatalf("root %+v", result)
	}
	if spent := result.Usage.InputTokens; spent < 1000 || spent > 1000+300 {
		t.Fatalf("the tree spent %d tokens against a 1,000 budget", spent)
	}
	// The first lead runs into the budget; the second, waiting for the one
	// lead slot, then finds it spent and does not start.
	views := subtaskResults(t, toolResults(t, db, id))
	var limited, refused int
	for _, v := range views {
		switch {
		case v.Status == "limit" && v.Reason == events.StopTokenBudget:
			limited++
		case v.Status == "refused" && strings.Contains(v.Reason, "shared token budget"):
			refused++
		}
	}
	if len(views) != 2 || limited != 1 || refused != 1 {
		t.Fatalf("leads ended %+v", views)
	}
}

func TestSharedBudgetStopsARunningSiblingMidStep(t *testing.T) {
	spender := toolResponse("unknown")
	spender.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 600}}}, spender.Events...)
	p := newGated(&routedProvider{root: fake.New(fanOut("worker", "spender", "sleeper"), fake.Text("root never")),
		children: map[string]*fake.Provider{"spender": fake.New(spender, fake.Text("never")), "sleeper": fake.New(fake.Response{Stall: true})}}, 2)
	e, db, id := setupEngine(t, p, "")
	e.cfg.TurnTokenBudget = 500
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = run.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a sibling stalled mid-step was not stopped when the shared budget ran out")
	}
	views := subtaskResults(t, toolResults(t, db, id))
	if len(views) != 2 || views[1].Status != "limit" || views[1].Reason != events.StopTokenBudget {
		t.Fatalf("results %+v", views)
	}
}

func TestBudgetReachesAGrandchild(t *testing.T) {
	spend := func(r fake.Response, tokens int) fake.Response {
		r.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: tokens}}}, r.Events...)
		return r
	}
	p := &routedProvider{root: fake.New(subtaskCall("lead", "council", false), fake.Text("root never")),
		children: map[string]*fake.Provider{
			"council": fake.New(subtaskCall("worker", "deep", false), fake.Text("council never")),
			"deep":    fake.New(spend(toolResponse("unknown"), 400), spend(toolResponse("unknown"), 400), spend(toolResponse("unknown"), 400), fake.Text("deep never")),
		}}
	e, _, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead.\n")
	e.cfg.TurnTokenBudget = 500
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != events.StopTokenBudget || result.Usage.InputTokens != 800 {
		t.Fatalf("root %+v; the grandchild should stop after two steps of 400", result)
	}
}

func TestCompactionSpendsTheSharedBudget(t *testing.T) {
	overflow := fake.Response{Err: &failure.Error{Class: failure.Overflow, Cause: errors.New("typed overflow")}}
	p := &routedProvider{root: fake.New(overflow, fake.Text("a summary of the older words"), subtaskCall("worker", "late", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"late": fake.New(returnCall("ok", "late"))}}
	e, db, id := setupEngine(t, p, "")
	seedLongHistory(t, db, id)
	// The summary call alone reads far more than 50 tokens of history.
	e.cfg.TurnTokenBudget = 50
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	if len(results) == 0 || !strings.Contains(results[len(results)-1].Content, "shared token budget") {
		t.Fatalf("the summary's spend was not charged to the turn: %+v", results)
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
		release, err := e.children.TryAcquire()
		if err != nil {
			t.Fatalf("a cancelled child kept its slot: %v", err)
		}
		defer release()
	}
}
