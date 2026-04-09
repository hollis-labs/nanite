package a2a

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// newHandoffTestSession inserts a real sessions row so that the
// session_handoffs(session_id) foreign key is satisfied. The session gets a
// unique short_code via the store's auto-generation, so multiple calls in a
// single test are safe. WorkspaceID is left empty so CreateSession stores
// NULL for workspace_id and doesn't trip the FK against the workspaces
// table (which is empty in tests).
func newHandoffTestSession(t *testing.T, s *store.Store) *store.Session {
	t.Helper()
	sess := &store.Session{}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

func TestHandoff_FullFlow(t *testing.T) {
	svc, s := newTestService(t, "file-backend", "file-frontend")

	sess := newHandoffTestSession(t, s)
	if err := s.EnsureSessionAgent(sess.ID, "file-backend", "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	handoffID, err := svc.RequestHandoff(context.Background(), sess.ID, "file-backend", "file-frontend", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}
	if handoffID == "" {
		t.Error("empty handoff id")
	}

	if err := svc.ApproveHandoff(context.Background(), handoffID); err != nil {
		t.Fatalf("ApproveHandoff: %v", err)
	}

	primary, err := s.GetSessionPrimaryAgent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionPrimaryAgent: %v", err)
	}
	if primary.AgentID != "file-frontend" {
		t.Errorf("primary = %q, want file-frontend", primary.AgentID)
	}
}

func TestHandoff_DoubleRequest(t *testing.T) {
	svc, s := newTestService(t, "file-a", "file-b", "file-c")

	sess := newHandoffTestSession(t, s)
	if err := s.EnsureSessionAgent(sess.ID, "file-a", "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	h1, err := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-b", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff h1: %v", err)
	}
	h2, err := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-c", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff h2: %v", err)
	}

	if err := svc.ApproveHandoff(context.Background(), h1); err != nil {
		t.Fatalf("ApproveHandoff h1: %v", err)
	}

	status, err := svc.getHandoffStatus(h2)
	if err != nil {
		t.Fatalf("getHandoffStatus h2: %v", err)
	}
	if status != "rejected" {
		t.Errorf("h2 status = %q, want rejected", status)
	}
}

func TestHandoff_ApproveCompleted_Idempotent(t *testing.T) {
	svc, s := newTestService(t, "file-a", "file-b")

	sess := newHandoffTestSession(t, s)
	if err := s.EnsureSessionAgent(sess.ID, "file-a", "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	h, err := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-b", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}
	if err := svc.ApproveHandoff(context.Background(), h); err != nil {
		t.Fatalf("ApproveHandoff first: %v", err)
	}
	if err := svc.ApproveHandoff(context.Background(), h); err != nil {
		t.Errorf("second approve errored: %v", err)
	}
}

func TestHandoff_ApproveRejected_Errors(t *testing.T) {
	svc, s := newTestService(t, "file-a", "file-b")

	sess := newHandoffTestSession(t, s)
	if err := s.EnsureSessionAgent(sess.ID, "file-a", "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	h, err := svc.RequestHandoff(context.Background(), sess.ID, "file-a", "file-b", "departing")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}
	if err := svc.RejectHandoff(context.Background(), h, "test"); err != nil {
		t.Fatalf("RejectHandoff: %v", err)
	}
	err = svc.ApproveHandoff(context.Background(), h)
	if err == nil {
		t.Fatal("expected error approving rejected handoff")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("expected error to mention rejected, got: %v", err)
	}
}
