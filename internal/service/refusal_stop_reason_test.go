package service

import (
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

// A provider turn that ends in stop_reason=refusal must reach the client as an
// error-report, not as an ordinary completed turn.
func TestRefusalStopReasonSurfacesAsError(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{
		{events: []llmtypes.StreamEvent{
			{Type: "usage", Usage: &llmtypes.Usage{StopReason: "refusal", InputTokens: 11}},
			{Type: "done"},
		}},
	})
	f.svc.activeGen = make(map[string]*inFlightGen)
	f.svc.lifecycle = lifecycle.NewManager("test.refusal")
	t.Cleanup(func() { _ = f.svc.lifecycle.Shutdown(5 * time.Second) })
	bindTestDefinedConfiguration(t, f)

	ctx := chat.WithDeltaMode(t.Context(), chat.DeltaModeLive)
	id, err := f.svc.SubmitCognitiveTurn(ctx, f.session, "hello")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := f.svc.streams.GetStream(id)
	var all strings.Builder
	for ev := range stream {
		all.WriteString(ev.Content)
	}
	got := all.String()
	if !strings.Contains(got, `"stop_reason":"refusal"`) || !strings.Contains(got, "declined") {
		t.Fatalf("stream did not carry a refusal error report: %q", got)
	}
}
