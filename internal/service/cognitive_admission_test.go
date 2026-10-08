package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

func TestCognitiveAdmissionConcurrentBoundAndNoRejectedMessage(t *testing.T) {
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	steps := make([]characterizationProviderStep, CognitiveQueuedTurnLimit+1)
	for i := range steps {
		steps[i].events = doneEvents(fmt.Sprintf("answer %d", i))
	}
	steps[0].hold = hold
	f := newHandleMessageFixture(t, steps)
	t.Cleanup(release)
	ctx := chat.WithDeltaMode(t.Context(), chat.DeltaModeLive)
	first, err := f.svc.SubmitCognitiveTurn(ctx, f.session, "first")
	if err != nil {
		t.Fatal(err)
	}
	consumer, ok := f.svc.streams.GetStream(first)
	if !ok {
		t.Fatal("missing first stream")
	}
	waitForDelta(t, consumer, "first turn")

	type outcome struct {
		id  string
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, CognitiveQueuedTurnLimit*2)
	var group sync.WaitGroup
	for i := 0; i < CognitiveQueuedTurnLimit*2; i++ {
		group.Go(func() {
			<-start
			id, submitErr := f.svc.SubmitCognitiveTurn(ctx, f.session, fmt.Sprintf("queued %d", i))
			results <- outcome{id, submitErr}
		})
	}
	close(start)
	group.Wait()
	close(results)
	var accepted []string
	for result := range results {
		if result.err == nil {
			accepted = append(accepted, result.id)
		} else if !errors.Is(result.err, ErrCognitiveQueueFull) || result.id != "" {
			t.Fatalf("admission result=%+v", result)
		}
	}
	if len(accepted) != CognitiveQueuedTurnLimit {
		t.Fatalf("queued=%d want=%d", len(accepted), CognitiveQueuedTurnLimit)
	}
	messages, err := f.st.ListMessages(t.Context(), f.session, 100)
	if err != nil {
		t.Fatal(err)
	}
	userMessages := 0
	for _, message := range messages {
		if message.Role == "user" {
			userMessages++
		}
	}
	// The fixture has one initial user message, plus first and accepted turns.
	if userMessages != CognitiveQueuedTurnLimit+2 {
		t.Fatalf("rejected admissions wrote messages: users=%d", userMessages)
	}
	release()
	drainStream(consumer)
	for _, id := range accepted {
		stream, ok := f.svc.streams.GetStream(id)
		if !ok {
			t.Fatalf("accepted stream %q missing", id)
		}
		if events := drainStream(stream); findEvent(events, "stream_end") == nil {
			t.Fatalf("queued run did not complete: %v", eventTypes(events))
		}
	}
}
