package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

func TestDrainCapture_DeltasConcatenateIntoSummary(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "Hello "}
	ch <- chat.StreamEvent{Type: "delta", Content: "world"}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, counts, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "Hello world" {
		t.Errorf("summary = %q, want %q", summary, "Hello world")
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q, want \"{}\" (no envelope events)", envelope)
	}
	if counts != (toolUsageCounts{}) {
		t.Errorf("counts = %+v, want zero value (no tool events)", counts)
	}
}

func TestDrainCapture_CapturesEnvelopeAsResultJSON(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "summary text"}
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"finding":"x"}`}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "summary text" {
		t.Errorf("summary = %q", summary)
	}
	if envelope != `{"finding":"x"}` {
		t.Errorf("envelope = %q", envelope)
	}
}

func TestDrainCapture_LastEnvelopeWins(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"first":1}`}
	ch <- chat.StreamEvent{Type: "delta", Content: "between"}
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"second":2}`}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	_, envelope, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if envelope != `{"second":2}` {
		t.Errorf("envelope = %q, want last-wins", envelope)
	}
}

// TestDrainCapture_CapturesStreamEndEnvelope verifies that the
// aggregated envelope JSON that production generateResponse attaches
// to stream_end.Envelope (chat_generate.go:837) is captured as
// Result.ResultJSON. Without this, ResultJSON stays "{}" when
// plugin_envelope routing doesn't fire mid-stream.
func TestDrainCapture_CapturesStreamEndEnvelope(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "delta", Content: "final answer"}
	ch <- chat.StreamEvent{Type: "stream_end", Envelope: `[{"type":"x"}]`}
	close(ch)

	summary, envelope, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "final answer" {
		t.Errorf("summary = %q", summary)
	}
	if envelope != `[{"type":"x"}]` {
		t.Errorf("envelope = %q, want stream_end.Envelope content", envelope)
	}
}

// TestDrainCapture_StreamEndEnvelopeWinsOverMidStream verifies that
// when both a mid-stream plugin_envelope and a terminal stream_end
// carry an Envelope, stream_end wins — it's the aggregated truth that
// production generateResponse emits last (chat_generate.go:837).
func TestDrainCapture_StreamEndEnvelopeWinsOverMidStream(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"mid":1}`}
	ch <- chat.StreamEvent{Type: "stream_end", Envelope: `{"final":2}`}
	close(ch)

	_, envelope, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if envelope != `{"final":2}` {
		t.Errorf("envelope = %q, want stream_end.Envelope to win", envelope)
	}
}

func TestDrainCapture_EmptySummaryFallback(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, _, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty (fallback applied by caller, not drainCapture)", summary)
	}
}

func TestDrainCapture_ErrorEventTerminatesDrain(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "partial"}
	ch <- chat.StreamEvent{Type: "error", Error: "provider exploded"}
	ch <- chat.StreamEvent{Type: "delta", Content: "should not appear"}
	close(ch)

	_, _, _, err := drainCapture(ch)
	if err == nil {
		t.Fatal("expected error from drainCapture, got nil")
	}
	if !errors.Is(err, errStreamFailure) && err.Error() == "" {
		t.Errorf("error = %v", err)
	}
}

func TestDrainCapture_IgnoresOtherEventTypes(t *testing.T) {
	ch := make(chan chat.StreamEvent, 16)
	ch <- chat.StreamEvent{Type: "stream_start"}
	ch <- chat.StreamEvent{Type: "tool_call", Tool: "x"}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: "x", Summary: "ok"}
	ch <- chat.StreamEvent{Type: "status", Content: "thinking"}
	ch <- chat.StreamEvent{Type: "delta", Content: "real text"}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, counts, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "real text" {
		t.Errorf("summary = %q, want only delta content", summary)
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q", envelope)
	}
	// tool_call + tool_result events feed counts but not summary —
	// CW-20260512-0095 contract change. The successful tool_result here
	// (IsError=false default) means the fabrication detector would NOT
	// fire for this turn; that's the intended distinction.
	if counts.calls != 1 {
		t.Errorf("counts.calls = %d, want 1", counts.calls)
	}
	if counts.resultsSuccess != 1 {
		t.Errorf("counts.resultsSuccess = %d, want 1", counts.resultsSuccess)
	}
	if counts.resultsError != 0 {
		t.Errorf("counts.resultsError = %d, want 0", counts.resultsError)
	}
}

// TestDrainCapture_CountsToolErrors verifies the CW-20260512-0095 contract:
// tool_result events with IsError=true increment counts.resultsError, and
// successful ones increment counts.resultsSuccess. This is the raw signal
// the fabrication detector reads.
func TestDrainCapture_CountsToolErrors(t *testing.T) {
	ch := make(chan chat.StreamEvent, 16)
	ch <- chat.StreamEvent{Type: "tool_call", Tool: "dev_read"}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: "dev_read", Summary: "path denied", IsError: true}
	ch <- chat.StreamEvent{Type: "tool_call", Tool: "dev_glob"}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: "dev_glob", Summary: "no matches", IsError: false}
	ch <- chat.StreamEvent{Type: "delta", Content: "Found nothing useful."}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	_, _, counts, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if counts.calls != 2 {
		t.Errorf("counts.calls = %d, want 2", counts.calls)
	}
	if counts.resultsError != 1 {
		t.Errorf("counts.resultsError = %d, want 1", counts.resultsError)
	}
	if counts.resultsSuccess != 1 {
		t.Errorf("counts.resultsSuccess = %d, want 1", counts.resultsSuccess)
	}
}

// TestDetectFabrication_FiresOnAllErrorWithText is the c160 regression
// case (CW-20260512-0095). The researcher subagent invoked tools that
// failed (no codebase access), every tool_result came back IsError=true,
// yet the assistant still produced 8.1KB of fabricated analysis. The
// detector must convert this turn into a non-nil error so the run lands
// as subagent_runs.status = "failed" rather than "completed".
func TestDetectFabrication_FiresOnAllErrorWithText(t *testing.T) {
	counts := toolUsageCounts{calls: 3, resultsError: 3, resultsSuccess: 0}
	err := detectFabrication("Since I cannot access the codebase, I'll provide a framework... [8.1KB of fabricated text]", counts)
	if err == nil {
		t.Fatal("expected fabrication-suspected error, got nil")
	}
	if !errors.Is(err, errSubagentFabricationSuspected) {
		t.Errorf("error = %v; want wrapped errSubagentFabricationSuspected", err)
	}
	// Reason string must surface the counts for operators inspecting
	// subagent_runs.error after the fact.
	for _, fragment := range []string{"tool_calls=3", "tool_results_error=3", "tool_results_success=0"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q missing fragment %q", err.Error(), fragment)
		}
	}
}

// TestDetectFabrication_SkipsWhenAnyToolSucceeded — at least one
// successful tool_result means the assistant text is at least partially
// grounded; the universal Refusal rules govern further fabrication risk
// from there, but this detector should not double-fire.
func TestDetectFabrication_SkipsWhenAnyToolSucceeded(t *testing.T) {
	counts := toolUsageCounts{calls: 3, resultsError: 2, resultsSuccess: 1}
	err := detectFabrication("Found 3 tasks. Here's the analysis...", counts)
	if err != nil {
		t.Errorf("expected nil (one tool succeeded); got %v", err)
	}
}

// TestDetectFabrication_SkipsWhenNoToolsAttempted — text-only replies
// (no tool calls) are out of this detector's scope. The parent-side
// fabrication risk is W1B's territory (CW-20260512-0096).
func TestDetectFabrication_SkipsWhenNoToolsAttempted(t *testing.T) {
	counts := toolUsageCounts{calls: 0, resultsError: 0, resultsSuccess: 0}
	err := detectFabrication("Plain prose reply.", counts)
	if err != nil {
		t.Errorf("expected nil (no tools attempted); got %v", err)
	}
}

// TestDetectFabrication_SkipsWhenSummaryEmpty — empty summary is handled
// by the empty-summary fallback in ChatRunner.Run; double-failing the
// run for that case would over-report.
func TestDetectFabrication_SkipsWhenSummaryEmpty(t *testing.T) {
	counts := toolUsageCounts{calls: 2, resultsError: 2, resultsSuccess: 0}
	err := detectFabrication("", counts)
	if err != nil {
		t.Errorf("expected nil (empty summary); got %v", err)
	}
	// Whitespace-only summary trips the same skip — TrimSpace before checking.
	err = detectFabrication("   \n\t  ", counts)
	if err != nil {
		t.Errorf("expected nil (whitespace-only summary); got %v", err)
	}
}

// TestChatRunner_FabricationSuspectedFailsRun is the integration-level
// acceptance test for CW-20260512-0095. A subagent task whose tool calls
// all fail but still produces non-empty assistant text must surface a
// non-nil errSubagentFabricationSuspected so subagent.Service.execute
// flips the row to status=failed.
//
// This is the runtime backstop for the c160 evidence — the universal
// Refusal rules (CW-20260512-0100) teach the model to return failure
// rather than fabricate, but if the model fabricates anyway, this
// detector converts the turn into a failure the parent can read.
//
// CW-20260519-0071: the runner now ALSO returns a partial *subagent.Result
// alongside the fabrication error (additive partial-result capture). The
// error is unchanged and still drives status=failed; the partial result
// carries the suspect text + tool counts so an operator inspecting the
// row sees what the child produced. This test pins both: the error is
// intact AND the partial result is captured.
func TestChatRunner_FabricationSuspectedFailsRun(t *testing.T) {
	// Emit a stream mirroring the c160 researcher: two tool calls, both
	// IsError, followed by a long polished assistant reply.
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "tool_call", Tool: "dev_read", ToolID: "tu_1"},
		{Type: "tool_result", Tool: "dev_read", ToolID: "tu_1", Summary: "PERMISSION DENIED: path outside grants", IsError: true},
		{Type: "tool_call", Tool: "dev_glob", ToolID: "tu_2"},
		{Type: "tool_result", Tool: "dev_glob", ToolID: "tu_2", Summary: "PERMISSION DENIED: path outside grants", IsError: true},
		{Type: "delta", Content: "Since I cannot access the codebase directly, I'll provide a comprehensive framework. "},
		{Type: "delta", Content: "The Fragments Engine architecture is based on..."},
		{Type: "stream_end"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"researcher": {ID: "ag-researcher", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID:              "run-fab",
		Role:            "researcher",
		ParentSessionID: "sess-parent",
		Prompt:          "Analyze ~/Projects-apps/Fragments Engine codebase",
	}
	result, err := runner.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected fabrication-suspected error from runner.Run; got nil")
	}
	if !errors.Is(err, errSubagentFabricationSuspected) {
		t.Errorf("error = %v; want wrapped errSubagentFabricationSuspected", err)
	}
	// CW-20260519-0071: partial-result capture. The error above already
	// maps the run to status=failed; the runner additionally returns a
	// partial result so result_json records the suspect text + tool
	// counts. The fabrication path saw 2 failed tool calls, so a partial
	// result is expected.
	if result == nil {
		t.Fatal("result = nil; want non-nil partial result on fabrication-suspected (CW-20260519-0071 capture)")
	}
	if !contains(result.ResultJSON, `"partial":true`) {
		t.Errorf("result.ResultJSON = %q; want partial-capture marker", result.ResultJSON)
	}
	if !contains(result.ResultJSON, `"calls":2`) {
		t.Errorf("result.ResultJSON = %q; want tool-call count captured", result.ResultJSON)
	}
}

// TestChatRunner_GroundedReplyDoesNotFireFabricationDetector pins the
// negative side of the contract: a subagent that runs tools successfully
// and produces text MUST land as a normal success, not a false-positive
// fabrication failure. Without this, the detector would over-report and
// any subagent reply with a leading text-error followed by recovery
// would be incorrectly marked failed.
func TestChatRunner_GroundedReplyDoesNotFireFabricationDetector(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "tool_call", Tool: "dev_read", ToolID: "tu_a"},
		{Type: "tool_result", Tool: "dev_read", ToolID: "tu_a", Summary: "// file contents...", IsError: false},
		{Type: "delta", Content: "Found 12 functions in the package."},
		{Type: "stream_end"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"worker": {ID: "ag-worker", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID:              "run-grounded",
		Role:            "worker",
		ParentSessionID: "sess-parent",
		Prompt:          "Count the functions",
	}
	result, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("runner.Run unexpected error: %v", err)
	}
	if result == nil || result.Summary == "" {
		t.Fatal("expected non-nil result with non-empty summary on grounded reply")
	}
	if !strings.Contains(result.Summary, "Found 12 functions") {
		t.Errorf("summary = %q; want the grounded delta content", result.Summary)
	}
}

// stubAgentReaderForRunner satisfies agentSlugResolver for ChatRunner tests.
// Returns sql.ErrNoRows-wrapped errors for unknown slugs (matching the real
// store.GetAgentBySlug behavior), so the runner's slug-fallback path is
// exercised by tests.
type stubAgentReaderForRunner struct {
	agents map[string]*store.AgentProfile
}

func (s *stubAgentReaderForRunner) GetAgentBySlug(slug string) (*store.AgentProfile, error) {
	a, ok := s.agents[slug]
	if !ok {
		// Mirror store.GetAgentBySlug — wrap sql.ErrNoRows with %w so
		// errors.Is(err, sql.ErrNoRows) reports true. CW-20260512-0002 (a).
		return nil, fmt.Errorf("get agent by slug %s: %w", slug, sql.ErrNoRows)
	}
	return a, nil
}

// TestChatRunner_ResolveRoleFallsBackToWorker — CW-20260512-0002 subtodo (a):
// unknown slugs should fall back to the `worker` profile rather than
// hard-erroring. Mirrors the reflex-catalog drift guard.
func TestChatRunner_ResolveRoleFallsBackToWorker(t *testing.T) {
	workerProfile := &store.AgentProfile{ID: "ag-worker", Slug: "worker", DefaultProvider: "anthropic", DefaultModel: "m"}
	r := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"worker": workerProfile,
		}},
	}
	agent, err := r.resolveRole("researcher")
	if err != nil {
		t.Fatalf("resolveRole(\"researcher\"): expected fallback to worker, got error %v", err)
	}
	if agent == nil || agent.Slug != "worker" {
		t.Errorf("resolveRole(\"researcher\") returned slug=%q, want \"worker\"", agentSlugOrEmpty(agent))
	}
}

// TestChatRunner_ResolveRoleFailsWhenFallbackMissing — when even the
// fallback `worker` profile is missing, the runner surfaces the ORIGINAL
// slug in the error so the deployment misconfiguration is alertable.
func TestChatRunner_ResolveRoleFailsWhenFallbackMissing(t *testing.T) {
	r := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{}},
	}
	_, err := r.resolveRole("nonexistent")
	if err == nil {
		t.Fatal("expected error when neither slug nor fallback resolve, got nil")
	}
	if !errors.Is(err, errRoleResolveFailed) {
		t.Errorf("error = %v, want wrapped errRoleResolveFailed", err)
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %v: must surface original slug \"nonexistent\"", err)
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("error %v: must mention fallback was also missing", err)
	}
}

// TestChatRunner_ResolveRoleSurfacesNonNoRowsError — non-sql.ErrNoRows
// errors (DB I/O, etc.) must NOT trigger the fallback; they need to
// surface as runner failures so the caller learns about the real fault.
func TestChatRunner_ResolveRoleSurfacesNonNoRowsError(t *testing.T) {
	r := &ChatRunner{agents: errAgentReader{err: errors.New("db connection lost")}}
	_, err := r.resolveRole("worker")
	if err == nil {
		t.Fatal("expected error to surface, got nil")
	}
	if !errors.Is(err, errRoleResolveFailed) {
		t.Errorf("error = %v, want wrapped errRoleResolveFailed", err)
	}
	if !strings.Contains(err.Error(), "db connection lost") {
		t.Errorf("error %v: must preserve underlying cause", err)
	}
}

// errAgentReader returns the configured error for every lookup. Used to
// verify that resolveRoleWithFallback does NOT swallow non-sql.ErrNoRows
// errors with a silent fallback.
type errAgentReader struct{ err error }

func (e errAgentReader) GetAgentBySlug(string) (*store.AgentProfile, error) { return nil, e.err }

func agentSlugOrEmpty(a *store.AgentProfile) string {
	if a == nil {
		return ""
	}
	return a.Slug
}

// recordingSessionStore implements sessionStoreForRunner in memory.
type recordingSessionStore struct {
	created  []*store.Session
	parents  map[string]*store.Session
	bindings []sessionAgentBinding
	messages []*store.Message
}

type sessionAgentBinding struct {
	sessionID, agentID, mode string
	isPrimary                bool
}

func (r *recordingSessionStore) CreateSession(s *store.Session) error {
	r.created = append(r.created, s)
	return nil
}

func (r *recordingSessionStore) GetSession(id string) (*store.Session, error) {
	if s, ok := r.parents[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("session not found: %s", id)
}

func (r *recordingSessionStore) EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error {
	r.bindings = append(r.bindings, sessionAgentBinding{sessionID, agentID, mode, isPrimary})
	return nil
}

func (r *recordingSessionStore) CreateMessage(m *store.Message) error {
	r.messages = append(r.messages, m)
	return nil
}

// fakeChatService is a chatInvoker that emits canned events.
type fakeChatService struct {
	events []chat.StreamEvent
	called int
}

func (f *fakeChatService) generateResponse(_ context.Context, _, _, _ string, ch chan chat.StreamEvent) {
	defer close(ch)
	f.called++
	for _, e := range f.events {
		ch <- e
	}
}

func TestChatRunner_DrainsSummaryAndEnvelope(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "Project X has "},
		{Type: "delta", Content: "3 open tasks."},
		{Type: "plugin_envelope", Envelope: `{"open_tasks":3}`},
		{Type: "stream_end"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-1", Role: "role-1", ParentSessionID: "sess-parent", Prompt: "summarize",
	}
	result, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary != "Project X has 3 open tasks." {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.ResultJSON != `{"open_tasks":3}` {
		t.Errorf("ResultJSON = %q", result.ResultJSON)
	}
	if fake.called != 1 {
		t.Errorf("generateResponse called %d times, want 1", fake.called)
	}
	if run.ChildSessionID == "" {
		t.Error("ChildSessionID not set on run")
	}

	// User message must be persisted before invokeChat so the provider
	// context assembly (ListMessages) sees the prompt. Without this the
	// LLM receives an empty conversation and ignores run.Prompt.
	if len(st.messages) != 1 {
		t.Fatalf("messages created = %d, want 1", len(st.messages))
	}
	userMsg := st.messages[0]
	if userMsg.SessionID != run.ChildSessionID {
		t.Errorf("user message SessionID = %q, want child %q", userMsg.SessionID, run.ChildSessionID)
	}
	if userMsg.Role != "user" {
		t.Errorf("user message Role = %q, want \"user\"", userMsg.Role)
	}
	if userMsg.Content != "summarize" {
		t.Errorf("user message Content = %q, want %q", userMsg.Content, "summarize")
	}
}

// TestChatRunner_CapturesPartialResultOnStreamError is the acceptance
// test for CW-20260519-0071 (audit §P2). A productive subagent that did
// real tool-iteration work and then had its in-flight provider stream
// cancelled by the run-budget deadline must not have its accumulated
// work discarded. The runner now returns the partial *subagent.Result
// (summary + envelope + tool counts) ALONGSIDE the stream error, so
// subagent.execute can persist result_json on the StatusFailed branch
// instead of leaving it at the insert-time default.
func TestChatRunner_CapturesPartialResultOnStreamError(t *testing.T) {
	// Stream mirrors a worker cut mid-productive-work: several real
	// file-writing tool roundtrips, partial assistant text, then the
	// deadline cancels the stream → error event (no stream_end).
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "tool_call", Tool: "dev_write", ToolID: "tu_1"},
		{Type: "tool_result", Tool: "dev_write", ToolID: "tu_1", Summary: "wrote internal/foo.go", IsError: false},
		{Type: "tool_call", Tool: "dev_write", ToolID: "tu_2"},
		{Type: "tool_result", Tool: "dev_write", ToolID: "tu_2", Summary: "wrote internal/bar.go", IsError: false},
		{Type: "plugin_envelope", Envelope: `{"files_written":2}`},
		{Type: "delta", Content: "I have implemented the first two files and am "},
		{Type: "error", Error: "http chat stream error / cause:http_stream"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"worker": {ID: "ag-worker", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-cut", Role: "worker", ParentSessionID: "sess-parent", Prompt: "implement the feature",
	}
	result, err := runner.Run(context.Background(), run)

	// The error must be intact — capture is additive, not suppression.
	if err == nil {
		t.Fatal("expected stream error from runner.Run; got nil")
	}
	if !errors.Is(err, errStreamFailure) {
		t.Errorf("error = %v; want wrapped errStreamFailure", err)
	}
	// The partial result must be returned alongside the error.
	if result == nil {
		t.Fatal("result = nil; want non-nil partial result (CW-20260519-0071)")
	}
	if result.Summary != "I have implemented the first two files and am " {
		t.Errorf("partial Summary = %q; want the accumulated assistant text", result.Summary)
	}
	if !contains(result.ResultJSON, `"partial":true`) {
		t.Errorf("result.ResultJSON = %q; want partial-capture marker", result.ResultJSON)
	}
	if !contains(result.ResultJSON, `"files_written":2`) {
		t.Errorf("result.ResultJSON = %q; want captured envelope", result.ResultJSON)
	}
	if !contains(result.ResultJSON, `"calls":2`) || !contains(result.ResultJSON, `"results_success":2`) {
		t.Errorf("result.ResultJSON = %q; want tool counts (calls=2, results_success=2)", result.ResultJSON)
	}
}

// TestPartialResult_NilWhenNothingAccumulated pins the negative side:
// a genuinely empty failure (no text, no envelope, no tool activity)
// yields a nil partial result so subagent.execute leaves result_json
// at its insert-time default rather than persisting an empty trace.
func TestPartialResult_NilWhenNothingAccumulated(t *testing.T) {
	if got := partialResult("", "{}", toolUsageCounts{}); got != nil {
		t.Errorf("partialResult(empty) = %+v; want nil", got)
	}
	if got := partialResult("  \n ", "{}", toolUsageCounts{}); got != nil {
		t.Errorf("partialResult(whitespace-only) = %+v; want nil", got)
	}
	// Any one signal present → non-nil capture.
	if got := partialResult("some text", "{}", toolUsageCounts{}); got == nil {
		t.Error("partialResult(text) = nil; want non-nil")
	}
	if got := partialResult("", "{}", toolUsageCounts{calls: 1}); got == nil {
		t.Error("partialResult(tool activity) = nil; want non-nil")
	}
}

// TestChatRunner_UserMessageCreationError verifies that a failure to
// persist the user message aborts Run before invoking the chat loop —
// otherwise the provider would see an empty conversation and silently
// produce wrong output.
func TestChatRunner_UserMessageCreationError(t *testing.T) {
	fake := &fakeChatService{}
	st := &messageFailingStore{
		recordingSessionStore: recordingSessionStore{
			parents: map[string]*store.Session{
				"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
			},
		},
		createMessageErr: errors.New("db write failed"),
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}
	_, err := runner.Run(context.Background(), &subagent.Run{
		ID: "run-1", Role: "role-1", ParentSessionID: "sess-parent", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected error from CreateMessage failure")
	}
	if !strings.Contains(err.Error(), "create user message") {
		t.Errorf("error = %v, want create-user-message wrap", err)
	}
	if fake.called != 0 {
		t.Errorf("generateResponse called %d times, want 0 (should abort before chat invocation)", fake.called)
	}
}

// messageFailingStore extends recordingSessionStore with an injected
// CreateMessage error.
type messageFailingStore struct {
	recordingSessionStore
	createMessageErr error
}

func (m *messageFailingStore) CreateMessage(msg *store.Message) error {
	if m.createMessageErr != nil {
		return m.createMessageErr
	}
	return m.recordingSessionStore.CreateMessage(msg)
}

// TestChatRunner_ProviderOverride_UsesFallbackWhenEmpty verifies that
// when run.Provider is empty, createChildSession uses agent.DefaultProvider.
func TestChatRunner_ProviderOverride_UsesFallbackWhenEmpty(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "done"},
		{Type: "stream_end"},
	}}
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-a": {ID: "ag-a", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-empty-prov", Role: "role-a", ParentSessionID: "sess-p", Prompt: "go",
		// Provider intentionally empty — should fall back to agent default
	}
	_, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected 1 child session created, got %d", len(st.created))
	}
	if st.created[0].Provider != "anthropic" {
		t.Errorf("child session Provider = %q, want %q (agent default)", st.created[0].Provider, "anthropic")
	}
}

// TestChatRunner_ProviderOverride_InheritsParentWhenNoOverrideOrAgentDefault
// verifies that the child session inherits the parent provider when both the
// spawn request and the agent profile leave the provider empty.
func TestChatRunner_ProviderOverride_InheritsParentWhenNoOverrideOrAgentDefault(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "done"},
		{Type: "stream_end"},
	}}
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p", WorkspaceID: "ws-1", Provider: "anthropic"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-a": {ID: "ag-a", DefaultProvider: "", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-inherit-parent", Role: "role-a", ParentSessionID: "sess-p", Prompt: "go",
	}
	_, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected 1 child session created, got %d", len(st.created))
	}
	if st.created[0].Provider != "anthropic" {
		t.Errorf("child session Provider = %q, want %q (parent provider)", st.created[0].Provider, "anthropic")
	}
}

// TestChatRunner_ProviderOverride_UsesOverrideWhenSet verifies that a
// non-empty run.Provider is used in preference to agent.DefaultProvider.
func TestChatRunner_ProviderOverride_UsesOverrideWhenSet(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "done"},
		{Type: "stream_end"},
	}}
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-a": {ID: "ag-a", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-override", Role: "role-a", ParentSessionID: "sess-p", Prompt: "go",
		Provider: "pty-claude", // override: heavy-execution worker
	}
	_, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected 1 child session created, got %d", len(st.created))
	}
	if st.created[0].Provider != "pty-claude" {
		t.Errorf("child session Provider = %q, want %q (override)", st.created[0].Provider, "pty-claude")
	}
}

// --- CW-20260519-0067: output-presence gate (audit §P5) ---------------------

// TestZeroOutputRun_PresenceCheck pins the gate's presence predicate. A
// run that emitted no text AND no tool calls AND no envelope is a
// zero-output run; any single signal present clears the gate.
func TestZeroOutputRun_PresenceCheck(t *testing.T) {
	// All-empty: the pathological hung-planner shape — trips the gate.
	if !zeroOutputRun("", "{}", toolUsageCounts{}) {
		t.Error("zeroOutputRun(empty) = false; want true (hung-planner shape)")
	}
	// Whitespace-only deltas are still silence.
	if !zeroOutputRun("  \n\t ", "{}", toolUsageCounts{}) {
		t.Error("zeroOutputRun(whitespace) = false; want true")
	}
	// Real assistant text → not zero-output.
	if zeroOutputRun("here is the answer", "{}", toolUsageCounts{}) {
		t.Error("zeroOutputRun(text) = true; want false")
	}
	// Tool calls but no prose → text-light success, NOT a stall.
	if zeroOutputRun("", "{}", toolUsageCounts{calls: 1}) {
		t.Error("zeroOutputRun(tool calls) = true; want false (text-light success)")
	}
	// A successful tool roundtrip alone clears the gate.
	if zeroOutputRun("", "{}", toolUsageCounts{resultsSuccess: 1}) {
		t.Error("zeroOutputRun(tool success) = true; want false")
	}
	// A failed tool roundtrip is still activity — the child tried.
	if zeroOutputRun("", "{}", toolUsageCounts{resultsError: 1}) {
		t.Error("zeroOutputRun(tool error) = true; want false")
	}
	// A structured envelope is a delivered result even with no prose.
	if zeroOutputRun("", `{"open_tasks":3}`, toolUsageCounts{}) {
		t.Error("zeroOutputRun(envelope) = true; want false (envelope is a deliverable)")
	}
}

// TestChatRunner_ZeroOutputRunGatedAsStalled is the CW-20260519-0067
// acceptance test. A child turn that drains cleanly (channel closes
// without an error event) having emitted nothing — no delta, no
// tool_call, no envelope — must NOT be returned as a success. Before the
// gate this produced (&Result{Summary:"...completed without text
// response"}, nil) and subagent.execute stamped StatusCompleted on a run
// that did nothing for the full budget. The gate now returns
// errZeroOutput joined with subagent.ErrStalled so classifyRunOutcome
// routes it to StatusStalled.
func TestChatRunner_ZeroOutputRunGatedAsStalled(t *testing.T) {
	// The hung-planner shape: the child loop exits and closes the capture
	// channel WITHOUT a stream_end and WITHOUT an error event. drainCapture
	// treats a channel closed without stream_end as a clean drain, so
	// runErr is nil — exactly the path the gate must catch.
	fake := &fakeChatService{events: []chat.StreamEvent{}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"planner": {ID: "ag-planner", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-hung", Role: "planner", ParentSessionID: "sess-parent", Prompt: "plan the work",
	}
	result, err := runner.Run(context.Background(), run)

	if err == nil {
		t.Fatal("expected zero-output run to be gated as an error; got nil (would be stamped completed)")
	}
	if !errors.Is(err, errZeroOutput) {
		t.Errorf("error = %v; want wrapped errZeroOutput", err)
	}
	// Joined with ErrStalled so the CW-20260519-0074 classifier routes
	// the run to StatusStalled rather than StatusCompleted.
	if !errors.Is(err, subagent.ErrStalled) {
		t.Errorf("error = %v; want joined subagent.ErrStalled (→ classifyRunOutcome StatusStalled)", err)
	}
	// No partial result — a zero-output run has nothing worth persisting.
	if result != nil {
		t.Errorf("result = %+v; want nil for a zero-output run", result)
	}
}

// TestChatRunner_TextLightSuccessNotGated pins the negative side of the
// gate: a child that produced no closing prose but DID make a tool call
// is a genuine (text-light) completion and must still return a nil error
// → StatusCompleted. The output-presence gate must not punish a quiet
// but productive run.
func TestChatRunner_TextLightSuccessNotGated(t *testing.T) {
	// Tool roundtrip, an envelope, no delta — a real result, no prose.
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "tool_call", Tool: "dev_write", ToolID: "tu_1"},
		{Type: "tool_result", Tool: "dev_write", ToolID: "tu_1", Summary: "wrote file", IsError: false},
		{Type: "plugin_envelope", Envelope: `{"files_written":1}`},
		{Type: "stream_end"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"worker": {ID: "ag-worker", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-quiet", Role: "worker", ParentSessionID: "sess-parent", Prompt: "implement",
	}
	result, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("text-light productive run gated unexpectedly: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil; want a completed result for a text-light productive run")
	}
	// The empty-summary fallback still applies — but as a success, not a stall.
	if !contains(result.Summary, "completed without text response") {
		t.Errorf("Summary = %q; want the text-light completion fallback", result.Summary)
	}
	if result.ResultJSON != `{"files_written":1}` {
		t.Errorf("ResultJSON = %q; want the captured envelope", result.ResultJSON)
	}
}
