package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestCreateAgent_FU28Defaults pins the FU-28 default application: any
// new agent must land with class='advisor', activation_mode='singleton'
// and default_state='sleeping'.
//
// The URN half of this test is INVERTED as of migration 159
// (CW-20260912-0017). It used to require a freshly-minted profile URN and a
// slug-form alias; a profile is a definition rather than a recipient, so it
// must now mint neither. Actor identity moved to durable_agent_instances.urn
// and is covered by TestCreateDurableAgentInstance_MintsNaniteActorURN.
func TestCreateAgent_FU28Defaults(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "FU28 Default", Slug: "fu28-default", SystemPrompt: "x"}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.Class != "advisor" {
		t.Errorf("Class = %q, want 'advisor'", a.Class)
	}
	if a.ActivationMode != "singleton" {
		t.Errorf("ActivationMode = %q, want 'singleton'", a.ActivationMode)
	}
	if a.DefaultState != "sleeping" {
		t.Errorf("DefaultState = %q, want 'sleeping'", a.DefaultState)
	}
	// No profile URN is minted any more, in either authority. Read straight
	// from SQL: the retired columns have no Go representation by design, and
	// asserting through a struct field would mean re-adding the modeling
	// this migration removed. Asserting emptiness rather than a pattern is
	// also deliberate — a pattern check would still pass if some future code
	// minted into `nanite`, and the point is that a profile has no address.
	var legacyURN, legacyAliases string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COALESCE(legacy_urn,''), COALESCE(legacy_urn_aliases,'[]')
		   FROM agent_profiles WHERE id = ?`, a.ID).Scan(&legacyURN, &legacyAliases); err != nil {
		t.Fatalf("read retired URN columns: %v", err)
	}
	if legacyURN != "" {
		t.Errorf("legacy_urn = %q, want empty — CreateAgent must not mint a profile URN "+
			"(migration 159: a profile is a definition, not a recipient)", legacyURN)
	}
	var aliases []string
	if err := json.Unmarshal([]byte(legacyAliases), &aliases); err != nil {
		t.Fatalf("legacy_urn_aliases not valid JSON: %v (%s)", err, legacyAliases)
	}
	if len(aliases) != 0 {
		t.Errorf("legacy_urn_aliases = %q, want empty — the slug-form alias existed to keep "+
			"msg://agent/agent-mux/<slug> routable, and nothing routes on it", legacyAliases)
	}

	// Round-trip via GetAgent to confirm the row still reads.
	if _, err := s.GetAgent(context.Background(), a.ID); err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
}

// TestCreateAgent_FU28InvalidEnum confirms the Go-layer enum validation
// (skipped in migration 070 to keep ALTER ADD COLUMN idempotent) catches
// bogus activation_mode / class / default_state values.
func TestCreateAgent_FU28InvalidEnum(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "Bad", Slug: "bad-enum", SystemPrompt: "x", Class: "wizard"}
	err := s.CreateAgent(context.Background(), a)
	if err == nil || !strings.Contains(err.Error(), "class") {
		t.Fatalf("expected class validation error, got: %v", err)
	}
}

// TestListAgentsFilter covers the class/activation_mode/status filter axes.
func TestListAgentsFilter(t *testing.T) {
	s := newTestStore(t)
	// Build a small, deterministic set of agents.
	makeOne := func(slug, class, mode, status string) {
		a := &AgentProfile{
			Name: slug, Slug: slug, SystemPrompt: "x",
			Class: class, ActivationMode: mode, Status: status,
		}
		if err := s.CreateAgent(context.Background(), a); err != nil {
			t.Fatalf("CreateAgent %s: %v", slug, err)
		}
	}
	makeOne("filt-a", "advisor", "singleton", "active")
	makeOne("filt-b", "process", "fresh-per-wake", "active")
	makeOne("filt-c", "advisor", "fresh-per-wake", "paused")

	// Class filter.
	got, err := s.ListAgentsFilter(context.Background(), "advisor", "", "", nil)
	if err != nil {
		t.Fatalf("filter class: %v", err)
	}
	if len(got) < 2 {
		t.Errorf("expected >= 2 advisors, got %d", len(got))
	}
	for _, a := range got {
		if a.Class != "advisor" {
			t.Errorf("non-advisor leaked: %s class=%q", a.Slug, a.Class)
		}
	}

	// Activation_mode filter.
	got, err = s.ListAgentsFilter(context.Background(), "", "fresh-per-wake", "", nil)
	if err != nil {
		t.Fatalf("filter mode: %v", err)
	}
	for _, a := range got {
		if a.ActivationMode != "fresh-per-wake" {
			t.Errorf("non-fresh-per-wake leaked: %s mode=%q", a.Slug, a.ActivationMode)
		}
	}

	// Combined: advisor + fresh-per-wake → only filt-c.
	got, err = s.ListAgentsFilter(context.Background(), "advisor", "fresh-per-wake", "", nil)
	if err != nil {
		t.Fatalf("filter combo: %v", err)
	}
	foundC := false
	for _, a := range got {
		if a.Slug == "filt-c" {
			foundC = true
		}
		if a.Class != "advisor" || a.ActivationMode != "fresh-per-wake" {
			t.Errorf("combo leak: %s class=%q mode=%q", a.Slug, a.Class, a.ActivationMode)
		}
	}
	if !foundC {
		t.Errorf("filt-c missing from advisor+fresh-per-wake result")
	}
}

// TestDeleteAgentByID confirms the ID-based delete removes the row.
func TestDeleteAgentByID(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "delete-by-id")
	if err := s.DeleteAgentByID(context.Background(), a.ID); err != nil {
		t.Fatalf("DeleteAgentByID: %v", err)
	}
	if _, err := s.GetAgent(context.Background(), a.ID); err == nil {
		t.Errorf("agent still present after delete")
	}
	// Delete on missing ID must not error.
	if err := s.DeleteAgentByID(context.Background(), "does-not-exist"); err != nil {
		t.Errorf("delete missing ID returned error: %v", err)
	}
}

func TestDeleteAgentByIDPropagatesLookupError(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "delete-by-id-lookup-error")
	if err := s.DB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	err := s.DeleteAgentByID(context.Background(), a.ID)
	if err == nil {
		t.Fatal("DeleteAgentByID returned nil for a database lookup error")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteAgentByID error = %v, want non-not-found error", err)
	}
	if !strings.Contains(err.Error(), "delete agent by id "+a.ID) {
		t.Fatalf("DeleteAgentByID error = %q, want operation and agent ID context", err)
	}
}

// TestCloneAgent confirms clone produces a fresh URN + version=1 row.
func TestCloneAgent(t *testing.T) {
	s := newTestStore(t)
	src := &AgentProfile{
		Name:         "Source",
		Slug:         "clone-src",
		SystemPrompt: "x",
		Tags:         `["alpha"]`,
		RoleTools:    `["read_file"]`,
	}
	if err := s.CreateAgent(context.Background(), src); err != nil {
		t.Fatalf("CreateAgent src: %v", err)
	}
	cloned, err := s.CloneAgent(context.Background(), src.ID, "clone-dst", "Cloned Agent")
	if err != nil {
		t.Fatalf("CloneAgent: %v", err)
	}
	if cloned.ID == src.ID {
		t.Errorf("clone retained source ID")
	}
	// Previously: a clone had to get a freshly-minted URN distinct from its
	// source. Nothing mints a profile URN now, and the columns are not
	// modeled, so a clone cannot carry an address forward by construction.
	// Asserted at the row instead.
	var clonedLegacyURN string
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COALESCE(legacy_urn,'') FROM agent_profiles WHERE id = ?`,
		cloned.ID).Scan(&clonedLegacyURN); err != nil {
		t.Fatalf("read cloned legacy_urn: %v", err)
	}
	if clonedLegacyURN != "" {
		t.Errorf("clone carried a legacy profile URN forward: %q", clonedLegacyURN)
	}
	if cloned.Version != 1 {
		t.Errorf("clone version = %d, want 1", cloned.Version)
	}
	if cloned.Tags != `["alpha"]` {
		t.Errorf("clone tags lost: %q", cloned.Tags)
	}
	if cloned.RoleTools != `["read_file"]` {
		t.Errorf("clone role_tools lost: %q", cloned.RoleTools)
	}
}
