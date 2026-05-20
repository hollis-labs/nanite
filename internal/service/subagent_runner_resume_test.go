package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// CW-20260519-0075 (audit §P6) — ChatRunner resume / retry behavior.
//
// On a retry / resume attempt the subagent.Service.execute loop hands
// the runner a Run whose ChildSessionID is already populated. The
// runner must:
//   - skip the createChildSession + persistChild + EnsureSessionAgent
//     work and reuse the prior child session (so the chat-history
//     resume substrate is preserved),
//   - append a continuation-prompt user message instead of the
//     original prompt verbatim (so the LLM continues rather than
//     replaying),
//   - still drive a single assistant turn and drain the result.

// TestChatRunner_ResumeReusesChildSession pins the first invariant: a
// Run with ChildSessionID preset must NOT create a new session.
func TestChatRunner_ResumeReusesChildSession(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "continued where I left off."},
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
		store:   st,
		invoker: fake,
		// persistFn = nil because resume must skip persistChildSessionID
		// entirely; a nil function here surfaces accidental invocation
		// as an immediate test failure rather than a silent no-op.
	}

	// Resume attempt: ChildSessionID is preset; AttemptsJSON records a
	// prior over_budget attempt so resumeContinuationPrompt picks the
	// over_budget branch.
	preexistingChildID := "child-from-prior-attempt"
	run := &subagent.Run{
		ID:              "run-1",
		Role:            "role-1",
		ParentSessionID: "sess-parent",
		Prompt:          "implement the feature",
		ChildSessionID:  preexistingChildID,
		AttemptsJSON:    `[{"status":"over_budget","error":"deadline"}]`,
	}
	result, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if result == nil || result.Summary == "" {
		t.Fatal("expected non-nil result")
	}

	if len(st.created) != 0 {
		t.Errorf("created %d sessions on resume, want 0 (resume must reuse the prior child session)", len(st.created))
	}
	if len(st.bindings) != 0 {
		t.Errorf("created %d EnsureSessionAgent bindings on resume, want 0", len(st.bindings))
	}
	// One message — the continuation prompt — was appended to the child
	// session. Verify it points at the SAME child session as the run.
	if len(st.messages) != 1 {
		t.Fatalf("appended %d messages, want 1 (the continuation prompt)", len(st.messages))
	}
	if st.messages[0].SessionID != preexistingChildID {
		t.Errorf("appended message SessionID = %q, want %q (resume must target the prior child session)",
			st.messages[0].SessionID, preexistingChildID)
	}
}

// TestChatRunner_ResumeAppendsContinuationPrompt pins that the user
// message appended on a resume is the continuation directive, not the
// original prompt verbatim. The branch is keyed on the prior attempt's
// status recorded in AttemptsJSON.
func TestChatRunner_ResumeAppendsContinuationPrompt(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "ok"},
		{Type: "stream_end"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"}},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:   st,
		invoker: fake,
	}
	originalPrompt := "implement the feature for X"

	cases := []struct {
		name           string
		attempts       string
		wantContinHint string
	}{
		{"over_budget", `[{"status":"over_budget"}]`, "wall-clock backstop"},
		{"stalled", `[{"status":"stalled"}]`, "stream went silent"},
		{"failed", `[{"status":"failed"}]`, "Your prior attempt failed"},
		{"unknown prior status uses generic copy", `[{"status":"weird"}]`, "[continuation]"},
		{"no attempts json yet uses generic copy", "", "[continuation]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st.messages = nil // reset per-case so we look at this case's appended message
			run := &subagent.Run{
				ID:              "run-1",
				Role:            "role-1",
				ParentSessionID: "sess-parent",
				Prompt:          originalPrompt,
				ChildSessionID:  "child-1",
				AttemptsJSON:    tc.attempts,
			}
			if _, err := runner.Run(context.Background(), run); err != nil {
				t.Fatalf("runner.Run: %v", err)
			}
			if len(st.messages) == 0 {
				t.Fatal("no user message was appended")
			}
			body := st.messages[0].Content
			if !strings.Contains(body, tc.wantContinHint) {
				t.Errorf("appended user message = %q\n  expected to contain hint %q", body, tc.wantContinHint)
			}
			// The original prompt is included as a reminder so the model
			// can re-anchor if its working state was compacted.
			if !strings.Contains(body, originalPrompt) {
				t.Errorf("appended user message = %q\n  missing original prompt reminder %q", body, originalPrompt)
			}
		})
	}
}

// TestChatRunner_FirstAttemptCreatesChildSession pins that the first
// attempt (no preset ChildSessionID) still hits the full bootstrap
// path — createChildSession + EnsureSessionAgent + persistChild +
// the original prompt as the user message. This is the regression guard
// for "resume" not accidentally changing first-attempt behavior.
func TestChatRunner_FirstAttemptCreatesChildSession(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "done"},
		{Type: "stream_end"},
	}}
	st := &recordingSessionStore{
		parents: map[string]*store.Session{"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"}},
	}
	persistCalled := 0
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { persistCalled++; return nil },
	}

	originalPrompt := "do the thing"
	run := &subagent.Run{
		ID:              "run-1",
		Role:            "role-1",
		ParentSessionID: "sess-parent",
		Prompt:          originalPrompt,
		// ChildSessionID intentionally empty — first attempt.
	}
	if _, err := runner.Run(context.Background(), run); err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if len(st.created) != 1 {
		t.Errorf("created %d sessions on first attempt, want 1", len(st.created))
	}
	if len(st.bindings) != 1 {
		t.Errorf("EnsureSessionAgent bindings = %d, want 1", len(st.bindings))
	}
	if persistCalled != 1 {
		t.Errorf("persistChild called %d times, want 1", persistCalled)
	}
	// First attempt posts the ORIGINAL prompt verbatim — no continuation
	// hint.
	if len(st.messages) != 1 {
		t.Fatalf("appended %d messages, want 1", len(st.messages))
	}
	if st.messages[0].Content != originalPrompt {
		t.Errorf("first-attempt user message = %q, want %q (verbatim original)",
			st.messages[0].Content, originalPrompt)
	}
}
