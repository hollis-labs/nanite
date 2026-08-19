package store

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestCreateAgent_FU28Defaults pins the FU-28 default application: any
// new agent must land with class='advisor', activation_mode='singleton',
// default_state='sleeping', a freshly-minted URN matching the spec
// pattern, and a urn_aliases JSON array containing the slug-form alias.
func TestCreateAgent_FU28Defaults(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "FU28 Default", Slug: "fu28-default", SystemPrompt: "x"}
	if err := s.CreateAgent(a); err != nil {
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
	urnRE := regexp.MustCompile(`^msg://agent/agent-mux/agt_[a-z2-7]{10}$`)
	if !urnRE.MatchString(a.URN) {
		t.Errorf("URN = %q, want match %s", a.URN, urnRE.String())
	}
	// urn_aliases must include the slug-form URN.
	var aliases []string
	if err := json.Unmarshal([]byte(a.URNAliases), &aliases); err != nil {
		t.Fatalf("URNAliases not valid JSON: %v (%s)", err, a.URNAliases)
	}
	expectedAlias := "msg://agent/agent-mux/fu28-default"
	found := false
	for _, x := range aliases {
		if x == expectedAlias {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("URNAliases %q does not contain slug-form alias %q", a.URNAliases, expectedAlias)
	}

	// Round-trip via GetAgent to confirm the DB row was populated.
	got, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.URN != a.URN {
		t.Errorf("round-trip URN mismatch: got %q, want %q", got.URN, a.URN)
	}
}

// TestCreateAgent_FU28InvalidEnum confirms the Go-layer enum validation
// (skipped in migration 070 to keep ALTER ADD COLUMN idempotent) catches
// bogus activation_mode / class / default_state values.
func TestCreateAgent_FU28InvalidEnum(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "Bad", Slug: "bad-enum", SystemPrompt: "x", Class: "wizard"}
	err := s.CreateAgent(a)
	if err == nil || !strings.Contains(err.Error(), "class") {
		t.Fatalf("expected class validation error, got: %v", err)
	}
}

// TestGetAgentByURN covers both primary URN lookup and alias lookup.
func TestGetAgentByURN(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "URN", Slug: "urn-lookup", SystemPrompt: "x"}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Primary URN lookup.
	got, ok, err := s.GetAgentByURN(a.URN)
	if err != nil {
		t.Fatalf("GetAgentByURN(primary): %v", err)
	}
	if !ok || got == nil || got.ID != a.ID {
		t.Fatalf("primary URN lookup miss for %q", a.URN)
	}

	// Slug-alias lookup.
	got, ok, err = s.GetAgentByURN("msg://agent/agent-mux/urn-lookup")
	if err != nil {
		t.Fatalf("GetAgentByURN(alias): %v", err)
	}
	if !ok || got == nil || got.ID != a.ID {
		t.Fatalf("alias URN lookup miss")
	}

	// Miss returns (nil, false, nil).
	got, ok, err = s.GetAgentByURN("msg://agent/agent-mux/nope")
	if err != nil {
		t.Fatalf("GetAgentByURN(miss): %v", err)
	}
	if ok || got != nil {
		t.Errorf("expected miss for unknown URN, got hit")
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
		if err := s.CreateAgent(a); err != nil {
			t.Fatalf("CreateAgent %s: %v", slug, err)
		}
	}
	makeOne("filt-a", "advisor", "singleton", "active")
	makeOne("filt-b", "process", "fresh-per-wake", "active")
	makeOne("filt-c", "advisor", "fresh-per-wake", "paused")

	// Class filter.
	got, err := s.ListAgentsFilter("advisor", "", "", nil)
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
	got, err = s.ListAgentsFilter("", "fresh-per-wake", "", nil)
	if err != nil {
		t.Fatalf("filter mode: %v", err)
	}
	for _, a := range got {
		if a.ActivationMode != "fresh-per-wake" {
			t.Errorf("non-fresh-per-wake leaked: %s mode=%q", a.Slug, a.ActivationMode)
		}
	}

	// Combined: advisor + fresh-per-wake → only filt-c.
	got, err = s.ListAgentsFilter("advisor", "fresh-per-wake", "", nil)
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
	if err := s.DeleteAgentByID(a.ID); err != nil {
		t.Fatalf("DeleteAgentByID: %v", err)
	}
	if _, err := s.GetAgent(a.ID); err == nil {
		t.Errorf("agent still present after delete")
	}
	// Delete on missing ID must not error.
	if err := s.DeleteAgentByID("does-not-exist"); err != nil {
		t.Errorf("delete missing ID returned error: %v", err)
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
	if err := s.CreateAgent(src); err != nil {
		t.Fatalf("CreateAgent src: %v", err)
	}
	cloned, err := s.CloneAgent(src.ID, "clone-dst", "Cloned Agent")
	if err != nil {
		t.Fatalf("CloneAgent: %v", err)
	}
	if cloned.ID == src.ID {
		t.Errorf("clone retained source ID")
	}
	if cloned.URN == src.URN {
		t.Errorf("clone retained source URN")
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
