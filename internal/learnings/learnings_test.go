package learnings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/memory"
)

// stubStore is a minimal in-memory LearningStore used by every test in
// this file. Capturing the inputs lets each test assert on the exact
// shape of the Vanta call without needing a real Conduit instance.
type stubStore struct {
	stored      []memory.Memory
	storeErr    error
	recallReply []memory.Memory
	recallErr   error
	recallOpts  memory.RecallOpts
}

func (s *stubStore) Store(_ context.Context, m memory.Memory) error {
	if s.storeErr != nil {
		return s.storeErr
	}
	s.stored = append(s.stored, m)
	return nil
}

func (s *stubStore) Recall(_ context.Context, opts memory.RecallOpts) ([]memory.Memory, error) {
	s.recallOpts = opts
	if s.recallErr != nil {
		return nil, s.recallErr
	}
	return s.recallReply, nil
}

// TestNamespace_ToolUse pins the user-scoped namespace shape that
// tool_use lessons land in. The tool identity rides in tags +
// memory_key, not in the namespace, because Vanta's strict three-form
// contract forbids extra segments under /memory.
func TestNamespace_ToolUse(t *testing.T) {
	got := Namespace(ScopeToolUse, "alice", "card_show")
	want := "user/alice/memory"
	if got != want {
		t.Errorf("Namespace tool_use mismatch: got %q want %q", got, want)
	}
}

// TestNamespace_Project verifies the project namespace lands in
// Vanta's documented "user/{user}/project/{id}/memory" form.
func TestNamespace_Project(t *testing.T) {
	got := Namespace(ScopeProject, "alice", "nanite")
	want := "user/alice/project/nanite/memory"
	if got != want {
		t.Errorf("Namespace project mismatch: got %q want %q", got, want)
	}
}

// TestNamespace_Session verifies the session namespace lands in
// Vanta's "user/{user}/session/{id}/memory" form.
func TestNamespace_Session(t *testing.T) {
	got := Namespace(ScopeSession, "alice", "sess-9")
	want := "user/alice/session/sess-9/memory"
	if got != want {
		t.Errorf("Namespace session mismatch: got %q want %q", got, want)
	}
}

// TestNamespace_DefaultUser verifies an empty userID falls back to the
// "default" namespace root, matching the rest of the memory layer.
func TestNamespace_DefaultUser(t *testing.T) {
	got := Namespace(ScopeProject, "", "PRJ-1")
	want := "user/default/project/prj-1/memory"
	if got != want {
		t.Errorf("Namespace default user mismatch: got %q want %q", got, want)
	}
}

// TestNamespace_SubjectSanitization confirms project/session subjects
// with awkward characters get normalised so Vanta's segment constraint
// never trips. (tool_use does not embed the subject in the namespace,
// so it's not exercised here.)
func TestNamespace_SubjectSanitization(t *testing.T) {
	cases := []struct {
		scope Scope
		in    string
		want  string
	}{
		{ScopeProject, "My Project", "user/default/project/my_project/memory"},
		{ScopeSession, "-leading-dash-", "user/default/session/leading-dash/memory"},
		{ScopeProject, "!!!", "user/default/project/_/memory"},
	}
	for _, c := range cases {
		got := Namespace(c.scope, "", c.in)
		if got != c.want {
			t.Errorf("Namespace(%s, %q) = %q, want %q", c.scope, c.in, got, c.want)
		}
	}
}

// TestDeriveMemoryKey_Deterministic verifies the same (scope, subject,
// hint) always maps to the same key (lesson dedup at the key level —
// see ticket scope).
func TestDeriveMemoryKey_Deterministic(t *testing.T) {
	hint := "report-card requires {title, metrics}; sections are not allowed"
	k1 := DeriveMemoryKey(ScopeToolUse, "card_show", hint)
	k2 := DeriveMemoryKey(ScopeToolUse, "card_show", hint)
	if k1 != k2 {
		t.Errorf("DeriveMemoryKey is non-deterministic: %q vs %q", k1, k2)
	}
	if k1 == "" {
		t.Error("DeriveMemoryKey returned empty key")
	}
	// must not contain disallowed Vanta key chars
	for _, r := range k1 {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if !ok {
			t.Errorf("memory_key contains illegal char %q in %q", r, k1)
		}
	}
}

// TestDeriveMemoryKey_DifferentToolsDoNotCollide guards against the
// shared-namespace risk: same hint sentence, different tool, must
// produce different keys so Vanta doesn't overwrite the wrong row.
func TestDeriveMemoryKey_DifferentToolsDoNotCollide(t *testing.T) {
	hint := "shared-shape lesson"
	a := DeriveMemoryKey(ScopeToolUse, "card_show", hint)
	b := DeriveMemoryKey(ScopeToolUse, "giphy_search", hint)
	if a == b {
		t.Errorf("DeriveMemoryKey collided across tools: %q == %q", a, b)
	}
}

// TestDeriveMemoryKey_FitsVantaSegmentBudget pins the truncation
// invariant — a generously-long hint must still produce a key within
// Vanta's per-segment cap so writes don't fail at the boundary.
func TestDeriveMemoryKey_FitsVantaSegmentBudget(t *testing.T) {
	long := strings.Repeat("longhint-", 30) // ~270 chars
	k := DeriveMemoryKey(ScopeToolUse, "card_show", long)
	if len(k) > MaxMemoryKeyLen {
		t.Errorf("memory_key length %d exceeds budget %d (key=%q)", len(k), MaxMemoryKeyLen, k)
	}
}

// TestCapture_HappyPath_ToolUse is the headline acceptance case from
// the ticket: capture a lesson under tool_use/<tool> with the right
// namespace, tags, confidence, and status.
func TestCapture_HappyPath_ToolUse(t *testing.T) {
	store := &stubStore{}
	rec := NewRecorder(store)

	out, err := rec.Capture(context.Background(), CaptureInput{
		Scope:         ScopeToolUse,
		Subject:       "card_show",
		Hint:          "report-card requires {title, metrics}; sections are not allowed",
		SourceEventID: "evt-c107",
		SessionID:     "sess-1",
		UserID:        "alice",
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if out == nil {
		t.Fatal("Capture returned nil outcome on success")
	}
	if out.Namespace != "user/alice/memory" {
		t.Errorf("namespace mismatch: %s", out.Namespace)
	}
	if out.MemoryID == "" {
		t.Error("memory_id must not be empty on success")
	}

	if len(store.stored) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(store.stored))
	}
	got := store.stored[0]
	if got.Namespace != out.Namespace {
		t.Errorf("stored namespace mismatch: %s != %s", got.Namespace, out.Namespace)
	}
	if got.Confidence != DefaultConfidence {
		t.Errorf("confidence = %v, want %v", got.Confidence, DefaultConfidence)
	}
	if got.Status != DefaultStatus {
		t.Errorf("status = %q, want %q", got.Status, DefaultStatus)
	}
	if got.Origin != "feedback" {
		t.Errorf("origin = %q, want feedback", got.Origin)
	}
	if got.Trigger != "explicit" {
		t.Errorf("trigger = %q, want explicit", got.Trigger)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("session_id = %q", got.SessionID)
	}
	// Tag invariants: order matters for human review tools.
	wantTags := []string{
		"learning", "self_healed", "captured_during_session",
		"tool_use", "tool:card_show", "source:evt-c107",
	}
	if len(got.Tags) != len(wantTags) {
		t.Fatalf("tags len = %d, want %d (got=%v)", len(got.Tags), len(wantTags), got.Tags)
	}
	for i := range wantTags {
		if got.Tags[i] != wantTags[i] {
			t.Errorf("tag[%d] = %q, want %q", i, got.Tags[i], wantTags[i])
		}
	}
}

// TestCapture_HintRequired covers the validation guard for blank hints.
func TestCapture_HintRequired(t *testing.T) {
	rec := NewRecorder(&stubStore{})
	_, err := rec.Capture(context.Background(), CaptureInput{
		Scope:   ScopeToolUse,
		Subject: "card_show",
		Hint:    "   ",
	})
	if err == nil {
		t.Fatal("expected error for blank hint")
	}
	if !strings.Contains(err.Error(), "hint") {
		t.Errorf("expected hint-related error, got: %v", err)
	}
}

// TestCapture_SubjectRequired verifies tool_use without a tool name
// fails — the namespace would collapse to a useless bucket otherwise.
func TestCapture_SubjectRequired(t *testing.T) {
	rec := NewRecorder(&stubStore{})
	_, err := rec.Capture(context.Background(), CaptureInput{
		Scope: ScopeToolUse,
		Hint:  "valid hint",
	})
	if err == nil {
		t.Fatal("expected error when subject is empty for tool_use scope")
	}
	if !strings.Contains(err.Error(), "subject") {
		t.Errorf("expected subject-related error, got: %v", err)
	}
}

// TestCapture_InvalidScope rejects unknown scopes.
func TestCapture_InvalidScope(t *testing.T) {
	rec := NewRecorder(&stubStore{})
	_, err := rec.Capture(context.Background(), CaptureInput{
		Scope:   Scope("workspace"), // not in v1 vocabulary
		Subject: "x",
		Hint:    "y",
	})
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

// TestCapture_NilStore returns a clear error rather than panicking.
func TestCapture_NilStore(t *testing.T) {
	rec := NewRecorder(nil)
	_, err := rec.Capture(context.Background(), CaptureInput{
		Scope:   ScopeToolUse,
		Subject: "card_show",
		Hint:    "anything",
	})
	if err == nil {
		t.Fatal("expected error from nil-store recorder")
	}
}

// TestCapture_StoreErrorPropagates makes sure a Vanta failure surfaces
// as a Go error so the MCP handler can render it as a structured tool
// error instead of silently dropping the write.
func TestCapture_StoreErrorPropagates(t *testing.T) {
	store := &stubStore{storeErr: errors.New("conduit unavailable")}
	rec := NewRecorder(store)
	_, err := rec.Capture(context.Background(), CaptureInput{
		Scope:   ScopeToolUse,
		Subject: "card_show",
		Hint:    "x",
	})
	if err == nil {
		t.Fatal("expected error when store fails")
	}
	if !strings.Contains(err.Error(), "conduit unavailable") {
		t.Errorf("error should wrap underlying store error, got: %v", err)
	}
}

// TestCapture_ProjectScope walks the project namespace shape end-to-end.
func TestCapture_ProjectScope(t *testing.T) {
	store := &stubStore{}
	rec := NewRecorder(store)
	out, err := rec.Capture(context.Background(), CaptureInput{
		Scope:   ScopeProject,
		Subject: "nanite",
		Hint:    "always run cerberus_rebuild after self-tool changes",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "user/default/project/nanite/memory"
	if out.Namespace != want {
		t.Errorf("project namespace = %q, want %q", out.Namespace, want)
	}
}

// TestCapture_SessionScope walks the session namespace shape.
func TestCapture_SessionScope(t *testing.T) {
	store := &stubStore{}
	rec := NewRecorder(store)
	out, err := rec.Capture(context.Background(), CaptureInput{
		Scope:   ScopeSession,
		Subject: "sess-9",
		Hint:    "user prefers terse replies in this session",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "user/default/session/sess-9/memory"
	if out.Namespace != want {
		t.Errorf("session namespace = %q, want %q", out.Namespace, want)
	}
}

// TestRecallByToolName_HappyPath surfaces stored hints from the user
// namespace, filtered down to the requested tool by tag.
func TestRecallByToolName_HappyPath(t *testing.T) {
	store := &stubStore{
		recallReply: []memory.Memory{
			{Summary: "report-card requires metrics, not sections", Confidence: 0.85, MemoryKey: "k1", Namespace: "user/alice/memory",
				Tags: []string{"learning", "tool:card_show"}},
			{Summary: "info-card 'sources' is optional", Confidence: 0.85, MemoryKey: "k2", Namespace: "user/alice/memory",
				Tags: []string{"learning", "tool:card_show"}},
		},
	}
	r := NewRecaller(store)
	hints := r.RecallByToolName(context.Background(), "alice", "card_show")
	if len(hints) != 2 {
		t.Fatalf("expected 2 hints, got %d", len(hints))
	}
	if !strings.Contains(hints[0].Summary, "report-card") {
		t.Errorf("first hint should be the report-card lesson, got: %s", hints[0].Summary)
	}
	wantNS := "user/alice/memory"
	if store.recallOpts.Namespaces[0] != wantNS {
		t.Errorf("recall namespace = %q, want %q", store.recallOpts.Namespaces[0], wantNS)
	}
	// We always pass the per-tool tag so Conduit narrows by tool.
	wantTag := "tool:card_show"
	hasToolTag := false
	for _, tg := range store.recallOpts.Tags {
		if tg == wantTag {
			hasToolTag = true
		}
	}
	if !hasToolTag {
		t.Errorf("recall must tag-filter on %q; got tags=%v", wantTag, store.recallOpts.Tags)
	}
}

// TestRecallByToolName_FiltersOutWrongTool guards against the
// shared-namespace risk: a hit for a different tool that landed in the
// same user namespace must not bleed into the result.
func TestRecallByToolName_FiltersOutWrongTool(t *testing.T) {
	store := &stubStore{
		recallReply: []memory.Memory{
			{Summary: "show_card lesson", Tags: []string{"learning", "tool:card_show"}},
			{Summary: "giphy lesson", Tags: []string{"learning", "tool:giphy_search"}},
			{Summary: "untagged learning", Tags: []string{"learning"}}, // missing tool tag — discard
		},
	}
	r := NewRecaller(store)
	hints := r.RecallByToolName(context.Background(), "alice", "card_show")
	if len(hints) != 1 {
		t.Fatalf("expected exactly 1 hint after client-side filter, got %d (%+v)", len(hints), hints)
	}
	if hints[0].Summary != "show_card lesson" {
		t.Errorf("wrong tool's lesson surfaced: %s", hints[0].Summary)
	}
}

// TestRecallByToolName_FailsOpen verifies a Vanta error returns an
// empty slice rather than bubbling up. Failing-open is the documented
// design choice.
func TestRecallByToolName_FailsOpen(t *testing.T) {
	store := &stubStore{recallErr: errors.New("conduit timeout")}
	r := NewRecaller(store)
	hints := r.RecallByToolName(context.Background(), "alice", "card_show")
	if hints != nil {
		t.Errorf("expected nil hints on store error, got %v", hints)
	}
}

// TestRecallByToolName_NilSafe verifies the package's nil-safety
// contract: a nil recaller and a nil store both yield zero hints with
// no panic.
func TestRecallByToolName_NilSafe(t *testing.T) {
	var nilRecaller *Recaller
	if got := nilRecaller.RecallByToolName(context.Background(), "alice", "x"); got != nil {
		t.Errorf("nil-recaller should return nil, got %v", got)
	}
	r := NewRecaller(nil)
	if got := r.RecallByToolName(context.Background(), "alice", "x"); got != nil {
		t.Errorf("nil-store recaller should return nil, got %v", got)
	}
}

// TestRecallByToolName_EmptyToolName guards against accidental recall
// against the default "_" sanitized namespace, which would surface
// unrelated entries.
func TestRecallByToolName_EmptyToolName(t *testing.T) {
	store := &stubStore{recallReply: []memory.Memory{{Summary: "x"}}}
	r := NewRecaller(store)
	hints := r.RecallByToolName(context.Background(), "alice", "  ")
	if hints != nil {
		t.Errorf("blank tool name should not query Vanta, got hints %v", hints)
	}
}

// TestSystemPromptBlock_Format pins the rendered block shape.
func TestSystemPromptBlock_Format(t *testing.T) {
	hints := []Hint{
		{Summary: "report-card requires metrics, not sections"},
		{Summary: "info-card sources is optional"},
	}
	got := SystemPromptBlock("card_show", hints)
	want := "## Prior learnings for card_show\n" +
		"- report-card requires metrics, not sections\n" +
		"- info-card sources is optional\n"
	if got != want {
		t.Errorf("SystemPromptBlock mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestSystemPromptBlock_EmptyReturnsBlank ensures callers can
// concatenate the block unconditionally.
func TestSystemPromptBlock_EmptyReturnsBlank(t *testing.T) {
	if SystemPromptBlock("card_show", nil) != "" {
		t.Error("empty hints should render an empty block")
	}
}
