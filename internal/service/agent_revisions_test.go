package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentRevisionPartialRestorePreservesCurrentAuthorityAndProvenance(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()
	tool, err := st.UpsertKnownTool(ctx, "restore-tool", "builtin", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	role := &store.Role{Slug: "restore-role", Name: "Restore Role"}
	if err = st.CreateRole(ctx, role); err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateWithAssignments(ctx, &store.AgentProfile{Name: "Original", Slug: "partial-restore", SystemPrompt: "Preserve this prompt", RoleTools: `["restore-tool"]`}, nil, AgentAssignments{RoleID: &role.ID})
	if err != nil {
		t.Fatal(err)
	}
	oldRevision := created.Revision
	if err = st.RevokeAgentTool(ctx, created.Profile.ID, tool); err != nil {
		t.Fatal(err)
	}
	if err = st.GrantAgentDispatchTool(ctx, created.Profile.ID, tool); err != nil {
		t.Fatal(err)
	}
	if err = st.SetAgentDefaultTrustTier(ctx, created.Profile.ID, "trusted"); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetAgent(ctx, created.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Description = "current provenance"
	current.SourceRef = "/provenance/retained.md"
	current.ImportedAt = "2026-10-08T00:00:00Z"
	current.OriginSystem = "host-import"
	current.TetherURN = "urn:tether:current"
	current.SystemPrompt = "Changed"
	current.RoleID = ""
	if err = st.UpdateAgent(ctx, current); err != nil {
		t.Fatal(err)
	}
	// An unrelated edit must not seed the old role declaration back into grants.
	updated := *current
	updated.Name = "Edited"
	result, err := svc.UpdateWithAssignments(ctx, current, &updated, nil, current.Revision, AgentAssignments{})
	if err != nil {
		t.Fatal(err)
	}
	names, err := st.ListAgentToolNames(ctx, current.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("edit re-granted revoked tool: %v, %v", names, err)
	}
	restored, err := svc.RestoreRevision(ctx, current.ID, oldRevision, result.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p := restored.Profile
	if p.Name != "Original" || p.SystemPrompt != "Preserve this prompt" || p.RoleID != role.ID {
		t.Fatalf("editable profile/assignments not restored: %#v", p)
	}
	if p.ID != current.ID || p.Source != current.Source || p.SourceRef != current.SourceRef || p.ImportedAt != current.ImportedAt || p.OriginSystem != current.OriginSystem || p.TetherURN != current.TetherURN {
		t.Fatalf("current identity/provenance lost: %#v", p)
	}
	names, err = st.ListAgentToolNames(ctx, p.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("restore re-granted revoked tool: %v, %v", names, err)
	}
	dispatch, err := st.ListAgentDispatchToolNames(ctx, p.ID)
	if err != nil || len(dispatch) != 1 || dispatch[0] != "restore-tool" {
		t.Fatalf("current dispatch grant changed: %v, %v", dispatch, err)
	}
	var tier string
	if err = st.DB.QueryRow(`SELECT default_trust_tier FROM agent_profiles WHERE id = ?`, p.ID).Scan(&tier); err != nil || tier != "trusted" {
		t.Fatalf("trust = %q, %v", tier, err)
	}
	// A later boot must not re-derive grants from the restored declarations.
	if _, err = BackfillAgentToolsFromLegacyColumns(ctx, st); err != nil {
		t.Fatal(err)
	}
	names, err = st.ListAgentToolNames(ctx, p.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("restart backfill changed restored grants: %v, %v", names, err)
	}
	latest, err := st.GetAgentRevision(ctx, p.ID, restored.Revision)
	if err != nil || latest.Operation != "restore_partial" || latest.RestoredFrom != oldRevision || restored.Revision == oldRevision {
		t.Fatalf("restore is not a new labeled revision: %#v, %v", latest, err)
	}
	if _, err := svc.RestoreRevision(ctx, p.ID, oldRevision, result.Revision); !errors.Is(err, store.ErrAgentRevisionConflict) {
		t.Fatalf("stale restore = %v", err)
	}
}

func TestAgentRevisionRestoreRejectsOwnedTargetsAndForeignHistory(t *testing.T) {
	for _, source := range []string{"internal", "builtin", "plugin", "system"} {
		t.Run(source, func(t *testing.T) {
			svc, st, _ := newAgentConfigTestService(t)
			ctx := context.Background()
			p := &store.AgentProfile{Name: "Owned", Slug: "owned-" + source, SystemPrompt: "Owned prompt", Source: source}
			if err := st.CreateAgent(ctx, p); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.RestoreRevision(ctx, p.ID, p.Revision, p.Revision); !errors.Is(err, ErrAgentNotManaged) {
				t.Fatalf("owned restore = %v", err)
			}
			current, err := st.GetAgent(ctx, p.ID)
			if err != nil || current.Revision != p.Revision {
				t.Fatal("refused restoration mutated target")
			}
		})
	}
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()
	first, err := svc.Create(&store.AgentProfile{Name: "First", Slug: "first-history", SystemPrompt: "First"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(&store.AgentProfile{Name: "Second", Slug: "second-history", SystemPrompt: "Second"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreRevision(ctx, second.Profile.ID, first.Revision, second.Revision); err == nil {
		t.Fatal("foreign history accepted")
	}
	// Source alone cannot override actual plugin ownership.
	p := &store.AgentProfile{Name: "Plugin", Slug: "plugin-id-owned", SystemPrompt: "Plugin", Source: "user", PluginID: "plugin-owner"}
	if err := st.CreateAgent(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreRevision(ctx, p.ID, p.Revision, p.Revision); !errors.Is(err, ErrAgentNotManaged) {
		t.Fatalf("plugin-id restore = %v", err)
	}
}

func TestAgentRevisionRestoreFailureRollsBackHistoryAndProfile(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()
	created, err := svc.Create(&store.AgentProfile{Name: "Original", Slug: "restore-rollback", SystemPrompt: "Historical"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := *created.Profile
	p.SystemPrompt = "Current"
	changed, err := svc.Update(created.Profile, &p, nil, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(`CREATE TRIGGER fail_restore_history BEFORE UPDATE ON agent_profile_revisions
 WHEN NEW.operation = 'restore_partial' BEGIN SELECT RAISE(ABORT,'restore history unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RestoreRevision(ctx, p.ID, created.Revision, changed.Revision); err == nil {
		t.Fatal("failed history annotation accepted")
	}
	current, err := st.GetAgent(ctx, p.ID)
	if err != nil || current.SystemPrompt != "Current" || current.Revision != changed.Revision {
		t.Fatalf("partial restoration survived: %#v, %v", current, err)
	}
	rows, err := svc.ListRevisions(ctx, p.ID, 100, 0)
	if err != nil || rows[0].ID != changed.Revision {
		t.Fatalf("failed restoration left history: %#v, %v", rows, err)
	}
}

func TestAgentRevisionRestoreMissingAssignmentRefusesBeforeMutation(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()
	role := &store.Role{Name: "Retired", Slug: "retired-history-role"}
	if err := st.CreateRole(ctx, role); err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateWithAssignments(ctx, &store.AgentProfile{Name: "Reference", Slug: "reference-history", SystemPrompt: "Reference prompt"}, nil, AgentAssignments{RoleID: &role.ID})
	if err != nil {
		t.Fatal(err)
	}
	p := *created.Profile
	empty := ""
	current, err := svc.UpdateWithAssignments(ctx, created.Profile, &p, nil, created.Revision, AgentAssignments{RoleID: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteRole(ctx, role.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RestoreRevision(ctx, p.ID, created.Revision, current.Revision); err == nil {
		t.Fatal("restore recreated or accepted missing assignment")
	}
	after, err := st.GetAgent(ctx, p.ID)
	if err != nil || after.RoleID != "" || after.Revision != current.Revision {
		t.Fatalf("refused restore changed target: %#v, %v", after, err)
	}
}

func TestAgentRevisionRestorePreservesEditableSource(t *testing.T) {
	for _, source := range []string{"api", "cli", "managed_file", "nanite", "project", "user"} {
		t.Run(source, func(t *testing.T) {
			svc, st, _ := newAgentConfigTestService(t)
			ctx := context.Background()
			p := &store.AgentProfile{Name: "Managed", Slug: "managed-history", SystemPrompt: "Managed prompt", Source: source, SourceRef: "/provenance/only.md"}
			if err := st.CreateAgent(ctx, p); err != nil {
				t.Fatal(err)
			}
			res, err := svc.RestoreRevision(ctx, p.ID, p.Revision, p.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if res.Profile.Source != source || res.Profile.SourceRef != p.SourceRef || !res.Class.Editable() {
				t.Fatalf("editability/provenance changed: %#v", res)
			}
		})
	}
}
func TestAgentRevisionRestoreRecoversPromptLostByDirectWriter(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := context.Background()
	created, err := svc.Create(&store.AgentProfile{Name: "Recover", Slug: "recover-prompt", SystemPrompt: "Historical instructions"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Historical low-level writers could erase a prompt. History must capture
	// that mutation and allow the operator to recover the earlier instructions.
	if _, err = st.DB.ExecContext(ctx, `UPDATE agent_profiles SET system_prompt = '' WHERE id = ?`, created.Profile.ID); err != nil {
		t.Fatal(err)
	}
	broken, err := st.GetAgent(ctx, created.Profile.ID)
	if err != nil || broken.SystemPrompt != "" || broken.Revision == created.Revision {
		t.Fatalf("lost-prompt fixture = %#v, %v", broken, err)
	}
	restored, err := svc.RestoreRevision(ctx, broken.ID, created.Revision, broken.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Profile.SystemPrompt != "Historical instructions" || restored.Revision == broken.Revision || restored.Revision == created.Revision {
		t.Fatalf("prompt was not recovered as a new revision: %#v", restored)
	}
	lost, err := st.GetAgentRevision(ctx, broken.ID, broken.Revision)
	if err != nil || lost.Profile.SystemPrompt != "" {
		t.Fatalf("lost-prompt write was not retained: %#v, %v", lost, err)
	}
}
