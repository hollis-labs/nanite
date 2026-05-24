package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestAgentLog_AppendAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "log-rt")

	// Append three entries with caller-supplied UUIDs.
	for _, kind := range []string{"pass", "lesson", "pass"} {
		row := AgentLogEntry{
			ID:        uuid.New().String(),
			AgentID:   agent.ID,
			SessionID: "sess-1",
			Kind:      kind,
			Entry:     "entry body for " + kind,
		}
		if err := s.AppendLog(ctx, row); err != nil {
			t.Fatalf("AppendLog(%s): %v", kind, err)
		}
	}

	all, err := s.ListLog(ctx, agent.ID, 0)
	if err != nil {
		t.Fatalf("ListLog all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListLog all: got %d, want 3", len(all))
	}

	// Verify the limit clamp.
	top, err := s.ListLog(ctx, agent.ID, 2)
	if err != nil {
		t.Fatalf("ListLog limit=2: %v", err)
	}
	if len(top) != 2 {
		t.Fatalf("ListLog limit=2: got %d, want 2", len(top))
	}

	// Verify timestamp got populated by the column default.
	if all[0].Timestamp == "" {
		t.Error("Timestamp: expected default-populated value, got empty")
	}
}

func TestAgentLog_AppendRequiresID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "log-no-id")

	err := s.AppendLog(ctx, AgentLogEntry{AgentID: agent.ID, Kind: "x", Entry: "y"})
	if err == nil {
		t.Fatal("AppendLog without ID: want error, got nil")
	}
}
