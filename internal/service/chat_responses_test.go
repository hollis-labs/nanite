package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
)

func TestResponsesOutputReplayedWithoutLeakingToTranscript(t *testing.T) {
	const opaque = `[{"type":"reasoning","encrypted_content":"opaque-secret"},{"type":"function_call","call_id":"call-echo","name":"echo","arguments":"{\"value\":\"one\"}"}]`
	first := []llmtypes.StreamEvent{
		{Type: "thinking", ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "Checking "}},
		{Type: "thinking", ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "the tool."}},
		{Type: "openai_response_output", Content: opaque},
	}
	first = append(first, toolTurnEvents(llmtypes.ToolUseBlock{ID: "call-echo", Name: "echo", Input: map[string]any{"value": "one"}})...)
	last := []llmtypes.StreamEvent{
		{Type: "thinking", ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "Now "}},
		{Type: "thinking", ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "answering."}},
	}
	last = append(last, doneEvents("The tool succeeded.")...)
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: first}, {events: last}}, "echo")
	events := f.run(t, "assistant-responses")
	var summaries strings.Builder
	for _, event := range events {
		if strings.Contains(event.Content, "opaque-secret") {
			t.Fatal("encrypted reasoning leaked into transcript events")
		}
		if event.Type == "delta" && event.Phase == chat.PhaseThinking {
			summaries.WriteString(event.Content)
		}
	}
	if summaries.String() != "Checking the tool.Now answering." || findEvent(events, "stream_end") == nil {
		t.Fatalf("summary or completion missing: %q, %v", summaries.String(), eventTypes(events))
	}
	replayed := false
	for _, request := range f.provider.requestsSnapshot()[1:] {
		for _, message := range request.Messages {
			for _, block := range message.ContentBlocks {
				if block.Type == "openai_response_output" && block.Text == opaque {
					replayed = true
				}
			}
		}
	}
	if !replayed {
		t.Fatal("provider output did not reach the tool continuation")
	}
	message, err := f.st.GetMessage(context.Background(), "assistant-responses")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(message.Content+message.Metadata, "opaque-secret") {
		t.Fatal("ephemeral provider state was persisted into the transcript")
	}
	var meta struct {
		Blocks []struct {
			Thinking string `json:"thinking"`
		} `json:"thinking_blocks"`
	}
	if err := json.Unmarshal([]byte(message.Metadata), &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Blocks) == 0 || meta.Blocks[0].Thinking != "Now answering." {
		t.Fatalf("summary fragments were not merged for reload: %+v", meta)
	}
}
