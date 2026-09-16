package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newAgentConfigTestService(t *testing.T) (*AgentConfigService, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	st := newConfigTestStoreAt(t, filepath.Join(root, "agent-config.db"))
	return NewAgentConfigService(st, agent.NewClassification(), nil), st, root
}

func newConfigTestStore(t *testing.T) *store.Store {
	t.Helper()
	return newConfigTestStoreAt(t, filepath.Join(t.TempDir(), "config-test.db"))
}

func newConfigTestStoreAt(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := storetest.New(t, context.Background(), path)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	return st
}

func TestAgentConfigCreateIsDatabaseOnly(t *testing.T) {
	svc, st, root := newAgentConfigTestService(t)
	res, err := svc.Create(&store.AgentProfile{
		Name: "Atlas", Slug: "atlas", SystemPrompt: "Curate.", RoleTools: `["dev_read"]`,
	}, []agent.ProcedureDefinition{{Name: "lint", Body: "Lint the graph."}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Profile.ID == "" || res.Profile.Source != "user" || res.Profile.SourceRef != "" {
		t.Fatalf("created profile = %#v", res.Profile)
	}
	if res.Revision != "" || svc.Revision(res.Profile) != "" {
		t.Fatalf("database profile unexpectedly has a file revision: %#v", res)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".nanite", "agents")); !os.IsNotExist(statErr) {
		t.Fatalf("create wrote an agent projection directory: %v", statErr)
	}
	procedures, err := st.ListAgentProcedures(context.Background(), res.Profile.ID)
	if err != nil || len(procedures) != 1 || procedures[0].Name != "lint" {
		t.Fatalf("procedures = %#v, %v", procedures, err)
	}
	var trust string
	if err := st.DB.QueryRow(`SELECT default_trust_tier FROM agent_profiles WHERE id = ?`, res.Profile.ID).Scan(&trust); err != nil {
		t.Fatalf("read trust tier: %v", err)
	}
	if trust != "untrusted" {
		t.Fatalf("trust tier = %q, want untrusted", trust)
	}
}

func TestAgentConfigUpdateRenamePreservesIdentityAndCapabilities(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"},
		[]agent.ProcedureDefinition{{Name: "lint", Body: "Lint."}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated := *created.Profile
	updated.Slug = "atlas-curator"
	updated.Description = "renamed in the database"
	updated.SourceRef = "/tmp/legacy-authority-must-not-be-written.md"
	res, err := svc.Update(created.Profile, &updated, nil, "obsolete-file-revision")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Profile.ID != created.Profile.ID || res.Profile.Slug != "atlas-curator" {
		t.Fatalf("identity/slug changed incorrectly: %#v", res.Profile)
	}
	if res.Profile.SourceRef != "" {
		t.Fatalf("legacy source_ref round-tripped after edit: %q", res.Profile.SourceRef)
	}
	procedures, err := st.ListAgentProcedures(context.Background(), res.Profile.ID)
	if err != nil || len(procedures) != 1 || procedures[0].Name != "lint" {
		t.Fatalf("procedures after rename = %#v, %v", procedures, err)
	}
}

func TestAgentConfigRejectsUnsafeSlugBeforeDatabaseWrite(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	before, err := st.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents before: %v", err)
	}
	for _, slug := range []string{"../evil", "a/b", "UPPER"} {
		if _, createErr := svc.Create(&store.AgentProfile{Name: "Evil", Slug: slug, SystemPrompt: "x"}, nil); createErr == nil {
			t.Errorf("Create(%q) unexpectedly succeeded", slug)
		}
	}
	rows, err := st.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(rows) != len(before) {
		t.Fatalf("unsafe creates changed row count: before=%d after=%d", len(before), len(rows))
	}
}

func TestAgentConfigCopyDoesNotDereferenceSourceRef(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	source := &store.AgentProfile{
		Name: "Plugin Agent", Slug: "plugin-agent", SystemPrompt: "x", Source: "plugin",
		SourceRef: "/path/that/must/not/be/read/plugin-agent.md",
	}
	if err := st.CreateAgent(context.Background(), source); err != nil {
		t.Fatalf("seed plugin: %v", err)
	}
	if err := st.InsertAgentProcedure(context.Background(), store.AgentProcedure{
		AgentID: source.ID, Name: "search", Body: "Search upstream.", Scope: "agent",
	}); err != nil {
		t.Fatalf("seed plugin procedure: %v", err)
	}
	res, err := svc.CopyToManaged(source, nil)
	if err != nil {
		t.Fatalf("CopyToManaged: %v", err)
	}
	if res.Profile.ID == source.ID || res.Profile.Source != "user" || res.Profile.SourceRef != "" {
		t.Fatalf("copy = %#v", res.Profile)
	}
	procedures, err := st.ListAgentProcedures(context.Background(), res.Profile.ID)
	if err != nil || len(procedures) != 1 || procedures[0].Name != "search" {
		t.Fatalf("copied procedures = %#v, %v", procedures, err)
	}
	if _, err := svc.CopyToManaged(res.Profile, nil); !errors.Is(err, ErrAgentAlreadyManaged) {
		t.Fatalf("managed copy error = %v, want ErrAgentAlreadyManaged", err)
	}
}

func TestAgentConfigCopyRejectsInternalRegardlessOfSourceRef(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	internal := &store.AgentProfile{
		Name: "Chat", Slug: "chat-internal", SystemPrompt: "x", Source: "internal",
		SourceRef: "/plugin-looking/path/that/must-not-change-ownership.md",
	}
	if err := st.CreateAgent(context.Background(), internal); err != nil {
		t.Fatalf("seed internal: %v", err)
	}
	if _, err := svc.CopyToManaged(internal, nil); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("CopyToManaged internal error = %v, want ErrAgentNotManaged", err)
	}
	if _, err := st.GetAgentBySlug(context.Background(), "chat-internal-copy"); err == nil {
		t.Fatal("internal profile was copied despite CopyToManagedAllowed=false")
	}
}

func TestAgentConfigCopyAllowsExternalRegardlessOfSourceRef(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	external := &store.AgentProfile{
		Name: "Adapter Agent", Slug: "adapter-agent", SystemPrompt: "x", Source: "adapter",
		SourceRef: "/internal-looking/path/that-must-not-change-ownership.md",
	}
	if err := st.CreateAgent(context.Background(), external); err != nil {
		t.Fatalf("seed external: %v", err)
	}
	result, err := svc.CopyToManaged(external, nil)
	if err != nil {
		t.Fatalf("CopyToManaged external: %v", err)
	}
	if result.Profile.Source != "user" || result.Profile.SourceRef != "" || result.Profile.ID == external.ID {
		t.Fatalf("external copy = %+v", result.Profile)
	}
}

func TestAgentConfigDeleteIsDatabaseOnlyAndCascades(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	created, err := svc.Create(&store.AgentProfile{Name: "Atlas", Slug: "atlas", SystemPrompt: "x"},
		[]agent.ProcedureDefinition{{Name: "lint", Body: "Lint."}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if deleteErr := svc.Delete(created.Profile); deleteErr != nil {
		t.Fatalf("Delete: %v", deleteErr)
	}
	if _, getErr := st.GetAgentBySlug(context.Background(), "atlas"); getErr == nil {
		t.Fatal("profile remained after delete")
	}
	procedures, err := st.ListAgentProcedures(context.Background(), created.Profile.ID)
	if err != nil || len(procedures) != 0 {
		t.Fatalf("procedure children remained: %#v, %v", procedures, err)
	}
}

func TestAgentConfigRejectsReadOnlyInternalUpdate(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	internal := &store.AgentProfile{Name: "Chat", Slug: "chat", SystemPrompt: "x", Source: "internal"}
	if err := st.CreateAgent(context.Background(), internal); err != nil {
		t.Fatalf("seed internal: %v", err)
	}
	updated := *internal
	updated.Description = "not allowed"
	if _, err := svc.Update(internal, &updated, nil, ""); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("Update error = %v, want ErrAgentNotManaged", err)
	}
}

// TestAgentConfigCreateSeedsRoleSkillsAsCatalogNotGrant pins the distinction
// the roleSkills seeder exists to preserve: a seeded skill is discoverable but
// not yet executable. internal/skill/gate.go refuses a row with no
// ApprovedContentHash (GrantRequiredError, "never been approved"), so seeding
// an approval here would turn every declared skill into an ambient capability.
func TestAgentConfigCreateSeedsRoleSkillsAsCatalogNotGrant(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()

	if err := st.CreateSkill(ctx, &store.Skill{
		Name: "KB Triage", Slug: "kb-triage",
		Description: "Search the KB before offering a ticket.",
		SourceTier:  "user", ContentHash: "skl-test-abc123", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	res, err := svc.Create(&store.AgentProfile{
		Name: "Desk", Slug: "desk", SystemPrompt: "Work the queue.",
		// "no-such-skill" must be skipped without failing the create.
		RoleSkills: `["kb-triage","no-such-skill"]`,
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := st.ListAgentKnownSkills(ctx, res.Profile.ID)
	if err != nil {
		t.Fatalf("ListAgentKnownSkills: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d known-skill rows, want 1 (the unknown slug must be skipped): %+v", len(rows), rows)
	}
	got := rows[0]
	if got.SkillName != "kb-triage" {
		t.Errorf("skill_name = %q, want %q", got.SkillName, "kb-triage")
	}
	if !got.Pinned {
		t.Error("seeded row should be pinned")
	}
	if got.Reason != "role_seed" {
		t.Errorf("reason = %q, want %q", got.Reason, "role_seed")
	}
	if got.ApprovedContentHash != "" {
		t.Errorf("seeded row carries approval %q — roleSkills must seed a catalog entry, never a grant", got.ApprovedContentHash)
	}
}

// TestAgentConfigUpdateDoesNotRevokeSkillApproval guards a regression found in
// dogfooding: the roleSkills seeder runs on every Update, and
// InsertAgentKnownSkill is INSERT OR REPLACE — so re-seeding an already-granted
// slug blanked its approval columns. Editing an unrelated field must not
// silently revoke a skill the operator approved.
func TestAgentConfigUpdateDoesNotRevokeSkillApproval(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()

	if err := st.CreateSkill(ctx, &store.Skill{
		Name: "KB Triage", Slug: "kb-triage", Description: "d",
		SourceTier: "user", ContentHash: "skl-test-abc123", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	res, err := svc.Create(&store.AgentProfile{
		Name: "Desk", Slug: "desk", SystemPrompt: "Work the queue.",
		RoleSkills: `["kb-triage"]`,
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Approve it, the way the grant API does.
	if err := st.InsertAgentKnownSkill(ctx, store.AgentKnownSkill{
		AgentID: res.Profile.ID, SkillName: "kb-triage", Pinned: true,
		Reason: "role_seed", ApprovedContentHash: "skl-test-abc123",
		GrantedAt: "2026-09-16T00:00:00Z", GrantedBy: "operator@example.com",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	updated := *res.Profile
	updated.Description = "edited for an unrelated reason"
	if _, err := svc.Update(res.Profile, &updated, nil, ""); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := st.GetAgentKnownSkill(ctx, res.Profile.ID, "kb-triage")
	if err != nil || got == nil {
		t.Fatalf("GetAgentKnownSkill after update: %v (row=%+v)", err, got)
	}
	if got.ApprovedContentHash != "skl-test-abc123" {
		t.Errorf("approval lost on update: approved_content_hash = %q, want %q",
			got.ApprovedContentHash, "skl-test-abc123")
	}
	if got.GrantedBy != "operator@example.com" {
		t.Errorf("granted_by lost on update: %q", got.GrantedBy)
	}
}
