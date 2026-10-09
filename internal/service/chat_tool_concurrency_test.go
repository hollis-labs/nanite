package service

// CW-20260929-0012 #4: a multi-tool-call turn runs its concurrent-safe calls
// on a bounded number of goroutines, not one per call.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/agent/tooluse"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/store"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// peakGauge tracks how many callers are inside enter/leave at once.
type peakGauge struct{ cur, peak atomic.Int64 }

func (g *peakGauge) enter() {
	n := g.cur.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			return
		}
	}
}
func (g *peakGauge) leave() { g.cur.Add(-1) }

func TestRunBounded_PeakNeverExceedsLimit(t *testing.T) {
	for _, tc := range []struct{ n, limit int }{{40, 3}, {40, 1}, {5, 8}, {8, 8}, {1, 4}} {
		t.Run(fmt.Sprintf("n%d_limit%d", tc.n, tc.limit), func(t *testing.T) {
			var g peakGauge
			var ran atomic.Int64
			tooluse.RunBounded(context.Background(), tc.n, tc.limit,
				func(int) {
					g.enter()
					defer g.leave()
					ran.Add(1)
					time.Sleep(2 * time.Millisecond)
				},
				func(int) { t.Error("nothing should be skipped without cancellation") })
			if int(ran.Load()) != tc.n {
				t.Errorf("ran %d items, want %d", ran.Load(), tc.n)
			}
			want := int64(min(tc.limit, tc.n))
			if p := g.peak.Load(); p > want {
				t.Errorf("peak concurrency %d exceeds limit %d", p, want)
			}
		})
	}
}

// Slots hold results by index, so which worker ran an item cannot reorder them.
func TestRunBounded_ResultsKeepIndexOrder(t *testing.T) {
	const n = 50
	got := make([]int, n)
	tooluse.RunBounded(context.Background(), n, 4, func(i int) {
		time.Sleep(time.Duration(n-i) * 50 * time.Microsecond) // later items finish first
		got[i] = i * 2
	}, func(int) {})
	for i, v := range got {
		if v != i*2 {
			t.Fatalf("slot %d = %d, want %d", i, v, i*2)
		}
	}
}

// A panic in one item is confined to it: the rest still run, and the call returns.
func TestRunBounded_PanicConfinedToItem(t *testing.T) {
	var ran atomic.Int64
	tooluse.RunBounded(context.Background(), 10, 2, func(i int) {
		if i == 3 {
			panic("boom")
		}
		ran.Add(1)
	}, func(int) {})
	if ran.Load() != 9 {
		t.Errorf("ran %d of the 9 non-panicking items", ran.Load())
	}
}

// Cancel with work queued: running items finish, queued items are skipped
// (each exactly once), every index is accounted for, and the call returns
// having waited for the running ones.
func TestRunBounded_CancellationDrainsCleanly(t *testing.T) {
	const n, limit = 30, 3
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var g peakGauge
	var startedCount atomic.Int64
	started := make(chan struct{}, n)
	release := make(chan struct{})
	seen := make([]atomic.Int64, n) // times each index was run or skipped
	var running atomic.Int64

	done := make(chan struct{})
	go func() {
		defer close(done)
		tooluse.RunBounded(ctx, n, limit,
			func(i int) {
				g.enter()
				defer g.leave()
				seen[i].Add(1)
				startedCount.Add(1)
				running.Add(1)
				started <- struct{}{}
				<-release
				running.Add(-1)
			},
			func(i int) { seen[i].Add(1) })
	}()

	for i := 0; i < limit; i++ { // the first `limit` items are now running, the rest queued
		<-started
	}
	cancel()
	select {
	case <-done:
		t.Fatal("runBounded returned while items were still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runBounded did not drain after cancellation")
	}

	if running.Load() != 0 {
		t.Errorf("%d items still running after return", running.Load())
	}
	if startedCount.Load() != limit {
		t.Errorf("%d items started, want %d (the rest were queued at cancel)", startedCount.Load(), limit)
	}
	for i := range seen {
		if c := seen[i].Load(); c != 1 {
			t.Errorf("index %d handled %d times, want exactly 1", i, c)
		}
	}
	if p := g.peak.Load(); p > limit {
		t.Errorf("peak concurrency %d exceeds limit %d", p, limit)
	}
}

// countingToolService is a ToolService whose Execute records concurrency.
type countingToolService struct {
	ToolService
	gauge   peakGauge
	hold    time.Duration
	entered chan struct{}
	release chan struct{}
}

func (c *countingToolService) Execute(_ context.Context, _, toolName string, _ map[string]any) (*ToolResult, error) {
	c.gauge.enter()
	defer c.gauge.leave()
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.release != nil {
		<-c.release
	} else {
		time.Sleep(c.hold)
	}
	return &ToolResult{Output: "ok:" + toolName}, nil
}

func concurrentPlans(n int) []toolPlan {
	plans := make([]toolPlan, n)
	for i := range plans {
		plans[i] = toolPlan{
			tu:         llmtypes.ToolUseBlock{ID: fmt.Sprintf("tu-%d", i), Name: fmt.Sprintf("tool_%d", i), Input: map[string]any{}},
			status:     toolPlanReady,
			concurrent: true,
		}
	}
	return plans
}

func drainBatchStream(t *testing.T) (chan chat.StreamEvent, func()) {
	t.Helper()
	ch := make(chan chat.StreamEvent, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
		}
	}()
	return ch, func() { close(ch); <-done }
}

// Through the real executeToolBatch path: 20 concurrent-safe calls with a
// limit of 4 never have more than 4 inside ToolService.Execute, every call
// gets its result, and results stay in plan order.
func TestExecuteToolBatch_ConcurrencyCapHolds(t *testing.T) {
	tools := &countingToolService{hold: 5 * time.Millisecond}
	svc := &chatServiceImpl{streams: NewStreamManager(), tools: tools, store: &e2eStore{}, maxConcurrentTools: 4}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch, stop := drainBatchStream(t)
	defer stop()

	plans := concurrentPlans(20)
	results := svc.executeToolBatch(context.Background(), plans, ls, "agent", ch, "sess")

	if p := tools.gauge.peak.Load(); p > 4 {
		t.Errorf("peak concurrent Execute calls = %d, want <= 4", p)
	}
	if p := tools.gauge.peak.Load(); p < 2 {
		t.Errorf("peak concurrent Execute calls = %d; the cap must not serialize the batch", p)
	}
	for i, r := range results {
		if r.isError || r.rawOutput != "ok:"+plans[i].tu.Name {
			t.Errorf("result %d = %+v, want output for %s", i, r, plans[i].tu.Name)
		}
		if r.resultBlock.ToolUseID != plans[i].tu.ID {
			t.Errorf("result %d answers %q, want %q", i, r.resultBlock.ToolUseID, plans[i].tu.ID)
		}
	}
}

// Cancel a batch with calls queued behind the cap: the running calls finish,
// each queued call gets a canceled tool_result for its own tool_use_id, and
// executeToolBatch returns.
func TestExecuteToolBatch_CancellationFillsQueuedResults(t *testing.T) {
	const n, limit = 12, 3
	tools := &countingToolService{entered: make(chan struct{}, n), release: make(chan struct{})}
	svc := &chatServiceImpl{streams: NewStreamManager(), tools: tools, store: &e2eStore{}, maxConcurrentTools: limit}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch, stop := drainBatchStream(t)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plans := concurrentPlans(n)
	var results []toolExecResult
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		results = svc.executeToolBatch(ctx, plans, ls, "agent", ch, "sess")
	}()

	for i := 0; i < limit; i++ {
		<-tools.entered
	}
	cancel()
	close(tools.release)
	waitDone := make(chan struct{})
	go func() { wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("executeToolBatch did not return after cancellation")
	}

	if p := tools.gauge.peak.Load(); p > limit {
		t.Errorf("peak concurrent Execute calls = %d, want <= %d", p, limit)
	}
	var ok, canceled int
	for i, r := range results {
		if r.resultBlock.ToolUseID != plans[i].tu.ID {
			t.Errorf("result %d answers %q, want %q — every tool_use needs its own tool_result", i, r.resultBlock.ToolUseID, plans[i].tu.ID)
		}
		switch {
		case r.ref.Status == "canceled" && r.isError:
			canceled++
		case !r.isError:
			ok++
		default:
			t.Errorf("result %d unexpected: %+v", i, r)
		}
	}
	if ok != limit || canceled != n-limit {
		t.Errorf("ok=%d canceled=%d, want %d and %d", ok, canceled, limit, n-limit)
	}
}

// The bound comes from the run's harness profile; the service field overrides
// it for tests, and a run with no resolved profile gets the computed default.
func TestToolConcurrencyLimit_Sources(t *testing.T) {
	s := &chatServiceImpl{}
	if got := s.toolConcurrencyLimit(nil); got != harnessprofile.DefaultMaxConcurrentTools {
		t.Errorf("no loop state: %d, want the default %d", got, harnessprofile.DefaultMaxConcurrentTools)
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if got := s.toolConcurrencyLimit(ls); got != harnessprofile.DefaultMaxConcurrentTools {
		t.Errorf("no profile: %d, want the default", got)
	}

	res, err := harnessSvc(t).resolveHarness(context.Background(), &store.Session{}, chat.AgentConstraints{}, "")
	if err != nil {
		t.Fatal(err)
	}
	three := 3
	res.Values.MaxConcurrentTools = three
	applyHarness(ls, res, false)
	if got := s.toolConcurrencyLimit(ls); got != 3 {
		t.Errorf("profile value: %d, want 3", got)
	}
	s.maxConcurrentTools = 5
	if got := s.toolConcurrencyLimit(ls); got != 5 {
		t.Errorf("service override: %d, want 5", got)
	}
}

// The real path: a session whose launch override says 2 never has more than 2
// calls inside ToolService.Execute.
func TestExecuteToolBatch_HonorsProfileLimit(t *testing.T) {
	res, err := harnessSvc(t).resolveHarness(context.Background(), &store.Session{}, chat.AgentConstraints{}, "")
	if err != nil {
		t.Fatal(err)
	}
	res.Values.MaxConcurrentTools = 2
	tools := &countingToolService{hold: 5 * time.Millisecond}
	svc := &chatServiceImpl{streams: NewStreamManager(), tools: tools, store: &e2eStore{}}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	applyHarness(ls, res, false)
	ch, stop := drainBatchStream(t)
	defer stop()

	svc.executeToolBatch(context.Background(), concurrentPlans(12), ls, "agent", ch, "sess")
	if p := tools.gauge.peak.Load(); p > 2 {
		t.Errorf("peak = %d, want <= 2 (the profile's max_concurrent_tools)", p)
	}
}
