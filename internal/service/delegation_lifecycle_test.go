package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/worker"
)

type workerSpawnerFunc func(context.Context, worker.SpawnRequest) (*worker.Result, error)

func (f workerSpawnerFunc) SpawnFull(ctx context.Context, req worker.SpawnRequest) (*worker.Result, error) {
	return f(ctx, req)
}

func TestDelegateSubTasks_PanicBeforeSendDoesNotHangCollector(t *testing.T) {
	owner := lifecycle.NewManager("delegation-test")
	t.Cleanup(func() { _ = owner.Shutdown(time.Second) })

	done := make(chan struct{})
	var results []chat.SubTaskResult
	var err error
	go func() {
		results, err = delegateSubTasks(context.Background(), owner,
			workerSpawnerFunc(func(context.Context, worker.SpawnRequest) (*worker.Result, error) {
				panic("spawn exploded")
			}),
			[]chat.SubTask{{Title: "panic path", Description: "reproduce pre-send panic"}},
			"parent", "model")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector hung after worker panic before result send")
	}
	if err != nil {
		t.Fatalf("delegateSubTasks: %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Error, "worker panicked: spawn exploded") {
		t.Fatalf("results = %#v, want synthetic panic result", results)
	}
}

func TestDelegateSubTasks_AllWorkersSucceedInOriginalOrder(t *testing.T) {
	owner := lifecycle.NewManager("delegation-happy-test")
	t.Cleanup(func() { _ = owner.Shutdown(time.Second) })

	results, err := delegateSubTasks(context.Background(), owner,
		workerSpawnerFunc(func(_ context.Context, req worker.SpawnRequest) (*worker.Result, error) {
			if req.Title == "first" {
				time.Sleep(20 * time.Millisecond)
			}
			return &worker.Result{Content: "output-" + req.Title, Success: true}, nil
		}),
		[]chat.SubTask{{Title: "first"}, {Title: "second"}}, "parent", "model")
	if err != nil {
		t.Fatalf("delegateSubTasks: %v", err)
	}
	if len(results) != 2 || results[0].Output != "output-first" || results[1].Output != "output-second" {
		t.Fatalf("results = %#v, want successful input order", results)
	}
}

func TestDelegateSubTasks_NilOwnerExecutesInline(t *testing.T) {
	results, err := delegateSubTasks(context.Background(), nil,
		workerSpawnerFunc(func(context.Context, worker.SpawnRequest) (*worker.Result, error) {
			panic("bare owner panic")
		}),
		[]chat.SubTask{{Title: "bare owner"}}, "parent", "model")
	if err != nil {
		t.Fatalf("delegateSubTasks: %v", err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Error, "worker panicked: bare owner panic") {
		t.Fatalf("results = %#v, want inline synthetic panic result", results)
	}
}

func TestDelegateSubTasks_NilOwnerPreCanceledStartsNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32

	results, err := delegateSubTasks(ctx, nil,
		workerSpawnerFunc(func(context.Context, worker.SpawnRequest) (*worker.Result, error) {
			calls.Add(1)
			return &worker.Result{Success: true}, nil
		}),
		[]chat.SubTask{{Title: "first"}, {Title: "second"}}, "parent", "model")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if results != nil {
		t.Fatalf("results = %#v, want nil on cancellation", results)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("worker calls = %d, want 0 for pre-canceled context", got)
	}
}

func TestDelegateSubTasks_NilOwnerStopsBeforeLaterWorkOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	laterInvoked := make(chan struct{})
	releaseLater := make(chan struct{})
	var laterCalls atomic.Int32
	t.Cleanup(func() { close(releaseLater) })

	done := make(chan struct{})
	var results []chat.SubTaskResult
	var err error
	go func() {
		results, err = delegateSubTasks(ctx, nil,
			workerSpawnerFunc(func(_ context.Context, req worker.SpawnRequest) (*worker.Result, error) {
				if req.Title == "first" {
					cancel()
					return &worker.Result{Content: "first-output", Success: true}, nil
				}
				// Deliberately ignore cancellation. The inline fallback must never
				// invoke this later task after the first task cancels the caller.
				if laterCalls.Add(1) == 1 {
					close(laterInvoked)
				}
				<-releaseLater
				return &worker.Result{Content: "late-output", Success: true}, nil
			}),
			[]chat.SubTask{{Title: "first"}, {Title: "must-not-start"}, {Title: "also-must-not-start"}},
			"parent", "model")
		close(done)
	}()

	select {
	case <-done:
	case <-laterInvoked:
		t.Fatal("a later cancellation-ignoring worker was invoked")
	case <-time.After(time.Second):
		t.Fatal("inline delegation did not return after mid-sequence cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if results != nil {
		t.Fatalf("results = %#v, want nil on cancellation", results)
	}
	select {
	case <-laterInvoked:
		t.Fatal("a later worker was invoked after cancellation")
	default:
	}
}

func TestDelegateSubTasks_NilOwnerSuccessPreservesOrder(t *testing.T) {
	results, err := delegateSubTasks(context.Background(), nil,
		workerSpawnerFunc(func(_ context.Context, req worker.SpawnRequest) (*worker.Result, error) {
			return &worker.Result{Content: "output-" + req.Title, Success: true}, nil
		}),
		[]chat.SubTask{{Title: "first"}, {Title: "second"}, {Title: "third"}},
		"parent", "model")
	if err != nil {
		t.Fatalf("delegateSubTasks: %v", err)
	}
	if len(results) != 3 ||
		results[0].Output != "output-first" ||
		results[1].Output != "output-second" ||
		results[2].Output != "output-third" {
		t.Fatalf("results = %#v, want successful input order", results)
	}
}
