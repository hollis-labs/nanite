package service

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestCognitiveProviderEOFDoesNotCompleteTurn(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: []llmtypes.StreamEvent{{Type: "delta", Content: "partial answer"}}}})
	bindTestDefinedConfiguration(t, f)
	messageID, err := f.svc.SubmitCognitiveTurn(chat.WithDeltaMode(t.Context(), chat.DeltaModeLive), f.session, "hello")
	if err != nil {
		t.Fatal(err)
	}
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("missing accepted stream")
	}
	events := drainStream(consumer)
	assertMessageStreamOutcome(t, events, "error")
	failure := findEvent(events, "error")
	if failure == nil || failure.StructuredError == nil || failure.StructuredError.Code != "upstream_truncated" {
		t.Fatalf("failure=%+v events=%v", failure, eventTypes(events))
	}
	snapshot, snapshotErr := f.svc.streams.CognitiveTurns().Get(f.session, messageID)
	if snapshotErr != nil || snapshot.State != "failed" {
		t.Fatalf("truncation became canonical success: snapshot=%+v err=%v", snapshot, snapshotErr)
	}
	message, err := f.st.GetMessage(t.Context(), messageID)
	if err != nil || message == nil || !strings.Contains(message.Content, "partial answer") {
		t.Fatalf("partial response not retained: message=%+v err=%v", message, err)
	}
}
