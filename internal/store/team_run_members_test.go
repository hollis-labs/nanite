package store

import (
	"context"
	"errors"
	"testing"

	"github.com/oklog/ulid/v2"
)

// makeTestWorkflowRun inserts a minimal workflow_runs row (the FK target
// for team_run_members.workflow_run_id) and returns its ID.
func makeTestWorkflowRun(t *testing.T, s *Store) string {
	t.Helper()
	id := "wfr-" + ulid.Make().String()
	if err := s.CreateWorkflowRun(&WorkflowRunRow{ID: id}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	return id
}

func TestTeamRunMember_InsertAndListByRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "trm-insert")
	session := makeTestSession(t, s)
	runID := makeTestWorkflowRun(t, s)

	created, err := s.InsertTeamRunMember(ctx, TeamRunMember{
		WorkflowRunID: runID,
		SlotName:      "orchestrator",
		AgentID:       agent.ID,
		SessionID:     session.ID,
	})
	if err != nil {
		t.Fatalf("InsertTeamRunMember: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("InsertTeamRunMember: expected generated ID")
	}
	if created.Status != TeamRunMemberStatusActive {
		t.Fatalf("InsertTeamRunMember: default status = %q, want %q", created.Status, TeamRunMemberStatusActive)
	}
	if created.ResolvedAt == "" {
		t.Fatalf("InsertTeamRunMember: expected resolved_at to be populated")
	}

	members, err := s.ListTeamRunMembersByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListTeamRunMembersByRun: %v", err)
	}
	if len(members) != 1 || members[0].ID != created.ID {
		t.Fatalf("ListTeamRunMembersByRun: got %+v, want single row %+v", members, created)
	}

	got, err := s.GetTeamRunMember(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTeamRunMember: %v", err)
	}
	if got.SlotName != "orchestrator" || got.AgentID != agent.ID || got.SessionID != session.ID {
		t.Fatalf("GetTeamRunMember: unexpected row: %+v", got)
	}
}

// TestTeamRunMember_ListBySlot_ConcurrentSlotMultipleMembers is this task's
// required "Done means" regression test: insert 3 team_run_members rows for
// one (workflow_run_id, slot_name) pair (simulating a resolved concurrent,
// max:4-capable slot with 3 live members) and confirm
// ListTeamRunMembersBySlot returns all 3, correctly distinguished from a
// different slot's rows in the same run.
func TestTeamRunMember_ListBySlot_ConcurrentSlotMultipleMembers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	runID := makeTestWorkflowRun(t, s)

	// Three concrete engineer members resolved into the same
	// (workflow_run_id, "engineer") slot -- the design doc's
	// `engineer: min:1, max:4` concurrent-activation-mode example.
	engineerIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		agent := makeTestAgent(t, s, "trm-engineer-"+ulid.Make().String())
		session := makeTestSession(t, s)
		m, err := s.InsertTeamRunMember(ctx, TeamRunMember{
			WorkflowRunID: runID,
			SlotName:      "engineer",
			AgentID:       agent.ID,
			SessionID:     session.ID,
		})
		if err != nil {
			t.Fatalf("InsertTeamRunMember engineer #%d: %v", i, err)
		}
		engineerIDs = append(engineerIDs, m.ID)
	}

	// A different slot in the same run -- must not leak into the
	// "engineer" slot's results.
	reviewerAgent := makeTestAgent(t, s, "trm-reviewer")
	reviewerSession := makeTestSession(t, s)
	reviewerMember, err := s.InsertTeamRunMember(ctx, TeamRunMember{
		WorkflowRunID: runID,
		SlotName:      "reviewer",
		AgentID:       reviewerAgent.ID,
		SessionID:     reviewerSession.ID,
	})
	if err != nil {
		t.Fatalf("InsertTeamRunMember reviewer: %v", err)
	}

	engineers, err := s.ListTeamRunMembersBySlot(ctx, runID, "engineer")
	if err != nil {
		t.Fatalf("ListTeamRunMembersBySlot(engineer): %v", err)
	}
	if len(engineers) != 3 {
		t.Fatalf("ListTeamRunMembersBySlot(engineer): got %d rows, want 3: %+v", len(engineers), engineers)
	}
	gotIDs := make(map[string]bool, 3)
	for _, m := range engineers {
		if m.SlotName != "engineer" {
			t.Fatalf("ListTeamRunMembersBySlot(engineer): row with wrong slot_name: %+v", m)
		}
		gotIDs[m.ID] = true
	}
	for _, id := range engineerIDs {
		if !gotIDs[id] {
			t.Fatalf("ListTeamRunMembersBySlot(engineer): missing expected member id %s in %+v", id, engineers)
		}
	}

	reviewers, err := s.ListTeamRunMembersBySlot(ctx, runID, "reviewer")
	if err != nil {
		t.Fatalf("ListTeamRunMembersBySlot(reviewer): %v", err)
	}
	if len(reviewers) != 1 || reviewers[0].ID != reviewerMember.ID {
		t.Fatalf("ListTeamRunMembersBySlot(reviewer): got %+v, want single row %+v", reviewers, reviewerMember)
	}
}

func TestTeamRunMember_UpdateStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "trm-status")
	session := makeTestSession(t, s)
	runID := makeTestWorkflowRun(t, s)

	created, err := s.InsertTeamRunMember(ctx, TeamRunMember{
		WorkflowRunID: runID,
		SlotName:      "engineer",
		AgentID:       agent.ID,
		SessionID:     session.ID,
	})
	if err != nil {
		t.Fatalf("InsertTeamRunMember: %v", err)
	}

	if err := s.UpdateTeamRunMemberStatus(ctx, created.ID, TeamRunMemberStatusStopped); err != nil {
		t.Fatalf("UpdateTeamRunMemberStatus: %v", err)
	}
	got, err := s.GetTeamRunMember(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTeamRunMember: %v", err)
	}
	if got.Status != TeamRunMemberStatusStopped {
		t.Fatalf("Status after update: got %q, want %q", got.Status, TeamRunMemberStatusStopped)
	}

	if err := s.UpdateTeamRunMemberStatus(ctx, "does-not-exist", TeamRunMemberStatusFailed); !errors.Is(err, ErrTeamRunMemberNotFound) {
		t.Fatalf("UpdateTeamRunMemberStatus(missing id): got err=%v, want ErrTeamRunMemberNotFound", err)
	}

	if err := s.UpdateTeamRunMemberStatus(ctx, created.ID, "not-a-real-status"); err == nil {
		t.Fatalf("UpdateTeamRunMemberStatus: expected error for invalid status")
	}
}

func TestTeamRunMember_InsertValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "trm-validate")
	session := makeTestSession(t, s)
	runID := makeTestWorkflowRun(t, s)

	cases := []TeamRunMember{
		{SlotName: "engineer", AgentID: agent.ID, SessionID: session.ID},               // missing WorkflowRunID
		{WorkflowRunID: runID, AgentID: agent.ID, SessionID: session.ID},               // missing SlotName
		{WorkflowRunID: runID, SlotName: "engineer", SessionID: session.ID},            // missing AgentID
		{WorkflowRunID: runID, SlotName: "engineer", AgentID: agent.ID},                // missing SessionID
	}
	for i, c := range cases {
		if _, err := s.InsertTeamRunMember(ctx, c); err == nil {
			t.Fatalf("InsertTeamRunMember case %d: expected validation error, got nil (row: %+v)", i, c)
		}
	}
}
