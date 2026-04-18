package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

// TestRecordLastError_Truncates asserts long tool error text is truncated so
// the chat-loop-terminated envelope payload stays bounded (CW-20260417-0485).
func TestRecordLastError_Truncates(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	long := strings.Repeat("A", 2000)
	ls.recordLastError("some_tool", long)
	if ls.lastToolName != "some_tool" {
		t.Errorf("lastToolName = %q, want %q", ls.lastToolName, "some_tool")
	}
	if !strings.HasSuffix(ls.lastToolError, "(truncated)") {
		t.Errorf("lastToolError should be truncated, got length %d, tail=%q",
			len(ls.lastToolError),
			ls.lastToolError[len(ls.lastToolError)-30:])
	}
	if len(ls.lastToolError) > 600 {
		t.Errorf("lastToolError length = %d, want <= 600 (500 body + suffix)", len(ls.lastToolError))
	}
}

// TestEmitChatLoopTerminated_StreamEnvelope asserts that emitChatLoopTerminated
// sends a `plugin_envelope` stream event whose wrapper payload has the
// `chat-loop-terminated` type and a schema-conformant data body
// (CW-20260417-0485).
func TestEmitChatLoopTerminated_StreamEnvelope(t *testing.T) {
	s := &chatServiceImpl{}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.iteration = 10
	for i := 0; i < 10; i++ {
		ls.recordToolCall("list_tasks", false)
	}
	ls.recordLastError("list_tasks", "ARG_VALIDATION_FAILED: /limit: got string, want number")

	ch := make(chan chat.StreamEvent, 4)
	s.emitChatLoopTerminated(
		"sess-xyz",
		ls,
		TerminationRunawayToolFailures,
		"runaway: 10 consecutive tool failures",
		ch,
	)

	select {
	case evt := <-ch:
		if evt.Type != "plugin_envelope" {
			t.Fatalf("stream event type = %q, want plugin_envelope", evt.Type)
		}
		if evt.Envelope == "" {
			t.Fatal("stream event envelope body is empty")
		}
		var wrap struct {
			ID   string          `json:"id"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(evt.Envelope), &wrap); err != nil {
			t.Fatalf("unmarshal envelope wrap: %v", err)
		}
		if wrap.Type != "chat-loop-terminated" {
			t.Errorf("envelope type = %q, want chat-loop-terminated", wrap.Type)
		}
		var payload chatLoopTerminatedPayload
		if err := json.Unmarshal(wrap.Data, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.Code != string(TerminationRunawayToolFailures) {
			t.Errorf("payload.Code = %q, want %q", payload.Code, TerminationRunawayToolFailures)
		}
		if payload.ConsecutiveFailures != 10 {
			t.Errorf("payload.ConsecutiveFailures = %d, want 10", payload.ConsecutiveFailures)
		}
		if payload.Iteration != 10 {
			t.Errorf("payload.Iteration = %d, want 10", payload.Iteration)
		}
		if payload.LastError == "" || !strings.Contains(payload.LastError, "ARG_VALIDATION_FAILED") {
			t.Errorf("payload.LastError should contain the triggering tool error, got %q", payload.LastError)
		}
		if payload.LastTool != "list_tasks" {
			t.Errorf("payload.LastTool = %q, want list_tasks", payload.LastTool)
		}
		if payload.Timestamp == "" {
			t.Error("payload.Timestamp is empty")
		}
		if payload.Reason == "" {
			t.Error("payload.Reason is empty")
		}
	default:
		t.Fatal("no stream event emitted")
	}
}

// TestEmitChatLoopTerminated_AllTerminationCodes smokes each code value so a
// future enum addition fails the schema's `code` enum list loudly via the
// contract test, not silently in this one.
func TestEmitChatLoopTerminated_AllTerminationCodes(t *testing.T) {
	codes := []TerminationCode{
		TerminationRunawayToolFailures,
		TerminationMaxTurns,
		TerminationHardCeiling,
		TerminationIdleTimeout,
		TerminationRetryBudgetExhausted,
	}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			s := &chatServiceImpl{}
			ls := newLoopState(chat.AgentConstraints{}, nil, false)
			ch := make(chan chat.StreamEvent, 2)
			s.emitChatLoopTerminated("sess", ls, code, "reason "+string(code), ch)
			select {
			case evt := <-ch:
				if evt.Type != "plugin_envelope" {
					t.Errorf("type = %q, want plugin_envelope", evt.Type)
				}
			default:
				t.Fatalf("no event emitted for code %q", code)
			}
		})
	}
}
