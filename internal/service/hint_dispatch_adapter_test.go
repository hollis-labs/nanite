package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatch"
)

// fakeSpawner is a dispatch.Spawner test double that records the
// SpawnRequest and returns a programmable result.
type fakeSpawner struct {
	last dispatch.SpawnRequest
	res  *dispatch.SpawnResult
	err  error
}

func (f *fakeSpawner) Spawn(_ context.Context, req dispatch.SpawnRequest) (*dispatch.SpawnResult, error) {
	f.last = req
	return f.res, f.err
}

func TestHintDispatchAdapter_NilSpawnerReturnsNil(t *testing.T) {
	t.Parallel()
	if NewHintDispatchAdapter(nil) != nil {
		t.Fatal("expected nil adapter when spawner is nil")
	}
}

func TestHintDispatchAdapter_DispatchPassesPayloadAndSlug(t *testing.T) {
	t.Parallel()
	sp := &fakeSpawner{
		res: &dispatch.SpawnResult{Summary: `["scratchpad","memory_recall"]`},
	}
	d := NewHintDispatchAdapter(sp)
	got, err := d.Dispatch(context.Background(), `{"user_input":"hi"}`)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got != `["scratchpad","memory_recall"]` {
		t.Fatalf("got %q; want raw summary", got)
	}
	if sp.last.Role != chat.HintSelectorSlug {
		t.Fatalf("Role = %q; want %q", sp.last.Role, chat.HintSelectorSlug)
	}
	if sp.last.Prompt != `{"user_input":"hi"}` {
		t.Fatalf("Prompt = %q; want payload echoed", sp.last.Prompt)
	}
	if sp.last.Mode != "sync" {
		t.Fatalf("Mode = %q; want sync", sp.last.Mode)
	}
	if sp.last.ParentSessionID == "" {
		t.Fatalf("ParentSessionID is empty; subagent.Spawn requires non-empty")
	}
	if sp.last.ParentAgentID == "" {
		t.Fatalf("ParentAgentID is empty; required for spawn")
	}
}

func TestHintDispatchAdapter_DispatchPropagatesError(t *testing.T) {
	t.Parallel()
	sp := &fakeSpawner{err: errors.New("spawn boom")}
	d := NewHintDispatchAdapter(sp)
	_, err := d.Dispatch(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "spawn boom") {
		t.Fatalf("err = %v; want substring spawn boom", err)
	}
}

func TestHintDispatchAdapter_DispatchNilResultErrors(t *testing.T) {
	t.Parallel()
	sp := &fakeSpawner{} // res nil, err nil
	d := NewHintDispatchAdapter(sp)
	_, err := d.Dispatch(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error on nil result")
	}
}
