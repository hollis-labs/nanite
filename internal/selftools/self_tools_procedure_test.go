package selftools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// Retained procedure history cannot be restored as runtime content.
func TestProcedureGet_HappyPath(t *testing.T) {
	db := newTestStore(t)
	st := newTestSelfToolsTransport(db)
	profile := retainedSelfToolProfile(t, db, "test-pm-1", "user")
	before := retainedSelfToolState(t, db)
	if _, err := db.GetAgentProcedure(t.Context(), profile.ID, "boot"); !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("procedure source = %v", err)
	}
	ctx := mcp.WithCallerProfile(t.Context(), profile.ID)
	result, err := st.CallTool(ctx, "procedure_get", map[string]any{"name": "boot"})
	if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, store.ErrImmutableAgentProfile.Error()) || strings.Contains(result.Content[0].Text, "PRIVATE HISTORICAL PROCEDURE") {
		t.Fatalf("retained procedure = %+v,%v", result, err)
	}
	assertRetainedSelfToolState(t, db, before)
}

// Caller metadata cannot expose retired procedure history or replace reader controls.
func TestProcedureGet_ScopedToCallingAgent(t *testing.T) {
	db := newTestStore(t)
	st := newTestSelfToolsTransport(db)
	owner := retainedSelfToolProfile(t, db, "test-pm-2", "user")
	other := retainedSelfToolProfile(t, db, "test-other", "user")
	before := retainedSelfToolState(t, db)
	for _, caller := range []string{owner.ID, other.ID, "claimed-actor"} {
		ctx := mcp.WithCallerProfile(t.Context(), caller)
		result, err := st.CallTool(ctx, "procedure_get", map[string]any{"name": "boot", "agent_id": owner.ID})
		if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, store.ErrImmutableAgentProfile.Error()) || strings.Contains(result.Content[0].Text, "PRIVATE HISTORICAL PROCEDURE") {
			t.Fatalf("caller %q retained procedure = %+v,%v", caller, result, err)
		}
		assertRetainedSelfToolState(t, db, before)
	}
	// Required arguments and missing context still report their own errors.
	for _, test := range []struct {
		ctx  context.Context
		args map[string]any
		want string
	}{
		{t.Context(), map[string]any{"name": "boot"}, "no calling agent"},
		{mcp.WithCallerProfile(t.Context(), owner.ID), nil, "name is required"},
	} {
		result, err := st.CallTool(test.ctx, "procedure_get", test.args)
		if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, test.want) {
			t.Fatalf("procedure control = %+v,%v want %q", result, err, test.want)
		}
	}
	st.Reads.Procedures = nil
	result, err := st.CallTool(t.Context(), "procedure_get", map[string]any{"name": "boot"})
	if err != nil || result == nil || !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "no store configured") {
		t.Fatalf("missing reader = %+v,%v", result, err)
	}
	assertRetainedSelfToolState(t, db, before)
}

// TestProcedureGet_NotFound proves a clear error (not a panic or empty
// success) when the named procedure doesn't exist for the calling agent.
func TestProcedureGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	st := newTestSelfToolsTransport(s)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-pm-1")
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
	st := newTestSelfToolsTransport(s)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-pm-1")
	res, err := st.CallTool(ctx, "procedure_get", map[string]any{})
	if err != nil {
		t.Fatalf("procedure_get error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for missing name, got: %+v", res)
	}
}
