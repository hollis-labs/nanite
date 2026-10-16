package store

import (
	"errors"
	"fmt"
	"testing"
)

func TestAgentConstraintClassification(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "constraint-host")
	_, fk := s.DB.ExecContext(t.Context(), `INSERT INTO agent_actor_bindings(actor_uri,host_settings_id,binding_receipt) VALUES('msg://agent/private-fixture/missing','missing','fixture')`)
	if fk == nil || !IsForeignKeyViolation(fmt.Errorf("wrapped: %w", fk)) || IsUniqueConstraint(fk) {
		t.Fatal("FK classification", fk)
	}
	_, duplicate := s.DB.ExecContext(t.Context(), `INSERT INTO agent_host_settings SELECT * FROM agent_host_settings WHERE id=?`, h.ID)
	if duplicate == nil || !IsUniqueConstraint(fmt.Errorf("wrapped: %w", duplicate)) || IsForeignKeyViolation(duplicate) {
		t.Fatal("unique classification", duplicate)
	}
	if _, err := s.DB.ExecContext(t.Context(), `CREATE TRIGGER refuse_host_update BEFORE UPDATE ON agent_host_settings BEGIN SELECT RAISE(ABORT,'private infrastructure failure');END`); err != nil {
		t.Fatal(err)
	}
	h.Title = "Blocked"
	_, abort := s.UpdateAgentHostSettings(t.Context(), h, h.Revision)
	if abort == nil || IsForeignKeyViolation(abort) || IsUniqueConstraint(abort) {
		t.Fatal("abort classification", abort)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	_, closed := s.GetAgentHostSettings(t.Context(), h.ID)
	for _, err := range []error{nil, errors.New("SQLITE_CONSTRAINT_FOREIGNKEY is just text"), closed} {
		if IsForeignKeyViolation(err) || IsUniqueConstraint(err) {
			t.Fatal(err)
		}
	}
}
