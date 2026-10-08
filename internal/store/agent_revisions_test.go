package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestAgentRevisionsEveryRowWriter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := makeTestAgent(t, s, "revision-writers")
	if p.Revision == "" {
		t.Fatal("create did not return a persisted revision")
	}
	assertSnapshot := func(previous string, check func(AgentProfile) bool) {
		t.Helper()
		current, err := s.GetAgent(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Revision == previous || current.Revision == "" {
			t.Fatal("writer did not advance the revision")
		}
		row, err := s.GetAgentRevision(ctx, p.ID, current.Revision)
		if err != nil || !check(row.Profile) {
			t.Fatalf("snapshot = %#v, %v", row, err)
		}
		*p = *current
	}
	previous := p.Revision
	p.Description = "direct-store update"
	if err := s.UpdateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(previous, func(p AgentProfile) bool { return p.Description == "direct-store update" })
	previous = p.Revision
	if _, err := s.DB.ExecContext(ctx, `UPDATE agent_profiles SET description = 'raw registry/import writer' WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(previous, func(p AgentProfile) bool { return p.Description == "raw registry/import writer" })
	previous = p.Revision
	role := &Role{Slug: "revision-role", Name: "Revision Role"}
	if err := s.CreateRole(ctx, role); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAgentComposition(ctx, p.ID, &role.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(previous, func(p AgentProfile) bool { return p.RoleID == role.ID })
	previous = p.Revision
	protocol, transport := "acp", "stdio"
	if err := s.UpdateAgentACPConfig(ctx, p.ID, &protocol, &transport); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(previous, func(p AgentProfile) bool { return p.Protocol == "acp" && p.Transport == "stdio" })
	previous = p.Revision
	if err := s.SetAgentDefaultTrustTier(ctx, p.ID, "trusted"); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(previous, func(p AgentProfile) bool { return p.SystemPrompt == "You are a test agent." })
	rows, err := s.ListAgentRevisions(ctx, p.ID, 2, 0)
	if err != nil || len(rows) != 2 || rows[0].Sequence <= rows[1].Sequence {
		t.Fatalf("page/order = %#v, %v", rows, err)
	}
	older, err := s.ListAgentRevisions(ctx, p.ID, 2, 2)
	if err != nil || len(older) != 2 || older[0].Sequence >= rows[1].Sequence {
		t.Fatalf("offset = %#v, %v", older, err)
	}
}

func TestAgentRevisionsUpgradePreservesPreEditBaseline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	provider := newMigrationProvider(t, s)
	if _, err := provider.DownTo(ctx, 174); err != nil {
		t.Fatal(err)
	}
	p := makeTestAgentRawSQL(t, s, "legacy-revisions")
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetAgent(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Revision != "" {
		t.Fatal("migration fabricated application history")
	}
	legacy.SystemPrompt = "bad direct-store overwrite"
	if err = s.UpdateAgent(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListAgentRevisions(ctx, p.ID, 100, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("history = %#v, %v", rows, err)
	}
	if rows[1].Operation != "baseline" || rows[1].Profile.SystemPrompt != "You are a test agent." || rows[0].Profile.SystemPrompt != "bad direct-store overwrite" {
		t.Fatalf("pre-edit baseline lost: %#v", rows)
	}
}

func TestAgentRevisionsHistoryFailureRollsBackProfileAndAssignments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := makeTestAgent(t, s, "history-rollback")
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER reject_agent_history BEFORE INSERT ON agent_profile_revisions
 WHEN json_extract(NEW.profile_json,'$.system_prompt') = 'blocked write'
 BEGIN SELECT RAISE(ABORT,'history unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	updated := *p
	updated.SystemPrompt = "blocked write"
	protocol, transport := "acp", "stdio"
	if _, err := s.UpdateAgentConfigRevision(ctx, &updated, AgentAssignments{Protocol: &protocol, Transport: &transport}, AgentConfigSeeds{}, p.Revision, ""); err == nil {
		t.Fatal("history failure accepted")
	}
	current, err := s.GetAgent(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != p.Revision || current.SystemPrompt != p.SystemPrompt || current.Protocol != "" {
		t.Fatalf("partial write survived rollback: %#v", current)
	}
	rows, err := s.ListAgentRevisions(ctx, p.ID, 100, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("failed write left history: %#v, %v", rows, err)
	}
}

func TestAgentRevisionsConcurrentUpdatesHaveOneWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := makeTestAgent(t, s, "revision-race")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			updated := *p
			updated.Name = name
			_, err := s.UpdateAgentConfigRevision(ctx, &updated, AgentAssignments{}, AgentConfigSeeds{}, p.Revision, "")
			results <- err
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrAgentRevisionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	rows, err := s.ListAgentRevisions(ctx, p.ID, 100, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("losing edit left history: %#v, %v", rows, err)
	}
}

func TestAgentRevisionsSurviveDatabaseReopen(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := &AgentProfile{Name: "Reopen", Slug: "reopen-history", SystemPrompt: "Durable prompt", CanExecute: true, Durable: true, TetherManaged: true, DefaultProvider: "fixture-provider"}
	if err := s.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	path := s.DBPath(ctx)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err = reopened.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	current, err := reopened.GetAgent(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := reopened.GetAgentRevision(ctx, p.ID, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != p.Revision || row.Profile.SystemPrompt != "Durable prompt" || !row.Profile.CanExecute || !row.Profile.Durable || !row.Profile.TetherManaged || row.Profile.DefaultProvider != "fixture-provider" {
		t.Fatalf("reopened history = %#v", row)
	}
}
