package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// TestEnvelopeResponse_SubagentApproval_Approve drives the full G-4 gated flow:
// spawn-with-interactive-mode → envelope persisted + streamed → POST respond
// with Status=submitted → Approve dispatches the runner → run leaves requested.
func TestEnvelopeResponse_SubagentApproval_Approve(t *testing.T) {
	a, mux := newTestAPI(t)

	if err := a.Services.Store.CreateWorkspace(&store.Workspace{ID: "ws-g4-a", Name: "g4a"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws-g4-a"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := a.Services.Store.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings: %v", err)
	}
	// Seed an agent with slug "r" so the ChatRunner can resolve the role.
	if err := a.Services.Store.CreateAgent(&store.AgentProfile{
		ID: "agent-r", Name: "R", Slug: "r", SystemPrompt: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	svc := a.Services.Subagent
	runID, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: sess.ID,
		ParentAgentID:   "primary",
		Role:            "r",
		Prompt:          "hi",
		Mode:            subagent.ModeInteractive,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	run, err := svc.Status(context.Background(), runID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if run.EnvelopeInstanceID == "" {
		t.Fatalf("envelope instance not persisted on run %s", runID)
	}

	body, _ := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "subagent-spawn-approval", ID: run.EnvelopeInstanceID,
		Status: chat.StatusSubmitted,
	})
	w := doPost(mux, "/api/envelopes/"+run.EnvelopeInstanceID+"/respond", body)
	if w.Code != http.StatusOK {
		t.Fatalf("respond: %d body=%s", w.Code, w.Body.String())
	}

	// Poll until the run leaves the requested state (runner was dispatched).
	// In the test environment the ChatRunner may fail (no real provider),
	// so we accept any terminal state as evidence that approve dispatched correctly.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, _ = svc.Status(context.Background(), runID)
		switch run.Status {
		case subagent.StatusCompleted, subagent.StatusFailed:
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, _ = svc.Status(context.Background(), runID)
	t.Fatalf("run did not leave requested state after approve; status=%q", run.Status)
}

// TestEnvelopeResponse_SubagentApproval_Reject drives: spawn-with-interactive
// → POST respond with Status=cancelled + reason → row transitions to rejected
// with rejection_reason persisted.
func TestEnvelopeResponse_SubagentApproval_Reject(t *testing.T) {
	a, mux := newTestAPI(t)

	if err := a.Services.Store.CreateWorkspace(&store.Workspace{ID: "ws-g4-r", Name: "g4r"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws-g4-r"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := a.Services.Store.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings: %v", err)
	}

	svc := a.Services.Subagent
	runID, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: sess.ID,
		ParentAgentID:   "primary",
		Role:            "r",
		Prompt:          "hi",
		Mode:            subagent.ModeInteractive,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	run, _ := svc.Status(context.Background(), runID)
	envelopeID := run.EnvelopeInstanceID
	if envelopeID == "" {
		t.Fatalf("envelope instance not persisted")
	}

	body, _ := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "subagent-spawn-approval", ID: envelopeID,
		Status: chat.StatusCancelled,
		Data:   map[string]any{"reason": "too risky"},
	})
	w := doPost(mux, "/api/envelopes/"+envelopeID+"/respond", body)
	if w.Code != http.StatusOK {
		t.Fatalf("respond: %d body=%s", w.Code, w.Body.String())
	}

	run, _ = svc.Status(context.Background(), runID)
	if run.Status != subagent.StatusRejected {
		t.Errorf("status = %q, want rejected", run.Status)
	}
	if run.RejectionReason != "too risky" {
		t.Errorf("rejection_reason = %q, want 'too risky'", run.RejectionReason)
	}
}
