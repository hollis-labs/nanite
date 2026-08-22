// Integration coverage for TASKS/phase-4/03
// (migrate-promptrouter-to-reflexes.md)'s Done means: "All 6 non-phantom
// BuiltinReflexes() entries have real, working DB-backed reflex
// equivalents, verified by triggering each phrase in a real session and
// confirming the same dispatch target fires as before."
//
// Same real-pipeline boundary as
// chat_reflex_dispatch_integration_test.go (TASKS/phase-4/02): a REAL
// *store.Store (migrations applied), the REAL seeded agent_reflexes rows
// (via reflexes.SeedBaseReflexes — proves the actual seeds.go data, not a
// hand-authored fixture), real classify.Classify (via classifyAndAttach),
// the real reflexes.Engine, and the real attemptReflexDispatch call site.
// Only the outermost boundary — ToolService.Execute, i.e. what happens
// once task_execute is invoked — is faked; a real subagent spawn needs a
// live LLM provider, which this worker environment has no credentials
// for (see the task file's Work Log for the full honest accounting of
// what this does and doesn't exercise).
package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAttemptReflexDispatch_PromptrouterMigratedPhrases_FireExpectedTarget
// exercises each of the 6 non-phantom migrated internal/promptrouter
// BuiltinReflexes() entries (seeds.go's dispatch_to_agent_background_
// long_task / _planner_mention / _planner_large_task / _researcher_
// mention / _reviewer_mention / _worker_execute) with a real message that
// hits that reflex specifically (verified not to also satisfy any
// higher-priority dispatch_to_agent reflex, so the assertion proves THAT
// reflex fired, not a coincidentally-same-target neighbor).
func TestAttemptReflexDispatch_PromptrouterMigratedPhrases_FireExpectedTarget(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		wantReflex string
		wantSlug   string
	}{
		{
			// Old promptrouter entry: background-long-task (Priority 25 ->
			// AgentSlug "worker" via Resolution{Profile: "worker"}).
			// "in the background" is in both promptrouter's phrase list
			// AND classify.ExecutionPatternBackgroundKeywords, so it
			// satisfies both the phrase-match clause and the
			// execution_pattern="background" clause.
			name:       "background-long-task",
			message:    "index the whole codebase in the background",
			wantReflex: "dispatch_to_agent_background_long_task",
			wantSlug:   "worker",
		},
		{
			// Old promptrouter entry: planner-mention (Priority 20 ->
			// AgentSlug "planner"). "full migration" hits
			// classify.ScopeTierOpenKeywords (-> scope_tier=open);
			// "let's plan" is a planner-mention phrase.
			name:       "planner-mention",
			message:    "let's plan out the full migration end-to-end starting next sprint",
			wantReflex: "dispatch_to_agent_planner_mention",
			wantSlug:   "planner",
		},
		{
			// Old promptrouter entry: planner-large-task (Priority 18 ->
			// AgentSlug "planner"). "refactor" hits
			// classify.ScopeTierLargeKeywords (-> scope_tier=large);
			// "phases"/"big project" are planner-large-task phrases.
			name:       "planner-large-task",
			message:    "refactor this into multiple phases, it's a big project",
			wantReflex: "dispatch_to_agent_planner_large_task",
			wantSlug:   "planner",
		},
		{
			// Old promptrouter entry: researcher-mention (Priority 15 ->
			// AgentSlug "researcher"). No tier/pattern guard — pure
			// phrase match ("look into").
			name:       "researcher-mention",
			message:    "please look into the auth flow errors",
			wantReflex: "dispatch_to_agent_researcher_mention",
			wantSlug:   "researcher",
		},
		{
			// Old promptrouter entry: reviewer-mention (Priority 15 ->
			// AgentSlug "reviewer"). No tier/pattern guard — pure phrase
			// match ("review").
			name:       "reviewer-mention",
			message:    "can you review this PR",
			wantReflex: "dispatch_to_agent_reviewer_mention",
			wantSlug:   "reviewer",
		},
		{
			// Old promptrouter entry: worker-execute (Priority 10 ->
			// AgentSlug "worker"). No tier/pattern guard — pure phrase
			// match ("fix").
			name:       "worker-execute",
			message:    "please fix the login bug",
			wantReflex: "dispatch_to_agent_worker_execute",
			wantSlug:   "worker",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-"+tc.name+".db")
			st, err := store.New(context.Background(), dbPath)
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}
			t.Cleanup(func() {
				_ = st.Close(context.Background(

				// Real seed data — the same call container.go makes at boot.
				// Proves seeds.go's migrated entries (not a hand-authored
				// test fixture) actually reproduce the old promptrouter
				// catalog's dispatch target.
				))
			})

			if _, err := reflexes.SeedBaseReflexes(context.Background(), st, nil); err != nil {
				t.Fatalf("SeedBaseReflexes: %v", err)
			}

			tools := &recordingReflexDispatchToolService{}
			s := &chatServiceImpl{
				store:        st,
				reflexEngine: reflexes.NewEngine(st, nil),
				tools:        tools,
			}

			ls := newLoopState(chat.AgentConstraints{}, nil, false)
			classifyAndAttach(ls, "sess-"+tc.name, tc.message, nil)

			ch := make(chan chat.StreamEvent, 4)
			out := s.attemptReflexDispatch(
				context.Background(),
				"sess-"+tc.name, "turn-"+tc.name, tc.message,
				"agent-"+tc.name, "advisor",
				ls, ch,
			)
			close(ch)

			if !out.Matched {
				t.Fatalf("attemptReflexDispatch: Matched = false, want true (%s should have fired for %q)", tc.wantReflex, tc.message)
			}
			if out.ReflexName != tc.wantReflex {
				t.Errorf("ReflexName = %q, want %q (a different/higher-priority reflex fired instead — check for a phrase-list collision)", out.ReflexName, tc.wantReflex)
			}
			if out.AgentSlug != tc.wantSlug {
				t.Errorf("AgentSlug = %q, want %q", out.AgentSlug, tc.wantSlug)
			}
			if !out.Invoked {
				t.Errorf("Invoked = false, want true (task_execute should have been called)")
			}
			if len(tools.calls) != 1 {
				t.Fatalf("ToolService.Execute called %d times, want 1", len(tools.calls))
			}
			if tools.calls[0].toolName != "task_execute" {
				t.Errorf("tool name = %q, want task_execute", tools.calls[0].toolName)
			}
		})
	}
}
