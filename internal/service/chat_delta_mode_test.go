package service

// Coverage for chat.DeltaMode: the opt-in that lets a consumer receive provider
// text deltas as they arrive instead of held until each iteration's stop reason
// is known (F4 / CW-20260419-0029). Tests enter through the same production
// door as the characterization suite, and the HandleMessage tests cover the
// hop that matters most — that a mode stamped on the request context survives
// the detached generation context and reaches the stream loop.

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

// waitForDelta returns the first delta event on consumer, failing the test if
// none arrives while the provider stream is still held open.
func waitForDelta(t *testing.T, consumer <-chan chat.StreamEvent, what string) chat.StreamEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-consumer:
			if !ok {
				t.Fatalf("%s: stream closed before any delta arrived", what)
			}
			if evt.Type == "delta" {
				return evt
			}
		case <-timeout:
			t.Fatalf("%s: no delta within 5s while the provider stream was open — deltas are being buffered", what)
		}
	}
}

func drainStream(consumer <-chan chat.StreamEvent) []chat.StreamEvent {
	var events []chat.StreamEvent
	for evt := range consumer {
		events = append(events, evt)
	}
	return events
}

func deltaPhases(events []chat.StreamEvent) []string {
	var phases []string
	for _, evt := range events {
		if evt.Type == "delta" {
			phases = append(phases, evt.Phase)
		}
	}
	return phases
}

func deltaText(events []chat.StreamEvent) string {
	var sb strings.Builder
	for _, evt := range events {
		if evt.Type == "delta" {
			sb.WriteString(evt.Content)
		}
	}
	return sb.String()
}

// newHandleMessageFixture is a characterization fixture that can also run
// HandleMessage, which needs the generation registry and lifecycle owner.
func newHandleMessageFixture(t *testing.T, steps []characterizationProviderStep) *characterizationFixture {
	t.Helper()
	f := newCharacterizationFixture(t, steps)
	f.svc.activeGen = make(map[string]*inFlightGen)
	f.svc.lifecycle = lifecycle.NewManager("test.delta-mode")
	// Registered after the fixture's store cleanup, so it runs first: the
	// generation goroutine must be done before the store closes under it.
	t.Cleanup(func() { _ = f.svc.lifecycle.Shutdown(5 * time.Second) })
	return f
}

func TestDeltaMode_LiveSendsDeltaBeforeProviderStreamCloses(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("streamed live"), hold: hold}})
	// Registered after the fixture so a failing test releases the provider
	// before the store closes under the still-running turn.
	t.Cleanup(release)

	const messageID = "assistant-delta-live"
	producer := f.svc.streams.CreateStream(messageID, f.session)
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("GetStream: production stream was not registered")
	}
	ctx := chat.WithDeltaMode(context.Background(), chat.DeltaModeLive)
	runDone := make(chan error, 1)
	go func() {
		runDone <- f.svc.dispatcher.Run(ctx, dispatcher.Request{
			SessionID: f.session, AssistantMsgID: messageID, UserContent: "characterize this turn", CallerType: dispatcher.CallerChat,
		}, producer)
	}()

	// The provider has sent its delta and its stop reason but has not closed the
	// stream, so nothing here can be the post-stream flush.
	delta := waitForDelta(t, consumer, "live mode")
	if delta.Content != "streamed live" || delta.Phase != "" {
		t.Fatalf("live delta = %+v, want content %q and no phase", delta, "streamed live")
	}

	release()
	if err := <-runDone; err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}
	rest := drainStream(consumer)
	if findEvent(rest, "stream_end") == nil {
		t.Fatalf("events after release = %v, want stream_end", eventTypes(rest))
	}
	if got := deltaText(rest); got != "" {
		t.Fatalf("deltas after release = %q, want none: the text was already sent live", got)
	}
}

func TestDeltaMode_SavedMessageIdenticalAcrossModes(t *testing.T) {
	type outcome struct {
		events   []chat.StreamEvent
		content  string
		metadata string
	}
	runTurn := func(mode chat.DeltaMode) outcome {
		tu := llmtypes.ToolUseBlock{ID: "tool-single", Name: "echo", Input: map[string]any{"value": "one"}}
		f := newCharacterizationFixture(t, []characterizationProviderStep{
			{events: toolTurnEvents(tu)},
			{events: doneEvents("single tool complete")},
		}, "echo")
		const messageID = "assistant-parity"
		events := f.runCtx(chat.WithDeltaMode(context.Background(), mode), t, messageID)

		msgs, err := f.st.ListMessages(context.Background(), f.session, 20)
		if err != nil {
			t.Fatalf("%s: ListMessages: %v", mode, err)
		}
		for _, m := range msgs {
			if m.ID == messageID {
				return outcome{events: events, content: m.Content, metadata: m.Metadata}
			}
		}
		t.Fatalf("%s: assistant message %q was not persisted", mode, messageID)
		return outcome{}
	}
	phased, live := runTurn(chat.DeltaModePhased), runTurn(chat.DeltaModeLive)

	const wantText = "I will use the tools. single tool complete"
	if got := deltaText(phased.events); got != wantText {
		t.Errorf("phased delta text = %q, want %q", got, wantText)
	}
	if got := deltaText(live.events); got != wantText {
		t.Errorf("live delta text = %q, want %q", got, wantText)
	}
	if got, want := deltaPhases(phased.events), []string{chat.PhaseNarration, chat.PhaseFinal}; !reflect.DeepEqual(got, want) {
		t.Errorf("phased phases = %v, want %v", got, want)
	}
	if got, want := deltaPhases(live.events), []string{"", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("live phases = %v, want %v (untagged)", got, want)
	}
	if got, want := eventTypes(live.events), eventTypes(phased.events); !reflect.DeepEqual(got, want) {
		t.Errorf("live event order = %v, want the phased order %v", got, want)
	}

	// The mode changes delivery only. The saved message, including the
	// narration/final split that feeds metadata.thinking, must not depend on it.
	if live.content != phased.content {
		t.Errorf("saved content differs across modes:\n phased: %s\n   live: %s", phased.content, live.content)
	}
	if live.metadata != phased.metadata {
		t.Errorf("saved metadata differs across modes:\n phased: %s\n   live: %s", phased.metadata, live.metadata)
	}
	if !strings.Contains(live.metadata, "I will use the tools.") {
		t.Errorf("live metadata = %q, want the narration under thinking", live.metadata)
	}
}

func TestHandleMessage_LiveDeltaModeReachesGeneration(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("live via HandleMessage"), hold: hold}})
	// Registered after the fixture so it runs before the lifecycle shutdown,
	// which would otherwise wait out its timeout on the held provider.
	t.Cleanup(release)

	// The API handlers stamp the mode on the request context; generation runs on
	// a context detached from it, so this only passes if HandleMessage hands the
	// mode across explicitly.
	ctx := chat.WithDeltaMode(context.Background(), chat.DeltaModeLive)
	messageID, err := f.svc.HandleMessage(ctx, f.session, "hello")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("GetStream: HandleMessage did not register a stream")
	}

	delta := waitForDelta(t, consumer, "HandleMessage with a live ctx")
	if delta.Content != "live via HandleMessage" || delta.Phase != "" {
		t.Fatalf("live delta = %+v, want content %q and no phase", delta, "live via HandleMessage")
	}
	release()
	if rest := drainStream(consumer); findEvent(rest, "stream_end") == nil {
		t.Fatalf("events after release = %v, want stream_end", eventTypes(rest))
	}
}

func TestHandleMessage_DefaultDeltaModeIsPhased(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("phased by default")}})

	messageID, err := f.svc.HandleMessage(context.Background(), f.session, "hello")
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("GetStream: HandleMessage did not register a stream")
	}
	events := drainStream(consumer)

	if got, want := deltaPhases(events), []string{chat.PhaseFinal}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default-mode phases = %v, want %v (events %v)", got, want, eventTypes(events))
	}
}
