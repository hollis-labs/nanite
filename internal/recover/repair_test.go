package recover

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

// stubProvider implements provider.Provider with a synthetic response.
type stubProvider struct {
	response string
	err      error
	delay    time.Duration

	// captured for assertions
	lastModel        string
	lastSystemPrompt string
	lastUserContent  string
	calls            int
}

func (s *stubProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (s *stubProvider) StreamChat(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, errors.New("stub does not implement StreamChat")
}

func (s *stubProvider) Complete(ctx context.Context, req provider.ChatRequest) (string, error) {
	s.calls++
	s.lastModel = req.Model
	s.lastSystemPrompt = req.SystemPrompt
	if len(req.Messages) > 0 {
		s.lastUserContent = req.Messages[0].Content
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if s.err != nil {
		return "", s.err
	}
	return s.response, nil
}

// stubSchemaProvider feeds Repair a canned schema for the "tool_under_test".
type stubSchemaProvider struct {
	doc map[string]any
}

func (s *stubSchemaProvider) GetToolSchema(_ string) map[string]any { return s.doc }

func newRecoverable() *RecoverableError {
	return &RecoverableError{
		Kind:        KindSchemaValidation,
		ToolName:    "nanite_show_card",
		SentArgs:    map[string]any{"type": "report-card", "data": map[string]any{"title": "X", "sections": []any{"a"}}},
		SchemaURI:   "mem://nanite/envelope/report-card.schema.json",
		ErrorPath:   "/data/metrics",
		ErrorReason: "missing properties: 'metrics'",
		Suggestion:  "add field 'metrics' (array)",
	}
}

// TestRepair_Success_ProducesRepairedArgs is the happy path. The stub LLM
// returns a JSON object with repaired_args + lesson_hint. Repair must
// surface those verbatim.
func TestRepair_Success_ProducesRepairedArgs(t *testing.T) {
	stub := &stubProvider{response: `{"repaired_args": {"type":"report-card","data":{"title":"X","metrics":[{"label":"a","value":1}]}}, "missing_required": [], "lesson_hint": "report-card needs metrics, not sections"}`}
	rec := newRecoverable()

	out, err := Repair(context.Background(), rec, RepairOptions{
		Provider: stub,
		Model:    "claude-haiku-4-5",
		Timeout:  500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if !out.HasRepair() {
		t.Fatalf("expected HasRepair() true, got %+v", out)
	}
	if out.LessonHint == "" {
		t.Errorf("expected non-empty lesson hint")
	}
	if stub.lastModel != "claude-haiku-4-5" {
		t.Errorf("expected model claude-haiku-4-5, got %q", stub.lastModel)
	}
	if stub.lastSystemPrompt == "" {
		t.Errorf("expected system prompt to be set")
	}
	// The user content must mention the tool name and the error path.
	if !strings.Contains(stub.lastUserContent, "nanite_show_card") {
		t.Errorf("user prompt missing tool_name; got %q", stub.lastUserContent)
	}
	if !strings.Contains(stub.lastUserContent, "/data/metrics") {
		t.Errorf("user prompt missing error_path; got %q", stub.lastUserContent)
	}
	if out.LatencyMS < 0 {
		t.Errorf("expected latency_ms ≥ 0, got %d", out.LatencyMS)
	}
}

// TestRepair_MissingRequired_NoFabrication confirms the no-fabrication
// rule: when the LLM returns missing_required, repaired_args is nil and
// HasRepair is false. The caller MUST NOT retry the tool.
func TestRepair_MissingRequired_NoFabrication(t *testing.T) {
	stub := &stubProvider{response: `{"repaired_args": null, "missing_required": ["metrics"], "lesson_hint": "metrics is required and not derivable from sent args"}`}
	rec := newRecoverable()

	out, err := Repair(context.Background(), rec, RepairOptions{Provider: stub})
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if out.HasRepair() {
		t.Errorf("expected HasRepair()=false on missing_required path, got %+v", out)
	}
	if len(out.MissingRequired) != 1 || out.MissingRequired[0] != "metrics" {
		t.Errorf("expected missing_required=['metrics'], got %+v", out.MissingRequired)
	}
}

// TestRepair_Timeout_ReturnsError covers the timeout fall-through. The
// stub delays past the configured timeout; Repair must return an error
// (the caller falls through to the original C1 envelope).
func TestRepair_Timeout_ReturnsError(t *testing.T) {
	stub := &stubProvider{response: "{}", delay: 200 * time.Millisecond}
	rec := newRecoverable()

	out, err := Repair(context.Background(), rec, RepairOptions{
		Provider: stub,
		Timeout:  20 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("expected timeout error, got outcome %+v", out)
	}
}

// TestRepair_NilProvider_ReturnsError is a guard — defensive callers
// should never pass nil but we want a clean error rather than a panic.
func TestRepair_NilProvider_ReturnsError(t *testing.T) {
	_, err := Repair(context.Background(), newRecoverable(), RepairOptions{})
	if err == nil {
		t.Fatalf("expected error on nil provider")
	}
}

// TestRepair_NilRecoverable_ReturnsError mirrors the nil-provider guard.
func TestRepair_NilRecoverable_ReturnsError(t *testing.T) {
	_, err := Repair(context.Background(), nil, RepairOptions{Provider: &stubProvider{}})
	if err == nil {
		t.Fatalf("expected error on nil recoverable")
	}
}

// TestRepair_GarbageResponse_ReturnsError ensures malformed LLM output
// is reported, not silently swallowed (the harness will fall through to
// the original error envelope).
func TestRepair_GarbageResponse_ReturnsError(t *testing.T) {
	stub := &stubProvider{response: "I cannot reshape this."}
	rec := newRecoverable()

	_, err := Repair(context.Background(), rec, RepairOptions{Provider: stub})
	if err == nil {
		t.Fatalf("expected parse error for non-JSON response")
	}
}

// TestRepair_PartialJSONFences tolerates markdown-fenced output.
func TestRepair_PartialJSONFences_StillParses(t *testing.T) {
	stub := &stubProvider{response: "```json\n{\"repaired_args\": {\"a\":1}, \"missing_required\": [], \"lesson_hint\": \"ok\"}\n```"}
	rec := newRecoverable()

	out, err := Repair(context.Background(), rec, RepairOptions{Provider: stub})
	if err != nil {
		t.Fatalf("Repair failed on fenced JSON: %v", err)
	}
	if !out.HasRepair() {
		t.Fatalf("expected HasRepair on fenced JSON, got %+v", out)
	}
}

// TestRepair_EmptyOutcome_HasRepairFalse covers the "model declined"
// case where the JSON parses but carries neither repaired_args nor
// missing_required. HasRepair must be false; the caller falls through.
func TestRepair_EmptyOutcome_HasRepairFalse(t *testing.T) {
	stub := &stubProvider{response: `{"repaired_args": {}, "missing_required": [], "lesson_hint": "no change needed"}`}
	rec := newRecoverable()

	out, err := Repair(context.Background(), rec, RepairOptions{Provider: stub})
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if out.HasRepair() {
		t.Errorf("expected HasRepair=false for empty repaired_args, got %+v", out)
	}
}

// TestRepair_SchemaInjected confirms the schema document, when supplied,
// is included in the user prompt.
func TestRepair_SchemaInjected(t *testing.T) {
	stub := &stubProvider{response: `{"repaired_args": {"a":1}, "missing_required": [], "lesson_hint": "ok"}`}
	rec := newRecoverable()
	sp := &stubSchemaProvider{doc: map[string]any{"type": "object", "required": []string{"metrics"}}}

	_, err := Repair(context.Background(), rec, RepairOptions{
		Provider:       stub,
		SchemaProvider: sp,
	})
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if !strings.Contains(stub.lastUserContent, "schema:") {
		t.Errorf("expected schema block in user prompt, got %q", stub.lastUserContent)
	}
	if !strings.Contains(stub.lastUserContent, "metrics") {
		t.Errorf("expected schema content (mentioning metrics) in user prompt")
	}
}

// TestExtractJSONObject_StringEscapes confirms the brace-counting
// helper does not lose track of depth inside escaped strings.
func TestExtractJSONObject_StringEscapes(t *testing.T) {
	in := `prelude {"a":"b}c","d":1} trailer`
	got := extractJSONObject(in)
	want := `{"a":"b}c","d":1}`
	if got != want {
		t.Errorf("extractJSONObject = %q, want %q", got, want)
	}
}
