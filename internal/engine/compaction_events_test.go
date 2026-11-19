package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
)

type failingSummaries struct{ *watchedProvider }

func (failingSummaries) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "", errors.New("summary unavailable")
}

// seedLongHistory stores a finished turn large enough to be worth compacting.
func seedLongHistory(t *testing.T, db *store.Store, id string) {
	t.Helper()
	j, err := db.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seed := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := j.StartTurn(seed, now); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		msg := store.Message{ID: store.NewID(now), TurnID: seed.ID, Role: role, CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: strings.Repeat("older words ", 100)}}}
		if err := j.AppendMessage(msg, nil); err != nil {
			t.Fatal(err)
		}
	}
	seed.Status = "completed"
	if err := j.FinishTurn(seed, now); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

func compactionEvents(t *testing.T, run *Run) []string {
	t.Helper()
	var seen []string
	for event := range run.Events() {
		switch e := event.(type) {
		case events.CompactionStarted:
			seen = append(seen, "started")
		case events.Compacted:
			seen = append(seen, fmt.Sprintf("done shrank=%t", e.After < e.Before))
		case events.CompactionFailed:
			seen = append(seen, "failed: "+e.Err.Error())
		}
	}
	return seen
}

func overflowThen(responses ...fake.Response) *watchedProvider {
	overflow := fake.Response{Err: &failure.Error{Class: failure.Overflow, Cause: errors.New("typed overflow")}}
	return &watchedProvider{Provider: fake.New(append([]fake.Response{overflow}, responses...)...)}
}

func TestCompactionReportsStartAndDone(t *testing.T) {
	e, db, id := setupEngine(t, overflowThen(fake.Text("after compaction")), "")
	seedLongHistory(t, db, id)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(compactionEvents(t, run), "; "); got != "started; done shrank=true" {
		t.Fatalf("compaction events: %q", got)
	}
}

func TestCompactionReportsFailure(t *testing.T) {
	e, db, id := setupEngine(t, failingSummaries{overflowThen(fake.Text("unused"))}, "")
	seedLongHistory(t, db, id)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := compactionEvents(t, run)
	if len(got) != 2 || got[0] != "started" || !strings.HasPrefix(got[1], "failed: ") || !strings.Contains(got[1], "summary unavailable") {
		t.Fatalf("compaction events: %q", got)
	}
}

func TestCompactionWithoutProgressIsNotRetried(t *testing.T) {
	// A window this small always needs compaction, and a one-turn history
	// has nothing a summary can replace, so no pass frees anything.
	p := &watchedProvider{Provider: fake.New(toolResponse("unknown"), toolResponse("unknown"), toolResponse("unknown"), fake.Text("done"))}
	e, _, id := setupEngine(t, p, "runtime:\n  context_window: 200\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := compactionEvents(t, run)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "started" || !strings.HasPrefix(got[1], "failed: ") {
		t.Fatalf("compaction events: %q", got)
	}
	if p.summaries > 1 {
		t.Fatalf("summarizer called %d times", p.summaries)
	}
}

type emptySummaries struct{ *watchedProvider }

func (emptySummaries) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "  ", nil
}

func TestEmptySummaryIsNotACompaction(t *testing.T) {
	e, db, id := setupEngine(t, emptySummaries{overflowThen(fake.Text("unused"))}, "")
	seedLongHistory(t, db, id)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := compactionEvents(t, run)
	if len(got) != 2 || got[0] != "started" || !strings.Contains(got[1], "summary is empty") {
		t.Fatalf("compaction events: %q", got)
	}
	if summary, _, _, err := db.ModelHistory(t.Context(), id); err != nil || summary != "" {
		t.Fatalf("recorded summary %q, %v", summary, err)
	}
}

func TestStepCompactionFailureDoesNotEndTurn(t *testing.T) {
	// The window needs compaction after the tool step; the summarizer fails.
	p := failingSummaries{&watchedProvider{Provider: fake.New(toolResponse("unknown"), fake.Text("finished anyway"))}}
	e, db, id := setupEngine(t, p, "runtime:\n  context_window: 2000\n")
	seedLongHistory(t, db, id)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	got := compactionEvents(t, run)
	result, err := run.Wait()
	if err != nil || !strings.Contains(result.Text, "finished anyway") {
		t.Fatalf("result %+v, %v", result, err)
	}
	if len(got) != 2 || got[0] != "started" || !strings.Contains(got[1], "summary unavailable") {
		t.Fatalf("compaction events: %q", got)
	}
}
