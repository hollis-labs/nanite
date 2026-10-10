package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func protectedResidualServiceKeep(t *testing.T, st *store.Store) ProtectedProfileRetirementRequest {
	t.Helper()
	verified, verifiedErr := EmbeddedDefinition()
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	cfg, verifiedErr := MapChatDefinition(verified)
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	keep := &store.AgentProfile{Name: "General Chat", Slug: "private-general-chat", SystemPrompt: cfg.Instructions}
	if err := storetest.HistoricalProfile(t.Context(), st, keep); err != nil {
		t.Fatal(err)
	}
	settings, verifiedErr := json.Marshal(map[string]any{"provisioned_definition_ref": verified.Ref})
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET settings=? WHERE id=?`, string(settings), keep.ID); err != nil {
		t.Fatal(err)
	}
	keep, verifiedErr = st.GetHistoricalAgentProfile(t.Context(), keep.ID)
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	return ProtectedProfileRetirementRequest{GeneralChatID: keep.ID, GeneralChatRevision: keep.Revision, Actor: "untrusted audit label", Reason: "private source control"}
}

func protectedResidualServiceFixture(t *testing.T) (*AgentConfigService, *store.Store, string, *store.AgentProfile, ProtectedProfileRetirementRequest) {
	t.Helper()
	svc, st, root := newAgentConfigTestService(t)
	req := protectedResidualServiceKeep(t, st)
	target := &store.AgentProfile{Name: "Protected", Slug: "private-protected", Source: "internal", SystemPrompt: "private instructions"}
	if err := storetest.HistoricalProfile(t.Context(), st, target); err != nil {
		t.Fatal(err)
	}
	return svc, st, root, target, req
}

func TestProtectedRetirementResidualRequiresEmbeddedKeep(t *testing.T) {
	for _, kind := range []string{"missing", "custom-compatible", "keep-target"} {
		t.Run(kind, func(t *testing.T) {
			svc, st, _, target, req := protectedResidualServiceFixture(t)
			switch kind {
			case "missing":
				req.GeneralChatID = ""
			case "custom-compatible":
				source := strings.Replace(string(embeddedChatDefinition), "def:nanite-default", "def:custom-compatible", 1)
				definition, definitionErr := agentdef.Parse([]byte(source), agentpolicy.Option())
				if definitionErr != nil {
					t.Fatal(definitionErr)
				}
				digest, definitionErr := agentdef.Digest(definition)
				if definitionErr != nil {
					t.Fatal(definitionErr)
				}
				pin := DefinitionRef{DefinitionID: definition.DefinitionID, Revision: definition.Revision, SemanticDigest: digest}
				if _, err := MapChatDefinition(VerifiedDefinition{Ref: pin, Definition: definition}); err != nil {
					t.Fatal("fixture is not chat compatible", err)
				}
				settings, definitionErr := json.Marshal(map[string]any{"provisioned_definition_ref": pin})
				if definitionErr != nil {
					t.Fatal(definitionErr)
				}
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET settings=? WHERE id=?`, string(settings), req.GeneralChatID); err != nil {
					t.Fatal(err)
				}
				keep, definitionErr := st.GetHistoricalAgentProfile(t.Context(), req.GeneralChatID)
				if definitionErr != nil {
					t.Fatal(definitionErr)
				}
				req.GeneralChatRevision = keep.Revision
			case "keep-target":
				target.ID = req.GeneralChatID
			}
			receipt, err := svc.ExportProtectedProfile(t.Context(), target.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
			if !errors.Is(err, store.ErrProfileRetirementKeep) || result.Retired {
				t.Fatal("invalid keep accepted", result, err)
			}
			if _, err := st.GetHistoricalAgentProfile(t.Context(), target.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProtectedRetirementResidualActivityRefusal(t *testing.T) {
	for _, state := range []string{"starting", "active", "paused", "start_requested", "stop_requested", "resume_requested", "runtime-running", "runtime-orphaned", "subagent-requested", "subagent-stalled"} {
		t.Run(state, func(t *testing.T) {
			svc, st, _, target, req := protectedResidualServiceFixture(t)
			switch {
			case strings.HasPrefix(state, "runtime-"):
				_, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_runtime(id,agent_profile,state,started_at) VALUES('runtime',?,?,datetime('now'))`, target.Slug, strings.TrimPrefix(state, "runtime-"))
				if err != nil {
					t.Fatal(err)
				}
			case strings.HasPrefix(state, "subagent-"):
				_, err := st.DB.ExecContext(t.Context(), `INSERT INTO subagent_runs(id,parent_session_id,role,prompt,status,created_at) VALUES('subagent','parent',?,'historical request',?,datetime('now'))`, target.Slug, strings.TrimPrefix(state, "subagent-"))
				if err != nil {
					t.Fatal(err)
				}
			default:
				_, err := st.DB.ExecContext(t.Context(), `INSERT INTO durable_agent_instances(id,name,slug,profile_id,status) VALUES('instance','Historical','historical-instance',?,?)`, target.ID, state)
				if err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := svc.ExportProtectedProfile(t.Context(), target.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
			if !errors.Is(err, store.ErrProfileRetirementActive) || result.Retired {
				t.Fatal("activity accepted", state, result, err)
			}
			if _, err := st.GetHistoricalAgentProfile(t.Context(), target.ID); err != nil {
				t.Fatal(err)
			}
			if ledger, err := st.GetRetiredAgentProfile(t.Context(), target.ID); err != nil || ledger != nil {
				t.Fatal("activity refusal wrote ledger", ledger, err)
			}
		})
	}
}

func TestProtectedRetirementResidualReceiptRecovery(t *testing.T) {
	for _, failure := range []string{"missing", "partial", "foreign", "symlink", "wrong-export"} {
		t.Run(failure, func(t *testing.T) {
			svc, st, root, target, req := protectedResidualServiceFixture(t)
			receipt, receiptErr := svc.ExportProtectedProfile(t.Context(), target.ID)
			if receiptErr != nil {
				t.Fatal(receiptErr)
			}
			// Simulate a host exit precisely after the real retirement transaction,
			// before final receipt publication. No row recreation/second deletion.
			keep, receiptErr := protectedRetirementKeep(req)
			if receiptErr != nil {
				t.Fatal(receiptErr)
			}
			if err := st.RetireExportedProfileWithAudit(t.Context(), target.ID, receipt.Digest, store.RetireAgentProfileAudit{ExportID: receipt.ExportID, Keep: keep}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_second_delete BEFORE DELETE ON agent_profiles BEGIN SELECT RAISE(ABORT,'second deletion forbidden'); END`); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "profile-retirements", receipt.ExportID+".retired.json")
			switch failure {
			case "partial":
				if err := os.WriteFile(path, []byte(`{"export_id":`), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				if err := os.WriteFile(path, []byte(`{"export_id":"foreign"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "outside"), path); err != nil {
					t.Fatal(err)
				}
			case "wrong-export":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE retired_agent_profiles SET export_id='different' WHERE id=?`, target.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			reopened := newConfigTestStoreAt(t, filepath.Join(root, "agent-config.db"))
			svc.store = reopened
			result, receiptErr := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
			if failure == "missing" || failure == "partial" {
				if receiptErr != nil || !result.Retired || !result.ReceiptPersisted {
					t.Fatal("lost committed outcome", result, receiptErr)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("receipt lost private mode", err)
				}
				repeated, err := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
				if err != nil || repeated != result {
					t.Fatal("retry changed receipt", repeated, err)
				}
			} else if !errors.Is(receiptErr, store.ErrProfileRetirementConflict) {
				t.Fatal("foreign completion accepted", result, receiptErr)
			}
			if _, err := reopened.GetHistoricalAgentProfile(t.Context(), req.GeneralChatID); err != nil {
				t.Fatal("keep lost", err)
			}
			if _, err := reopened.GetHistoricalAgentProfile(t.Context(), target.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
		})
	}
}

func TestProtectedRetirementResidualFinalReceiptFailureRecovers(t *testing.T) {
	svc, st, root, target, req := protectedResidualServiceFixture(t)
	notifications := 0

	receipt, receiptErr := svc.ExportProtectedProfile(t.Context(), target.ID)
	if receiptErr != nil {
		t.Fatal(receiptErr)
	}
	final := filepath.Join(root, "profile-retirements", receipt.ExportID+".retired.json")
	// The notification runs after commit and before final publication, so
	// this obstruction exercises the real postcommit failure rather than a
	// preflight refusal.
	svc.notify = func(_, _ string) {
		notifications++
		if err := os.Mkdir(final, 0700); err != nil {
			t.Fatal(err)
		}
	}
	result, receiptErr := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
	if receiptErr == nil || !result.Retired || result.ReceiptPersisted {
		t.Fatal("postcommit failure hid outcome", result, receiptErr)
	}
	if row, err := st.GetRetiredAgentProfile(t.Context(), target.ID); err != nil || row == nil {
		t.Fatal("commit audit missing", row, err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER no_recovery_delete BEFORE DELETE ON agent_profiles BEGIN SELECT RAISE(ABORT,'no second deletion'); END`); err != nil {
		t.Fatal(err)
	}
	// Remove only the synthetic obstruction; no source path performs cleanup.
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	recovered, receiptErr := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
	if receiptErr != nil || !recovered.Retired || !recovered.ReceiptPersisted || notifications != 1 {
		t.Fatal("recovery replayed effects", recovered, notifications, receiptErr)
	}
}

func TestProtectedRetirementResidualCompletionNeedsLedger(t *testing.T) {
	svc, st, root, target, req := protectedResidualServiceFixture(t)
	receipt, receiptErr := svc.ExportProtectedProfile(t.Context(), target.ID)
	if receiptErr != nil {
		t.Fatal(receiptErr)
	}
	forged := receipt
	forged.Retired = true
	data, receiptErr := json.Marshal(forged)
	if receiptErr != nil {
		t.Fatal(receiptErr)
	}
	final := filepath.Join(root, "profile-retirements", receipt.ExportID+".retired.json")
	if err := os.WriteFile(final, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, receiptErr := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
	if !errors.Is(receiptErr, store.ErrProfileRetirementConflict) || result.Retired {
		t.Fatal("completion without ledger accepted", result, receiptErr)
	}
	if _, err := st.GetHistoricalAgentProfile(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedRetirementResidualLinkedActivity(t *testing.T) {
	for _, kind := range []string{"session-runtime", "team-active", "workflow-waiting", "loop-waiting", "intent-unknown", "terminal"} {
		t.Run(kind, func(t *testing.T) {
			svc, st, _, target, req := protectedResidualServiceFixture(t)
			sess := &store.Session{ID: "historical-session", Title: "Historical", Status: "paused"}
			if err := st.CreateSession(t.Context(), sess); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{
				`INSERT INTO workflow_runs(id,status,started_at) VALUES('workflow','completed',datetime('now'))`,
				`INSERT INTO team_run_members(id,workflow_run_id,slot_name,agent_id,session_id,status) VALUES('member','workflow','slot',?,'historical-session','stopped')`,
			} {
				var err error
				if strings.Contains(query, "?") {
					_, err = st.DB.ExecContext(t.Context(), query, target.ID)
				} else {
					_, err = st.DB.ExecContext(t.Context(), query)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "session-runtime":
				if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_runtime(id,agent_profile,state,parent_session_id,started_at) VALUES('different-runtime','another-profile','running','historical-session',datetime('now'))`); err != nil {
					t.Fatal(err)
				}
			case "team-active":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE team_run_members SET status='active' WHERE id='member'`); err != nil {
					t.Fatal(err)
				}
			case "workflow-waiting":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET status='waiting_on_gate' WHERE id='workflow'`); err != nil {
					t.Fatal(err)
				}
			case "loop-waiting":
				for _, query := range []string{
					`INSERT INTO goals(id,intent) VALUES('goal','Historical goal')`,
					`INSERT INTO loop_runs(id,goal_id,definition_name,status) VALUES('loop','goal','historical','waiting_on_escalation')`,
					`INSERT INTO loop_run_iterations(id,loop_run_id,iteration_number,workflow_run_id) VALUES('iteration','loop',1,'workflow')`,
				} {
					if _, err := st.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
			case "terminal":
				for _, query := range []string{
					`INSERT INTO teams(id,name) VALUES('team','Historical terminal team')`,
					`INSERT INTO team_run_launches(idempotency_key,team_id,request_digest,planning_json,workflow_run_id,status,created_at,updated_at) VALUES('completed-launch','team','private-digest','{}','workflow','routing_ready',datetime('now'),datetime('now'))`,
				} {
					if _, err := st.DB.ExecContext(t.Context(), query); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO team_run_member_intents(idempotency_key,ordinal,member_id,team_id,slot_name,agent_id,session_id,provisioning_kind,status,created_at,updated_at) VALUES('completed-launch',0,'terminal-member','team','slot',?,'historical-session','fresh','provisioned',datetime('now'),datetime('now'))`, target.ID); err != nil {
					t.Fatal(err)
				}
			case "intent-unknown":
				if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO teams(id,name) VALUES('team','Historical team')`); err != nil {
					t.Fatal(err)
				}
				if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO team_run_member_intents(idempotency_key,ordinal,member_id,team_id,slot_name,agent_id,session_id,provisioning_kind,created_at,updated_at) VALUES('unknown-launch',0,'planned-member','team','slot',?,'uncreated-session','fresh',datetime('now'),datetime('now'))`, target.ID); err != nil {
					t.Fatal(err)
				}
			}
			receipt, receiptErr := svc.ExportProtectedProfile(t.Context(), target.ID)
			if receiptErr != nil {
				t.Fatal(receiptErr)
			}
			result, receiptErr := svc.RetireProtectedProfile(t.Context(), target.ID, receipt.ExportID, receipt.Digest, req)
			if kind == "terminal" {
				if receiptErr != nil || !result.Retired {
					t.Fatal("terminal historical graph could not retire", result, receiptErr)
				}
				var retained string
				if err := st.DB.QueryRowContext(t.Context(), `SELECT status FROM workflow_runs WHERE id='workflow'`).Scan(&retained); err != nil || retained != "completed" {
					t.Fatal("parent workflow changed", err)
				}
			} else {
				if !errors.Is(receiptErr, store.ErrProfileRetirementActive) || result.Retired {
					t.Fatal("linked activity accepted", kind, result, receiptErr)
				}
				if _, err := st.GetHistoricalAgentProfile(t.Context(), target.ID); err != nil {
					t.Fatal(err)
				}
				if row, err := st.GetRetiredAgentProfile(t.Context(), target.ID); err != nil || row != nil {
					t.Fatal("refusal wrote ledger", row, err)
				}
			}
		})
	}
}
