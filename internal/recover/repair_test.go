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

// TestDefaultRepairTimeout_AccommodatesRealAPI guards against a regression
// where DefaultRepairTimeout drops below a value that can plausibly cover
// a real Anthropic Haiku-class API call (TCP+TLS+queue+inference for a
// repair-sized payload). c110 evidence (CW-20260429-0018) showed the prior
// 1500ms default firing before Anthropic could respond. Anything under 3s
// is a smell; if a future change tightens this, do it deliberately and
// update this floor with rationale.
func TestDefaultRepairTimeout_AccommodatesRealAPI(t *testing.T) {
	const floor = 3 * time.Second
	if DefaultRepairTimeout < floor {
		t.Fatalf("DefaultRepairTimeout=%v is below the %v floor required to cover a real Anthropic Haiku API call; see CW-20260429-0018", DefaultRepairTimeout, floor)
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

// TestExtractJSONObject_FencedWithLanguageTag confirms the
// brace-counting extractor handles input that already includes a
// markdown fence with a language tag. The leading ` ```json ` and the
// trailing fence are not part of a JSON object so the balanced
// extractor still finds the inner {...}.
func TestExtractJSONObject_FencedWithLanguageTag(t *testing.T) {
	in := "```json\n{\"a\":1,\"b\":\"x\"}\n```"
	got := extractJSONObject(in)
	want := `{"a":1,"b":"x"}`
	if got != want {
		t.Errorf("extractJSONObject = %q, want %q", got, want)
	}
}

// TestExtractJSONObject_FencedNoLanguageTag covers the bare-fence form.
func TestExtractJSONObject_FencedNoLanguageTag(t *testing.T) {
	in := "```\n{\"a\":1}\n```"
	got := extractJSONObject(in)
	want := `{"a":1}`
	if got != want {
		t.Errorf("extractJSONObject = %q, want %q", got, want)
	}
}

// TestExtractJSONObject_PlainObject is a regression guard — the helper
// must continue to return the object on already-clean input.
func TestExtractJSONObject_PlainObject(t *testing.T) {
	in := `{"a":1,"b":[2,3]}`
	got := extractJSONObject(in)
	if got != in {
		t.Errorf("extractJSONObject = %q, want %q", got, in)
	}
}

// TestStripCodeFence_LanguageTag verifies stripCodeFence peels
// ` ```json ... ``` ` cleanly.
func TestStripCodeFence_LanguageTag(t *testing.T) {
	in := "```json\n{\"repaired_args\": {\"a\":1}}\n```"
	got := stripCodeFence(in)
	want := `{"repaired_args": {"a":1}}`
	if got != want {
		t.Errorf("stripCodeFence = %q, want %q", got, want)
	}
}

// TestStripCodeFence_BareFence verifies stripCodeFence peels a bare
// ` ``` ... ``` ` block.
func TestStripCodeFence_BareFence(t *testing.T) {
	in := "```\n{\"a\":1}\n```"
	got := stripCodeFence(in)
	want := `{"a":1}`
	if got != want {
		t.Errorf("stripCodeFence = %q, want %q", got, want)
	}
}

// TestStripCodeFence_NoFence returns the input unchanged when no fence
// is present.
func TestStripCodeFence_NoFence(t *testing.T) {
	in := `{"a":1}`
	got := stripCodeFence(in)
	if got != in {
		t.Errorf("stripCodeFence = %q, want %q", got, in)
	}
}

// TestStripCodeFence_WithPreamble peels a fence that follows a
// chat-style preamble like "Here is the repair: ```json ... ```".
func TestStripCodeFence_WithPreamble(t *testing.T) {
	in := "Here is the repair:\n```json\n{\"a\":1}\n```"
	got := stripCodeFence(in)
	want := `{"a":1}`
	if got != want {
		t.Errorf("stripCodeFence = %q, want %q", got, want)
	}
}

// TestStripCodeFence_TrailingWhitespace tolerates whitespace after the
// closing fence.
func TestStripCodeFence_TrailingWhitespace(t *testing.T) {
	in := "```json\n{\"a\":1}\n```\n\n"
	got := stripCodeFence(in)
	want := `{"a":1}`
	if got != want {
		t.Errorf("stripCodeFence = %q, want %q", got, want)
	}
}

// TestParseRepairResponse_FencedWithLanguageTag is the primary c112
// regression guard: the repair LLM wrapped its response in
// ` ```json ... ``` ` despite the system prompt forbidding markdown
// fences. parseRepairResponse must still produce a populated
// RepairOutcome.
func TestParseRepairResponse_FencedWithLanguageTag(t *testing.T) {
	raw := "```json\n{\n  \"repaired_args\": {\"type\":\"report-card\",\"data\":{\"title\":\"X\",\"metrics\":[{\"label\":\"a\",\"value\":1}]}},\n  \"missing_required\": [],\n  \"lesson_hint\": \"report-card requires metrics, not sections\"\n}\n```"
	out, err := parseRepairResponse(raw)
	if err != nil {
		t.Fatalf("parseRepairResponse failed on fenced JSON: %v", err)
	}
	if !out.HasRepair() {
		t.Fatalf("expected HasRepair() true, got %+v", out)
	}
	if out.LessonHint != "report-card requires metrics, not sections" {
		t.Errorf("unexpected lesson hint: %q", out.LessonHint)
	}
	if out.RepairedArgs["type"] != "report-card" {
		t.Errorf("repaired_args.type = %v, want \"report-card\"", out.RepairedArgs["type"])
	}
}

// TestParseRepairResponse_FencedWithPreamble covers the case where the
// LLM emits a chat-style preamble before the fenced JSON.
func TestParseRepairResponse_FencedWithPreamble(t *testing.T) {
	raw := "Here is the repair:\n```json\n{\"repaired_args\": {\"a\":1}, \"missing_required\": [], \"lesson_hint\": \"ok\"}\n```"
	out, err := parseRepairResponse(raw)
	if err != nil {
		t.Fatalf("parseRepairResponse failed: %v", err)
	}
	if !out.HasRepair() {
		t.Fatalf("expected HasRepair, got %+v", out)
	}
}

// TestParseRepairResponse_NoJSONObject_ErrorPreserved guards against a
// regression where the new fence-stripping path silently hides a
// genuinely malformed response. The "no JSON object" error must still
// fire when there is no `{` anywhere in the input.
func TestParseRepairResponse_NoJSONObject_ErrorPreserved(t *testing.T) {
	_, err := parseRepairResponse("I cannot reshape this — sorry.")
	if err == nil {
		t.Fatalf("expected error for response with no JSON object")
	}
	if !strings.Contains(err.Error(), "no JSON object") {
		t.Errorf("expected 'no JSON object' in error, got %v", err)
	}
}

// TestParseRepairResponse_PlainObject_RegressionGuard confirms the
// happy path still works after the stripCodeFence wrapper was
// introduced.
func TestParseRepairResponse_PlainObject_RegressionGuard(t *testing.T) {
	raw := `{"repaired_args": {"a":1}, "missing_required": [], "lesson_hint": "ok"}`
	out, err := parseRepairResponse(raw)
	if err != nil {
		t.Fatalf("parseRepairResponse failed on plain JSON: %v", err)
	}
	if !out.HasRepair() {
		t.Fatalf("expected HasRepair, got %+v", out)
	}
}

// TestResolveRepairMaxTokens_Default returns the package default when
// the caller leaves RepairOptions.MaxTokens at zero. Guards the
// CW-20260429-0028 default-resolution rule.
func TestResolveRepairMaxTokens_Default(t *testing.T) {
	got := resolveRepairMaxTokens(RepairOptions{})
	if got != DefaultRepairMaxTokens {
		t.Fatalf("resolveRepairMaxTokens(zero opts) = %d, want %d", got, DefaultRepairMaxTokens)
	}
}

// TestResolveRepairMaxTokens_Override honors a positive caller-supplied
// MaxTokens. CW-20260429-0028.
func TestResolveRepairMaxTokens_Override(t *testing.T) {
	const want = 8192
	got := resolveRepairMaxTokens(RepairOptions{MaxTokens: want})
	if got != want {
		t.Fatalf("resolveRepairMaxTokens(MaxTokens=%d) = %d, want %d", want, got, want)
	}
}

// TestResolveRepairMaxTokens_NegativeFallsBack treats a negative value
// as "unset" and falls back to the default; the resolver must never
// return ≤ 0 (the caller would otherwise propagate an invalid cap to
// the provider).
func TestResolveRepairMaxTokens_NegativeFallsBack(t *testing.T) {
	got := resolveRepairMaxTokens(RepairOptions{MaxTokens: -1})
	if got != DefaultRepairMaxTokens {
		t.Fatalf("resolveRepairMaxTokens(MaxTokens=-1) = %d, want %d", got, DefaultRepairMaxTokens)
	}
}

// TestDefaultRepairMaxTokens_Reasonable guards against a regression
// where a future edit drops DefaultRepairMaxTokens to a value too small
// to fit a typical repair payload (repaired_args + missing_required +
// lesson_hint). CW-20260429-0028 introduced this constant to fix the
// c113 truncation symptom; if a future change tightens it below 1024
// it should be a deliberate decision tied to an evidence ticket.
func TestDefaultRepairMaxTokens_Reasonable(t *testing.T) {
	const floor = 1024
	if DefaultRepairMaxTokens < floor {
		t.Fatalf("DefaultRepairMaxTokens=%d is below the %d floor required for a typical repair payload; see CW-20260429-0028", DefaultRepairMaxTokens, floor)
	}
}

// TestRepairSystemPrompt_AntiMarkdown asserts the repair system prompt
// carries the load-bearing anti-markdown phrase introduced in
// CW-20260429-0028. Guards against future prompt edits silently
// dropping the constraint that caused the c113 truncation symptom.
func TestRepairSystemPrompt_AntiMarkdown(t *testing.T) {
	prompt := repairSystemPrompt()
	const sig = "single bare JSON object"
	if !strings.Contains(prompt, sig) {
		t.Fatalf("repair system prompt missing anti-markdown signature %q", sig)
	}
	if !strings.Contains(prompt, "Do NOT wrap it in markdown code fences") {
		t.Errorf("repair system prompt missing explicit fence-forbid wording")
	}
}

// TestRepair_HonorsCallerMaxTokens is a smoke test confirming Repair
// accepts a caller-supplied MaxTokens override without erroring.
// Repair() threads this value into provider.ChatRequest.MaxTokens; this
// test keeps the recover-package surface covered without changing its
// existing smoke-test scope.
func TestRepair_HonorsCallerMaxTokens(t *testing.T) {
	stub := &stubProvider{response: `{"repaired_args": {"a":1}, "missing_required": [], "lesson_hint": "ok"}`}
	_, err := Repair(context.Background(), newRecoverable(), RepairOptions{
		Provider:  stub,
		MaxTokens: 8192,
	})
	if err != nil {
		t.Fatalf("Repair with MaxTokens override failed: %v", err)
	}
	if stub.calls != 1 {
		t.Errorf("expected exactly one Complete call, got %d", stub.calls)
	}
}
