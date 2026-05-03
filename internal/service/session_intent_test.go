package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestClassifySessionIntent_PinnedIsLongRunning(t *testing.T) {
	got := ClassifySessionIntent(context.Background(), IntentSignals{
		IsPinned:     true,
		HasWorkspace: true,
	})
	if got != store.SessionIntentLongRunning {
		t.Errorf("pinned + workspace should classify long-running; got %q", got)
	}
}

func TestClassifySessionIntent_BarePerTurn(t *testing.T) {
	// Workspace present but no other strong signals → per-turn.
	got := ClassifySessionIntent(context.Background(), IntentSignals{
		HasWorkspace: true,
	})
	if got != store.SessionIntentPerTurn {
		t.Errorf("workspace-only should classify per-turn; got %q (score %d)",
			got, ScoreIntent(IntentSignals{HasWorkspace: true}))
	}
}

func TestClassifySessionIntent_FileDefaultEphemeral(t *testing.T) {
	// file-default agent + no workspace → ephemeral.
	got := ClassifySessionIntent(context.Background(), IntentSignals{
		AgentIsDefault: true,
	})
	if got != store.SessionIntentEphemeral {
		t.Errorf("file-default with nothing else should classify ephemeral; got %q", got)
	}
}

func TestClassifySessionIntent_PlanModeIsLongRunning(t *testing.T) {
	got := ClassifySessionIntent(context.Background(), IntentSignals{
		HasSessionMode:  true,
		SessionModeSlug: "plan",
		HasProject:      true,
	})
	if got != store.SessionIntentLongRunning {
		t.Errorf("plan-mode + project should classify long-running; got %q", got)
	}
}

func TestScoreIntent_MessageCountBonusCaps(t *testing.T) {
	// MessageCount=30 → bonus 10 → capped at +3.
	low := ScoreIntent(IntentSignals{MessageCount: 0})
	high := ScoreIntent(IntentSignals{MessageCount: 30})
	if high-low > 3 {
		t.Errorf("message-count bonus should cap at +3; delta = %d", high-low)
	}
	if high-low < 3 {
		t.Errorf("message-count=30 should hit the cap (+3); delta = %d", high-low)
	}
}

func TestSignalsFromSession_PicksUpPinAndProject(t *testing.T) {
	sess := &store.Session{
		WorkspaceID: "ws1",
		ProjectID:   "p1",
		IsPinned:    true,
	}
	mode := &store.Mode{Slug: "work"}
	agent := &store.AgentProfile{
		ID:         "agent-1",
		Slug:       "backend",
		CanExecute: true,
		Tags:       `["backend","worker"]`,
	}

	signals := SignalsFromSession(sess, agent, mode)
	if !signals.IsPinned || !signals.HasProject || !signals.HasWorkspace {
		t.Errorf("session signals not picked up: %+v", signals)
	}
	if !signals.HasSessionMode || signals.SessionModeSlug != "work" {
		t.Errorf("mode signals not picked up: %+v", signals)
	}
	if !signals.AgentCanExecute || signals.AgentIsDefault {
		t.Errorf("agent signals wrong: %+v", signals)
	}
	if len(signals.AgentTags) != 2 {
		t.Errorf("agent tags not parsed: %+v", signals.AgentTags)
	}
}

func TestSignalsFromSession_NilSafe(t *testing.T) {
	signals := SignalsFromSession(nil, nil, nil)
	if signals.IsPinned || signals.HasWorkspace || signals.HasProject ||
		signals.HasSessionMode || signals.AgentCanExecute || signals.AgentIsDefault ||
		signals.SessionModeSlug != "" || signals.AgentSlug != "" ||
		len(signals.AgentTags) != 0 || signals.MessageCount != 0 {
		t.Errorf("nil inputs should produce zero signals; got %+v", signals)
	}
}

func TestSignalsFromSession_FileDefaultAgentDetected(t *testing.T) {
	sess := &store.Session{}
	agent := &store.AgentProfile{ID: "file-default", Slug: "file-default"}
	signals := SignalsFromSession(sess, agent, nil)
	if !signals.AgentIsDefault {
		t.Errorf("file-default agent should be flagged as AgentIsDefault")
	}
}
