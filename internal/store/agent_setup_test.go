package store

import (
	"context"
	"errors"
	"testing"
)

func setupInput(def []byte, tools ...string) AgentSetup {
	return AgentSetup{Definition: def, Provider: "anthropic", Model: "m", Tools: tools, GrantedBy: "cli:setup", Reason: "test"}
}

func TestSetupAgentCreatesEverythingAndIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	nativeTool(t, s, "dev_read")
	nativeTool(t, s, "dev_grep")
	def := definitionTestBytes("def:setup-one", "1", "Setup fixture.")
	first, err := s.SetupAgent(ctx, setupInput(def, AllAvailableTools))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.HostSettings.Slug != "def-setup-one" || len(first.Tools) < 2 || first.AuthorityID == "" {
		t.Fatalf("first = %+v", first)
	}
	hosts, auths := authorityRowCount(t, s, "agent_host_settings"), authorityRowCount(t, s, "native_authorities")
	audit, grants := authorityRowCount(t, s, "native_authority_audit"), authorityRowCount(t, s, "native_authority_tools")
	second, err := s.SetupAgent(ctx, setupInput(def, "dev.read", "dev_grep"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.HostSettings.ID != first.HostSettings.ID || second.AuthorityID != first.AuthorityID {
		t.Fatalf("second = %+v, want the existing ids and Created=false", second)
	}
	if authorityRowCount(t, s, "agent_host_settings") != hosts || authorityRowCount(t, s, "native_authorities") != auths ||
		authorityRowCount(t, s, "native_authority_audit") != audit || authorityRowCount(t, s, "native_authority_tools") != grants {
		t.Fatal("a repeat setup wrote rows")
	}
}

func TestSetupAgentRefusesBeforeAnyWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	nativeTool(t, s, "dev_read")
	def := definitionTestBytes("def:setup-two", "1", "Setup fixture.")
	if _, err := s.SetupAgent(ctx, setupInput(def, "no_such_tool")); !errors.Is(err, ErrNativeToolNotGrantable) {
		t.Fatalf("unknown tool: %v", err)
	}
	in := setupInput(def, "dev_read")
	in.Runtime = "wasm"
	if _, err := s.SetupAgent(ctx, in); err == nil {
		t.Fatal("unsupported runtime accepted")
	}
	in = setupInput(def, "dev_read")
	in.GrantedBy = " "
	if _, err := s.SetupAgent(ctx, in); !errors.Is(err, ErrNativeAuthorityActor) {
		t.Fatalf("no actor: %v", err)
	}
	for _, table := range []string{"agent_definitions", "agent_host_settings", "native_authorities", "native_authority_tools"} {
		if n := authorityRowCount(t, s, table); n != 0 {
			t.Fatalf("%s has %d rows after refusals", table, n)
		}
	}
}

func TestSetupAgentRollsBackWholeSetupOnLateFailure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	nativeTool(t, s, "dev_read")
	// The tool disappears between preflight and grant: force it by removing it
	// from the catalog after ResolveSetupTools would have seen it.
	def := definitionTestBytes("def:setup-three", "1", "Setup fixture.")
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_grant BEFORE INSERT ON native_authority_tools BEGIN SELECT RAISE(ABORT, 'forced'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetupAgent(ctx, setupInput(def, "dev_read")); err == nil {
		t.Fatal("expected the forced grant failure")
	}
	for _, table := range []string{"agent_definitions", "agent_host_settings", "native_authorities", "native_authority_audit"} {
		if n := authorityRowCount(t, s, table); n != 0 {
			t.Fatalf("%s kept %d rows after a failed setup", table, n)
		}
	}
}

func TestSetupAgentRefusesSlugBoundToDifferentSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	nativeTool(t, s, "dev_read")
	def := definitionTestBytes("def:setup-four", "1", "Setup fixture.")
	if _, err := s.SetupAgent(ctx, setupInput(def, "dev_read")); err != nil {
		t.Fatal(err)
	}
	in := setupInput(def, "dev_read")
	in.Model = "other-model"
	if _, err := s.SetupAgent(ctx, in); !errors.Is(err, ErrAgentSetupConflict) {
		t.Fatalf("different model on same slug: %v", err)
	}
}

func TestSetupAgentValidatesLikeCreateHostSettings(t *testing.T) {
	s := newTestStore(t)
	nativeTool(t, s, "dev_read")
	def := definitionTestBytes("def:setup-five", "1", "Setup fixture.")
	for _, slug := range []string{"../outside", "user", "Has Space", "-lead"} {
		in := setupInput(def, "dev_read")
		in.Slug = slug
		if _, err := s.SetupAgent(context.Background(), in); err == nil {
			t.Fatalf("slug %q accepted", slug)
		}
	}
	if n := authorityRowCount(t, s, "agent_host_settings"); n != 0 {
		t.Fatalf("%d host settings after refusals", n)
	}
}
