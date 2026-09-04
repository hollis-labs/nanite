package selftools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/learnings"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/memory"
)

// rememberStubStore captures Capture and Recall calls for assertions.
// Mirrors the package-internal stubStore in internal/learnings — kept
// here so the mcp tests do not import learnings test files.
type rememberStubStore struct {
	stored      []memory.Memory
	storeErr    error
	recallReply []memory.Memory
	recallErr   error
}

func (s *rememberStubStore) Store(_ context.Context, m memory.Memory) error {
	if s.storeErr != nil {
		return s.storeErr
	}
	s.stored = append(s.stored, m)
	return nil
}

func (s *rememberStubStore) Recall(_ context.Context, _ memory.RecallOpts) ([]memory.Memory, error) {
	if s.recallErr != nil {
		return nil, s.recallErr
	}
	return s.recallReply, nil
}

// newRememberSelfTools wires a SelfToolsTransport with a stub
// LearningStore so callRemember executes end-to-end without touching
// real Tesseract.
func newRememberSelfTools(t *testing.T, store learnings.LearningStore) *SelfToolsTransport {
	t.Helper()
	st := newSelfTools(t)
	st.LearningRecorder = learnings.NewRecorder(store)
	st.LearningRecaller = learnings.NewRecaller(store)
	return st
}

// rememberResp is the on-the-wire success shape for lesson_capture.
type rememberResp struct {
	MemoryID  string `json:"memory_id"`
	Namespace string `json:"namespace"`
}

// TestRememberToolDefinition_Surface pins the registered surface so a
// future refactor that drops the tool from selfToolDefinitions or
// mis-renames it surfaces here loudly.
func TestRememberToolDefinition_Surface(t *testing.T) {
	defs := selfToolDefinitions()
	var found *mcp.Tool
	for i := range defs {
		if defs[i].Name == "lesson_capture" {
			found = &defs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("lesson_capture not in selfToolDefinitions()")
	}
	if found.InputSchema == nil {
		t.Fatal("lesson_capture missing InputSchema")
	}
	required, _ := found.InputSchema["required"].([]string)
	want := map[string]bool{"scope": false, "subject": false, "hint": false}
	for _, r := range required {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for k, present := range want {
		if !present {
			t.Errorf("lesson_capture required-set missing %q (got %v)", k, required)
		}
	}
}

// TestRemember_HappyPath_ToolUse is the headline acceptance check from
// the ticket: lesson_capture(scope='tool_use', subject='card_show',
// hint='...') writes through to Tesseract with the right tags + namespace
// and returns the memory_id.
func TestRemember_HappyPath_ToolUse(t *testing.T) {
	store := &rememberStubStore{}
	st := newRememberSelfTools(t, store)

	res, err := st.CallTool(context.Background(), "lesson_capture", map[string]any{
		"scope":           "tool_use",
		"subject":         "card_show",
		"hint":            "report-card requires {title, metrics}; sections are not allowed",
		"source_event_id": "evt-c107",
		"session_id":      "sess-1",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected IsError=false, got: %s", res.Content[0].Text)
	}

	var out rememberResp
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, res.Content[0].Text)
	}
	if out.Namespace != "user/default/memory/learnings" {
		t.Errorf("namespace = %q, want user/default/memory/learnings", out.Namespace)
	}
	if out.MemoryID == "" {
		t.Error("memory_id must be non-empty")
	}

	if len(store.stored) != 1 {
		t.Fatalf("expected 1 store write, got %d", len(store.stored))
	}
	got := store.stored[0]
	if !containsTag(got.Tags, "tool:card_show") {
		t.Errorf("missing tool tag in %v", got.Tags)
	}
	if !containsTag(got.Tags, "source:evt-c107") {
		t.Errorf("missing source tag in %v", got.Tags)
	}
	if !containsTag(got.Tags, "learning") {
		t.Errorf("missing learning tag in %v", got.Tags)
	}

	// Telemetry counter must have ticked for the tool_use scope on this session.
	tu, _, _ := st.RememberCounters.SnapshotSession("sess-1")
	if tu != 1 {
		t.Errorf("tool_use counter = %d, want 1", tu)
	}
}

// TestRemember_MissingFields_ReturnStructuredErrors covers the
// validate-at-the-boundary contract.
func TestRemember_MissingFields_ReturnStructuredErrors(t *testing.T) {
	st := newRememberSelfTools(t, &rememberStubStore{})

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "missing scope",
			args: map[string]any{"subject": "x", "hint": "y"},
			want: "invalid scope",
		},
		{
			name: "missing subject",
			args: map[string]any{"scope": "tool_use", "hint": "y"},
			want: "subject is required",
		},
		{
			name: "missing hint",
			args: map[string]any{"scope": "tool_use", "subject": "x"},
			want: "hint is required",
		},
		{
			name: "blank hint",
			args: map[string]any{"scope": "tool_use", "subject": "x", "hint": "   "},
			want: "hint is required",
		},
		{
			name: "invalid scope value",
			args: map[string]any{"scope": "workspace", "subject": "x", "hint": "y"},
			want: "invalid scope",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := st.CallTool(context.Background(), "lesson_capture", c.args)
			if err != nil {
				t.Fatalf("CallTool error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected IsError=true, got body: %s", res.Content[0].Text)
			}
			if !strings.Contains(res.Content[0].Text, c.want) {
				t.Errorf("expected error to mention %q, got: %s", c.want, res.Content[0].Text)
			}
		})
	}
}

// TestRemember_NilRecorder_ClearError pins the wiring-miss diagnostic
// message — silent no-op would let bad lessons accumulate undiagnosed.
func TestRemember_NilRecorder_ClearError(t *testing.T) {
	st := newSelfTools(t)
	// LearningRecorder intentionally nil
	res, err := st.CallTool(context.Background(), "lesson_capture", map[string]any{
		"scope": "tool_use", "subject": "x", "hint": "y",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(res.Content[0].Text, "LearningRecorder is not configured") {
		t.Errorf("expected misconfig error, got: %s", res.Content[0].Text)
	}
}

// TestRemember_StoreFailure_SurfacesAsErrorResult ensures Tesseract
// transport failures surface as structured errors (so the agent can
// react) rather than crashing.
func TestRemember_StoreFailure_SurfacesAsErrorResult(t *testing.T) {
	store := &rememberStubStore{storeErr: errors.New("tesseract closed")}
	st := newRememberSelfTools(t, store)
	res, err := st.CallTool(context.Background(), "lesson_capture", map[string]any{
		"scope": "tool_use", "subject": "x", "hint": "y",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true on store failure")
	}
	if !strings.Contains(res.Content[0].Text, "tesseract closed") {
		t.Errorf("error must wrap underlying store error; got: %s", res.Content[0].Text)
	}
}

// TestRecallToolLearnings_Bridges verifies the SelfToolsTransport
// helper used by the slot-assembly path returns hints from the
// LearningRecaller.
func TestRecallToolLearnings_Bridges(t *testing.T) {
	store := &rememberStubStore{
		recallReply: []memory.Memory{
			{Summary: "report-card requires metrics", Tags: []string{"learning", "tool:card_show"}},
		},
	}
	st := newRememberSelfTools(t, store)
	hints := st.RecallToolLearnings(context.Background(), "default", "card_show")
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}
	if hints[0].Summary != "report-card requires metrics" {
		t.Errorf("unexpected hint: %s", hints[0].Summary)
	}
}

// TestRecallToolLearnings_NilRecaller returns nil rather than panicking
// when the wire is missing.
func TestRecallToolLearnings_NilRecaller(t *testing.T) {
	st := newSelfTools(t)
	hints := st.RecallToolLearnings(context.Background(), "default", "card_show")
	if hints != nil {
		t.Errorf("expected nil hints from nil recaller, got %v", hints)
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// TestDescribe_SurfacesPriorLearnings is the headline acceptance check
// for the recall integration: tool_describe with prior tool-use
// learnings on file includes them in the describe payload as
// `prior_learnings`. This is the "lesson recall during similar tool
// selection" surface from the lens.
func TestDescribe_SurfacesPriorLearnings(t *testing.T) {
	store := &rememberStubStore{
		recallReply: []memory.Memory{
			{Summary: "report-card requires metrics, not sections", Confidence: 0.85,
				Tags: []string{"learning", "tool:card_show"}},
		},
	}
	st := newRememberSelfTools(t, store)

	res, err := st.CallTool(context.Background(), "tool_describe", map[string]any{
		"name": "card_show",
	})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if res.IsError {
		t.Fatalf("describe returned error: %s", res.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("describe JSON: %v", err)
	}
	priors, ok := out["prior_learnings"].([]any)
	if !ok || len(priors) == 0 {
		t.Fatalf("expected prior_learnings to be surfaced, got: %v (full=%v)", out["prior_learnings"], out)
	}
	first, _ := priors[0].(map[string]any)
	if hint, _ := first["hint"].(string); !strings.Contains(hint, "report-card requires metrics") {
		t.Errorf("unexpected hint shape: %v", first)
	}
}

// TestDescribe_NoPriorLearningsField_WhenAbsent verifies the
// prior_learnings field is omitted (rather than rendered as an empty
// array) when there are no captured lessons — keeps the describe
// output uncluttered for fresh tools.
func TestDescribe_NoPriorLearningsField_WhenAbsent(t *testing.T) {
	store := &rememberStubStore{} // no recall reply
	st := newRememberSelfTools(t, store)
	res, err := st.CallTool(context.Background(), "tool_describe", map[string]any{
		"name": "card_show",
	})
	if err != nil || res.IsError {
		t.Fatalf("describe failed: err=%v body=%s", err, res.Content[0].Text)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &out)
	if _, present := out["prior_learnings"]; present {
		t.Errorf("prior_learnings should be omitted when empty, got: %v", out["prior_learnings"])
	}
}
