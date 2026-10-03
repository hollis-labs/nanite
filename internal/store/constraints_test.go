package store

import (
	"errors"
	"fmt"
	"testing"
)

func TestAgentConstraintClassification(t *testing.T) {
	st := newTestStore(t)
	p := makeTestAgent(t, st, "constraints-agent")
	invalid := "missing-role"
	fk := st.UpdateAgentComposition(t.Context(), p.ID, &invalid, nil, nil)
	if fk == nil || !IsForeignKeyViolation(fmt.Errorf("wrapped: %w", fk)) || IsUniqueConstraint(fk) {
		t.Fatalf("foreign key classification: %v", fk)
	}
	duplicate := st.CreateAgent(t.Context(), &AgentProfile{Name: "Duplicate", Slug: p.Slug, SystemPrompt: "fixture"})
	if duplicate == nil || !IsUniqueConstraint(fmt.Errorf("wrapped: %w", duplicate)) || IsForeignKeyViolation(duplicate) {
		t.Fatalf("unique classification: %v", duplicate)
	}
	if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER abort_agent_assignment BEFORE UPDATE OF role_id ON agent_profiles BEGIN SELECT RAISE(ABORT,'private infrastructure failure'); END`); err != nil {
		t.Fatal(err)
	}
	empty := ""
	abort := st.UpdateAgentComposition(t.Context(), p.ID, &empty, nil, nil)
	if abort == nil || IsForeignKeyViolation(abort) || IsUniqueConstraint(abort) {
		t.Fatalf("abort classification: %v", abort)
	}
	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	closed := st.UpdateAgentComposition(t.Context(), p.ID, &empty, nil, nil)
	for _, err := range []error{nil, errors.New("SQLITE_CONSTRAINT_FOREIGNKEY is just text"), closed} {
		if IsForeignKeyViolation(err) || IsUniqueConstraint(err) {
			t.Fatalf("classified an untyped/closed error as a constraint: %v", err)
		}
	}
}
