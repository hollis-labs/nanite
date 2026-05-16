package subagent

import (
	"context"
	"errors"
	"testing"
)

// stubParentage is a test ParentageChecker. childSessions is the set of
// session ids that should be reported as subagents (have a parent).
type stubParentage struct {
	childSessions map[string]bool
	err           error
}

func (p stubParentage) IsSubagentSession(sessionID string) (bool, error) {
	if p.err != nil {
		return false, p.err
	}
	return p.childSessions[sessionID], nil
}

// TestSpawn_RecursionCap_RejectsParentedCaller is the CW-20260516-0066
// regression test: a spawn requested by a session that is itself a
// subagent (appears as a child_session_id) must be rejected, and a spawn
// from a root session must be allowed.
func TestSpawn_RecursionCap_RejectsParentedCaller(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	svc := NewService(db, EchoRunner{}, poster, nil, stubSettings{})

	// "sess-child" is itself a subagent; "sess-root" is a root session.
	svc.SetParentageChecker(stubParentage{
		childSessions: map[string]bool{"sess-child": true},
	})

	// Root session may spawn.
	rootID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-root",
		ParentAgentID:   "chat",
		Role:            "file-summarizer",
		Prompt:          "do the task",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("root spawn rejected unexpectedly: %v", err)
	}
	if rootID == "" {
		t.Fatal("root spawn returned empty run id")
	}

	// Subagent session must be rejected.
	childID, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-child",
		ParentAgentID:   "worker",
		Role:            "file-summarizer",
		Prompt:          "re-dispatch the task",
		Mode:            ModeSync,
	})
	if err == nil {
		t.Fatal("subagent spawn was allowed; recursion cap not enforced")
	}
	if !errors.Is(err, ErrRecursionBlocked) {
		t.Errorf("error = %v, want ErrRecursionBlocked", err)
	}
	if childID != "" {
		t.Errorf("rejected spawn returned non-empty run id %q — orphan row risk", childID)
	}

	// The rejected spawn must not have created a subagent_runs row.
	var rows int
	if qerr := db.QueryRow(
		`SELECT COUNT(*) FROM subagent_runs WHERE parent_session_id = ?`, "sess-child",
	).Scan(&rows); qerr != nil {
		t.Fatalf("count rows: %v", qerr)
	}
	if rows != 0 {
		t.Errorf("rejected spawn left %d subagent_runs row(s); want 0", rows)
	}
}

// TestSpawn_RecursionCap_FailsClosedOnCheckError verifies that a
// parentage-check DB error fails closed — the spawn is refused rather
// than risking an unbounded recursive chain.
func TestSpawn_RecursionCap_FailsClosedOnCheckError(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetParentageChecker(stubParentage{err: errors.New("db exploded")})

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-root",
		ParentAgentID:   "chat",
		Role:            "file-summarizer",
		Prompt:          "do the task",
		Mode:            ModeSync,
	})
	if err == nil {
		t.Fatal("spawn allowed despite parentage-check error; expected fail-closed")
	}
}

// TestSpawn_RecursionCap_DisabledWhenUnwired verifies that with no
// ParentageChecker wired, Spawn behaves as before (cap is off) — both
// root and would-be-child sessions spawn successfully.
func TestSpawn_RecursionCap_DisabledWhenUnwired(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	// No SetParentageChecker call.

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-any",
		ParentAgentID:   "chat",
		Role:            "file-summarizer",
		Prompt:          "do the task",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("spawn rejected with cap unwired: %v", err)
	}
	if id == "" {
		t.Fatal("spawn returned empty run id")
	}
}
