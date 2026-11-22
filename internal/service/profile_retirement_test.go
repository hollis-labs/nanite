package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestProfileRetirementExportsBeforeDeleteAndRetainsReceipt(t *testing.T) {
	svc, st, root := newAgentConfigTestService(t)
	ctx := t.Context()
	p, err := historicalRetirementFixture(svc, &store.AgentProfile{Name: "Test", Slug: "retirement-test", SystemPrompt: "private instructions"}, []agent.ProcedureDefinition{{Name: "test", Body: "private procedure"}})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := svc.ExportEditableProfile(ctx, p.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Retired || !receipt.ReceiptPersisted {
		t.Fatal("export claims deletion or is not persisted")
	}
	file := filepath.Join(root, "profile-retirements", receipt.ExportID+".json")
	bytes, err := os.ReadFile(file) // #nosec G304 -- owner-private fixture export under t.TempDir, not caller input.
	if err != nil {
		t.Fatal(err)
	}
	var archive profileRetirementArchive
	if err = json.Unmarshal(bytes, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Export.Revision != p.Revision || len(archive.Export.Tables["agent_procedures"].Rows) != 1 || len(archive.Export.Tables["agent_profile_revisions"].Rows) == 0 {
		t.Fatal("export lost profile history/children")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("export is not private", err)
	}
	info, err = os.Stat(filepath.Dir(file))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("export directory is not private", err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.Profile.ID); err != nil {
		t.Fatal("export deleted profile", err)
	}
	result, err := svc.RetireEditableProfile(ctx, p.Profile.ID, receipt.ExportID, receipt.Digest)
	if err != nil || !result.Retired || !result.ReceiptPersisted {
		t.Fatal(result, err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.Profile.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("retirement did not delete profile", err)
	}
	var remainingProcedures int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_procedures WHERE agent_id=?`, p.Profile.ID).Scan(&remainingProcedures); err != nil || remainingProcedures != 0 {
		t.Fatal("retirement left historical procedures", err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("retirement removed export", err)
	}
	retry, err := svc.RetireEditableProfile(ctx, p.Profile.ID, receipt.ExportID, receipt.Digest)
	if err != nil || retry != result {
		t.Fatal("completed retry lost receipt", err)
	}
}

func TestProfileRetirementRefusesProtectedSources(t *testing.T) {
	for _, tc := range []struct{ source, plugin string }{{"builtin", ""}, {"internal", ""}, {"system", ""}, {"plugin", ""}, {"external", ""}, {"user", "plugin-owner"}} {
		t.Run(tc.source+tc.plugin, func(t *testing.T) {
			svc, st, _ := newAgentConfigTestService(t)
			p := &store.AgentProfile{Name: "Protected", Slug: "protected", SystemPrompt: "x", Source: tc.source, PluginID: tc.plugin}
			if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.ExportEditableProfile(t.Context(), p.ID); !errors.Is(err, store.ErrProfileRetirementProtected) {
				t.Fatal("protected export accepted", err)
			}
			if _, err := st.GetHistoricalAgentProfile(t.Context(), p.ID); err != nil {
				t.Fatal("protected profile changed", err)
			}
		})
	}
}

func TestProtectedProfileRetirementExportsAuditsAndSuppressesBootReingest(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := t.Context()
	p := &store.AgentProfile{Name: "Protected", Slug: "protected-retire", SystemPrompt: "x", Source: "builtin", Class: "process"}
	if err := storetest.HistoricalProfile(ctx, st, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExportEditableProfile(ctx, p.ID); !errors.Is(err, store.ErrProfileRetirementProtected) {
		t.Fatal("editable export accepted protected profile", err)
	}
	receipt, err := svc.ExportProtectedProfile(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.RetireProtectedProfile(ctx, p.ID, receipt.ExportID, receipt.Digest, ProtectedProfileRetirementRequest{Actor: "test", Reason: "clean break"})
	if err != nil || !result.Retired {
		t.Fatal(result, err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("protected retirement did not delete profile", err)
	}
	retired, err := st.GetRetiredAgentProfileBySlug(ctx, p.Slug)
	if err != nil || retired == nil {
		t.Fatal("protected retirement did not write tombstone", retired, err)
	}
	if retired.ID != p.ID || retired.ExportID != receipt.ExportID || retired.Digest != receipt.Digest || retired.Actor != "test" || retired.Reason != "clean break" {
		t.Fatalf("unexpected tombstone: %+v", retired)
	}
	err = upsertAgentDef(st, &agent.Definition{Name: "Protected", Slug: p.Slug, SystemPrompt: "resurrect", Source: "internal"})
	if !errors.Is(err, store.ErrProfileIngestionRetired) {
		t.Fatal("boot reingest was not suppressed", err)
	}
	if _, err = st.GetAgentBySlug(ctx, p.Slug); err == nil {
		t.Fatal("boot reingest resurrected retired profile")
	}
}

func TestProfileRetirementRejectsChangedProfileAndGrantWithoutRevisionChange(t *testing.T) {
	for _, mutation := range []string{"profile", "grant", "procedure", "protected"} {
		t.Run(mutation, func(t *testing.T) {
			svc, st, _ := newAgentConfigTestService(t)
			ctx := t.Context()
			p, err := historicalRetirementFixture(svc, &store.AgentProfile{Name: "Test", Slug: "state-change", SystemPrompt: "x"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := svc.ExportEditableProfile(ctx, p.Profile.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "profile":
				changed := *p.Profile
				changed.SystemPrompt = "changed"
				if err = func() error {
					_, e := st.DB.ExecContext(ctx, `UPDATE agent_profiles SET system_prompt=? WHERE id=?`, changed.SystemPrompt, changed.ID)
					return e
				}(); err != nil {
					t.Fatal(err)
				}
			case "grant":
				tool, e := st.UpsertKnownTool(ctx, "retirement-tool", "builtin", "available", "")
				if e != nil {
					t.Fatal(e)
				}
				if err = func() error {
					_, e := st.DB.ExecContext(ctx, `INSERT INTO agent_dispatch_tool_allowlist(agent_id,tool_id,created_at) VALUES(?,?,datetime('now'))`, p.Profile.ID, tool)
					return e
				}(); err != nil {
					t.Fatal(err)
				}
			case "procedure":
				_, err = st.DB.ExecContext(ctx, `INSERT INTO agent_procedures(agent_id,name,body) VALUES(?,?,?)`, p.Profile.ID, "new", "changed")
				if err != nil {
					t.Fatal(err)
				}
			case "protected":
				_, err = st.DB.ExecContext(ctx, `UPDATE agent_profiles SET source='builtin' WHERE id=?`, p.Profile.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := st.GetHistoricalAgentProfile(ctx, p.Profile.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (mutation == "grant" || mutation == "procedure") && before.Revision != receipt.Revision {
				t.Fatal("fixture unexpectedly changed profile revision")
			}
			_, err = svc.RetireEditableProfile(ctx, p.Profile.ID, receipt.ExportID, receipt.Digest)
			if !errors.Is(err, store.ErrProfileRetirementConflict) && !errors.Is(err, store.ErrProfileRetirementProtected) {
				t.Fatal("changed state accepted", err)
			}
			after, e := st.GetHistoricalAgentProfile(ctx, p.Profile.ID)
			if e != nil || after.Revision != before.Revision {
				t.Fatal("refusal changed profile", e)
			}
		})
	}
}

func TestProfileRetirementRequiresOwnExportAndRollsBackFailedCleanup(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	ctx := t.Context()
	p, err := historicalRetirementFixture(svc, &store.AgentProfile{Name: "Test", Slug: "retire-rollback", SystemPrompt: "x"}, []agent.ProcedureDefinition{{Name: "keep", Body: "keep"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := historicalRetirementFixture(svc, &store.AgentProfile{Name: "Other", Slug: "other-retire", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := svc.ExportEditableProfile(ctx, p.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ id, export, digest string }{{other.Profile.ID, receipt.ExportID, receipt.Digest}, {p.Profile.ID, "../foreign", receipt.Digest}, {p.Profile.ID, receipt.ExportID, "sha256:wrong"}} {
		if _, err = svc.RetireEditableProfile(ctx, request.id, request.export, request.digest); err == nil {
			t.Fatal("foreign or invalid receipt accepted")
		}
	}
	if _, err = st.DB.Exec(`CREATE TRIGGER reject_profile_delete BEFORE DELETE ON agent_profiles BEGIN SELECT RAISE(ABORT,'fixture delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	result, err := svc.RetireEditableProfile(ctx, p.Profile.ID, receipt.ExportID, receipt.Digest)
	if err == nil || result.Retired {
		t.Fatal("failed deletion claims success")
	}
	var rows int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_procedures WHERE agent_id=?`, p.Profile.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("failed delete lost historical children", err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.Profile.ID); err != nil {
		t.Fatal("failed delete lost profile", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = svc.RetireEditableProfile(canceled, p.Profile.ID, receipt.ExportID, receipt.Digest); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled request accepted", err)
	}
}

func TestProfileRetirementRefusesSymlinkArchive(t *testing.T) {
	svc, _, root := newAgentConfigTestService(t)
	p, err := historicalRetirementFixture(svc, &store.AgentProfile{Name: "Test", Slug: "symlink-export", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(root, "profile-retirements")); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ExportEditableProfile(t.Context(), p.Profile.ID); err == nil {
		t.Fatal("symlink archive accepted")
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("archive followed symlink", err)
	}
}

func TestProfileRetirementOldExportFormatRequiresFreshExport(t *testing.T) {
	svc, st, root := newAgentConfigTestService(t)
	ctx := t.Context()
	p, err := historicalRetirementFixture(svc, &store.AgentProfile{Name: "Test", Slug: "old-export", SystemPrompt: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := svc.ExportEditableProfile(ctx, p.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "profile-retirements", receipt.ExportID+".json")
	data, err := os.ReadFile(file) // #nosec G304 -- owner-private fixture export, not caller input.
	if err != nil {
		t.Fatal(err)
	}
	var archive profileRetirementArchive
	if err = json.Unmarshal(data, &archive); err != nil {
		t.Fatal(err)
	}
	archive.Export.SchemaVersion = 1
	archive.Receipt.Digest, err = archive.Export.Digest()
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RetireEditableProfile(ctx, p.Profile.ID, receipt.ExportID, archive.Receipt.Digest); !errors.Is(err, store.ErrProfileRetirementConflict) {
		t.Fatal("old format admitted", err)
	}
	if _, err = st.GetHistoricalAgentProfile(ctx, p.Profile.ID); err != nil {
		t.Fatal("old-format refusal deleted profile", err)
	}
	fresh, err := svc.ExportEditableProfile(ctx, p.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := svc.RetireEditableProfile(ctx, p.Profile.ID, fresh.ExportID, fresh.Digest); err != nil || !result.Retired {
		t.Fatal("fresh format refused", result, err)
	}
}

// Build the pre-cut historical state only; none of these rows is a runtime actor.
func historicalRetirementFixture(svc *AgentConfigService, p *store.AgentProfile, procedures []agent.ProcedureDefinition) (*AgentConfigResult, error) {
	ctx := context.Background()
	if err := storetest.HistoricalProfile(ctx, svc.store, p); err != nil {
		return nil, err
	}
	for _, proc := range procedures {
		if _, err := svc.store.DB.ExecContext(ctx, `INSERT INTO agent_procedures(agent_id,name,body,scope) VALUES(?,?,?,?)`, p.ID, proc.Name, proc.Body, proc.Scope); err != nil {
			return nil, err
		}
	}
	return &AgentConfigResult{Profile: p, Class: svc.Classify(p), Revision: p.Revision}, nil
}
