package mcp

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestProcedureGet_HappyPath is the end-to-end proof for CW-20260815-0014:
// a procedure seeded for an agent (mirroring what service/ingest.go's
// seedProcedures writes from a profile's `procedures:` frontmatter) is
// actually deliverable to a running agent via the procedure_get tool call,
// not just present in the table.
func TestProcedureGet_HappyPath(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	if err := s.CreateAgent(&store.AgentProfile{ID: "agent-pm-1", Slug: "test-pm-1", Status: "active"}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := s.InsertAgentProcedure(context.Background(), store.AgentProcedure{
		AgentID: "agent-pm-1",
		Name:    "boot",
		Body:    "Step 1. Call torque_task_list. Step 2. ...",
		Scope:   "agent",
	}); err != nil {
		t.Fatalf("seed procedure: %v", err)
	}

	ctx := WithCallerProfile(context.Background(), "agent-pm-1")
	res, err := st.CallTool(ctx, "procedure_get", map[string]any{"name": "boot"})
	if err != nil {
		t.Fatalf("procedure_get error: %v", err)
	}
	if res.IsError {
		t.Fatalf("procedure_get returned IsError: %+v", res)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "Step 1. Call torque_task_list. Step 2. ..." {
		t.Errorf("unexpected body: %+v", res.Content)
	}
}

// TestProcedureGet_ScopedToCallingAgent proves a procedure seeded for one
// agent is NOT visible to a different calling agent — the tool resolves
// the agent from context, not from a caller-suppliable ID.
func TestProcedureGet_ScopedToCallingAgent(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	if err := s.CreateAgent(&store.AgentProfile{ID: "agent-pm-1", Slug: "test-pm-2", Status: "active"}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := s.CreateAgent(&store.AgentProfile{ID: "agent-other", Slug: "test-other", Status: "active"}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := s.InsertAgentProcedure(context.Background(), store.AgentProcedure{
		AgentID: "agent-pm-1",
		Name:    "boot",
		Body:    "PM's private boot procedure",
	}); err != nil {
		t.Fatalf("seed procedure: %v", err)
	}

	ctx := WithCallerProfile(context.Background(), "agent-other")
	res, err := st.CallTool(ctx, "procedure_get", map[string]any{"name": "boot"})
	if err != nil {
		t.Fatalf("procedure_get error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for a different agent's procedure, got: %+v", res)
	}
}

// TestProcedureGet_NotFound proves a clear error (not a panic or empty
// success) when the named procedure doesn't exist for the calling agent.
func TestProcedureGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	ctx := WithCallerProfile(context.Background(), "agent-pm-1")
	res, err := st.CallTool(ctx, "procedure_get", map[string]any{"name": "nonexistent"})
	if err != nil {
		t.Fatalf("procedure_get error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for missing procedure, got: %+v", res)
	}
}

// TestProcedureGet_MissingName proves the required-arg validation fires
// before any store lookup.
func TestProcedureGet_MissingName(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	ctx := WithCallerProfile(context.Background(), "agent-pm-1")
	res, err := st.CallTool(ctx, "procedure_get", map[string]any{})
	if err != nil {
		t.Fatalf("procedure_get error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for missing name, got: %+v", res)
	}
}
