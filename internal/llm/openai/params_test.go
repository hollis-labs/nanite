package openai

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// Chat Completions compatibility stays explicit while reasoning turns use Responses.

func toolReq(model string) llmtypes.ChatRequest {
	return llmtypes.ChatRequest{
		Model:    model,
		Messages: []llmtypes.ChatMessage{{Role: "user", Content: "ping"}},
		Tools: []llmtypes.ToolDefinition{{
			Name:        "noop",
			Description: "does nothing",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
}

func TestBuildChatParams_ReasoningEffortOnlyForReasoningModelsWithTools(t *testing.T) {
	cases := []struct {
		name string
		req  llmtypes.ChatRequest
		want string
		why  string
	}{
		{
			name: "gpt-5.6 with tools is disabled",
			req:  toolReq("gpt-5.6"),
			want: "none",
			why:  "the model reasons by default and tools would 400 without this",
		},
		{
			name: "Astra is never sent unsupported none",
			req:  toolReq("gpt-6-astra"),
			want: "",
		},
		{
			name: "gpt-5.6-luna with tools is disabled",
			req:  toolReq("gpt-5.6-luna"),
			want: "none",
		},
		{
			name: "gpt-4o with tools is left alone",
			req:  toolReq("gpt-4o"),
			want: "",
			why:  "gpt-4o has no reasoning support; sending the field would break a working path",
		},
		{
			name: "gpt-5.6 without tools is left alone",
			req: llmtypes.ChatRequest{
				Model:    "gpt-5.6",
				Messages: []llmtypes.ChatMessage{{Role: "user", Content: "ping"}},
			},
			want: "",
			why:  "no tools means no conflict, so the model keeps its default reasoning",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, err := buildChatParams(tc.req)
			if err != nil {
				t.Fatalf("buildChatParams: %v", err)
			}
			if got := string(params.ReasoningEffort); got != tc.want {
				t.Fatalf("ReasoningEffort = %q, want %q — %s", got, tc.want, tc.why)
			}
		})
	}
}

func TestModelSupportsReasoning(t *testing.T) {
	cases := map[string]bool{
		// Reasoning models — seeded 2026-09-12.
		"gpt-5.6":      true,
		"gpt-5.6-luna": true,
		"gpt-6-astra":  true,
		// Do not infer capabilities for future major versions.
		"gpt-7":  false,
		"gpt-10": false,
		// o-series.
		"o1":      true,
		"o3-mini": true,
		"o4-mini": true,
		// Not reasoning models. gpt-4o is the one that must stay false —
		// it is the OpenAI path that works today.
		"gpt-4o":        false,
		"gpt-4o-mini":   false,
		"gpt-4-turbo":   false,
		"gpt-3.5-turbo": false,
		// Not OpenAI names at all; the predicate must not claim them.
		"claude-sonnet-5": false,
		"omni":            false,
		"":                false,
		"gpt-":            false,
	}
	for model, want := range cases {
		if got := modelSupportsReasoning(model); got != want {
			t.Errorf("modelSupportsReasoning(%q) = %v, want %v", model, got, want)
		}
	}
}
