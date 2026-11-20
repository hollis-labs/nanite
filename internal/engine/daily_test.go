package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

func TestDailyBudgetUsageRestartAndRollover(t *testing.T) {
	response := fake.Text("done")
	response.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 40, OutputTokens: 20, CacheReadTokens: 10, CacheCreationTokens: 10}}}, response.Events...)
	e, _, id := setupEngine(t, fake.New(response, response), "")
	e.cfg.DailyTokenBudget = 100
	now := time.Date(2026, 1, 2, 23, 0, 0, 0, time.UTC)
	e.daily.now = func() time.Time { return now }
	notices := e.SubscribeNotices(t.Context())
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notices:
		if n.Kind != "budget" {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("missing budget notice")
	}
	if e.daily.state.Tokens != 80 {
		t.Fatalf("tokens=%d", e.daily.state.Tokens)
	}
	// Re-read persisted state as a restarted engine does.
	e.daily.state = dailyState{}
	if err := e.loadDaily(); err != nil {
		t.Fatal(err)
	}
	e.daily.now = func() time.Time { return now }
	if !e.daily.state.Warned || e.daily.state.Tokens != 80 {
		t.Fatalf("restored %+v", e.daily.state)
	}
	run, err = e.Send(t.Context(), id, Input{Text: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Send(t.Context(), id, Input{Text: "refuse"}); !errors.Is(err, ErrDailyBudget) {
		t.Fatalf("cap: %v", err)
	}
	select {
	case n := <-notices:
		t.Fatalf("duplicate notice %+v", n)
	default:
	}
	now = now.Add(24 * time.Hour)
	if err := e.checkDaily(); err != nil {
		t.Fatal(err)
	}
	if err := e.spendDaily(t.Context(), id, 1); err != nil {
		t.Fatal(err)
	}
	if e.daily.state.Tokens != 1 || e.daily.state.Warned {
		t.Fatalf("rollover %+v", e.daily.state)
	}
}

func TestDailyBudgetConcurrentSpend(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	e.cfg.DailyTokenBudget = 100
	done := make(chan error, 10)
	for range 10 {
		go func() { done <- e.spendDaily(t.Context(), id, 10) }()
	}
	for range 10 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if e.daily.state.Tokens != 100 {
		t.Fatalf("tokens=%d", e.daily.state.Tokens)
	}
	if !errors.Is(e.checkDaily(), ErrDailyBudget) {
		t.Fatal("cap not enforced")
	}
}

func TestStopAllLatchesAdmission(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	// Register a turn and a background task which can finish only on cancellation.
	e.mu.Lock()
	run, ctx := e.newRunLocked(context.Background(), id, "running")
	e.mu.Unlock()
	go func() {
		<-ctx.Done()
		e.release(id, run, func() { run.finish(events.Result{}, ctx.Err(), events.RunFailed{Err: ctx.Err()}) })
	}()
	job, err := e.jobs.Start(t.Context(), store.Job{ID: store.NewID(time.Now()), ConversationID: id, Kind: "shell"}, waitingJob)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.StopAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Send(t.Context(), id, Input{Text: "new"}); !errors.Is(err, ErrStopped) {
		t.Fatalf("new work: %v", err)
	}
	if e.jobs.Cancel(job.ID) {
		t.Fatal("job still active")
	}
	if err := e.StopAll(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func waitingJob(ctx context.Context, _ io.Writer) (background.Task, error) {
	return background.Task{Wait: func() background.Outcome { <-ctx.Done(); return background.Outcome{Err: ctx.Err()} }}, nil
}

func TestDailyBudgetCountsSubtasksOnce(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprint(background), func(t *testing.T) {
			child := returnCall("ok", "child")
			child.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 30, OutputTokens: 3}}}, child.Events...)
			p := &routedProvider{root: fake.New(subtaskCall("worker", "child work", background), fake.Text("done")), children: map[string]*fake.Provider{"child work": fake.New(child)}}
			e, _, id := setupEngine(t, p, "")
			e.cfg.DailyTokenBudget = 100
			notices := e.SubscribeNotices(t.Context())
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			if background {
				select {
				case <-notices:
				case <-time.After(3 * time.Second):
					t.Fatal("no child completion")
				}
			}
			e.daily.mu.Lock()
			tokens := e.daily.state.Tokens
			e.daily.mu.Unlock()
			if tokens != 33 {
				t.Fatalf("counted %d tokens, want child usage once", tokens)
			}
		})
	}
}

func TestStopAllCancelsRealSubtasks(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprint(background), func(t *testing.T) {
			p := &routedProvider{root: fake.New(subtaskCall("worker", "child work", background), fake.Response{Stall: true}), children: map[string]*fake.Provider{"child work": fake.New(fake.Response{Stall: true})}}
			e, _, id := setupEngine(t, p, "")
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			started := false
			for event := range run.Events() {
				if _, ok := event.(events.SubagentStarted); ok {
					started = true
					break
				}
			}
			if !started {
				t.Fatal("child did not start")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err := e.StopAll(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := run.Wait(); err == nil {
				t.Fatal("running parent not cancelled")
			}
			e.mu.Lock()
			active := len(e.active)
			e.mu.Unlock()
			if active != 0 {
				t.Fatalf("%d runs still active", active)
			}
		})
	}
}

func TestDailyBudgetStopsNextModelStep(t *testing.T) {
	response := toolResponse("unknown")
	response.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 100}}}, response.Events...)
	e, _, id := setupEngine(t, fake.New(response), "")
	e.cfg.DailyTokenBudget = 100
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); !errors.Is(err, ErrDailyBudget) {
		t.Fatalf("next model step: %v", err)
	}
}

func TestDailyBudgetRejectsDispatchAndMalformedState(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	e.cfg.DailyTokenBudget = 100
	if err := e.spendDaily(t.Context(), id, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(t.Context(), id, []subagent.Request{{Goal: "child", Variant: "worker", ReturnSpec: json.RawMessage(`"a note"`)}}); !errors.Is(err, ErrDailyBudget) {
		t.Fatalf("dispatch cap: %v", err)
	}
	if err := e.cfg.Store.SetSetting(t.Context(), "", dailyBudgetKey, `{"day":"bad","tokens":1}`); err != nil {
		t.Fatal(err)
	}
	if err := e.loadDaily(); err == nil {
		t.Fatal("malformed state accepted")
	}
}

func TestSummaryBudgetPersistenceErrorPropagates(t *testing.T) {
	want := errors.New("budget persistence failed")
	span := []llmtypes.ChatMessage{{Role: "user", Content: "remember this"}}
	summary := &capturingSummarizer{Summarizer: ctxpkg.NewProviderSummarizer(fake.New(fake.Text("summary")), "fixture"), messages: span, messageIDs: []string{"message"}, spend: func(string, []llmtypes.ChatMessage, string) error { return want }}
	if _, err := summary.Summarize(t.Context(), "summarize", span); !errors.Is(err, want) {
		t.Fatalf("persistence failure lost: %v", err)
	}
}
