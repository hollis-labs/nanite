package anthropic

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

func ttlRequest(ttl string) llmtypes.ChatRequest {
	return llmtypes.ChatRequest{Model: "test-model", MaxTokens: 17, SystemPrompt: "system", Tools: []llmtypes.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}, Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hello"}}, CacheHints: []llmtypes.CacheHint{{Position: "system", TTL: ttl}, {Position: "tools", TTL: ttl}, {Position: "recent_message", Index: 0}}}
}

func TestCacheTTLWire(t *testing.T) {
	// Captured from the adapter before TTL support; compare whole request bytes.
	const before = `{"max_tokens":17,"messages":[{"content":[{"text":"hello","cache_control":{"type":"ephemeral"},"type":"text"}],"role":"user"}],"model":"test-model","system":[{"text":"system","cache_control":{"type":"ephemeral"},"type":"text"}],"tools":[{"input_schema":{"properties":{},"type":"object"},"name":"lookup","cache_control":{"type":"ephemeral"}}]}`
	c := New()
	for _, ttl := range []string{"", "5m", "1h"} {
		t.Run("ttl="+ttl, func(t *testing.T) {
			raw, err := json.Marshal(c.buildMessageParams(ttlRequest(ttl), "test-model", false, llmcontracts.ReasoningConfig{}))
			if err != nil {
				t.Fatal(err)
			}
			if ttl == "" {
				if string(raw) != before {
					t.Fatalf("unset TTL changed wire bytes:\n%s", raw)
				}
				return
			}
			if strings.Count(string(raw), `"ttl":"`+ttl+`"`) != 2 || strings.Count(string(raw), `"cache_control"`) != 3 {
				t.Fatalf("markers: %s", raw)
			}
		})
	}
}

func TestCacheTTLStableSlotsAndRecentMessages(t *testing.T) {
	req := ttlRequest("1h")
	req.SystemPrompt = ""
	req.SlotBlocks = []llmtypes.SlotBlock{{Name: "universal", Content: "stable"}, {Name: "system", Content: "also stable"}, {Name: "context", Content: "dynamic", Changed: true}}
	req.Messages = append(req.Messages, llmtypes.ChatMessage{Role: "assistant", Content: "reply"}, llmtypes.ChatMessage{Role: "user", Content: "latest"})
	req.CacheHints = append(req.CacheHints, llmtypes.CacheHint{Position: "recent_message", Index: 1, TTL: "1h"})
	raw, err := json.Marshal(New().buildMessageParams(req, "test-model", false, llmcontracts.ReasoningConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"cache_control"`) != 4 || strings.Count(string(raw), `"ttl":"1h"`) != 3 {
		t.Fatalf("marker budget/TTL: %s", raw)
	}
}

func TestCacheTTLInvalidRejectedBeforeClient(t *testing.T) {
	c := New()
	for _, ttl := range []string{"2h", "60m", " 1h"} {
		req := ttlRequest(ttl)
		if _, err := c.StreamChat(t.Context(), req); err == nil || !strings.Contains(err.Error(), "TTL") {
			t.Fatalf("stream TTL %q: %v", ttl, err)
		}
		if _, err := c.Complete(t.Context(), req); err == nil || !strings.Contains(err.Error(), "TTL") {
			t.Fatalf("complete TTL %q: %v", ttl, err)
		}
	}
}

func TestCacheTTLConcurrentRequests(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		ttl := []string{"", "1h"}[i%2]
		wg.Go(func() {
			raw, err := json.Marshal(c.buildMessageParams(ttlRequest(ttl), "test-model", false, llmcontracts.ReasoningConfig{}))
			if err != nil {
				t.Error(err)
				return
			}
			if (ttl != "") != strings.Contains(string(raw), `"ttl"`) {
				t.Errorf("TTL crossed requests: %s", raw)
			}
		})
	}
	wg.Wait()
}
