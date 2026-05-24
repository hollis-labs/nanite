package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentBootPlanRoundTrip(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "boot-plan-roundtrip")

	doc, err := s.PutAgentBootPlan(context.Background(), AgentBootPlanDocument{
		AgentID:       agent.ID,
		SchemaVersion: AgentBootPlanSchemaVersion1,
		PlantItems: []AgentBootPlantItem{
			{
				ID:              "seed-readme",
				Name:            "Seed README",
				SourceKind:      "literal_file",
				Content:         "# hello\n",
				TargetRelPath:   "docs/README.md",
				EntryKind:       "file",
				Timing:          []string{"create", "every_boot"},
				OverwritePolicy: "if_missing",
				FailurePolicy:   "fail_boot",
				Enabled:         true,
			},
		},
		Callbacks: []AgentBootCallback{
			{
				ID:             "inject-note",
				Name:           "Inject Note",
				Timing:         "after_boot",
				CallbackType:   "message_injection",
				Message:        "Boot complete.",
				TimeoutSeconds: 30,
				FailurePolicy:  "warn",
				Enabled:        true,
			},
		},
	})
	if err != nil {
		t.Fatalf("PutAgentBootPlan: %v", err)
	}
	if doc.CreatedAt == "" || doc.UpdatedAt == "" {
		t.Fatalf("timestamps missing: %+v", doc)
	}

	got, err := s.GetAgentBootPlan(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("GetAgentBootPlan: %v", err)
	}
	if got.AgentID != agent.ID || got.SchemaVersion != AgentBootPlanSchemaVersion1 {
		t.Fatalf("unexpected boot plan header: %+v", got)
	}
	if len(got.PlantItems) != 1 || got.PlantItems[0].TargetRelPath != "docs/README.md" {
		t.Fatalf("plant items mismatch: %+v", got.PlantItems)
	}
	if len(got.Callbacks) != 1 || got.Callbacks[0].CallbackType != "message_injection" {
		t.Fatalf("callbacks mismatch: %+v", got.Callbacks)
	}
}

func TestDeleteAgentBootPlan(t *testing.T) {
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "boot-plan-delete")

	if _, err := s.PutAgentBootPlan(context.Background(), AgentBootPlanDocument{
		AgentID:       agent.ID,
		SchemaVersion: AgentBootPlanSchemaVersion1,
	}); err != nil {
		t.Fatalf("PutAgentBootPlan: %v", err)
	}
	if err := s.DeleteAgentBootPlan(context.Background(), agent.ID); err != nil {
		t.Fatalf("DeleteAgentBootPlan: %v", err)
	}
	if _, err := s.GetAgentBootPlan(context.Background(), agent.ID); !errors.Is(err, ErrAgentBootPlanNotFound) {
		t.Fatalf("GetAgentBootPlan after delete = %v, want ErrAgentBootPlanNotFound", err)
	}
}
