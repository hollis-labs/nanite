package store

import "testing"

// TestCreateAgent_RoleIDModelIDNullableRoundTrip confirms the two new
// nullable FK columns (TASKS/phase-1/02-add-agents-composition-columns.md)
// round-trip correctly both when left unset (empty string in, empty string
// out -- nullIfEmpty on the write path, COALESCE(...,'') on the read path)
// and when explicitly set to a real value.
func TestCreateAgent_RoleIDModelIDNullableRoundTrip(t *testing.T) {
	s := newTestStore(t)

	unset := &AgentProfile{Name: "No FKs", Slug: "no-fks", SystemPrompt: "x"}
	if err := s.CreateAgent(unset); err != nil {
		t.Fatalf("CreateAgent(unset): %v", err)
	}
	got, err := s.GetAgent(unset.ID)
	if err != nil {
		t.Fatalf("GetAgent(unset): %v", err)
	}
	if got.RoleID != "" {
		t.Errorf("RoleID = %q, want empty for a row that never set one", got.RoleID)
	}
	if got.ModelID != "" {
		t.Errorf("ModelID = %q, want empty for a row that never set one", got.ModelID)
	}

	role := &Role{Slug: "sme-role", Name: "SME"}
	if err := s.CreateRole(role); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	// newTestStore only runs migrations (no Seed()/SeedProviders()), so
	// models/providers start empty -- insert a minimal real row directly
	// to give model_id's FK something to reference.
	if _, err := s.DB.Exec(`INSERT INTO providers (id, name, provider_type) VALUES ('prov-test', 'Test Provider', 'anthropic')`); err != nil {
		t.Fatalf("insert test provider: %v", err)
	}
	if _, err := s.DB.Exec(`INSERT INTO models (id, provider_id, model_id, display_name) VALUES ('claude-sonnet', 'prov-test', 'claude-sonnet-4-5-20250929', 'Claude Sonnet')`); err != nil {
		t.Fatalf("insert test model: %v", err)
	}

	bound := &AgentProfile{
		Name: "With FKs", Slug: "with-fks", SystemPrompt: "x",
		RoleID: role.ID, ModelID: "claude-sonnet",
	}
	if err := s.CreateAgent(bound); err != nil {
		t.Fatalf("CreateAgent(bound): %v", err)
	}
	got, err = s.GetAgent(bound.ID)
	if err != nil {
		t.Fatalf("GetAgent(bound): %v", err)
	}
	if got.RoleID != role.ID {
		t.Errorf("RoleID = %q, want %q", got.RoleID, role.ID)
	}
	if got.ModelID != "claude-sonnet" {
		t.Errorf("ModelID = %q, want %q", got.ModelID, "claude-sonnet")
	}

	// UpdateAgent must round-trip the same two columns.
	got.ModelID = ""
	if err := s.UpdateAgent(got); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	after, err := s.GetAgent(bound.ID)
	if err != nil {
		t.Fatalf("GetAgent(after update): %v", err)
	}
	if after.ModelID != "" {
		t.Errorf("ModelID after clearing = %q, want empty", after.ModelID)
	}
	if after.RoleID != role.ID {
		t.Errorf("RoleID after unrelated update = %q, want unchanged %q", after.RoleID, role.ID)
	}
}

// TestCreateAgent_RuntimeKindDefaultedFromProvider is the Go-side mirror of
// migration 111's SQL backfill: a newly created row with no explicit
// RuntimeKind infers 'cli' for any pty/pty-*/sub-* provider and 'api' for
// everything else (including an empty provider), exactly matching
// chat.IsCLIProvider's classification.
func TestCreateAgent_RuntimeKindDefaultedFromProvider(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{"", "api"},
		{"anthropic", "api"},
		{"openai", "api"},
		{"pty", "cli"},
		{"pty-claude", "cli"},
		{"pty-codex", "cli"},
		{"sub-opencode", "cli"},
	}
	s := newTestStore(t)
	for i, c := range cases {
		a := &AgentProfile{
			Name: "RK", Slug: "rk-" + string(rune('a'+i)), SystemPrompt: "x",
			DefaultProvider: c.provider,
		}
		if err := s.CreateAgent(a); err != nil {
			t.Fatalf("CreateAgent(provider=%q): %v", c.provider, err)
		}
		if a.RuntimeKind != c.want {
			t.Errorf("provider %q: RuntimeKind = %q, want %q", c.provider, a.RuntimeKind, c.want)
		}
		got, err := s.GetAgent(a.ID)
		if err != nil {
			t.Fatalf("GetAgent: %v", err)
		}
		if got.RuntimeKind != c.want {
			t.Errorf("provider %q round-trip: RuntimeKind = %q, want %q", c.provider, got.RuntimeKind, c.want)
		}
	}
}

// TestCreateAgent_RuntimeKindInvalidRejected confirms the Go-layer
// validation (mirroring the DB-level CHECK migration 111 adds) rejects any
// value outside 'cli'/'api'/empty.
func TestCreateAgent_RuntimeKindInvalidRejected(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "Bad RK", Slug: "bad-rk", SystemPrompt: "x", RuntimeKind: "pty"}
	err := s.CreateAgent(a)
	if err == nil {
		t.Fatal("expected validation error for runtime_kind='pty', got nil")
	}
}

// TestValidateAgentMultiAgentFields_ActivationModeThreeValueEnum confirms
// the design decision this task made explicit: activation_mode now accepts
// singleton/fresh-per-wake/concurrent, and the old 'instance' value (valid
// before migration 111) is rejected going forward.
func TestValidateAgentMultiAgentFields_ActivationModeThreeValueEnum(t *testing.T) {
	valid := []string{"", "singleton", "fresh-per-wake", "concurrent"}
	for _, v := range valid {
		a := &AgentProfile{ActivationMode: v}
		if err := validateAgentMultiAgentFields(a); err != nil {
			t.Errorf("activation_mode %q: unexpected error %v", v, err)
		}
	}
	a := &AgentProfile{ActivationMode: "instance"}
	if err := validateAgentMultiAgentFields(a); err == nil {
		t.Error("activation_mode 'instance': expected rejection, got nil (it was retired by migration 111)")
	}
}

// TestDefaultActivationModeForClass pins the class -> activation_mode
// default mapping documented on DefaultActivationModeForClass: process and
// template both get the non-blocking 'fresh-per-wake' default (matching
// durable_wake.go's pre-migration-110 behavior for process, and
// deliberately extending it to template -- see this task's Work Log and
// the migration's own Up comment for the CW-20260817 template-class latent
// bug this closes); every other class (including unrecognized ones)
// defaults to the blocking 'singleton'.
func TestDefaultActivationModeForClass(t *testing.T) {
	cases := map[string]string{
		"process":  "fresh-per-wake",
		"template": "fresh-per-wake",
		"advisor":  "singleton",
		"harness":  "singleton",
		"":         "singleton",
		"bogus":    "singleton",
	}
	for class, want := range cases {
		if got := DefaultActivationModeForClass(class); got != want {
			t.Errorf("DefaultActivationModeForClass(%q) = %q, want %q", class, got, want)
		}
	}
}

// TestCreateAgent_ActivationModeDefaultsFromClass confirms
// applyMultiAgentDefaults actually wires DefaultActivationModeForClass into
// the real CreateAgent path (not just the pure-function unit test above):
// a caller that sets Class but leaves ActivationMode empty lands on the
// class-appropriate default, not a flat 'singleton' for every class.
func TestCreateAgent_ActivationModeDefaultsFromClass(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "Process Agent", Slug: "process-agent-default", SystemPrompt: "x", Class: "process"}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.ActivationMode != "fresh-per-wake" {
		t.Errorf("ActivationMode = %q, want 'fresh-per-wake' for an unset activation_mode on a process-class agent", a.ActivationMode)
	}
}
