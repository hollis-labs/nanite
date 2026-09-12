package openai

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CW-20260912-0107. All three newly-seeded OpenAI models returned 400 on
// every tool-bearing turn while gpt-4o kept working. The provider named
// both the cause and the remedy: chat-completions refuses function tools
// alongside an active reasoning effort, and these models apply one by
// default when reasoning_effort is absent.
//
// The regression guard here is the gpt-4o case. Setting the field
// unconditionally would fix the three new models by breaking the one
// OpenAI path that already worked.

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
			name: "gpt-6-astra with tools is disabled",
			req:  toolReq("gpt-6-astra"),
			want: "none",
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

func TestModelDefaultsToReasoning(t *testing.T) {
	cases := map[string]bool{
		// Reasoning models — seeded 2026-09-12.
		"gpt-5.6":      true,
		"gpt-5.6-luna": true,
		"gpt-6-astra":  true,
		// Major version is read, not matched by name, so this needs no edit.
		"gpt-7":  true,
		"gpt-10": true,
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
		if got := modelDefaultsToReasoning(model); got != want {
			t.Errorf("modelDefaultsToReasoning(%q) = %v, want %v", model, got, want)
		}
	}
}
