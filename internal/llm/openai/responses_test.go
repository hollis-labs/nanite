package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

func reasoningContext(budget int) context.Context {
	return llmcontracts.WithReasoningConfig(context.Background(), llmcontracts.ReasoningConfig{Enabled: budget > 0, BudgetTokens: budget})
}

func TestResponsesRoutingAndEffort(t *testing.T) {
	for _, tc := range []struct {
		model     string
		budget    int
		responses bool
		effort    string
	}{
		{"gpt-6-astra", 0, true, "low"},
		{"gpt-6-astra", 8000, true, "medium"},
		{"gpt-6-astra", 20000, true, "high"},
		{"gpt-5.6", 0, false, "none"},
		{"gpt-5.6", 8000, true, "medium"},
		{"gpt-5.6-luna", 20000, true, "high"},
		{"gpt-5", 0, true, "low"},
		{"o3", 0, true, "low"},
		{"gpt-4o", 20000, false, ""},
		{"gpt-7", 8000, false, ""},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.model, tc.budget), func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body := readJSON(t, r)
				w.Header().Set("Content-Type", "application/json")
				if tc.responses {
					if r.URL.Path != "/responses" {
						t.Errorf("path = %s", r.URL.Path)
					}
					if got := body["reasoning"].(map[string]any)["effort"]; got != tc.effort {
						t.Errorf("effort = %v", got)
					}
					if body["store"] != false {
						t.Error("Responses must remain stateless")
					}
					_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
				} else {
					if r.URL.Path != "/chat/completions" {
						t.Errorf("path = %s", r.URL.Path)
					}
					got, _ := body["reasoning_effort"].(string)
					if got != tc.effort {
						t.Errorf("effort = %q", got)
					}
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"answer"}}]}`)
				}
				for _, field := range []string{"temperature", "top_p", "top_logprobs"} {
					if _, found := body[field]; found {
						t.Errorf("unexpected %s", field)
					}
				}
			})
			got, err := c.Complete(reasoningContext(tc.budget), toolReq(tc.model))
			if err != nil || got != "answer" {
				t.Fatalf("Complete = %q, %v", got, err)
			}
		})
	}
}

func writeResponseEvent(w http.ResponseWriter, kind, body string) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(body)); err != nil {
		panic(err)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, compact.String())
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestResponsesToolContinuationPreservesNativeOutput(t *testing.T) {
	const output = `[
	 {"type":"reasoning","id":"rs_1","encrypted_content":"opaque-reasoning","summary":[{"type":"summary_text","text":"Checking the tool."}]},
	 {"type":"message","id":"msg_1","role":"assistant","phase":"commentary","status":"completed","content":[{"type":"output_text","text":"Checking.","annotations":[]}]},
	 {"type":"function_call","id":"fc_1","call_id":"call_1","name":"noop","arguments":"{\"value\":7}","status":"completed"}
	]`
	var requests atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		if r.URL.Path != "/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if body["instructions"] != "System\n\nSlot" {
			t.Errorf("instructions = %v", body["instructions"])
		}
		if body["max_output_tokens"] != float64(4096) {
			t.Errorf("max_output_tokens = %v", body["max_output_tokens"])
		}
		if body["reasoning"].(map[string]any)["summary"] != "auto" {
			t.Error("summary not requested")
		}
		tool := body["tools"].([]any)[0].(map[string]any)
		if tool["name"] != "noop" || tool["type"] != "function" || tool["strict"] != false {
			t.Errorf("tool = %v", tool)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			writeResponseEvent(w, "response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","delta":"Checking the tool."}`)
			writeResponseEvent(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"Checking."}`)
			writeResponseEvent(w, "response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"value\":"}`)
			writeResponseEvent(w, "response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"value\":7}"}`)
			writeResponseEvent(w, "response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":`+output+`,"usage":{"input_tokens":27,"output_tokens":12,"input_tokens_details":{"cached_tokens":10}}}}`)
		} else {
			input := body["input"].([]any)
			var native []any
			if err := json.Unmarshal([]byte(output), &native); err != nil {
				t.Fatal(err)
			}
			want := append([]any{map[string]any{"role": "user", "content": "ping"}}, native...)
			want = append(want, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "tool returned 7"})
			if !reflect.DeepEqual(input, want) {
				t.Errorf("continuation = %#v, want %#v", input, want)
			}
			writeResponseEvent(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"The result is 7."}`)
			writeResponseEvent(w, "response.completed", `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The result is 7."}]}],"usage":{"input_tokens":35,"output_tokens":6}}}`)
		}
	})
	req := toolReq("gpt-6-astra")
	req.SystemPrompt = "System"
	req.SlotBlocks = []llmtypes.SlotBlock{{Content: "Slot"}}
	req.MaxTokens = 4096
	var native, thinking, text string
	var tool *llmtypes.ToolUseBlock
	var usage *llmtypes.Usage
	done := false
	events, err := c.StreamChat(reasoningContext(8000), req)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		switch event.Type {
		case llmtypes.EventDelta:
			text += event.Content
		case llmtypes.EventThinking:
			thinking += event.ThinkingBlock.Thinking
		case responseOutputBlock:
			native = event.Content
		case llmtypes.EventToolUse:
			tool = event.ToolUse
		case llmtypes.EventUsage:
			usage = event.Usage
		case llmtypes.EventDone:
			done = true
		case llmtypes.EventError:
			t.Fatal(event.Error)
		case llmtypes.EventSessionID:
		}
	}
	if thinking != "Checking the tool." || text != "Checking." || !done {
		t.Fatalf("thinking/text/done = %q/%q/%v", thinking, text, done)
	}
	if tool == nil || tool.ID != "call_1" || tool.Input["value"] != float64(7) {
		t.Fatalf("tool = %+v", tool)
	}
	if usage == nil || usage.InputTokens != 27 || usage.OutputTokens != 12 || usage.CacheReadTokens != 10 || usage.StopReason != "tool_use" {
		t.Fatalf("usage = %+v", usage)
	}
	req.Messages = append(req.Messages,
		llmtypes.ChatMessage{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{
			{Type: responseOutputBlock, Text: native}, {Type: "text", Text: text},
			{Type: "tool_use", ID: tool.ID, Name: tool.Name, Input: &tool.Input},
		}},
		llmtypes.ChatMessage{Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: tool.ID, Content: "tool returned 7"}}},
	)
	events, err = c.StreamChat(reasoningContext(8000), req)
	if err != nil {
		t.Fatal(err)
	}
	text = ""
	for event := range events {
		if event.Type == llmtypes.EventError {
			t.Fatal(event.Error)
		}
		if event.Type == llmtypes.EventDelta {
			text += event.Content
		}
	}
	if text != "The result is 7." {
		t.Fatalf("final text = %q", text)
	}
}

func TestResponsesFailuresDoNotFinishOrExecuteTools(t *testing.T) {
	for _, tc := range []struct{ name, kind, event, want string }{
		{"EOF", "", "", "before completion"},
		{"error", "error", `{"type":"error","code":"server_error","message":"try later"}`, "try later"},
		{"failed", "response.failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"provider failed"}}}`, "provider failed"},
		{"incomplete", "response.incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, "max_output_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeResponseEvent(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"Partial answer."}`)
				writeResponseEvent(w, "response.output_item.done", `{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"noop","arguments":"{}"}}`)
				if tc.kind != "" {
					writeResponseEvent(w, tc.kind, tc.event)
				}
			})
			events, err := c.StreamChat(context.Background(), toolReq("gpt-6-astra"))
			if err != nil {
				t.Fatal(err)
			}
			var text, failure string
			for event := range events {
				if event.Type == llmtypes.EventDelta {
					text += event.Content
				}
				if event.Type == llmtypes.EventError {
					failure = event.Error
				}
				if event.Type == llmtypes.EventDone || event.Type == llmtypes.EventToolUse {
					t.Fatalf("unsafe event after failure: %+v", event)
				}
			}
			if text != "Partial answer." || !strings.Contains(failure, tc.want) {
				t.Fatalf("text/error = %q/%q", text, failure)
			}
		})
	}
}

func TestResponsesCancellationClosesTransport(t *testing.T) {
	closed := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeResponseEvent(w, "response.created", `{"type":"response.created","response":{"status":"in_progress"}}`)
		<-r.Context().Done()
		close(closed)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := c.StreamChat(ctx, toolReq("gpt-6-astra"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP stream did not close")
	}
	for range events {
	}
}
