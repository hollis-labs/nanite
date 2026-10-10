package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestHistoricalRevisionJournalPreservedAndMutableRestoreRefused(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p := makeTestAgentRawSQL(t, s, "historical-journal")
	before, err := s.GetHistoricalAgentProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListAgentRevisions(ctx, p.ID, 100, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	changed := *before
	changed.SystemPrompt = "refused change"
	if _, err = s.UpdateAgentConfigRevision(ctx, &changed, AgentAssignments{}, AgentConfigSeeds{}, before.Revision, rows[0].ID); !errors.Is(err, ErrImmutableAgentProfile) {
		t.Fatal(err)
	}
	after, err := s.GetHistoricalAgentProfile(ctx, p.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal(after, err)
	}
	path := s.DBPath(ctx)
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close(ctx) }()
	persisted, err := reopened.ListAgentRevisions(ctx, p.ID, 100, 0)
	if err != nil || !reflect.DeepEqual(rows, persisted) {
		t.Fatal("journal changed after reopen", persisted, err)
	}
}
func TestHistoricalRevisionUpgradeRetainsBaselineOnExplicitHistoricalWriter(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	provider := newMigrationProvider(t, s)
	if _, err := provider.DownTo(ctx, 174); err != nil {
		t.Fatal(err)
	}
	p := makeTestAgentRawSQL(t, s, "pre-journal")
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetHistoricalAgentProfile(ctx, p.ID)
	if err != nil || before.Revision != "" {
		t.Fatal(before, err)
	}
	// An external historical writer is simulated only in this private database.
	// The audit trigger still retains its baseline; runtime never reads it.
	if _, err = s.DB.ExecContext(ctx, `UPDATE agent_profiles SET system_prompt='external historical edit' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListAgentRevisions(ctx, p.ID, 100, 0)
	if err != nil || len(rows) != 2 || rows[1].Operation != "baseline" || rows[1].Profile.SystemPrompt != "You are a test agent." || rows[0].Profile.SystemPrompt != "external historical edit" {
		t.Fatal(rows, err)
	}
}
