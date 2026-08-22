package service

import (
	"context"
	"strings"
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
