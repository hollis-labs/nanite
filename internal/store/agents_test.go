package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// makeTestAgent simulates an ALREADY host-authorized binding in a private
// database. It is not an enrollment port, and a host ID is never its actor ID.
func makeTestAgent(t *testing.T, s *Store, slug string) *AgentProfile {
	t.Helper()
	slug = strings.ToLower(slug)
	h := makeTestHost(t, s, slug)
	actor := "msg://agent/private-fixture/" + slug
	if _, err := s.DB.ExecContext(t.Context(), `INSERT INTO agent_actor_bindings(actor_uri,host_settings_id,binding_receipt) VALUES(?,?,?)`, actor, h.ID, "private-test-existing-host-authorization"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetAgentForActor(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func makeTestHost(t *testing.T, s *Store, slug string) AgentHostSettings {
	t.Helper()
	pin, err := s.InstallAgentDefinition(t.Context(), definitionTestBytes("def:private-test-host", "1", "You are a test agent."), nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CreateAgentHostSettings(t.Context(), AgentHostSettings{Slug: slug, Title: "Test Agent " + slug, DefinitionRef: pin, Settings: NativeHostSettings{Version: "1", Runtime: "api"}, Source: "explicit-private-test-authoring", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// makeTestAgentRawSQL inserts an agent_profiles row using only the columns
// that have existed since the table's original shape (id, name, slug,
// system_prompt, timestamps) — every other column has carried a DEFAULT
// since the migration that added it, so SQLite fills them in without this
// insert naming them.
//
// Use this instead of makeTestAgent in any test that has rolled the schema
// back with goose DownTo: CreateAgent's INSERT always targets the CURRENT
// column shape, so it fails the moment the live schema is older than the
// newest migration — this is how migration 159's durable_agent_instances.urn
// first surfaced this exact fixture problem (see plantPre159Instance below),
// and migration 162's agent_profiles.tether_managed/tether_urn surfaced it
// again for agent_profiles itself. Naming only the columns a test needs
// keeps the fixture independent of columns added later.
func makeTestAgentRawSQL(t *testing.T, s *Store, slug string) *AgentProfile {
	t.Helper()
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO agent_profiles (id, name, slug, system_prompt, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, "Test Agent "+slug, slug, "You are a test agent.", now, now,
	); err != nil {
		t.Fatalf("makeTestAgentRawSQL: insert %s: %v", slug, err)
	}
	return &AgentProfile{ID: id, Name: "Test Agent " + slug, Slug: slug, SystemPrompt: "You are a test agent.", CreatedAt: now, UpdatedAt: now}
}

func TestRetiredAgentWritersPreserveHistoricalState(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	old := makeTestAgentRawSQL(t, s, "historical-writer-target")
	before, err := s.GetHistoricalAgentProfile(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := *before
	changed.SystemPrompt = "attempted mutation"
	role, protocol, transport := "claimed-role", "acp", "stdio"
	attempts := []struct {
		name  string
		write func() error
	}{
		{"create", func() error { return s.CreateAgent(ctx, &AgentProfile{Name: "New", Slug: "new-retired-writer"}) }},
		{"update", func() error { return s.UpdateAgent(ctx, &changed) }},
		{"delete", func() error { return s.DeleteAgent(ctx, old.Slug) }},
		{"delete-id", func() error { return s.DeleteAgentByID(ctx, old.ID) }},
		{"composition", func() error { return s.UpdateAgentComposition(ctx, old.ID, &role, nil, nil) }},
		{"protocol", func() error { return s.UpdateAgentACPConfig(ctx, old.ID, &protocol, &transport) }},
		{"config", func() error {
			_, e := s.UpdateAgentConfigRevision(ctx, &changed, AgentAssignments{}, AgentConfigSeeds{Tools: []string{"admin"}}, before.Revision, "")
			return e
		}},
		{"clone", func() error { _, e := s.CloneAgent(ctx, old.ID, "clone-retired", "Clone"); return e }},
	}
	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			if writeErr := attempt.write(); !errors.Is(writeErr, ErrImmutableAgentProfile) {
				t.Fatal(writeErr)
			}
		})
	}
	after, err := s.GetHistoricalAgentProfile(ctx, old.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("historical profile mutated: %+v %v", after, err)
	}
	if _, err = s.GetAgentBySlug(ctx, "clone-retired"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("retired clone installed runtime state", err)
	}
}

func TestFreshAgentHostProjectionAndSessionBinding(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	host := makeTestHost(t, s, "projection-host")
	got, err := s.GetAgentBySlug(ctx, host.Slug)
	if err != nil || got.ID != host.ID || !strings.Contains(got.SystemPrompt, "You are a test agent.") {
		t.Fatal(got, err)
	}
	all, err := s.ListAgents(ctx)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	session := makeTestSession(t, s)
	if err = s.EnsureSessionAgent(ctx, session.ID, host.ID, "default", true); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("host UUID gained actor authority", err)
	}
	actor := makeTestAgent(t, s, "bound-session-actor")
	if err = s.EnsureSessionAgent(ctx, session.ID, actor.ID, "default", true); err != nil {
		t.Fatal(err)
	}
	primary, err := s.GetSessionPrimaryAgent(ctx, session.ID)
	if err != nil || primary.AgentID != actor.ID {
		t.Fatal(primary, err)
	}
	members, err := s.ListSessionAgents(ctx, session.ID)
	if err != nil || len(members) != 1 || members[0].AgentID != actor.ID {
		t.Fatal(members, err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureSessionAgent(ctx, session.ID, actor.ID, "default", true); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("disabled binding admitted", err)
	}
}
