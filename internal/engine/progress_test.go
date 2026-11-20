package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

// lookupTool is a read-only host tool children may call.
func lookupTool(t *testing.T, e *Engine) {
	t.Helper()
	e.cfg.Gate = permission.AllowGate
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "lookup", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{Text: "found"}, nil }}); err != nil {
		t.Fatal(err)
	}
}

func progressOf(run *Run) []events.SubtaskProgress {
	var out []events.SubtaskProgress
	for event := range run.Events() {
		if p, ok := event.(events.SubtaskProgress); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestSubtaskProgressIsEmitted(t *testing.T) {
	p := &routedProvider{
		root:     fake.New(subtaskCall("worker", "look around", false), fake.Text("root done")),
		children: map[string]*fake.Provider{"look around": fake.New(toolResponse("lookup"), returnCall("ok", "seen"))},
	}
	e, db, id := setupEngine(t, p, "")
	lookupTool(t, e)
	e.progressEvery = 0 // every change is sent
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := progressOf(run)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	var lines []string
	sawLookup := false
	for _, p := range got {
		if p.ID != child.ID || p.Variant != "worker" {
			t.Fatalf("progress for %q/%q, want %q/worker", p.ID, p.Variant, child.ID)
		}
		lines = append(lines, p.StatusLine)
		sawLookup = sawLookup || (p.Tool == "lookup" && strings.Contains(p.StatusLine, "calling lookup"))
	}
	if len(got) < 2 || got[0].StatusLine != "starting" || !sawLookup {
		t.Fatalf("progress lines %q", lines)
	}
	// The parent model never sees progress: its next request carries none.
	p.mu.Lock()
	defer p.mu.Unlock()
	last := p.requests[""][len(p.requests[""])-1]
	raw, _ := json.Marshal(last.Messages)
	if strings.Contains(string(raw), "calling lookup") || strings.Contains(string(raw), "starting") {
		t.Fatalf("progress reached the parent model: %s", raw)
	}
}

func TestSubtaskProgressIsRateLimited(t *testing.T) {
	p := &routedProvider{
		root: fake.New(subtaskCall("worker", "look around", false), fake.Text("root done")),
		children: map[string]*fake.Provider{"look around": fake.New(toolResponse("lookup"), toolResponse("lookup"), toolResponse("lookup"),
			returnCall("ok", "seen"))},
	}
	e, _, id := setupEngine(t, p, "")
	lookupTool(t, e)
	e.progressEvery = time.Hour
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := progressOf(run)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	// The first state goes at once; the three tool calls within the interval
	// are held, and only the latest is sent, when the child ends.
	if len(got) != 2 || got[0].StatusLine != "starting" || got[1].StatusLine != "step 4: calling return_result" {
		t.Fatalf("rate-limited progress %+v", got)
	}
}

func TestThrottleHoldsTheLatest(t *testing.T) {
	th := &throttle{interval: time.Second}
	t0 := time.Unix(1000, 0)
	if _, ok := th.offer(t0, events.SubtaskProgress{StatusLine: "a"}); !ok {
		t.Fatal("the first state was held")
	}
	for _, line := range []string{"b", "c"} {
		if _, ok := th.offer(t0.Add(100*time.Millisecond), events.SubtaskProgress{StatusLine: line}); ok {
			t.Fatalf("%s passed within the interval", line)
		}
	}
	// The held state is due one interval after the last one sent, not later.
	if due, held := th.due(); !held || !due.Equal(t0.Add(time.Second)) {
		t.Fatalf("due %v %v, want %v", due, held, t0.Add(time.Second))
	}
	if _, ok := th.flush(t0.Add(500*time.Millisecond), false); ok {
		t.Fatal("flushed before the interval")
	}
	if p, ok := th.flush(t0.Add(time.Second), false); !ok || p.StatusLine != "c" {
		t.Fatalf("flush %+v %v, want the latest (c)", p, ok)
	}
	if _, ok := th.flush(t0.Add(3*time.Second), true); ok {
		t.Fatal("flushed twice")
	}
	// Forced, a held state goes at once.
	th.offer(t0.Add(3*time.Second), events.SubtaskProgress{StatusLine: "d"})
	th.offer(t0.Add(3*time.Second+time.Millisecond), events.SubtaskProgress{StatusLine: "e"})
	if p, ok := th.flush(t0.Add(3*time.Second+2*time.Millisecond), true); !ok || p.StatusLine != "e" {
		t.Fatalf("forced flush %+v %v", p, ok)
	}
}

// With no interval nothing is ever held, so follow never arms a timer.
func TestZeroIntervalHoldsNothing(t *testing.T) {
	th := &throttle{}
	now := time.Unix(1000, 0)
	for i := range 3 {
		if _, ok := th.offer(now, events.SubtaskProgress{Step: i}); !ok {
			t.Fatalf("state %d held with no interval", i)
		}
		if _, held := th.due(); held {
			t.Fatal("a state is held with no interval")
		}
	}
}

// Each child follows with its own limiter, so concurrent children cannot
// share or race on one.
func TestConcurrentFollowersKeepTheirOwnLimits(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	emit := func(_ context.Context, event func(events.EventMeta) events.Event) {
		p := event(events.EventMeta{}).(events.SubtaskProgress)
		mu.Lock()
		counts[p.ID]++
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run := &Run{events: make(chan events.Event, 32), done: make(chan struct{})}
			go func() {
				for i := range 10 {
					run.events <- events.ToolCallStarted{CallID: "c", Name: "tool" + string(rune('0'+i))}
				}
				close(run.events)
				close(run.done)
			}()
			if _, err := follow(t.Context(), run, followed{ID: id, Variant: "worker", Depth: 1}, time.Hour, emit, emit); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// Each sends its first state and, when its child ends, the latest held
	// one: two each, whatever the others do.
	for _, id := range []string{"a", "b", "c", "d"} {
		if counts[id] != 2 {
			t.Fatalf("counts %v, want two each", counts)
		}
	}
}

func TestGrandchildProgressReachesTheRoot(t *testing.T) {
	p := &routedProvider{
		root: fake.New(subtaskCall("lead", "run a council", false), fake.Text("root done")),
		children: map[string]*fake.Provider{
			"run a council": fake.New(subtaskCall("worker", "one view", false), returnCall("ok", "council")),
			"one view":      fake.New(toolResponse("lookup"), returnCall("ok", "view")),
		},
	}
	e, db, id := setupEngine(t, p, "")
	lookupTool(t, e)
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead a council.\n")
	e.progressEvery = 0
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := progressOf(run)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	lead := childOf(t, db, id)
	worker := childOf(t, db, lead.ID)
	seen := map[string]bool{}
	for _, p := range got {
		seen[p.ID+"/"+p.Variant] = true
	}
	if !seen[lead.ID+"/lead"] || !seen[worker.ID+"/worker"] {
		t.Fatalf("progress ids %v; want lead %s and worker %s", seen, lead.ID, worker.ID)
	}
}

func TestSubtaskTraceLinksToItsParentTurn(t *testing.T) {
	p := &routedProvider{
		root:     fake.New(subtaskCall("worker", "look around", false), fake.Text("root done")),
		children: map[string]*fake.Provider{"look around": fake.New(toolResponse("lookup"), returnCall("ok", "seen"))},
	}
	e, db, id := setupEngine(t, p, "")
	lookupTool(t, e)
	var mu sync.Mutex
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { mu.Lock(); sunk = append(sunk, rec); mu.Unlock() }
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	mu.Lock()
	defer mu.Unlock()
	var root, kid *trace.Turn
	for i := range sunk {
		switch sunk[i].ConversationID {
		case id:
			root = &sunk[i]
		case child.ID:
			kid = &sunk[i]
		}
	}
	if root == nil || kid == nil {
		t.Fatalf("records %+v", sunk)
	}
	if kid.ParentTurnID != root.TurnID || kid.ParentCallID != "call-1" || kid.Depth != 1 || kid.Source != trace.SourceSubagent || kid.ParentConversationID != id {
		t.Fatalf("child record %+v, root turn %s", kid, root.TurnID)
	}
	if len(kid.Situation) != 0 || len(root.Situation) != 0 {
		t.Fatal("situation is reserved and must stay empty")
	}
	if len(root.Subtasks) != 1 {
		t.Fatalf("root subtasks %+v", root.Subtasks)
	}
	s := root.Subtasks[0]
	if s.ConversationID != child.ID || s.ToolCallID != "call-1" || s.Variant != "worker" || s.Status != trace.SubtaskOK || s.Steps != 2 || s.Background || s.DurationMS < 0 {
		t.Fatalf("root's subtask entry %+v", s)
	}
	if root.ParentTurnID != "" || root.Depth != 0 || len(kid.Subtasks) != 0 {
		t.Fatalf("root %+v / child subtasks %+v", root, kid.Subtasks)
	}
}

func TestBackgroundSubtaskIsListedRunning(t *testing.T) {
	p := &routedProvider{
		root:     fake.New(subtaskCall("worker", "later", true), fake.Text("root done")),
		children: map[string]*fake.Provider{"later": fake.New(returnCall("ok", "done"))},
	}
	e, db, id := setupEngine(t, p, "")
	var mu sync.Mutex
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { mu.Lock(); sunk = append(sunk, rec); mu.Unlock() }
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(sunk)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	var rootRec, childRec *trace.Turn
	for i := range sunk {
		switch sunk[i].ConversationID {
		case id:
			rootRec = &sunk[i]
		case child.ID:
			childRec = &sunk[i]
		}
	}
	if rootRec == nil || childRec == nil {
		t.Fatalf("records %d", len(sunk))
	}
	if len(rootRec.Subtasks) != 1 || rootRec.Subtasks[0].Status != trace.SubtaskRunning || !rootRec.Subtasks[0].Background || rootRec.Subtasks[0].ConversationID != child.ID || rootRec.Subtasks[0].ToolCallID != "call-1" {
		t.Fatalf("root subtasks %+v", rootRec.Subtasks)
	}
	if childRec.ParentTurnID != rootRec.TurnID || childRec.Depth != 1 {
		t.Fatalf("background child record %+v", childRec)
	}
}

// anthropicShaped adds the two usage events the native Anthropic adapter
// sends per response (message_start, then message_delta).
func anthropicShaped(r fake.Response) fake.Response {
	ev := []llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 10}}}
	n := len(r.Events)
	ev = append(ev, r.Events[:n-1]...)
	ev = append(ev, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{OutputTokens: 5, StopReason: "tool_use"}}, r.Events[n-1])
	return fake.Response{Events: ev}
}

// Steps come from the turn's own step count, so a provider that reports
// usage twice per response, or never, still numbers them right.
func TestProgressStepsFollowRealSteps(t *testing.T) {
	for name, shape := range map[string]func(fake.Response) fake.Response{
		"two usage events": anthropicShaped,
		"no usage":         func(r fake.Response) fake.Response { return r },
	} {
		t.Run(name, func(t *testing.T) {
			p := &routedProvider{
				root:     fake.New(subtaskCall("worker", "look around", false), fake.Text("root done")),
				children: map[string]*fake.Provider{"look around": fake.New(shape(toolResponse("lookup")), shape(returnCall("ok", "seen")))},
			}
			e, _, id := setupEngine(t, p, "")
			lookupTool(t, e)
			e.progressEvery = 0
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			finished := -1
			for ev := range run.Events() {
				switch x := ev.(type) {
				case events.SubtaskProgress:
					lines = append(lines, x.StatusLine)
				case events.SubagentFinished:
					finished = x.Result.Steps
				}
			}
			want := []string{"starting", "step 1: calling lookup", "step 2: calling return_result"}
			if strings.Join(lines, "|") != strings.Join(want, "|") || finished != 2 {
				t.Fatalf("lines %q, finished at %d steps; want %q and 2", lines, finished, want)
			}
		})
	}
}

// The root's stream shows a depth-2 subtask's whole life: start, progress
// and finish, each naming its lead as parent.
func TestGrandchildLifecycleReachesTheRoot(t *testing.T) {
	p := &routedProvider{
		root: fake.New(subtaskCall("lead", "run a council", false), fake.Text("root done")),
		children: map[string]*fake.Provider{
			"run a council": fake.New(subtaskCall("worker", "one view", false), returnCall("ok", "council")),
			"one view":      fake.New(toolResponse("lookup"), returnCall("ok", "view")),
		},
	}
	e, db, id := setupEngine(t, p, "")
	lookupTool(t, e)
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead a council.\n")
	e.progressEvery = time.Hour // held progress must still arrive before done
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	parents := map[string]string{}
	depths := map[string]int{}
	for ev := range run.Events() {
		switch x := ev.(type) {
		case events.SubagentStarted:
			order, parents[x.ID], depths[x.ID] = append(order, "start "+x.ID), x.ParentID, x.Depth
		case events.SubtaskProgress:
			order = append(order, "progress "+x.ID)
			if x.ParentID != parents[x.ID] || x.Depth != depths[x.ID] {
				t.Errorf("progress %+v disagrees with its start", x)
			}
		case events.SubagentFinished:
			order = append(order, "done "+x.ID)
			if x.ParentID != parents[x.ID] || x.Depth != depths[x.ID] {
				t.Errorf("finish %+v disagrees with its start", x)
			}
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	lead := childOf(t, db, id)
	worker := childOf(t, db, lead.ID)
	if parents[worker.ID] != lead.ID || depths[worker.ID] != 2 || parents[lead.ID] != id || depths[lead.ID] != 1 {
		t.Fatalf("parents %v depths %v", parents, depths)
	}
	index := func(s string) int { return slices.Index(order, s) }
	start, done := index("start "+worker.ID), index("done "+worker.ID)
	last := -1
	for i, s := range order {
		if s == "progress "+worker.ID {
			last = i
		}
	}
	if start < 0 || done < 0 || last < 0 || start >= last || last >= done {
		t.Fatalf("worker order %q", order)
	}
	if index("done "+lead.ID) < done {
		t.Fatalf("the lead finished before its worker: %q", order)
	}
}

// A background subtask's progress lands in whatever turn its parent runs:
// it is marked background, and its last state says how it ended.
func TestBackgroundProgressIsMarked(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	p := &routedProvider{
		root:     fake.New(subtaskCall("worker", "later", true), fake.Text("root done"), toolResponse("hold"), fake.Text("second turn")),
		children: map[string]*fake.Provider{"later": fake.New(toolResponse("bgwork"), toolResponse("bgwork"), returnCall("ok", "done"))},
	}
	e, _, id := setupEngine(t, p, "")
	e.cfg.Gate = permission.AllowGate
	var once sync.Once
	if err := e.cfg.Tools.SetGroup("host",
		tools.Tool{Name: "bgwork", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(context.Context, tools.Call) (tools.Result, error) {
				entered <- struct{}{}
				<-release
				return tools.Result{Text: "ok"}, nil
			}},
		tools.Tool{Name: "hold", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
				once.Do(func() { close(release) })
				// Hold this turn open until the background job has finished, so
				// its progress, final state included, lands here.
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					if bgFinished(ctx, e, id) {
						return tools.Result{Text: "held"}, nil
					}
					time.Sleep(5 * time.Millisecond)
				}
				return tools.Result{Text: "timed out"}, nil
			}}); err != nil {
		t.Fatal(err)
	}
	e.backgroundProgressEvery = 0
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	for range run.Events() {
	}
	<-entered
	second, err := e.Send(t.Context(), id, Input{Text: "unrelated question"})
	if err != nil {
		t.Fatal(err)
	}
	var got []events.SubtaskProgress
	for ev := range second.Events() {
		if x, ok := ev.(events.SubtaskProgress); ok {
			got = append(got, x)
		}
	}
	if len(got) == 0 {
		t.Fatal("no background progress reached the running turn")
	}
	for _, x := range got {
		if !x.Background {
			t.Fatalf("background progress not marked: %+v", x)
		}
	}
	if final := got[len(got)-1]; final.Status != "ok" || !strings.HasPrefix(final.StatusLine, "done") {
		t.Fatalf("last background progress %+v, want done (ok)", final)
	}
}

// Stopping the parent while a foreground child is mid-tool, or closing the
// engine while a background child is mid-tool, emits nothing after the
// terminal event.
func TestNoProgressAfterTerminal(t *testing.T) {
	for _, bg := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground cancel", true: "background close"}[bg], func(t *testing.T) {
			entered := make(chan struct{}, 4)
			p := &routedProvider{
				root:     fake.New(subtaskCall("worker", "later", bg), fake.Text("root done")),
				children: map[string]*fake.Provider{"later": fake.New(toolResponse("bgwork"), returnCall("ok", "done"))},
			}
			e, _, id := setupEngine(t, p, "")
			e.cfg.Gate = permission.AllowGate
			if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "bgwork", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
				Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
					entered <- struct{}{}
					<-ctx.Done()
					return tools.Result{}, ctx.Err()
				}}); err != nil {
				t.Fatal(err)
			}
			e.progressEvery, e.backgroundProgressEvery = time.Millisecond, time.Millisecond
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				terminal := false
				for ev := range run.Events() {
					if terminal {
						t.Errorf("event %T after the terminal event", ev)
					}
					switch ev.(type) {
					case events.RunFinished, events.RunFailed:
						terminal = true
					}
				}
			}()
			<-entered
			if !bg {
				run.Cancel()
			}
			<-done
		})
	}
}

// A trace record's usage is the turn's own requests; the subtask entry
// carries the child's, so they add up to the tree's total. The run's result
// and its Usage events still include the child, as before.
func TestTraceUsageIsTheTurnsOwn(t *testing.T) {
	withUsage := func(r fake.Response, in, out int) fake.Response {
		r.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: in, OutputTokens: out}}}, r.Events...)
		return r
	}
	p := &routedProvider{
		root:     fake.New(withUsage(subtaskCall("worker", "child work", false), 100, 10), withUsage(fake.Text("parent done"), 200, 20)),
		children: map[string]*fake.Provider{"child work": fake.New(withUsage(returnCall("ok", "child done"), 30, 3))},
	}
	e, db, id := setupEngine(t, p, "")
	var mu sync.Mutex
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { mu.Lock(); sunk = append(sunk, rec); mu.Unlock() }
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.InputTokens != 330 || result.Usage.OutputTokens != 33 {
		t.Fatalf("run result usage %+v, want the rolled-up 330/33", result.Usage)
	}
	child := childOf(t, db, id)
	mu.Lock()
	defer mu.Unlock()
	for _, rec := range sunk {
		switch rec.ConversationID {
		case id:
			if rec.Usage.InputTokens != 300 || rec.Usage.OutputTokens != 30 {
				t.Fatalf("root record usage %+v, want its own 300/30", rec.Usage)
			}
			if len(rec.Subtasks) != 1 || rec.Subtasks[0].Usage.InputTokens != 30 || rec.Subtasks[0].Usage.OutputTokens != 3 {
				t.Fatalf("root subtasks %+v", rec.Subtasks)
			}
		case child.ID:
			if rec.Usage.InputTokens != 30 || rec.Usage.OutputTokens != 3 {
				t.Fatalf("child record usage %+v", rec.Usage)
			}
		}
	}
}

// A turn that fails after a subtask rolled its usage up (a panic, say) has
// no complete result, but its record still shows its own requests, never a
// negative or child-inflated count.
func TestTraceUsageSurvivesAnIncompleteResult(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("unused")), "")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	run := &Run{events: make(chan events.Event, 8), done: make(chan struct{})}
	x := &execution{engine: e, run: run}
	x.ownUsage = events.Usage{InputTokens: 10, OutputTokens: 1}
	x.result.Usage = x.ownUsage
	x.rollUp(t.Context(), "child", events.Usage{InputTokens: 30, OutputTokens: 3})
	rec := newTraceRecord(time.Now().UTC(), "r1", store.Conversation{ID: id}, false, nil)
	// The panic path hands writeTrace an empty result.
	e.writeTrace(t.Context(), rec, x, events.Result{}, errors.New("turn panicked"))
	if len(sunk) != 1 || sunk[0].Usage.InputTokens != 10 || sunk[0].Usage.OutputTokens != 1 {
		t.Fatalf("records %+v, want own usage 10/1", sunk)
	}
}

// A depth-2 subtask's lifecycle reaches the root's stream through its lead,
// but the runtime sink, which already saw it on the lead's run, counts it
// once.
func TestGrandchildTelemetryCountsOnce(t *testing.T) {
	p := &routedProvider{
		root: fake.New(subtaskCall("lead", "run a council", false), fake.Text("root done")),
		children: map[string]*fake.Provider{
			"run a council": fake.New(subtaskCall("worker", "one view", false), returnCall("ok", "council")),
			"one view":      fake.New(toolResponse("lookup"), returnCall("ok", "view")),
		},
	}
	e, db, id := setupEngine(t, p, "")
	lookupTool(t, e)
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead a council.\n")
	var mu sync.Mutex
	kinds := map[string]map[runtimeevents.EventKind]int{}
	e.runtime = events.NewRuntime(runtimeevents.SinkFunc(func(_ context.Context, ev runtimeevents.Event) error {
		var payload struct {
			ChildID string `json:"child_id"`
		}
		if json.Unmarshal(ev.Payload, &payload) != nil || payload.ChildID == "" {
			return nil
		}
		child := payload.ChildID
		mu.Lock()
		defer mu.Unlock()
		if kinds[child] == nil {
			kinds[child] = map[runtimeevents.EventKind]int{}
		}
		kinds[child][ev.Kind]++
		return nil
	}), e.cfg.Logger)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	lead := childOf(t, db, id)
	worker := childOf(t, db, lead.ID)
	mu.Lock()
	defer mu.Unlock()
	for _, child := range []string{lead.ID, worker.ID} {
		got := kinds[child]
		if got[runtimeevents.KindAgentSubagentSpawn] != 1 || got[runtimeevents.KindAgentSubagentStop] != 1 {
			t.Fatalf("runtime events for %s: %v, want one spawn and one stop", child, got)
		}
	}
}

// With no interval nothing is held, so follow never wakes on a timer, however
// many changes arrive.
func TestFollowWithNoIntervalNeverWakes(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		var wakes atomic.Int64
		followWakeHook = func() { wakes.Add(1) }
		run := &Run{events: make(chan events.Event, 32), done: make(chan struct{})}
		go func() {
			for i := range 10 {
				run.events <- events.ToolCallStarted{CallID: "c", Name: fmt.Sprintf("tool%d", i)}
				time.Sleep(10 * time.Millisecond)
			}
			close(run.events)
			close(run.done)
		}()
		sent := 0
		emit := func(_ context.Context, event func(events.EventMeta) events.Event) { sent++ }
		if _, err := follow(t.Context(), run, followed{ID: "a", Variant: "worker", Depth: 1}, interval, emit, emit); err != nil {
			t.Fatal(err)
		}
		followWakeHook = nil
		if wakes.Load() != 0 || sent != 11 {
			t.Fatalf("interval %v: %d timer wakes, %d sent; want 0 and 11", interval, wakes.Load(), sent)
		}
	}
}

// Foreground progress is sent at most every 500ms, background progress at
// most every 5s.
func TestProgressIntervals(t *testing.T) {
	e, _, _ := setupEngine(t, fake.New(fake.Text("unused")), "")
	if e.progressEvery != 500*time.Millisecond || e.backgroundProgressEvery != 5*time.Second {
		t.Fatalf("intervals %v / %v, want 500ms / 5s", e.progressEvery, e.backgroundProgressEvery)
	}
}

// bgFinished reports whether conversation's background subtask job has
// finished in the store.
func bgFinished(ctx context.Context, e *Engine, conversation string) bool {
	conversations, err := e.cfg.Store.Conversations(ctx)
	if err != nil {
		return false
	}
	for _, c := range conversations {
		if c.ParentID != conversation {
			continue
		}
		job, err := e.cfg.Store.Job(ctx, conversation, subtaskJobKind, c.ID)
		return err == nil && job.Status != "" && job.Status != "running"
	}
	return false
}

// A foreground subtask that has to wait for a slot says so, under the ID it
// then starts with.
func TestSubtaskWaitingForASlotSaysSo(t *testing.T) {
	// Whichever subtask gets the slot holds it in a tool until the other has
	// said it is waiting.
	seen := make(chan struct{})
	var once sync.Once
	children := map[string]*fake.Provider{
		"one": fake.New(toolResponse("hold_slot"), returnCall("ok", "one")),
		"two": fake.New(toolResponse("hold_slot"), returnCall("ok", "two")),
	}
	p := &routedProvider{root: fake.New(fanOut("worker", "one", "two"), fake.Text("parent done")), children: children}
	e, _, id := setupEngine(t, p, "")
	e.cfg.Gate = permission.AllowGate
	first := true
	var firstMu sync.Mutex
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "hold_slot", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			firstMu.Lock()
			holder := first
			first = false
			firstMu.Unlock()
			if holder {
				select {
				case <-seen:
				case <-ctx.Done():
				case <-time.After(10 * time.Second):
				}
			}
			return tools.Result{Text: "held"}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	e.children = subagent.NewLimit(1)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	waited := map[string]int{}
	started := map[string]int{}
	i := 0
	for ev := range run.Events() {
		i++
		switch x := ev.(type) {
		case events.SubtaskProgress:
			if x.StatusLine == "waiting for a subtask slot" {
				if x.ParentID != id || x.Depth != 1 || x.Variant != "worker" || x.ID == "" {
					t.Errorf("waiting progress %+v", x)
				}
				waited[x.ID] = i
				once.Do(func() { close(seen) })
			}
		case events.SubagentStarted:
			started[x.ID] = i
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(waited) != 1 {
		t.Fatalf("waiting events %v, want exactly one (limit 1, two subtasks)", waited)
	}
	for child, at := range waited {
		if s, ok := started[child]; !ok || s < at {
			t.Fatalf("the waiting subtask %s started at %d (waited at %d); starts %v", child, s, at, started)
		}
	}
}
