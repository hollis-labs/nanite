package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/envelope"
	"github.com/hollis-labs/nanite/internal/store"
)

// repairStubProvider is a minimal provider.Provider that returns a
// canned repair response. Captures the model + system prompt for
// assertions.
type repairStubProvider struct {
	response string
	err      error

	calls            int
	lastModel        string
	lastSystemPrompt string
	lastUserContent  string
}

func (s *repairStubProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (s *repairStubProvider) StreamChat(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, errors.New("repair stub does not stream")
}

func (s *repairStubProvider) Complete(_ context.Context, req provider.ChatRequest) (string, error) {
	s.calls++
	s.lastModel = req.Model
	s.lastSystemPrompt = req.SystemPrompt
	if len(req.Messages) > 0 {
		s.lastUserContent = req.Messages[0].Content
	}
	if s.err != nil {
		return "", s.err
	}
	return s.response, nil
}

// settingsReaderStub implements UserSettingsReader with a canned pref.
type settingsReaderStub struct {
	pref string
}

func (s *settingsReaderStub) GetUserSettings() (*store.UserSettings, error) {
	return &store.UserSettings{AutoRepairPref: s.pref}, nil
}

// makeRepairableError returns a real envelope ValidationError for a
// payload missing the required `metrics` field on report-card. Mirrors
// the C1 acceptance test pattern (tool_recover_test.go).
func makeRepairableError() error {
	return envelope.ValidateData("report-card", map[string]any{"title": "X", "sections": []any{"a"}})
}

// transportRecorder records calls for retry assertions.
type transportRecorder struct {
	calls []map[string]any
	// errOn[n] returns errOnErr for the (n+1)-th call; an entry of nil
	// means "succeed and return the success string".
	errOn   []error
	success string
}

func (r *transportRecorder) hook() func(ctx context.Context, agentID, toolName string, input map[string]any) (string, error) {
	return func(_ context.Context, _ string, _ string, input map[string]any) (string, error) {
		idx := len(r.calls)
		// Capture the input by value (serialize-then-restore so later
		// mutations don't bleed into earlier captures).
		raw, _ := json.Marshal(input)
		var copied map[string]any
		_ = json.Unmarshal(raw, &copied)
		r.calls = append(r.calls, copied)
		if idx < len(r.errOn) && r.errOn[idx] != nil {
			return "", r.errOn[idx]
		}
		return r.success, nil
	}
}

// TestExecute_Repair_Success_WrapsResultWithRepairNote is the C2
// acceptance gate: a `nanite_show_card` call with a missing
// required field is repaired by the LLM, retried once, and the
// success result comes back wrapped with a repair_note.
func TestExecute_Repair_Success_WrapsResultWithRepairNote(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":{"type":"report-card","data":{"title":"X","metrics":[{"label":"a","value":1}]}},"missing_required":[],"lesson_hint":"report-card requires metrics, not sections"}`}
	transport := &transportRecorder{
		errOn:   []error{makeRepairableError(), nil},
		success: `{"ok":true,"id":"card-1"}`,
	}

	svc := &toolServiceImpl{
		repairConfig: &RepairConfig{
			Provider: llm,
			Model:    "claude-haiku-4-5",
			Timeout:  500 * time.Millisecond,
		},
		transportHook: transport.hook(),
	}

	args := map[string]any{"type": "report-card", "data": map[string]any{"title": "X", "sections": []any{"a"}}}
	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", res.Output)
	}
	// Two transport calls: original + retry.
	if len(transport.calls) != 2 {
		t.Fatalf("expected 2 transport calls (original + retry), got %d", len(transport.calls))
	}
	// Output is the wrapped JSON envelope with repair_note + result.
	var wrapper map[string]any
	if err := json.Unmarshal([]byte(res.Output), &wrapper); err != nil {
		t.Fatalf("expected JSON envelope, got %q (decode err %v)", res.Output, err)
	}
	note, ok := wrapper["repair_note"].(map[string]any)
	if !ok {
		t.Fatalf("missing repair_note in output: %+v", wrapper)
	}
	if note["lesson_hint"] != "report-card requires metrics, not sections" {
		t.Errorf("unexpected lesson_hint: %v", note["lesson_hint"])
	}
	if note["tool"] != "nanite_show_card" {
		t.Errorf("unexpected tool: %v", note["tool"])
	}
	if note["kind"] != "schema_validation" {
		t.Errorf("unexpected kind: %v", note["kind"])
	}
	repaired, _ := note["repaired_args"].(map[string]any)
	if repaired == nil {
		t.Errorf("missing repaired_args in repair_note")
	}
	// result is embedded as parsed JSON.
	if _, ok := wrapper["result"]; !ok {
		t.Errorf("missing 'result' key in wrapped output: %+v", wrapper)
	}
	// LLM was called exactly once.
	if llm.calls != 1 {
		t.Errorf("expected exactly 1 LLM call, got %d", llm.calls)
	}
}

// TestExecute_Repair_MissingRequired_ReturnsStructuredEnvelope covers
// the no-fabrication path. The repair LLM declines to invent a value
// → no retry, no compounded error, and the agent gets a missing_required
// envelope.
func TestExecute_Repair_MissingRequired_ReturnsStructuredEnvelope(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":null,"missing_required":["metrics"],"lesson_hint":"metrics is required"}`}
	transport := &transportRecorder{
		errOn: []error{makeRepairableError()},
	}

	svc := &toolServiceImpl{
		repairConfig: &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	args := map[string]any{"type": "report-card", "data": map[string]any{"title": "X"}}
	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on missing-required path")
	}
	// Only the original transport call — no retry.
	if len(transport.calls) != 1 {
		t.Fatalf("expected 1 transport call (no retry), got %d", len(transport.calls))
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Output), &env); err != nil {
		t.Fatalf("expected JSON envelope, got %q", res.Output)
	}
	missing, ok := env["missing_required"].([]any)
	if !ok || len(missing) != 1 || missing[0] != "metrics" {
		t.Errorf("expected missing_required=['metrics'], got %v", env["missing_required"])
	}
	if env["kind"] != "schema_validation" {
		t.Errorf("expected kind=schema_validation, got %v", env["kind"])
	}
}

// TestExecute_Repair_RetryFails_ReturnsOriginalEnvelope verifies the
// "no compounded errors" rule. When the retry tool call fails, the
// agent sees the C1 envelope from the FIRST call, not a fresh
// classification of the second.
func TestExecute_Repair_RetryFails_ReturnsOriginalEnvelope(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":{"x":1},"missing_required":[],"lesson_hint":"reshape"}`}
	transport := &transportRecorder{
		errOn: []error{makeRepairableError(), errors.New("upstream is on fire")},
	}

	svc := &toolServiceImpl{
		repairConfig:  &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on retry failure")
	}
	// Two transport calls (original + retry).
	if len(transport.calls) != 2 {
		t.Errorf("expected 2 transport calls, got %d", len(transport.calls))
	}
	// Output is the C1 envelope (no repair_note).
	if strings.Contains(res.Output, "repair_note") {
		t.Errorf("retry-failure path must NOT produce a repair_note: %s", res.Output)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Output), &env); err != nil {
		t.Fatalf("expected JSON envelope, got %q", res.Output)
	}
	if env["recoverable_error"] != true {
		t.Errorf("expected the original C1 envelope (recoverable_error=true), got %+v", env)
	}
}

// TestExecute_Repair_IterationCap_Hard verifies a deliberately
// pathological loop (every retry fails recoverably) does NOT cascade
// into multiple repair attempts. There must be exactly one LLM call
// and at most one retry.
func TestExecute_Repair_IterationCap_Hard(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":{"x":1},"missing_required":[],"lesson_hint":"reshape"}`}
	// Both calls return a recoverable error. If the iteration cap were
	// soft, the harness would loop. It must NOT.
	transport := &transportRecorder{
		errOn: []error{makeRepairableError(), makeRepairableError(), makeRepairableError()},
	}

	svc := &toolServiceImpl{
		repairConfig:  &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	_, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(transport.calls) != 2 {
		t.Errorf("iteration cap broken: expected 2 transport calls, got %d", len(transport.calls))
	}
	if llm.calls != 1 {
		t.Errorf("iteration cap broken: expected 1 LLM call, got %d", llm.calls)
	}
}

// TestExecute_Repair_TimeoutFallthrough verifies a repair LLM error
// (timeout, transport, parse) returns the C1 envelope unchanged with
// no retry attempt.
func TestExecute_Repair_TimeoutFallthrough(t *testing.T) {
	llm := &repairStubProvider{err: errors.New("provider timeout")}
	transport := &transportRecorder{
		errOn: []error{makeRepairableError()},
	}

	svc := &toolServiceImpl{
		repairConfig:  &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on repair-timeout fall-through")
	}
	if len(transport.calls) != 1 {
		t.Errorf("expected exactly 1 transport call (no retry on timeout), got %d", len(transport.calls))
	}
	// C1 envelope is returned.
	if !strings.Contains(res.Output, "recoverable_error") {
		t.Errorf("expected the C1 envelope, got %q", res.Output)
	}
	if strings.Contains(res.Output, "repair_note") {
		t.Errorf("timeout fall-through must not produce a repair_note")
	}
}

// TestExecute_Repair_EnvDisabled_BypassesPipeline confirms
// NANITE_AUTO_REPAIR=false short-circuits the entire pipeline and
// returns the C1 envelope directly. No LLM call, no retry.
func TestExecute_Repair_EnvDisabled_BypassesPipeline(t *testing.T) {
	t.Setenv("NANITE_AUTO_REPAIR", "false")

	llm := &repairStubProvider{response: `{"repaired_args":{},"missing_required":[],"lesson_hint":""}`}
	transport := &transportRecorder{
		errOn: []error{makeRepairableError()},
	}

	svc := &toolServiceImpl{
		repairConfig:  &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on env-disabled path")
	}
	if llm.calls != 0 {
		t.Errorf("env disabled but LLM was called %d times", llm.calls)
	}
	if len(transport.calls) != 1 {
		t.Errorf("expected exactly 1 transport call, got %d", len(transport.calls))
	}
	if strings.Contains(res.Output, "repair_note") {
		t.Errorf("env-disabled path must not produce a repair_note")
	}
}

// TestExecute_Repair_UserPrefNever_BypassesPipeline confirms the
// auto_repair_pref="never" user setting short-circuits the pipeline.
func TestExecute_Repair_UserPrefNever_BypassesPipeline(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":{},"missing_required":[],"lesson_hint":""}`}
	transport := &transportRecorder{
		errOn: []error{makeRepairableError()},
	}

	svc := &toolServiceImpl{
		repairConfig: &RepairConfig{
			Provider:       llm,
			SettingsReader: &settingsReaderStub{pref: "never"},
		},
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on user-pref=never path")
	}
	if llm.calls != 0 {
		t.Errorf("user pref disabled but LLM was called %d times", llm.calls)
	}
}

// TestExecute_Repair_NoRepairConfig_PreservesC1Envelope confirms a
// service without a repair config behaves identically to pre-C2: the
// C1 envelope is returned with no LLM spend.
func TestExecute_Repair_NoRepairConfig_PreservesC1Envelope(t *testing.T) {
	transport := &transportRecorder{
		errOn: []error{makeRepairableError()},
	}

	svc := &toolServiceImpl{
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true")
	}
	if !strings.Contains(res.Output, "recoverable_error") {
		t.Errorf("expected the C1 envelope, got %q", res.Output)
	}
}

// TestExecute_Repair_NonRecoverableErrors_PreserveLegacyShape covers
// the negative half: an auth/permission error must continue to flow
// through with the byte-stable `Error: <prose>` shape, even with a
// repair config wired.
func TestExecute_Repair_NonRecoverableErrors_PreserveLegacyShape(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":{},"missing_required":[],"lesson_hint":""}`}
	authErr := errors.New("permission denied: tool not permitted")
	transport := &transportRecorder{errOn: []error{authErr}}

	svc := &toolServiceImpl{
		repairConfig:  &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "shell_exec", nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true on auth path")
	}
	if !strings.HasPrefix(res.Output, "Error: ") {
		t.Errorf("auth path must keep the `Error: ...` prefix, got %q", res.Output)
	}
	if llm.calls != 0 {
		t.Errorf("non-recoverable error must not invoke the repair LLM (got %d calls)", llm.calls)
	}
}

// TestExecute_Repair_TransportSuccess_Unchanged confirms the happy
// path: when the original tool call succeeds, no repair pipeline runs.
func TestExecute_Repair_TransportSuccess_Unchanged(t *testing.T) {
	llm := &repairStubProvider{response: `{"repaired_args":{},"missing_required":[]}`}
	transport := &transportRecorder{success: `{"ok":true}`}

	svc := &toolServiceImpl{
		repairConfig:  &RepairConfig{Provider: llm},
		transportHook: transport.hook(),
	}

	res, err := svc.Execute(context.Background(), "agent-1", "nanite_show_card", nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Errorf("expected success, got %s", res.Output)
	}
	if res.Output != `{"ok":true}` {
		t.Errorf("output altered on success path: got %q", res.Output)
	}
	if llm.calls != 0 {
		t.Errorf("LLM should not be called on success, got %d calls", llm.calls)
	}
}

// TestAutoRepairEnvEnabled_Variants exercises the env-var gate.
func TestAutoRepairEnvEnabled_Variants(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", true},
		{"1", true},
		{"true", true},
		{"on", true},
		{"yes", true},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"no", false},
		{"NO", false},
		{"off", false},
		{"OFF", false},
	}
	for _, c := range cases {
		t.Run(c.val, func(t *testing.T) {
			t.Setenv("NANITE_AUTO_REPAIR", c.val)
			if got := autoRepairEnvEnabled(); got != c.want {
				t.Errorf("autoRepairEnvEnabled() with %q = %v, want %v", c.val, got, c.want)
			}
		})
	}
}
