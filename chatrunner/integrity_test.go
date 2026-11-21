package chatrunner_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
	"github.com/hollis-labs/nanite/internal/store"
)

// corruptRunningTurn answers a call nobody made inside the turn the killed
// process left running.
func corruptRunningTurn(t *testing.T, dir string) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	running, err := db.RunningTurns(t.Context())
	if err != nil || len(running) != 1 {
		t.Fatalf("running turns %v, %v", running, err)
	}
	j, err := db.OpenJournal(t.Context(), running[0].ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := j.AppendMessage(store.Message{ID: store.NewID(now), TurnID: running[0].TurnID, Role: "tool", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "nobody", Content: "x"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

// With ResumeInterrupted, a turn whose conversation fails integrity checks
// is never resumed: RecoverTurns reports the refusal and offers the turn
// for discard only, Resume refuses synchronously, and Discard still works.
func TestRestartNeverResumesThroughAnUnsafeFinding(t *testing.T) {
	dir := crashedDir(t)
	corruptRunningTurn(t, dir)
	w := newWiredIn(t, dir, false, func(o *chatrunner.Options) { o.ResumeInterrupted = true })
	handled, err := w.runner.RecoverTurns(t.Context())
	if !errors.Is(err, agent.ErrIntegrity) || len(handled) != 0 {
		t.Fatalf("recover: %v, %v", handled, err)
	}
	var offered map[string]any
	w.until(t, "the discard-only offer", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityInterrupted {
			offered = activityValue(t, ev)
		}
		return ev.IsTerminal()
	})
	actions, _ := offered["actions"].([]any)
	if offered["integrity"] != "blocked" || !slices.Equal(actions, []any{"discard"}) {
		t.Fatalf("offer %v", offered)
	}
	var refused *agent.IntegrityError
	if err := w.runner.Resume(t.Context(), w.conv); !errors.As(err, &refused) || refused.Report.Unsafe()[0].Code != "orphan_tool_result" {
		t.Fatalf("resume: %v", err)
	}
	if n := w.brain.answers(resumeScript); n != 0 {
		t.Fatalf("the model was asked %d times", n)
	}
	if err := w.runner.Discard(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn != nil {
		t.Fatalf("still interrupted: %+v, %v", turn, err)
	}
}
