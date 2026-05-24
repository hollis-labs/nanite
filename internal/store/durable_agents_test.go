package store

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDurableAgentInstanceCreateListGetArchive(t *testing.T) {
	s := newTestStore(t)
	profile := makeTestAgent(t, s, "durable-profile")

	inst := &DurableAgentInstance{
		Name:             "Torque Supervisor",
		Slug:             "torque-supervisor-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "claude-sonnet-4",
		RuntimeKind:      "api",
		LaunchSourceType: DurableAgentLaunchDurableAdvisor,
		WorkRoot:         "/tmp/torque-supervisor",
	}
	if err := s.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if inst.ID == "" {
		t.Fatal("expected generated id")
	}

	got, err := s.GetDurableAgentInstance(inst.ID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstance: %v", err)
	}
	if got.Provider != "anthropic" || got.Model != "claude-sonnet-4" || got.RuntimeKind != "api" {
		t.Fatalf("immutable launch fields did not round-trip: %+v", got)
	}

	list, err := s.ListDurableAgentInstances(false)
	if err != nil {
		t.Fatalf("ListDurableAgentInstances: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("active list length = %d, want 1", len(list))
	}

	archived, err := s.ArchiveDurableAgentInstance(inst.ID)
	if err != nil {
		t.Fatalf("ArchiveDurableAgentInstance: %v", err)
	}
	if archived.Status != DurableAgentStatusArchived || archived.ArchivedAt == nil {
		t.Fatalf("archive state = %+v", archived)
	}
	list, err = s.ListDurableAgentInstances(false)
	if err != nil {
		t.Fatalf("ListDurableAgentInstances active after archive: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("active list after archive length = %d, want 0", len(list))
	}
}

func TestDurableAgentInstanceValidationAndImmutableUpdate(t *testing.T) {
	s := newTestStore(t)
	profile := makeTestAgent(t, s, "durable-validation")

	err := s.CreateDurableAgentInstance(&DurableAgentInstance{
		Name:             "Bad",
		Slug:             "bad-durable",
		ProfileID:        profile.ID,
		LifecycleClass:   "operator",
		LaunchSourceType: DurableAgentLaunchAPIChat,
	})
	if err == nil {
		t.Fatal("expected invalid lifecycle class error")
	}

	inst := &DurableAgentInstance{
		Name:             "Stable",
		Slug:             "stable-durable",
		ProfileID:        profile.ID,
		LifecycleClass:   DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: DurableAgentLaunchAPIChat,
	}
	if err := s.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	newName := "Stable Renamed"
	newMeta := `{"note":"metadata only"}`
	updated, err := s.UpdateDurableAgentInstance(inst.ID, DurableAgentInstanceUpdate{
		Name:         &newName,
		MetadataJSON: &newMeta,
	})
	if err != nil {
		t.Fatalf("UpdateDurableAgentInstance: %v", err)
	}
	if updated.Name != newName || updated.MetadataJSON != newMeta {
		t.Fatalf("metadata update did not apply: %+v", updated)
	}
	if updated.Provider != "anthropic" || updated.Model != "model-a" || updated.RuntimeKind != "api" {
		t.Fatalf("immutable launch fields changed: %+v", updated)
	}
}

func TestDurableAgentInstanceSessionAttachment(t *testing.T) {
	s := newTestStore(t)
	profile := makeTestAgent(t, s, "durable-attach")
	inst := &DurableAgentInstance{
		Name:             "Attached",
		Slug:             "attached-durable",
		ProfileID:        profile.ID,
		LaunchSourceType: DurableAgentLaunchAPIChat,
	}
	if err := s.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if inst.LifecycleClass != DurableAgentClassAdvisor || inst.RuntimeKind != "api" || inst.Status != DurableAgentStatusSleeping {
		t.Fatalf("defaults = lifecycle %q runtime %q status %q", inst.LifecycleClass, inst.RuntimeKind, inst.Status)
	}
	if inst.CurrentSessionID != "" || inst.FailureReason != "" {
		t.Fatalf("launch state defaults = current_session_id %q failure_reason %q", inst.CurrentSessionID, inst.FailureReason)
	}
	sess := &Session{Title: "owned session", Provider: "anthropic", Model: "model-a"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.AttachDurableAgentInstanceSession(inst.ID, sess.ID, DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}
	rels, err := s.ListDurableAgentInstanceSessions(inst.ID)
	if err != nil {
		t.Fatalf("ListDurableAgentInstanceSessions: %v", err)
	}
	if len(rels) != 1 || rels[0].SessionID != sess.ID || rels[0].Relation != DurableAgentSessionRelationWake {
		t.Fatalf("relations = %+v", rels)
	}
	states, err := s.ListDurableAgentInstanceSessionStates(inst.ID)
	if err != nil {
		t.Fatalf("ListDurableAgentInstanceSessionStates: %v", err)
	}
	if len(states) != 1 || states[0].SessionStatus != "active" || states[0].Provider != "anthropic" || states[0].Model != "model-a" {
		t.Fatalf("states = %+v", states)
	}
}

func TestDurableAgentEventsCreateListOrdering(t *testing.T) {
	s := newTestStore(t)
	profile := makeTestAgent(t, s, "durable-events")
	inst := &DurableAgentInstance{
		Name:             "Evented",
		Slug:             "evented-durable",
		ProfileID:        profile.ID,
		LaunchSourceType: DurableAgentLaunchAPIChat,
	}
	if err := s.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	events := []DurableAgentEvent{
		{InstanceID: inst.ID, EventType: DurableAgentEventCreated, StatusAfter: DurableAgentStatusSleeping, CreatedAt: base},
		{InstanceID: inst.ID, EventType: DurableAgentEventStartRequested, StatusBefore: DurableAgentStatusSleeping, StatusAfter: DurableAgentStatusStarting, CreatedAt: base.Add(time.Minute)},
		{InstanceID: inst.ID, EventType: DurableAgentEventStartSucceeded, StatusBefore: DurableAgentStatusStarting, StatusAfter: DurableAgentStatusActive, CreatedAt: base.Add(2 * time.Minute)},
	}
	for i := range events {
		if err := s.CreateDurableAgentEvent(&events[i]); err != nil {
			t.Fatalf("CreateDurableAgentEvent %d: %v", i, err)
		}
	}
	got, err := s.ListDurableAgentEvents(inst.ID, 2)
	if err != nil {
		t.Fatalf("ListDurableAgentEvents: %v", err)
	}
	if len(got) != 2 || got[0].EventType != DurableAgentEventStartSucceeded || got[1].EventType != DurableAgentEventStartRequested {
		t.Fatalf("events order/limit = %+v", got)
	}
	if got[0].Source != DurableAgentEventSourceAPI || got[0].MetadataJSON != "{}" {
		t.Fatalf("event defaults = %+v", got[0])
	}
}

func TestMigration080DurableAgentTablesExist(t *testing.T) {
	s := newTestStore(t)
	for _, table := range []string{"durable_agent_instances", "durable_agent_instance_sessions", "durable_agent_events"} {
		var name string
		if err := s.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s missing after migrations: %v", table, err)
		}
	}
	if _, err := s.DB.Exec(`SELECT current_session_id, failure_reason FROM durable_agent_instances LIMIT 1`); err != nil {
		t.Fatalf("phase 6 launch columns missing: %v", err)
	}
}

func TestMigration084SeedsLegacyDurableAgentProfiles(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{
		Name:            "Legacy Proxima",
		Slug:            "legacy-proxima",
		SystemPrompt:    "You are a durable profile.",
		Tags:            `["durable-agent","advisor"]`,
		Class:           "advisor",
		DefaultState:    "sleeping",
		DefaultProvider: "",
		DefaultModel:    "",
	}
	if err := s.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent legacy profile: %v", err)
	}
	regular := &AgentProfile{
		Name:         "Regular Profile",
		Slug:         "regular-profile",
		SystemPrompt: "You are not durable.",
		Tags:         `["advisor"]`,
	}
	if err := s.CreateAgent(regular); err != nil {
		t.Fatalf("CreateAgent regular profile: %v", err)
	}

	migration, err := os.ReadFile("migrations/084_seed_legacy_durable_agent_instances.sql")
	if err != nil {
		t.Fatalf("read migration 083: %v", err)
	}
	if _, err := s.DB.Exec(string(migration)); err != nil {
		t.Fatalf("exec migration 083: %v", err)
	}
	if _, err := s.DB.Exec(string(migration)); err != nil {
		t.Fatalf("exec migration 083 second run: %v", err)
	}

	list, err := s.ListDurableAgentInstances(false)
	if err != nil {
		t.Fatalf("ListDurableAgentInstances: %v", err)
	}
	var seeded *DurableAgentInstance
	for i := range list {
		if list[i].Slug == "legacy-proxima" {
			seeded = &list[i]
			break
		}
	}
	if seeded == nil {
		t.Fatalf("seeded instance not found in %+v", list)
	}
	if seeded.ProfileID != profile.ID || seeded.Provider != "anthropic" || seeded.Model != "claude-sonnet-4" {
		t.Fatalf("seeded instance = %+v", seeded)
	}
	for _, inst := range list {
		if inst.Slug == "regular-profile" {
			t.Fatalf("regular profile should not be seeded: %+v", inst)
		}
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM durable_agent_instances WHERE slug = ?`, "legacy-proxima").Scan(&count); err != nil {
		t.Fatalf("count seeded instances: %v", err)
	}
	if count != 1 {
		t.Fatalf("seeded instance count = %d, want 1", count)
	}
	if !strings.Contains(seeded.MetadataJSON, "agent_profiles") {
		t.Fatalf("seeded metadata = %q", seeded.MetadataJSON)
	}
}
