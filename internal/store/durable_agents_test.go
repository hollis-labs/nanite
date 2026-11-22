package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Existing actor instances are simulated directly in a PRIVATE test database.
// This is not a production enrollment or caller-asserted issuer interface.
func makeTestActorInstance(t *testing.T, s *Store, actor, slug string) *DurableAgentInstance {
	t.Helper()
	id := uuid.NewString()
	if _, err := s.DB.ExecContext(t.Context(), `INSERT INTO actor_instances(id,name,slug,profile_id,provider,model,urn) VALUES(?,?,?,?,?,?,?)`, id, "Existing", slug, actor, "fixture-provider", "fixture-model", actor); err != nil {
		t.Fatal(err)
	}
	instance, err := s.GetDurableAgentInstance(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}
func TestExistingActorInstanceLifecycleDoesNotMintOrRewriteIdentity(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "instance-owner")
	i := makeTestActorInstance(t, s, a.ID, "existing-instance")
	name, metadata := "Renamed", `{"private_fixture":true}`
	changed, err := s.UpdateDurableAgentInstance(t.Context(), i.ID, DurableAgentInstanceUpdate{Name: &name, MetadataJSON: &metadata})
	if err != nil || changed.Name != name || changed.MetadataJSON != metadata || changed.URN != a.ID || changed.ProfileID != a.ID || changed.Model != i.Model {
		t.Fatal(changed, err)
	}
	session := makeTestSession(t, s)
	if err = s.AttachDurableAgentInstanceSession(t.Context(), i.ID, session.ID, DurableAgentSessionRelationWake); err != nil {
		t.Fatal(err)
	}
	relations, err := s.ListDurableAgentInstanceSessions(t.Context(), i.ID)
	if err != nil || len(relations) != 1 || relations[0].SessionID != session.ID {
		t.Fatal(relations, err)
	}
	event := &DurableAgentEvent{InstanceID: i.ID, EventType: DurableAgentEventStartRequested}
	if err = s.CreateDurableAgentEvent(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListDurableAgentEvents(t.Context(), i.ID, 10)
	if err != nil || len(events) != 1 || events[0].ID != event.ID {
		t.Fatal(events, err)
	}
	archived, err := s.ArchiveDurableAgentInstance(t.Context(), i.ID)
	if err != nil || archived.Status != DurableAgentStatusArchived || archived.ArchivedAt == nil {
		t.Fatal(archived, err)
	}
	if _, err = s.SyncDurableAgentInstanceConfig(t.Context(), &DurableAgentInstance{Name: "Resurrect", Slug: i.Slug, ProfileID: a.ID}); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("config revived actor", err)
	}
	still, err := s.GetDurableAgentInstance(t.Context(), i.ID)
	if err != nil || still.Status != DurableAgentStatusArchived || still.URN != a.ID {
		t.Fatal(still, err)
	}
	active, err := s.ListDurableAgentInstances(t.Context(), false)
	if err != nil || len(active) != 0 {
		t.Fatal(active, err)
	}
}
func TestHistoricalDurableInstanceRemainsInvisibleToRuntime(t *testing.T) {
	s := newTestStore(t)
	old := makeTestAgentRawSQL(t, s, "old-durable")
	if _, err := s.DB.ExecContext(t.Context(), `INSERT INTO durable_agent_instances(id,name,slug,profile_id,urn) VALUES('old-instance','Old','old-instance',?,'msg://agent/nanite/old')`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDurableAgentInstance(t.Context(), "old-instance"); !errors.Is(err, ErrDurableAgentInstanceNotFound) {
		t.Fatal("historical fallback", err)
	}
	var owner string
	if err := s.DB.QueryRowContext(t.Context(), `SELECT profile_id FROM durable_agent_instances WHERE id='old-instance'`).Scan(&owner); err != nil || owner != old.ID {
		t.Fatal("history lost", owner, err)
	}
}
