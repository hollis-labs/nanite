package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

func researchStart(goal string) subagent.Request {
	return subagent.Request{Goal: goal, Variant: "research", ReturnSpec: json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)}
}

func dispatchWait(t *testing.T, run *Run) ([]events.Event, events.Result) {
	t.Helper()
	var seen []events.Event
	for event := range run.Events() {
		seen = append(seen, event)
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return seen, result
}

// A host dispatches three research subtasks at once: they run side by side
// and come back in the order given, as structured results.
func TestDispatchRunsResearchSubtasksInParallel(t *testing.T) {
	goals := []string{"find alpha", "find beta", "find gamma"}
	children := map[string]*fake.Provider{}
	for _, g := range goals {
		children[g] = fake.New(returnCall("ok", map[string]any{"answer": strings.TrimPrefix(g, "find ")}))
	}
	p := newGated(&routedProvider{root: fake.New(), children: children}, 3)
	e, db, id := setupEngine(t, p, "")
	var mu sync.Mutex
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { mu.Lock(); sunk = append(sunk, rec); mu.Unlock() }
	starts := []subagent.Request{researchStart(goals[0]), researchStart(goals[1]), researchStart(goals[2])}
	run, err := e.Dispatch(t.Context(), id, starts)
	if err != nil {
		t.Fatal(err)
	}
	seen, result := dispatchWait(t, run)
	if p.peak() != 3 {
		t.Fatalf("at most %d subtasks ran at once, want 3", p.peak())
	}
	if len(result.Dispatched) != 3 {
		t.Fatalf("outcomes %+v", result.Dispatched)
	}
	for i, out := range result.Dispatched {
		var view parentView
		if out.Err != "" || out.IsError || json.Unmarshal(out.Result, &view) != nil || view.Status != "ok" {
			t.Fatalf("outcome %d: %+v", i, out)
		}
		var answer struct{ Answer string }
		if json.Unmarshal(view.Result, &answer) != nil || answer.Answer != strings.TrimPrefix(goals[i], "find ") {
			t.Fatalf("outcome %d result %s, want %s", i, view.Result, goals[i])
		}
	}
	started := 0
	for _, ev := range seen {
		if s, ok := ev.(events.SubagentStarted); ok && s.Variant == "research" && s.ParentID == id {
			started++
		}
	}
	if started != 3 {
		t.Fatalf("SubagentStarted on the dispatch run: %d", started)
	}
	// A dispatch adds nothing to the conversation's history.
	if history, err := db.History(t.Context(), id); err != nil || len(history) != 0 {
		t.Fatalf("history %+v, %v", history, err)
	}
	// The dispatch run's record has source host and lists the subtasks; each
	// child's record links back to it.
	mu.Lock()
	defer mu.Unlock()
	var host *trace.Turn
	kids := 0
	for i := range sunk {
		switch {
		case sunk[i].ConversationID == id:
			host = &sunk[i]
		case sunk[i].ParentConversationID == id:
			kids++
			if sunk[i].Source != trace.SourceSubagent || sunk[i].ParentTurnID != run.ID() || !strings.HasPrefix(sunk[i].ParentCallID, "dispatch-") {
				t.Fatalf("child record %+v", sunk[i])
			}
		}
	}
	if host == nil || host.Source != trace.SourceHost || host.TurnID != run.ID() || len(host.Subtasks) != 3 || kids != 3 {
		t.Fatalf("host record %+v, %d child records", host, kids)
	}
	for _, sub := range host.Subtasks {
		if sub.Status != trace.SubtaskOK || !strings.HasPrefix(sub.ToolCallID, "dispatch-") {
			t.Fatalf("subtask entry %+v", sub)
		}
	}
}

// Every start is checked before anything runs.
func TestDispatchChecksStartsFirst(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	if _, err := e.Dispatch(t.Context(), id, nil); !errors.Is(err, ErrNoStarts) {
		t.Fatalf("no starts: %v", err)
	}
	_, err := e.Dispatch(t.Context(), id, []subagent.Request{researchStart("fine"), {Goal: "no variant"}})
	if err == nil || !strings.Contains(err.Error(), "start 2") || !strings.Contains(err.Error(), "variant required") {
		t.Fatalf("missing variant: %v", err)
	}
	if _, err := e.Dispatch(t.Context(), id, []subagent.Request{{Goal: "x", Variant: "nope"}}); err == nil {
		t.Fatal("an unknown variant was accepted")
	}
	e.mu.Lock()
	busy := e.active[id] != nil
	e.mu.Unlock()
	if busy {
		t.Fatal("a refused dispatch left the conversation busy")
	}
}

// A dispatch holds the conversation like a turn, and only runs on a root.
func TestDispatchHoldsTheConversation(t *testing.T) {
	release := make(chan struct{})
	p := &routedProvider{root: fake.New(toolResponse("hold"), fake.Text("done")), children: map[string]*fake.Provider{}}
	e, db, id := setupEngine(t, p, "")
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "hold", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			<-release
			return tools.Result{Text: "ok"}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(t.Context(), id, []subagent.Request{researchStart("x")}); !errors.Is(err, ErrBusy) {
		t.Fatalf("dispatch during a turn: %v", err)
	}
	close(release)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	// A subtask conversation cannot be dispatched on.
	child := subagent.Request{Goal: "kid", Variant: "worker", ReturnSpec: json.RawMessage(`"a note"`)}
	p.children["kid"] = fake.New(returnCall("ok", "x"))
	d, err := e.Dispatch(t.Context(), id, []subagent.Request{child})
	if err != nil {
		t.Fatal(err)
	}
	dispatchWait(t, d)
	kid := childOf(t, db, id)
	if _, err := e.Dispatch(t.Context(), kid.ID, []subagent.Request{researchStart("x")}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("dispatch on a subtask: %v", err)
	}
}

// A background dispatch returns its job handle at once; its finish is posted
// as a notice, and Job reads its result.
func TestBackgroundDispatchNotifiesWhenItFinishes(t *testing.T) {
	start := researchStart("slow work")
	start.Background = true
	p := &routedProvider{root: fake.New(), children: map[string]*fake.Provider{"slow work": fake.New(returnCall("ok", map[string]any{"answer": "done"}))}}
	e, _, id := setupEngine(t, p, "")
	run, err := e.Dispatch(t.Context(), id, []subagent.Request{start})
	if err != nil {
		t.Fatal(err)
	}
	_, result := dispatchWait(t, run)
	var handle struct{ ID, Kind, Status string }
	if len(result.Dispatched) != 1 || json.Unmarshal(result.Dispatched[0].Result, &handle) != nil || handle.Kind != "subtask" || handle.ID == "" {
		t.Fatalf("background outcome %+v", result.Dispatched)
	}
	select {
	case n := <-e.Notices():
		if n.ConversationID != id || n.Kind != subtaskJobKind || n.RunID != "" || !strings.Contains(n.Body, handle.ID) {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notice when the background dispatch finished")
	}
	job, err := e.Job(t.Context(), id, handle.ID)
	if err != nil || !strings.Contains(job, `"status":"ok"`) || !strings.Contains(job, `"answer":"done"`) {
		t.Fatalf("job %s, %v", job, err)
	}
}

// A dispatched subtask's ask is relayed to the dispatch run, attributed as
// dispatched by the host.
func TestDispatchedSubtaskRelaysItsAsk(t *testing.T) {
	p := &routedProvider{root: fake.New(), children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), returnCall("ok", "shipped"))}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	gate := &recordingGate{decide: allowFor(permission.ScopeOnce)}
	e.cfg.Gate = gate
	start := subagent.Request{Goal: "ship it", Variant: "worker", ReturnSpec: json.RawMessage(`"a note"`)}
	run, err := e.Dispatch(t.Context(), id, []subagent.Request{start})
	if err != nil {
		t.Fatal(err)
	}
	seen, result := dispatchWait(t, run)
	child := childOf(t, db, id)
	requests := gate.asked()
	if len(requests) != 1 || requests[0].Subtask == nil || !requests[0].Subtask.Dispatched || requests[0].Subtask.ChildID != child.ID ||
		requests[0].ConversationID != id || !strings.HasPrefix(requests[0].Subtask.CallID, "dispatch-") {
		t.Fatalf("gate requests %+v", requests)
	}
	asked := 0
	for _, ev := range seen {
		if a, ok := ev.(events.PermissionAsked); ok && a.Request.Subtask != nil && a.Request.Subtask.Dispatched {
			asked++
		}
	}
	if asked != 1 || ran.Load() != 1 || result.Dispatched[0].IsError {
		t.Fatalf("asked %d, ran %d, outcome %+v", asked, ran.Load(), result.Dispatched)
	}
	e.mu.Lock()
	left := len(e.dispatched)
	e.mu.Unlock()
	if left != 0 {
		t.Fatalf("dispatched marks left behind: %d", left)
	}
}

// The subtask limit holds for a dispatch: over it, a start waits for a slot.
func TestDispatchWaitsForASlot(t *testing.T) {
	children := map[string]*fake.Provider{
		"one": fake.New(returnCall("ok", map[string]any{"answer": "1"})),
		"two": fake.New(returnCall("ok", map[string]any{"answer": "2"})),
	}
	p := newGated(&routedProvider{root: fake.New(), children: children}, 2)
	e, _, id := setupEngine(t, p, "")
	e.children = subagent.NewLimit(1)
	run, err := e.Dispatch(t.Context(), id, []subagent.Request{researchStart("one"), researchStart("two")})
	if err != nil {
		t.Fatal(err)
	}
	seen, result := dispatchWait(t, run)
	if p.peak() != 1 {
		t.Fatalf("%d ran at once under a limit of 1", p.peak())
	}
	waited := false
	for _, ev := range seen {
		if pr, ok := ev.(events.SubtaskProgress); ok && pr.StatusLine == "waiting for a subtask slot" {
			waited = true
		}
	}
	if !waited || result.Dispatched[0].IsError || result.Dispatched[1].IsError {
		t.Fatalf("waited %v, outcomes %+v", waited, result.Dispatched)
	}
}

// The agent's per-turn token budget covers a dispatch: a dispatched subtask
// that spends past it is stopped with limit, reason token_budget.
func TestDispatchSpendsTheTurnBudget(t *testing.T) {
	spend := returnCall("ok", map[string]any{"answer": "1"})
	spend.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 900, OutputTokens: 200}}}, spend.Events...)
	p := &routedProvider{root: fake.New(), children: map[string]*fake.Provider{"first": fake.New(spend)}}
	e, _, id := setupEngine(t, p, "")
	e.cfg.TurnTokenBudget = 1000
	run, err := e.Dispatch(t.Context(), id, []subagent.Request{researchStart("first")})
	if err != nil {
		t.Fatal(err)
	}
	_, result := dispatchWait(t, run)
	var view parentView
	if len(result.Dispatched) != 1 || json.Unmarshal(result.Dispatched[0].Result, &view) != nil ||
		view.Status != "limit" || view.Reason != events.StopTokenBudget || !result.Dispatched[0].IsError {
		t.Fatalf("outcome %+v (%+v)", result.Dispatched, view)
	}
	if result.Usage.InputTokens != 900 {
		t.Fatalf("usage %+v", result.Usage)
	}
}

// A notice posted while a turn runs is reported on that turn and to
// subscribers, who learn which turn carried it.
func TestNoticeDuringATurnIsReportedOnIt(t *testing.T) {
	posted := make(chan struct{})
	p := &routedProvider{root: fake.New(toolResponse("post"), fake.Text("done")), children: map[string]*fake.Provider{}}
	e, _, id := setupEngine(t, p, "")
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "post", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			defer close(posted)
			return tools.Result{Text: "ok"}, e.PostNotice(ctx, id, "host", "results are in")
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var onRun []events.NoticePosted
	for ev := range run.Events() {
		if n, ok := ev.(events.NoticePosted); ok {
			onRun = append(onRun, n)
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(onRun) != 1 || onRun[0].Body != "results are in" || onRun[0].RunID != run.ID() {
		t.Fatalf("on the run: %+v", onRun)
	}
	select {
	case n := <-e.Notices():
		if n.RunID != run.ID() || n.ID != onRun[0].ID {
			t.Fatalf("subscriber got %+v", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber got nothing")
	}
}

// Every subscriber gets every notice, however many are posted before it reads.
func TestNoticeSubscribersLoseNothing(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	ctx, cancel := context.WithCancel(t.Context())
	a, b := e.SubscribeNotices(ctx), e.SubscribeNotices(ctx)
	const n = 200
	for i := range n {
		if err := e.PostNotice(t.Context(), id, "host", strings.Repeat("x", i%7+1)); err != nil {
			t.Fatal(err)
		}
	}
	for _, ch := range []<-chan events.NoticePosted{a, b} {
		for i := range n {
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
				t.Fatalf("lost notice %d", i)
			}
		}
	}
	cancel()
	select {
	case _, open := <-a:
		if open {
			t.Fatal("a stray notice after the last")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not close when its context ended")
	}
}

// A turn started because of a notice is recorded with source reflex; an
// unknown source is refused.
func TestNoticeTurnSource(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("told")), "")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	if _, err := e.Send(t.Context(), id, Input{Text: "x", Source: "cron"}); err == nil {
		t.Fatal("an unknown turn source was accepted")
	}
	run, err := e.Send(t.Context(), id, Input{Text: "[notice job] done", Source: SourceNotice})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(sunk) != 1 || sunk[0].Source != trace.SourceReflex {
		t.Fatalf("records %+v", sunk)
	}
}
