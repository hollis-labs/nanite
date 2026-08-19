package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestConsumersMigration_SeedsLoomRow verifies migration 112 creates the
// consumers table and seeds one real row for Loom, per
// TASKS/phase-1/03-add-consumers-table.md step 3.
func TestConsumersMigration_SeedsLoomRow(t *testing.T) {
	s := newTestStore(t)

	loom, err := s.GetConsumerBySlug("loom")
	if err != nil {
		t.Fatalf("GetConsumerBySlug(loom): %v", err)
	}
	if loom == nil {
		t.Fatal("expected a seeded Loom consumer row, got none")
	}
	if loom.Name != "Loom" {
		t.Errorf("Loom consumer Name: got %q want %q", loom.Name, "Loom")
	}
	if loom.ID == "" {
		t.Error("Loom consumer ID is empty")
	}

	// A re-migrate (simulated restart) must not duplicate the seed row.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	all, err := s.ListConsumers()
	if err != nil {
		t.Fatalf("ListConsumers: %v", err)
	}
	count := 0
	for _, c := range all {
		if c.Slug == "loom" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("loom consumer rows after re-migrate: got %d want 1", count)
	}
}

// TestConsumersCRUD exercises Create/Get/GetBySlug/Update/List/Delete on the
// consumers store layer.
func TestConsumersCRUD(t *testing.T) {
	s := newTestStore(t)

	c := &Consumer{Slug: "acme", Name: "Acme Corp"}
	if err := s.CreateConsumer(c); err != nil {
		t.Fatalf("CreateConsumer: %v", err)
	}
	if c.ID == "" {
		t.Fatal("CreateConsumer did not assign an ID")
	}
	if c.CreatedAt == "" {
		t.Error("CreateConsumer did not stamp CreatedAt")
	}

	got, err := s.GetConsumer(c.ID)
	if err != nil {
		t.Fatalf("GetConsumer: %v", err)
	}
	if got == nil || got.Slug != "acme" || got.Name != "Acme Corp" {
		t.Fatalf("GetConsumer round-trip: got %+v", got)
	}

	gotBySlug, err := s.GetConsumerBySlug("acme")
	if err != nil {
		t.Fatalf("GetConsumerBySlug: %v", err)
	}
	if gotBySlug == nil || gotBySlug.ID != c.ID {
		t.Fatalf("GetConsumerBySlug round-trip: got %+v", gotBySlug)
	}

	c.Name = "Acme Corporation"
	if err := s.UpdateConsumer(c); err != nil {
		t.Fatalf("UpdateConsumer: %v", err)
	}
	updated, err := s.GetConsumer(c.ID)
	if err != nil {
		t.Fatalf("GetConsumer after update: %v", err)
	}
	if updated.Name != "Acme Corporation" {
		t.Errorf("Name after update: got %q want %q", updated.Name, "Acme Corporation")
	}

	all, err := s.ListConsumers()
	if err != nil {
		t.Fatalf("ListConsumers: %v", err)
	}
	found := false
	for _, item := range all {
		if item.ID == c.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListConsumers did not include the created consumer")
	}

	if err := s.DeleteConsumer(c.ID); err != nil {
		t.Fatalf("DeleteConsumer: %v", err)
	}
	gone, err := s.GetConsumer(c.ID)
	if err != nil {
		t.Fatalf("GetConsumer after delete: %v", err)
	}
	if gone != nil {
		t.Error("expected consumer to be gone after DeleteConsumer")
	}

	if err := s.DeleteConsumer("does-not-exist"); err == nil {
		t.Error("expected error deleting a nonexistent consumer")
	}
}

// TestConsumersCreate_RequiresSlugAndName verifies the minimal validation
// on CreateConsumer.
func TestConsumersCreate_RequiresSlugAndName(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateConsumer(&Consumer{Name: "No Slug"}); err == nil {
		t.Error("expected error creating a consumer without a slug")
	}
	if err := s.CreateConsumer(&Consumer{Slug: "no-name"}); err == nil {
		t.Error("expected error creating a consumer without a name")
	}
}

// TestAgentProfile_ConsumerIDRoundTrip verifies agent_profiles.consumer_id
// is nullable (empty string = internal/operator-owned) and round-trips
// through CreateAgent/UpdateAgent/GetAgent, tagging an agent against the
// seeded Loom consumer row.
func TestAgentProfile_ConsumerIDRoundTrip(t *testing.T) {
	s := newTestStore(t)

	loom, err := s.GetConsumerBySlug("loom")
	if err != nil || loom == nil {
		t.Fatalf("GetConsumerBySlug(loom): %v, %+v", err, loom)
	}

	a := &AgentProfile{
		Name:         "Curator",
		Slug:         "curator-consumer-test",
		SystemPrompt: "test",
	}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.ConsumerID != "" {
		t.Errorf("default ConsumerID: got %q, want empty (operator-owned)", a.ConsumerID)
	}

	got, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.ConsumerID != "" {
		t.Errorf("round-trip ConsumerID on unset row: got %q want empty", got.ConsumerID)
	}

	got.ConsumerID = loom.ID
	if err := s.UpdateAgent(got); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	tagged, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent after tagging: %v", err)
	}
	if tagged.ConsumerID != loom.ID {
		t.Errorf("ConsumerID after tagging: got %q want %q", tagged.ConsumerID, loom.ID)
	}

	// Deleting the referenced consumer while an agent still points at it
	// must fail -- this codebase runs with PRAGMA foreign_keys=1.
	if err := s.DeleteConsumer(loom.ID); err == nil {
		t.Error("expected DeleteConsumer to fail while an agent_profiles row still references it")
	}

	// Clearing the tag first must allow the delete to proceed.
	tagged.ConsumerID = ""
	if err := s.UpdateAgent(tagged); err != nil {
		t.Fatalf("UpdateAgent (clear consumer_id): %v", err)
	}
	cleared, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent after clearing: %v", err)
	}
	if cleared.ConsumerID != "" {
		t.Errorf("ConsumerID after clearing: got %q want empty", cleared.ConsumerID)
	}
}

// TestMigrate112DownRemovesConsumersAndColumn is the tested Down half of
// migration 112: goose's DownTo must drop agent_profiles.consumer_id and
// the consumers table, then Up must be able to replay cleanly.
func TestMigrate112DownRemovesConsumersAndColumn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	loom, err := s.GetConsumerBySlug("loom")
	if err != nil || loom == nil {
		t.Fatalf("GetConsumerBySlug(loom) before down: %v, %+v", err, loom)
	}

	a := &AgentProfile{Name: "Down Test", Slug: "down-test-consumer", SystemPrompt: "x", ConsumerID: loom.ID}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 105); err != nil {
		t.Fatalf("goose DownTo 105 (reverse migration 112): %v", err)
	}

	exists, err := agentProfilesColumnExists(ctx, s, "consumer_id")
	if err != nil {
		t.Fatalf("check agent_profiles.consumer_id exists after down: %v", err)
	}
	if exists {
		t.Error("agent_profiles.consumer_id still present after Down migration")
	}

	var consumersTableExists int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='consumers'`,
	).Scan(&consumersTableExists); err != nil {
		t.Fatalf("check consumers table exists: %v", err)
	}
	if consumersTableExists != 0 {
		t.Error("consumers table still present after Down migration")
	}

	// The pre-existing agent_profiles row (created before Down dropped the
	// referencing column) must survive with the rest of its data intact.
	var name string
	if err := s.DB.QueryRowContext(ctx, `SELECT name FROM agent_profiles WHERE id = ?`, a.ID).Scan(&name); err != nil {
		t.Fatalf("agent_profiles row survived down: %v", err)
	}
	if name != "Down Test" {
		t.Errorf("agent_profiles.name after down: got %q want %q", name, "Down Test")
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up after DownTo 105: %v", err)
	}

	exists, err = agentProfilesColumnExists(ctx, s, "consumer_id")
	if err != nil {
		t.Fatalf("check agent_profiles.consumer_id exists after re-up: %v", err)
	}
	if !exists {
		t.Error("agent_profiles.consumer_id missing after Up replayed 106")
	}
	loomAgain, err := s.GetConsumerBySlug("loom")
	if err != nil {
		t.Fatalf("GetConsumerBySlug(loom) after re-up: %v", err)
	}
	if loomAgain == nil {
		t.Error("loom consumer row missing after Up replayed 106")
	}
}

func agentProfilesColumnExists(ctx context.Context, s *Store, column string) (bool, error) {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(agent_profiles)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return false, err
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return false, err
		}
		for i, c := range cols {
			if c == "name" {
				if name, ok := vals[i].(string); ok && name == column {
					return true, nil
				}
			}
		}
	}
	return false, rows.Err()
}
