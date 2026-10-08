package service

import (
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
)

func TestCognitiveProviderEOFDoesNotCompleteTurn(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: []llmtypes.StreamEvent{{Type: "delta", Content: "partial answer"}}}})
	messageID, err := f.svc.SubmitCognitiveTurn(chat.WithDeltaMode(t.Context(), chat.DeltaModeLive), f.session, "hello")
	if err != nil {
		t.Fatal(err)
	}
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("missing accepted stream")
	}
	events := drainStream(consumer)
	if findEvent(events, "stream_end") != nil {
		t.Fatalf("upstream truncation became success: %v", eventTypes(events))
	}
	failure := findEvent(events, "error")
	if failure == nil || failure.StructuredError == nil || failure.StructuredError.Code != "upstream_truncated" {
		t.Fatalf("failure=%+v events=%v", failure, eventTypes(events))
	}
	message, err := f.st.GetMessage(t.Context(), messageID)
	if err != nil || message == nil || !strings.Contains(message.Content, "partial answer") {
		t.Fatalf("partial response not retained: message=%+v err=%v", message, err)
	}
}
