package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func protectedRetirementFixture(t *testing.T, source, plugin string) (*AgentConfigService, *store.Store, string, *CognitiveViews, *store.AgentProfile, ProtectedRetirementRequest) {
	t.Helper()
	svc, st, root := newAgentConfigTestService(t)
	host, verified := logicalProvisionHost(t)
	keep, err := svc.ProvisionGeneralChat(t.Context(), host, ProvisionGeneralChatRequest{Name: "General chat", Slug: "general-chat", DefinitionRef: verified.Ref})
	if err != nil {
		t.Fatal(err)
	}
	p := &store.AgentProfile{Name: "Protected", Slug: "protected-fixture", Source: source, PluginID: plugin, SystemPrompt: "private protected content"}
	if checkErr := st.CreateAgent(t.Context(), p); checkErr != nil {
		t.Fatal(checkErr)
	}
	req := ProtectedRetirementRequest{Acknowledge: true, Actor: "fixture-owner", Reason: "Keep general chat only", KeepProfileID: keep.Agent.Profile.ID, KeepRevision: keep.Agent.Revision}
	return svc, st, root, host, p, req
}

func TestProfileRetirementProtectedExportAuditAndExactKeep(t *testing.T) {
	for _, tc := range []struct{ source, plugin string }{{"builtin", ""}, {"internal", ""}, {"system", ""}, {"plugin", "plugin-fixture"}, {"external", ""}, {"user", "plugin-fixture"}} {
		t.Run(tc.source, func(t *testing.T) {
			svc, st, root, host, p, req := protectedRetirementFixture(t, tc.source, tc.plugin)
			ctx := t.Context()
			keep, err := st.GetAgent(ctx, req.KeepProfileID)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(keep)
			if _, checkErr := svc.ExportEditableProfile(ctx, p.ID); !errors.Is(checkErr, store.ErrProfileRetirementProtected) {
				t.Fatal("editable authority changed", checkErr)
			}
			r, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			if r.Retired || !r.ReceiptPersisted || r.Authority.Actor != req.Actor || r.Authority.KeepProfileID != keep.ID || r.Authority.KeepRevision != keep.Revision {
				t.Fatal("export lost attribution")
			}
			file := filepath.Join(root, "profile-retirements", r.ExportID+".protected.json")
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("export not private", err)
			}
			if _, checkErr := st.GetAgent(ctx, p.ID); checkErr != nil {
				t.Fatal("export had deletion effect", checkErr)
			}
			retired, err := st.ProfileIngestionRetired(ctx, p.ID, p.Slug)
			if err != nil || retired {
				t.Fatal("export had suppression effect", err)
			}
			result, err := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req)
			if err != nil || !result.Retired || !result.ReceiptPersisted {
				t.Fatal(result, err)
			}
			if _, checkErr := st.GetAgent(ctx, p.ID); !errors.Is(checkErr, sql.ErrNoRows) {
				t.Fatal("protected profile survived", checkErr)
			}
			for _, identity := range [][2]string{{p.ID, "renamed-slug"}, {"different-id", p.Slug}} {
				retired, err = st.ProfileIngestionRetired(ctx, identity[0], identity[1])
				if err != nil || !retired {
					t.Fatal("retired identity not suppressed", err)
				}
			}
			keep, err = st.GetAgent(ctx, keep.ID)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(keep)
			if string(before) != string(after) {
				t.Fatal("exact logical profile changed")
			}
			var auditActor, auditReason, auditExport string
			if checkErr := st.DB.QueryRowContext(ctx, `SELECT actor,reason,export_id FROM profile_ingestion_retirements WHERE profile_id=?`, p.ID).Scan(&auditActor, &auditReason, &auditExport); checkErr != nil || auditActor != req.Actor || auditReason != req.Reason || auditExport != r.ExportID {
				t.Fatal("committed attribution lost", checkErr)
			}
			retry, err := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req)
			if err != nil || retry != result {
				t.Fatal("retry lost committed outcome", err)
			}
		})
	}
}

func TestProfileRetirementProtectedRefusesAuthorityAndStaleExports(t *testing.T) {
	for _, mutation := range []string{"acknowledgement", "actor", "reason", "keep-target", "keep-unprovisioned", "keep-changed", "child-type", "wrong-attribution", "missing-export", "editable-export"} {
		t.Run(mutation, func(t *testing.T) {
			svc, st, _, host, p, req := protectedRetirementFixture(t, "system", "")
			ctx := t.Context()
			if _, checkErr := st.DB.ExecContext(ctx, `INSERT INTO agent_procedures(agent_id,name,body) VALUES(?,?,?)`, p.ID, "typed", []byte("foo")); checkErr != nil {
				t.Fatal(checkErr)
			}
			r, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "acknowledgement":
				req.Acknowledge = false
			case "actor":
				req.Actor = ""
			case "reason":
				req.Reason = ""
			case "keep-target":
				req.KeepProfileID = p.ID
			case "keep-unprovisioned":
				keep, lookupErr := st.GetAgent(ctx, req.KeepProfileID)
				if lookupErr != nil {
					t.Fatal(lookupErr)
				}
				keep.Settings = "{}"
				if checkErr := st.UpdateAgent(ctx, keep); checkErr != nil {
					t.Fatal(checkErr)
				}
			case "keep-changed":
				keep, lookupErr := st.GetAgent(ctx, req.KeepProfileID)
				if lookupErr != nil {
					t.Fatal(lookupErr)
				}
				keep.Name = "Changed"
				if checkErr := st.UpdateAgent(ctx, keep); checkErr != nil {
					t.Fatal(checkErr)
				}
			case "child-type":
				if _, checkErr := st.DB.ExecContext(ctx, `UPDATE agent_procedures SET body='Zm9v' WHERE agent_id=?`, p.ID); checkErr != nil {
					t.Fatal(checkErr)
				}
			case "wrong-attribution":
				req.Actor = "other-owner"
			case "missing-export":
				r.ExportID = "00000000-0000-0000-0000-000000000000"
			case "editable-export":
				keep, exportErr := svc.ExportEditableProfile(ctx, req.KeepProfileID)
				if exportErr != nil {
					t.Fatal(exportErr)
				}
				r.ExportID = keep.ExportID
				r.Digest = keep.Digest
			}
			if mutation == "acknowledgement" || mutation == "actor" || mutation == "reason" || mutation == "keep-target" || mutation == "keep-unprovisioned" {
				if _, checkErr := svc.ExportProtectedProfile(ctx, host, p.ID, req); checkErr == nil {
					t.Fatal("invalid authority exported")
				}
			}
			if _, checkErr := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req); checkErr == nil {
				t.Fatal("invalid retirement accepted")
			}
			if _, checkErr := st.GetAgent(ctx, p.ID); checkErr != nil {
				t.Fatal("refusal lost profile", checkErr)
			}
			retired, err := st.ProfileIngestionRetired(ctx, p.ID, p.Slug)
			if err != nil || retired {
				t.Fatal("refusal committed suppression", err)
			}
		})
	}
}

func TestProfileRetirementProtectedCleanupRollsBackSuppression(t *testing.T) {
	svc, st, _, host, p, req := protectedRetirementFixture(t, "internal", "")
	ctx := t.Context()
	if _, checkErr := st.DB.ExecContext(ctx, `CREATE TRIGGER fixture_retirement_refusal BEFORE DELETE ON agent_profiles BEGIN SELECT RAISE(ABORT,'fixture rollback'); END`); checkErr != nil {
		t.Fatal(checkErr)
	}
	r, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req); checkErr == nil {
		t.Fatal("cleanup failure accepted")
	}
	if _, checkErr := st.GetAgent(ctx, p.ID); checkErr != nil {
		t.Fatal("rollback lost profile", checkErr)
	}
	retired, err := st.ProfileIngestionRetired(ctx, p.ID, p.Slug)
	if err != nil || retired {
		t.Fatal("rollback left suppression", err)
	}
}

func TestProfileRetirementProtectedReceiptFailureReconcilesAudit(t *testing.T) {
	svc, st, root, host, p, req := protectedRetirementFixture(t, "internal", "")
	ctx := t.Context()
	r, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profile-retirements", r.ExportID+".protected.retired.json")
	// A post-commit notification simulates a final receipt persistence failure.
	svc = NewAgentConfigService(st, agent.NewClassification(), func(_, _ string) {
		if checkErr := os.Mkdir(path, 0700); checkErr != nil {
			t.Fatal(checkErr)
		}
	})
	result, err := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req)
	if err == nil || !result.Retired || result.ReceiptPersisted {
		t.Fatal("committed outcome concealed", result, err)
	}
	if checkErr := os.Remove(path); checkErr != nil {
		t.Fatal(checkErr)
	}
	result, err = svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req)
	if err != nil || !result.Retired || !result.ReceiptPersisted {
		t.Fatal("audit retry did not repair receipt", result, err)
	}
}

func TestProfileRetirementProtectedRefusesRunningProcessesAndDurableInstances(t *testing.T) {
	for _, mode := range []string{"durable", "runtime-id", "runtime-slug"} {
		t.Run(mode, func(t *testing.T) {
			svc, st, _, host, p, req := protectedRetirementFixture(t, "system", "")
			ctx := t.Context()
			if mode == "durable" {
				inst := &store.DurableAgentInstance{Name: "Running", Slug: "running-fixture", ProfileID: p.ID, Status: store.DurableAgentStatusActive}
				if checkErr := st.CreateDurableAgentInstance(ctx, inst); checkErr != nil {
					t.Fatal(checkErr)
				}
			} else {
				identity := p.ID
				if mode == "runtime-slug" {
					identity = p.Slug
				}
				row := &store.AgentRuntimeRow{ID: "running-fixture", AgentProfile: identity, State: "running", Mode: "background", StartedAt: time.Now()}
				if checkErr := st.CreateAgentRuntimeRow(ctx, row); checkErr != nil {
					t.Fatal(checkErr)
				}
			}
			if _, checkErr := svc.ExportProtectedProfile(ctx, host, p.ID, req); !errors.Is(checkErr, store.ErrProfileRetirementActive) {
				t.Fatal("active retirement admitted", checkErr)
			}
			if _, checkErr := st.GetAgent(ctx, p.ID); checkErr != nil {
				t.Fatal("active profile changed", checkErr)
			}
		})
	}
}

func TestProfileRetirementProtectedRestartCannotResurrectSeeds(t *testing.T) {
	svc, st, _, host, p, req := protectedRetirementFixture(t, "builtin", "")
	ctx := t.Context()
	r, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req); checkErr != nil {
		t.Fatal(checkErr)
	}
	path := st.DBPath(ctx)
	if checkErr := st.Close(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	reopened, err := store.New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(ctx) })
	for _, def := range []*agent.Definition{
		{Name: "Seed", Slug: p.Slug, Source: "internal", SystemPrompt: "new boot body"},
		{ID: p.ID, Name: "Renamed seed", Slug: "renamed-protected-fixture", Source: "system", SystemPrompt: "new body"},
	} {
		if count := AutoIngestAgents(reopened, []*agent.Definition{def}, nil); count != 0 {
			t.Fatal("retired seed ingested", count)
		}
		if _, checkErr := reopened.GetAgentBySlug(ctx, def.Slug); !errors.Is(checkErr, sql.ErrNoRows) {
			t.Fatal("boot resurrected retired profile", checkErr)
		}
	}
	keep, err := reopened.GetAgent(ctx, req.KeepProfileID)
	if err != nil || keep.Revision != r.Authority.KeepRevision {
		t.Fatal("restart changed retained identity", err)
	}
	for _, identity := range [][2]string{{p.ID, "renamed-retired-id"}, {"new-id", p.Slug}} {
		candidate := &store.AgentProfile{ID: identity[0], Slug: identity[1], Name: "Attempted recreation", Source: "user", SystemPrompt: "x"}
		if checkErr := reopened.CreateAgent(ctx, candidate); !errors.Is(checkErr, store.ErrProfileIngestionRetired) {
			t.Fatal("row writer admitted retired identity", checkErr)
		}
	}
	keep.Slug = p.Slug
	if checkErr := reopened.UpdateAgent(ctx, keep); !errors.Is(checkErr, store.ErrProfileIngestionRetired) {
		t.Fatal("row writer rebound retired slug", checkErr)
	}
	unchanged, err := reopened.GetAgent(ctx, req.KeepProfileID)
	if err != nil || unchanged.Slug != "general-chat" || unchanged.Revision != r.Authority.KeepRevision {
		t.Fatal("refused rebinding changed retained profile", err)
	}
}

func TestProfileRetirementProtectedExportsLinkedRoleAndRefusesChangedParent(t *testing.T) {
	svc, st, root, host, p, req := protectedRetirementFixture(t, "plugin", "fixture-plugin")
	ctx := t.Context()
	role := &store.Role{Name: "Plugin role", Slug: "private-plugin-role", SystemPrompt: "private role instructions"}
	if checkErr := st.CreateRole(ctx, role); checkErr != nil {
		t.Fatal(checkErr)
	}
	consumer := &store.Consumer{Name: "Plugin consumer", Slug: "private-plugin-consumer"}
	if checkErr := st.CreateConsumer(ctx, consumer); checkErr != nil {
		t.Fatal(checkErr)
	}
	p.RoleID = role.ID
	p.ConsumerID = consumer.ID
	p.SystemPrompt = ""
	if checkErr := st.UpdateAgent(ctx, p); checkErr != nil {
		t.Fatal(checkErr)
	}
	r, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "profile-retirements", r.ExportID+".protected.json")) // #nosec G304 -- owner-private temporary fixture, not caller input.
	if err != nil {
		t.Fatal(err)
	}
	var archive protectedRetirementArchive
	if checkErr := json.Unmarshal(data, &archive); checkErr != nil {
		t.Fatal(checkErr)
	}
	parent := archive.Export.Tables["roles"]
	if len(parent.Rows) != 1 || len(archive.Export.Tables["consumers"].Rows) != 1 {
		t.Fatal("protected class lost linked definition")
	}
	var cells []struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	}
	if checkErr := json.Unmarshal(parent.Rows[0], &cells); checkErr != nil {
		t.Fatal(checkErr)
	}
	found := false
	for i, col := range parent.Columns {
		if col == "system_prompt" {
			var body []byte
			if checkErr := json.Unmarshal(cells[i].Value, &body); checkErr != nil || cells[i].Type != "text" || string(body) != role.SystemPrompt {
				t.Fatal("role body not exactly exported", checkErr)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("role body missing")
	}
	role.SystemPrompt = "Changed after export"
	if checkErr := st.UpdateRole(ctx, role); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := svc.RetireProtectedProfile(ctx, p.ID, r.ExportID, r.Digest, req); !errors.Is(checkErr, store.ErrProfileRetirementConflict) {
		t.Fatal("parent change escaped CAS", checkErr)
	}
	fresh, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := svc.RetireProtectedProfile(ctx, p.ID, fresh.ExportID, fresh.Digest, req); checkErr != nil {
		t.Fatal(checkErr)
	}
	retainedRole, err := st.GetRole(ctx, role.ID)
	if err != nil || retainedRole.SystemPrompt != role.SystemPrompt {
		t.Fatal("retirement modified shared parent", err)
	}
}

func TestProfileRetirementProtectedRefusesCustomChatCompatibleKeepDefinition(t *testing.T) {
	for _, mutation := range []string{"other-id", "unapproved-revision"} {
		t.Run(mutation, func(t *testing.T) {
			svc, st, _, host, p, req := protectedRetirementFixture(t, "system", "")
			_, verified := logicalProvisionHost(t)
			custom := *verified.Definition
			if mutation == "other-id" {
				custom.DefinitionID = "def:fixture-custom-chat"
			} else {
				custom.Revision = "custom-compatible"
			}
			digest, err := agentdef.Digest(&custom)
			if err != nil {
				t.Fatal(err)
			}
			pin := DefinitionRef{DefinitionID: custom.DefinitionID, Revision: custom.Revision, SemanticDigest: digest}
			host.Resolver = logicalProvisionResolver(func(context.Context, DefinitionRef) (VerifiedDefinition, error) {
				return VerifiedDefinition{Ref: pin, Definition: &custom}, nil
			})
			host.Models = ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
				return ModelSelection{Provider: "fixture-provider", Model: "fixture-model"}, nil
			})
			// This is a valid custom native-chat profile, even when named General chat.
			customKeep, err := svc.ProvisionGeneralChat(t.Context(), host, ProvisionGeneralChatRequest{Name: "General chat", Slug: "custom-compatible-chat", DefinitionRef: pin})
			if err != nil {
				t.Fatal("compatible fixture did not provision", err)
			}
			req.KeepProfileID = customKeep.Agent.Profile.ID
			req.KeepRevision = customKeep.Agent.Revision
			if _, checkErr := svc.ExportProtectedProfile(t.Context(), host, p.ID, req); !errors.Is(checkErr, store.ErrProtectedRetirementAuthority) {
				t.Fatal("arbitrary compatible definition accepted as keep", checkErr)
			}
			if _, checkErr := st.GetAgent(t.Context(), p.ID); checkErr != nil {
				t.Fatal("keep refusal changed protected profile", checkErr)
			}
		})
	}
}
