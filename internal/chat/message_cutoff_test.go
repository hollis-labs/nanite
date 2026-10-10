package chat

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestWorkingHistoryCutoffExactIDsAndCompletedOutputs(t *testing.T) {
	rows := []store.Message{
		{ID: "first", SessionID: "view", Role: "user", Content: "same"},
		{ID: "second", SessionID: "view", Role: "user", Content: "same"},
		{ID: "current", SessionID: "view", Role: "user", Content: "same"},
		{ID: "future", SessionID: "view", Role: "user", Content: "same"},
		{ID: "first-out", SessionID: "view", Role: "assistant", Content: "first answer"},
		{ID: "second-out", SessionID: "view", Role: "assistant", Content: "second answer"},
	}
	pairs := []WorkingHistoryTurn{{"second", "second-out"}, {"first", "first-out"}}
	ctx := WithWorkingHistoryPredecessors(WithWorkingHistoryThrough(t.Context(), "current"), pairs)
	pairs[0].OutputMessageID = "future" // the queued marker owns its copy
	got, err := workingHistoryThrough(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range got {
		ids = append(ids, row.ID)
	}
	if !reflect.DeepEqual(ids, []string{"first", "first-out", "second", "second-out", "current"}) {
		t.Fatal(ids)
	}
	for _, mutate := range []func([]store.Message) []store.Message{
		func(r []store.Message) []store.Message { return append(r[:2], r[3:]...) },
		func(r []store.Message) []store.Message { return r[:4] },
		func(r []store.Message) []store.Message { r[4].SessionID = "foreign"; return r },
		func(r []store.Message) []store.Message { r[0].IsCompacted = true; return r },
		func(r []store.Message) []store.Message { r[2].IsCompacted = true; return r },
		func(r []store.Message) []store.Message { return r[1:] },
	} {
		if data, refusal := workingHistoryThrough(ctx, mutate(append([]store.Message(nil), rows...))); !errors.Is(refusal, ErrWorkingHistoryBoundary) || data != nil {
			t.Fatalf("missing/foreign/cleared boundary admitted: %+v %v", data, refusal)
		}
	}
	plain, err := workingHistoryThrough(t.Context(), rows)
	if err != nil || !reflect.DeepEqual(plain, rows) {
		t.Fatal("unmarked history changed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = workingHistoryThrough(canceled, rows); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if HasWorkingHistoryThrough(CopyWorkingHistoryThrough(context.Background(), ctx)) {
		t.Fatal("successor inherited cutoff")
	}
}
