package service

import (
	"context"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/effort"
)

type effortRecordingProvider struct {
	llmcontracts.Provider
	received chan llmcontracts.ReasoningConfig
}

func (p *effortRecordingProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.received <- llmcontracts.ReasoningConfigFromContext(ctx)
	return p.Provider.StreamChat(ctx, req)
}

func TestCognitiveEffortReachesNativeProvider(t *testing.T) {
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: doneEvents("answer")}})
	recorded := &effortRecordingProvider{Provider: f.provider, received: make(chan llmcontracts.ReasoningConfig, 1)}
	f.svc.providers.Register("characterization", recorded)
	messageID, err := f.svc.HandleMessage(effort.WithContext(context.Background(), effort.EffortMax), f.session, "think carefully")
	if err != nil {
		t.Fatal(err)
	}
	consumer, ok := f.svc.streams.GetStream(messageID)
	if !ok {
		t.Fatal("missing accepted stream")
	}
	if events := drainStream(consumer); findEvent(events, "stream_end") == nil {
		t.Fatalf("turn failed: %v", eventTypes(events))
	}
	select {
	case cfg := <-recorded.received:
		if !cfg.Enabled || cfg.BudgetTokens != effort.EffortMax.ReasoningCfg().BudgetTokens {
			t.Fatalf("requested max effort was lost: %+v", cfg)
		}
	default:
		t.Fatal("provider did not receive the turn")
	}
}
