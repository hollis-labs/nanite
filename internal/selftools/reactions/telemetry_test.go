package reactions

// TASKS/harness-reactive-self-tools/05-selftool-reaction-telemetry.md's
// "Done means" requires two regression tests:
//
//  1. Firing two reactions together (one success, one
//     implemented=false-skip) produces exactly two event_log rows,
//     correctly distinguished by metadata's outcome field, both at
//     category = CategorySelftoolReaction.
//  2. The two telemetry streams (category = "reflex" vs.
//     category = CategorySelftoolReaction) are independently queryable
//     via the existing category-filtered event-log read path
//     ((*store.Store).ListEvents), with no cross-contamination.
//
// Both tests feed a synthetic reactions.Result directly into
// EmitReactionTrace, per this task's own "What to do" item 4: "this task
// itself doesn't need a full end-to-end self-tool to test against."

import (
	"context"
	"encoding/json"
	"testing"
)

// TestEmitReactionTrace_SuccessAndSkip_TwoRowsDistinguishedByOutcome
// covers Done-means bullet 1.
func TestEmitReactionTrace_SuccessAndSkip_TwoRowsDistinguishedByOutcome(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	result := Result{
		ToolName: "task_update_report",
		Reactions: []FiredReaction{
			{
				ReactionID: "reaction-success",
				Kind:       KindInternalAPICall,
				Category:   "execute",
				Config:     `{"endpoint":"/api/example/task-updates","method":"POST","body_template":{}}`,
				Outcome:    OutcomeSuccess,
			},
			{
				ReactionID: "reaction-skip",
				Kind:       KindExternalAPICall,
				Category:   "execute",
				Config:     `{"endpoint":"https://example.invalid/webhook"}`,
				Outcome:    OutcomeSkippedNotImplemented,
			},
		},
	}

	if err := EmitReactionTrace(ctx, s, "tool-call-abc", result); err != nil {
		t.Fatalf("EmitReactionTrace: %v", err)
	}

	events, err := s.ListEvents(CategorySelftoolReaction, 50)
	if err != nil {
		t.Fatalf("ListEvents(%q): %v", CategorySelftoolReaction, err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want exactly 2: %+v", len(events), events)
	}

	type decoded struct {
		eventType string
		detail    string
		meta      map[string]any
	}
	byReactionID := make(map[string]decoded)
	for _, e := range events {
		if e.Category != CategorySelftoolReaction {
			t.Errorf("event category = %q, want %q", e.Category, CategorySelftoolReaction)
		}
		if e.Detail != "task_update_report" {
			t.Errorf("event detail = %q, want tool name %q", e.Detail, "task_update_report")
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(e.Metadata), &meta); err != nil {
			t.Fatalf("event metadata not JSON: %v\nblob: %s", err, e.Metadata)
		}
		if meta["tool_call_id"] != "tool-call-abc" {
			t.Errorf("metadata.tool_call_id = %v, want %q", meta["tool_call_id"], "tool-call-abc")
		}
		id, _ := meta["reaction_id"].(string)
		byReactionID[id] = decoded{eventType: e.EventType, detail: e.Detail, meta: meta}
	}

	success, ok := byReactionID["reaction-success"]
	if !ok {
		t.Fatalf("no event_log row for reaction-success: %+v", events)
	}
	if success.eventType != KindInternalAPICall {
		t.Errorf("reaction-success event_type = %q, want %q", success.eventType, KindInternalAPICall)
	}
	if success.meta["outcome"] != string(OutcomeSuccess) {
		t.Errorf("reaction-success metadata.outcome = %v, want %q", success.meta["outcome"], OutcomeSuccess)
	}
	if _, hasErr := success.meta["error"]; hasErr {
		t.Errorf("reaction-success metadata.error present, want absent (omitempty, no failure): %v", success.meta["error"])
	}

	skip, ok := byReactionID["reaction-skip"]
	if !ok {
		t.Fatalf("no event_log row for reaction-skip: %+v", events)
	}
	if skip.eventType != KindExternalAPICall {
		t.Errorf("reaction-skip event_type = %q, want %q", skip.eventType, KindExternalAPICall)
	}
	if skip.meta["outcome"] != string(OutcomeSkippedNotImplemented) {
		t.Errorf("reaction-skip metadata.outcome = %v, want %q", skip.meta["outcome"], OutcomeSkippedNotImplemented)
	}
}

// TestEmitReactionTrace_ReflexAndSelftoolReaction_IndependentlyQueryable
// covers Done-means bullet 2. The "reflex" stream is synthesized directly
// via (*store.Store).LogEvent — the same event_log sink internal/agent/
// reflexes/telemetry.go's EmitFirings itself writes through — rather than
// importing internal/agent/reflexes, which would widen this package's
// dependency surface for a test-only need and risk exactly the kind of
// cross-package coupling this task's own scope fence (decision 7) exists
// to avoid.
func TestEmitReactionTrace_ReflexAndSelftoolReaction_IndependentlyQueryable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.LogEvent("sess-1", "dispatch_to_agent", "reflex", "some_reflex", `{"reflex_id":"r1"}`)

	result := Result{
		ToolName: "task_update_report",
		Reactions: []FiredReaction{
			{
				ReactionID: "reaction-render",
				Kind:       KindRenderCard,
				Category:   "render",
				Config:     `{"envelope_type":"info-card"}`,
				Outcome:    OutcomeSuccess,
			},
		},
	}
	if err := EmitReactionTrace(ctx, s, "tool-call-xyz", result); err != nil {
		t.Fatalf("EmitReactionTrace: %v", err)
	}

	reflexEvents, err := s.ListEvents("reflex", 50)
	if err != nil {
		t.Fatalf("ListEvents(reflex): %v", err)
	}
	if len(reflexEvents) != 1 {
		t.Fatalf("len(reflexEvents) = %d, want 1: %+v", len(reflexEvents), reflexEvents)
	}
	if reflexEvents[0].EventType != "dispatch_to_agent" {
		t.Errorf("reflex event_type = %q, want %q", reflexEvents[0].EventType, "dispatch_to_agent")
	}

	reactionEvents, err := s.ListEvents(CategorySelftoolReaction, 50)
	if err != nil {
		t.Fatalf("ListEvents(%q): %v", CategorySelftoolReaction, err)
	}
	if len(reactionEvents) != 1 {
		t.Fatalf("len(reactionEvents) = %d, want 1: %+v", len(reactionEvents), reactionEvents)
	}
	if reactionEvents[0].EventType != KindRenderCard {
		t.Errorf("reaction event_type = %q, want %q", reactionEvents[0].EventType, KindRenderCard)
	}

	// No cross-contamination: neither category-filtered read returns a
	// row belonging to the other stream.
	for _, e := range reflexEvents {
		if e.Category == CategorySelftoolReaction {
			t.Errorf("ListEvents(reflex) returned a %s row: %+v", CategorySelftoolReaction, e)
		}
	}
	for _, e := range reactionEvents {
		if e.Category == "reflex" {
			t.Errorf("ListEvents(%s) returned a reflex row: %+v", CategorySelftoolReaction, e)
		}
	}

	// An unfiltered read still sees both, unaffected by either stream's
	// own filtered query.
	all, err := s.ListEvents("", 50)
	if err != nil {
		t.Fatalf("ListEvents(\"\"): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2 (one reflex + one selftool_reaction): %+v", len(all), all)
	}
}
