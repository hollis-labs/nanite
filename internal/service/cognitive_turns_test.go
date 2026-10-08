package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentservice "github.com/hollis-labs/substrate/agent/service"

	chatstream "github.com/hollis-labs/go-chatstream"
	"github.com/hollis-labs/go-chatstream/hubbind"
	streamhub "github.com/hollis-labs/go-streamhub"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func readCognitiveEvents(t *testing.T, sub streamhub.Subscription) []chatstream.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	defer sub.Close()
	var events []chatstream.Event
	for {
		item, err := sub.Next(ctx)
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatal(err)
		}
		if item.Gap != nil {
			events = append(events, hubbind.GapEvent("", time.Now(), *item.Gap))
			continue
		}
		event, err := hubbind.Decode(item.Record)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
}

func TestCognitiveTurnStatusAndIndependentReplay(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(release)
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("answer"), hold: hold}})
	id, err := f.svc.SubmitCognitiveTurn(chat.WithDeltaMode(t.Context(), chat.DeltaModeLive), f.session, "question")
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := f.svc.streams.GetStream(id)
	waitForDelta(t, legacy, "native delta")
	turns := f.svc.streams.CognitiveTurns()
	snapshot, err := turns.Get(f.session, id)
	if err != nil || snapshot.State != "working" || snapshot.Content != "answer" || snapshot.Message.LastSeq != snapshot.EventCheckpoint {
		t.Fatalf("live snapshot=%+v err=%v", snapshot, err)
	}
	if _, lookupErr := f.st.GetMessage(t.Context(), id); !errors.Is(lookupErr, sql.ErrNoRows) {
		t.Fatalf("assistant persisted before completion: %v", lookupErr)
	}
	first, err := turns.Subscribe(t.Context(), f.session, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := turns.Subscribe(t.Context(), f.session, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if item, nextErr := first.Next(t.Context()); nextErr != nil || item.Record.Name != "run.start" {
		t.Fatalf("second observer took over first: %+v %v", item, nextErr)
	}
	_ = first.Close()
	release()
	drainStream(legacy)
	events := readCognitiveEvents(t, second)
	terminalCount := 0
	for _, ev := range events {
		if ev.IsTerminal() {
			terminalCount++
			if ev.Verb != chatstream.VerbRunFinish {
				t.Fatalf("unexpected terminal: %+v", ev)
			}
		}
	}
	if terminalCount != 1 {
		t.Fatalf("terminal count=%d", terminalCount)
	}
	final, err := turns.Get(f.session, id)
	if err != nil || final.State != "completed" || final.CommittedMessage == nil || final.Content != "answer" {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	reduced, err := chatstream.Reduce(events, nil)
	if err != nil || !reflect.DeepEqual(reduced, final.Message) {
		t.Fatalf("log/snapshot disagree: reduced=%+v snapshot=%+v err=%v", reduced, final.Message, err)
	}
	resume, err := turns.Subscribe(t.Context(), f.session, id, snapshot.EventCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	resumed := readCognitiveEvents(t, resume)
	if len(resumed) == 0 || !resumed[len(resumed)-1].IsTerminal() {
		t.Fatalf("completed replay=%+v", resumed)
	}
	if _, err := turns.Get("other-view", id); !errors.Is(err, agentservice.ErrTurnNotFound) {
		t.Fatalf("wrong-owner snapshot=%v", err)
	}
}

func TestCognitiveCanonicalGapSlowObserverAndSnapshotAfterExpiry(t *testing.T) {
	// Small real hub limits exercise the same recovery path without waiting
	// for production's five-minute grace or thousands of model tokens.
	turns := newCognitiveTurns(nil, streamhub.New(streamhub.NewMemoryLog(), streamhub.WithAutoOpen(false), streamhub.WithTerminal(hubbind.Terminal), streamhub.WithRetention(streamhub.Retention{MaxRecords: 8}), streamhub.WithRetainAfterClose(0)))
	run := turns.create("view", "turn", "fixture", "model", chat.DeltaModeLive, "normal", func() (*store.Message, error) {
		return &store.Message{ID: "turn", SessionID: "view", Content: chat.WrapResponse(strings.Repeat("x", 600), "default", nil, nil, false, false).MarshalContent()}, nil
	})
	run.owner.Working("turn")
	slow, err := turns.Subscribe(t.Context(), "view", "turn", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Consume the admission prefix so replay completes before the burst.
	for i := 0; i < 3; i++ {
		if _, nextErr := slow.Next(t.Context()); nextErr != nil {
			t.Fatal(nextErr)
		}
	}
	for i := 0; i < 600; i++ {
		run.consume(chat.StreamEvent{Type: "delta", Content: "x"})
	}
	for {
		_, nextErr := slow.Next(t.Context())
		if errors.Is(nextErr, streamhub.ErrSlowConsumer) {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
	}
	gapSub, err := turns.Subscribe(t.Context(), "view", "turn", 1)
	if err != nil {
		t.Fatal(err)
	}
	item, err := gapSub.Next(t.Context())
	if err != nil || item.Gap == nil || item.Gap.Reason != streamhub.GapRetention {
		t.Fatalf("retention gap=%+v %v", item, err)
	}
	_ = gapSub.Close()
	ahead, err := turns.Subscribe(t.Context(), "view", "turn", 9000)
	if err != nil {
		t.Fatal(err)
	}
	item, err = ahead.Next(t.Context())
	if err != nil || item.Gap == nil || item.Gap.Reason != streamhub.GapCursorAhead {
		t.Fatalf("ahead gap=%+v %v", item, err)
	}
	_ = ahead.Close()
	run.consume(chat.StreamEvent{Type: "stream_end"})
	if _, subscribeErr := turns.Subscribe(t.Context(), "view", "turn", 0); !errors.Is(subscribeErr, streamhub.ErrUnknownStream) {
		t.Fatalf("log did not expire: %v", subscribeErr)
	}
	snapshot, err := turns.Get("view", "turn")
	if err != nil || snapshot.State != "completed" || snapshot.Message.Text() != strings.Repeat("x", 600) || snapshot.CommittedMessage == nil {
		t.Fatalf("expired-log snapshot=%+v %v", snapshot, err)
	}
}

func TestCognitiveTerminalFailsOnMissingAssistantCommit(t *testing.T) {
	turns := NewCognitiveTurns()
	run := turns.create("view", "turn", "fixture", "model", chat.DeltaModeLive, "normal", func() (*store.Message, error) { return nil, errors.New("database unavailable") })
	run.consume(chat.StreamEvent{Type: "delta", Content: "partial"})
	run.consume(chat.StreamEvent{Type: "stream_end"})
	run.consume(chat.StreamEvent{Type: "delta", Content: "after terminal"})
	run.end()
	snapshot, err := turns.Get("view", "turn")
	if err != nil || snapshot.State != "failed" || snapshot.Message.Error.Code != "persistence_failed" || snapshot.Message.Text() != "partial" {
		t.Fatalf("save-failure outcome=%+v %v", snapshot, err)
	}
	sub, err := turns.Subscribe(t.Context(), "view", "turn", 0)
	if err != nil {
		t.Fatal(err)
	}
	events := readCognitiveEvents(t, sub)
	if last := events[len(events)-1]; last.Verb != chatstream.VerbRunError {
		t.Fatalf("save failure succeeded: %+v", last)
	}
}

func TestCognitiveSnapshotsSurviveDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns.db")
	backing, err := store.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backing.Close(context.Background()) })
	view := &store.Session{ID: "view", Provider: "fixture", Model: "model"}
	if err = backing.CreateCognitiveSession(t.Context(), view, ""); err != nil {
		t.Fatal(err)
	}
	turns := NewCognitiveTurns(backing)
	for _, id := range []string{"completed", "unfinished"} {
		snapshot := agentservice.Snapshot[store.Message]{SessionViewID: view.ID, TurnID: id, RunID: id, OutputMessageID: id, State: "submitted"}
		if id == "unfinished" {
			snapshot.State, snapshot.PendingApprovalID = "input_required", "lost-prompt"
		}
		initial, _ := json.Marshal(snapshot)
		if err = backing.CreateCognitiveTurn(t.Context(), &store.Message{ID: "input-" + id, SessionID: view.ID, Content: "question"}, id, string(initial)); err != nil {
			t.Fatal(err)
		}
	}
	output := &store.Message{ID: "completed", SessionID: view.ID, Role: "assistant", Content: chat.WrapResponse("answer", "default", nil, nil, false, false).MarshalContent()}
	if err = backing.CreateMessage(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	run := turns.create(view.ID, output.ID, "fixture", "model", chat.DeltaModeLive, "normal", func() (*store.Message, error) { return backing.GetMessage(t.Context(), output.ID) })
	run.consume(chat.StreamEvent{Type: "delta", Content: "answer"})
	run.consume(chat.StreamEvent{Type: "stream_end"})
	before, err := turns.Get(view.ID, output.ID)
	if err != nil || before.State != "completed" {
		t.Fatal(before, err)
	}
	if err = backing.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	restored := NewCognitiveTurns(reopened)
	after, err := restored.Get(view.ID, output.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("committed snapshot changed on reopen: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err = restored.Subscribe(t.Context(), view.ID, output.ID, 0); !errors.Is(err, streamhub.ErrUnknownStream) {
		t.Fatalf("process-local journal survived database reopen: %v", err)
	}
	lost, err := restored.Get(view.ID, "unfinished")
	if err != nil || lost.State != "failed" || lost.Message.Error.Code != "process_lost" || lost.PendingApprovalID != "" {
		t.Fatal(lost, err)
	}
	again, err := NewCognitiveTurns(reopened).Get(view.ID, "unfinished")
	if err != nil || again.Revision != lost.Revision || again.EventCheckpoint != lost.EventCheckpoint {
		t.Fatal("restart outcome/checkpoint changed", again, err)
	}
}
