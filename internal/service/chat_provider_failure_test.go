package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

func TestProviderFailureSurvivesReload(t *testing.T) {
	for _, partial := range []string{"", "Already completed the first part."} {
		t.Run(partial, func(t *testing.T) {
			captured := &capturingStore{}
			svc := &chatServiceImpl{store: captured}
			events := make(chan chat.StreamEvent, 1)
			svc.surfaceProviderFailure(context.Background(), events, "session", "message", "agent", partial, "gpt-6-astra", "openai", "profile", errors.New("openai: Bad Request 400 unsupported_value: reasoning_effort"), nil)
			msg := captured.lastMsg
			if msg == nil {
				t.Fatal("failure was not persisted")
			}
			var metadata struct {
				Failure chat.ChatError `json:"provider_error"`
				Partial bool           `json:"partial_output"`
			}
			if err := json.Unmarshal([]byte(msg.Metadata), &metadata); err != nil {
				t.Fatal(err)
			}
			live := <-events
			if live.StructuredError == nil || metadata.Failure.Message != live.StructuredError.Message {
				t.Fatal("live explanation differs from reload explanation")
			}
			if metadata.Failure.Details["request_rejected"] != true || metadata.Partial != (partial != "") {
				t.Fatalf("lost recovery/partial state: %+v", metadata)
			}
			if partial != "" && !strings.Contains(msg.Content, partial) {
				t.Fatal("partial output was lost")
			}
			if strings.Contains(msg.Content, "[generation interrupted]") {
				t.Fatal("generic placeholder replaced the explanation")
			}
		})
	}
}
