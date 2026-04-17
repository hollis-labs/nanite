package chat

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// newTestSubagentSvc builds a real subagent.Service backed by an in-memory
// store + EchoRunner, matching the pattern subagent tests use. Settings have
// SubagentApprovalRequired=true so Spawn() returns the run in 'requested'.
func newTestSubagentSvc(t *testing.T) *subagent.Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "chat_test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_ = s.Seed() // idempotent

	emitter := &stubApprovalEmitter{} // local fake
	settings := subagentSettingsStub{required: true}

	return subagent.NewService(s.DB, subagent.EchoRunner{}, nil, emitter, settings)
}

// stubApprovalEmitter: satisfies subagent.ApprovalEmitter without persisting.
type stubApprovalEmitter struct{}

func (stubApprovalEmitter) Emit(_ context.Context, sessionID, envelopeType string, _ []byte) (string, error) {
	return "env-stub-" + sessionID + "-" + envelopeType, nil
}

// subagentSettingsStub: satisfies subagent.SettingsReader.
type subagentSettingsStub struct{ required bool }

func (s subagentSettingsStub) GetUserSettings() (*store.UserSettings, error) {
	return &store.UserSettings{SubagentApprovalRequired: s.required}, nil
}

func TestSubagentApprovalHandler_Approve(t *testing.T) {
	svc := newTestSubagentSvc(t)
	runID, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: "s", ParentAgentID: "p",
		Role: "r", Prompt: "hi", Mode: subagent.ModeInteractive,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	h := NewSubagentApprovalHandler(svc)
	envelopeJSON, _ := json.Marshal(map[string]any{"run_id": runID})
	env := store.EnvelopeInstance{
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: string(envelopeJSON),
	}
	res, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Kind: "subagent-spawn-approval", Status: StatusSubmitted,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if res.FollowUp == "" {
		t.Error("expected follow-up on approve")
	}
	if res.TranscriptData["decision"] != "approved" {
		t.Errorf("decision = %v", res.TranscriptData["decision"])
	}
}

func TestSubagentApprovalHandler_Reject(t *testing.T) {
	svc := newTestSubagentSvc(t)
	runID, err := svc.Spawn(context.Background(), subagent.SpawnRequest{
		ParentSessionID: "s", ParentAgentID: "p",
		Role: "r", Prompt: "hi", Mode: subagent.ModeInteractive,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	h := NewSubagentApprovalHandler(svc)
	envelopeJSON, _ := json.Marshal(map[string]any{"run_id": runID})
	env := store.EnvelopeInstance{
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: string(envelopeJSON),
	}
	res, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Kind: "subagent-spawn-approval", Status: StatusCancelled,
		Data: map[string]any{"reason": "nope"},
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if res.FollowUp == "" {
		t.Error("expected follow-up on reject")
	}
	if res.TranscriptData["decision"] != "rejected" {
		t.Errorf("decision = %v", res.TranscriptData["decision"])
	}
	if res.TranscriptData["reason"] != "nope" {
		t.Errorf("reason = %v", res.TranscriptData["reason"])
	}
}

func TestSubagentApprovalHandler_MissingRunID(t *testing.T) {
	h := NewSubagentApprovalHandler(nil) // svc never reached
	env := store.EnvelopeInstance{
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: `{}`,
	}
	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, err) { /* just confirm err is non-nil and readable */ }
}

func TestSubagentApprovalHandler_UnsupportedStatus(t *testing.T) {
	h := NewSubagentApprovalHandler(nil) // not reached
	env := store.EnvelopeInstance{
		EnvelopeType: "subagent-spawn-approval",
		EnvelopeJSON: `{"run_id":"r-1"}`,
	}
	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: "partial",
	})
	if err == nil {
		t.Fatal("expected error for unsupported status")
	}
}
