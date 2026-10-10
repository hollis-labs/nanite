package store_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func protectedResidualKeepFixture(t *testing.T, st *store.Store) store.ProtectedRetirementKeep {
	t.Helper()
	verified, verifiedErr := service.EmbeddedDefinition()
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	cfg, verifiedErr := service.MapChatDefinition(verified)
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	p := &store.AgentProfile{Name: "General Chat", Slug: "historical-general-chat", SystemPrompt: cfg.Instructions}
	if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	settings, verifiedErr := json.Marshal(map[string]any{"provisioned_definition_ref": verified.Ref})
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET settings=? WHERE id=?`, string(settings), p.ID); err != nil {
		t.Fatal(err)
	}
	p, verifiedErr = st.GetHistoricalAgentProfile(t.Context(), p.ID)
	if verifiedErr != nil {
		t.Fatal(verifiedErr)
	}
	return store.ProtectedRetirementKeep{ID: p.ID, Revision: p.Revision, DefinitionRef: verified.Ref.MeshRef(), SystemPrompt: cfg.Instructions}
}

func protectedResidualStoreFixture(t *testing.T) (*store.Store, *store.AgentProfile, store.RetireAgentProfileAudit) {
	t.Helper()
	st, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "protected.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(t.Context()) })
	keep := protectedResidualKeepFixture(t, st)
	p := &store.AgentProfile{Name: "Protected", Slug: "historical-protected", Source: "internal", SystemPrompt: "private protected body"}
	if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	return st, p, store.RetireAgentProfileAudit{ExportID: "private-export", Keep: keep, Actor: "audit label", Reason: "private source test"}
}

func protectedResidualDigest(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	snapshot, err := st.ExportProtectedProfileRetirement(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestProtectedRetirementResidualParentsAndEditableFormat(t *testing.T) {
	for _, mutation := range []string{"roles", "consumers", "models"} {
		t.Run(mutation, func(t *testing.T) {
			st, p, audit := protectedResidualStoreFixture(t)
			for _, q := range []string{
				`INSERT INTO roles(id,slug,name,system_prompt,created_at,updated_at) VALUES('role','private-role','Private role','parent body',datetime('now'),datetime('now'))`,
				`INSERT INTO consumers(id,slug,name) VALUES('consumer','private-consumer','Private consumer')`,
				`INSERT INTO providers(id,name,provider_type) VALUES('provider','Private provider','private')`,
				`INSERT INTO models(id,provider_id,model_id,display_name) VALUES('model','provider','private-model','Private model')`,
			} {
				if _, err := st.DB.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET role_id='role',consumer_id='consumer',model_id='model' WHERE id=?`, p.ID); err != nil {
				t.Fatal(err)
			}
			snapshot, snapshotErr := st.ExportProtectedProfileRetirement(t.Context(), p.ID)
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if snapshot.SchemaVersion != store.ProtectedProfileExportSchemaVersion {
				t.Fatal("protected format lost")
			}
			for _, table := range []string{"roles", "consumers", "models"} {
				if len(snapshot.Tables[table].Rows) != 1 {
					t.Fatal("missing linked parent", table)
				}
			}
			// Read-only parent snapshots do not alter editable format 2.
			if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET source='user' WHERE id=?`, p.ID); err != nil {
				t.Fatal(err)
			}
			editable, snapshotErr := st.ExportProfileRetirement(t.Context(), p.ID)
			if snapshotErr != nil || editable.SchemaVersion != 2 {
				t.Fatal("editable format changed", snapshotErr)
			}
			if _, ok := editable.Tables["roles"]; ok {
				t.Fatal("editable export gained parent records")
			}
			digest := protectedResidualDigest(t, st, p.ID)
			queries := map[string]string{"roles": `UPDATE roles SET system_prompt=CAST(x'ff00' AS TEXT) WHERE id='role'`, "consumers": `UPDATE consumers SET name='Changed consumer' WHERE id='consumer'`, "models": `UPDATE models SET display_name='Changed model' WHERE id='model'`}
			if _, err := st.DB.ExecContext(t.Context(), queries[mutation]); err != nil {
				t.Fatal(err)
			}
			if err := st.RetireExportedProfileWithAudit(t.Context(), p.ID, digest, audit); !errors.Is(err, store.ErrProfileRetirementConflict) {
				t.Fatal("parent change passed CAS", err)
			}
			if _, err := st.GetHistoricalAgentProfile(t.Context(), p.ID); err != nil {
				t.Fatal(err)
			}
			if ledger, err := st.GetRetiredAgentProfile(t.Context(), p.ID); err != nil || ledger != nil {
				t.Fatal("conflict wrote tombstone", ledger, err)
			}
			fresh, snapshotErr := st.ExportProtectedProfileRetirement(t.Context(), p.ID)
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if mutation == "roles" {
				var cells []struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				}
				if err := json.Unmarshal(fresh.Tables["roles"].Rows[0], &cells); err != nil {
					t.Fatal(err)
				}
				for index, column := range fresh.Tables["roles"].Columns {
					if column == "system_prompt" {
						decoded, err := base64.StdEncoding.DecodeString(cells[index].Value)
						if err != nil || cells[index].Type != "text" || !bytes.Equal(decoded, []byte{0xff, 0}) {
							t.Fatal("parent text bytes lost", cells[index], err)
						}
					}
				}
			}
			newDigest, snapshotErr := fresh.Digest()
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if err := st.RetireExportedProfileWithAudit(t.Context(), p.ID, newDigest, audit); err != nil {
				t.Fatal(err)
			}
			var parentCount int
			if err := st.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM `+mutation).Scan(&parentCount); err != nil || parentCount < 1 {
				t.Fatal("retirement changed parent", err)
			}
		})
	}
}

func TestProtectedRetirementResidualKeepCASAndRollback(t *testing.T) {
	for _, mutation := range []string{"missing", "revision", "pin", "prompt", "target", "rollback", "effect-keep"} {
		t.Run(mutation, func(t *testing.T) {
			st, p, audit := protectedResidualStoreFixture(t)
			digest := protectedResidualDigest(t, st, p.ID)
			switch mutation {
			case "missing":
				audit.Keep.ID = "absent"
			case "revision":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET description='changed' WHERE id=?`, audit.Keep.ID); err != nil {
					t.Fatal(err)
				}
			case "pin":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET settings='{}' WHERE id=?`, audit.Keep.ID); err != nil {
					t.Fatal(err)
				}
			case "prompt":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET system_prompt='changed' WHERE id=?`, audit.Keep.ID); err != nil {
					t.Fatal(err)
				}
			case "target":
				p.ID = audit.Keep.ID
				digest = protectedResidualDigest(t, st, p.ID)
			case "effect-keep":
				if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER private_keep_effect AFTER INSERT ON retired_agent_profiles BEGIN UPDATE agent_profiles SET description='changed by cleanup' WHERE slug='historical-general-chat'; END`); err != nil {
					t.Fatal(err)
				}
			case "rollback":
				if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER private_delete_failure BEFORE DELETE ON agent_profiles BEGIN SELECT RAISE(ABORT,'private failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := st.RetireExportedProfileWithAudit(t.Context(), p.ID, digest, audit)
			if mutation == "rollback" {
				if err == nil {
					t.Fatal("fault accepted")
				}
			} else if !errors.Is(err, store.ErrProfileRetirementKeep) {
				t.Fatal("keep refused incorrectly", err)
			}
			if _, err := st.GetHistoricalAgentProfile(t.Context(), p.ID); err != nil {
				t.Fatal("refusal lost profile", err)
			}
			if row, err := st.GetRetiredAgentProfile(t.Context(), p.ID); err != nil || row != nil {
				t.Fatal("refusal wrote ledger", row, err)
			}
		})
	}
}

func TestProtectedRetirementResidualCompetingCAS(t *testing.T) {
	st, p, audit := protectedResidualStoreFixture(t)
	digest := protectedResidualDigest(t, st, p.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- st.RetireExportedProfileWithAudit(t.Context(), p.ID, digest, audit)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("competing retirement did not commit exactly once", successes)
	}
	if _, err := st.GetHistoricalAgentProfile(t.Context(), p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := st.GetHistoricalAgentProfile(t.Context(), audit.Keep.ID); err != nil {
		t.Fatal("keep lost", err)
	}
	if row, err := st.GetRetiredAgentProfile(t.Context(), p.ID); err != nil || row == nil || row.Digest != digest {
		t.Fatal("committed audit missing", row, err)
	}
}

func TestProtectedRetirementResidualCompetingKeepCAS(t *testing.T) {
	st, target, audit := protectedResidualStoreFixture(t)
	other, openErr := storetest.New(t, t.Context(), st.DBPath(t.Context()))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = other.Close(t.Context()) })
	for _, query := range []string{
		`CREATE TABLE private_retirement_order(sequence INTEGER PRIMARY KEY AUTOINCREMENT,event TEXT NOT NULL)`,
		`CREATE TRIGGER private_keep_update AFTER UPDATE OF description ON agent_profiles WHEN NEW.slug='historical-general-chat' BEGIN INSERT INTO private_retirement_order(event) VALUES('keep-update'); END`,
		`CREATE TRIGGER private_retire_order AFTER INSERT ON retired_agent_profiles BEGIN INSERT INTO private_retirement_order(event) VALUES('retirement'); END`,
	} {
		if _, err := st.DB.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	digest := protectedResidualDigest(t, st, target.ID)
	start := make(chan struct{})
	retirement := make(chan error, 1)
	update := make(chan error, 1)
	go func() {
		<-start
		retirement <- st.RetireExportedProfileWithAudit(t.Context(), target.ID, digest, audit)
	}()
	go func() {
		<-start
		_, err := other.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET description='competing edit' WHERE id=?`, audit.Keep.ID)
		update <- err
	}()
	close(start)
	retiredErr := <-retirement
	updateErr := <-update
	if updateErr != nil {
		t.Fatal("competing historical writer failed", updateErr)
	}
	rows, queryErr := st.DB.QueryContext(t.Context(), `SELECT event FROM private_retirement_order ORDER BY sequence`)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	var events []string
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if retiredErr == nil {
		if len(events) != 2 || events[0] != "retirement" || events[1] != "keep-update" {
			t.Fatal("retirement committed after stale keep change", events)
		}
	} else {
		if !errors.Is(retiredErr, store.ErrProfileRetirementKeep) {
			t.Fatal("unexpected competing refusal", retiredErr)
		}
		if len(events) != 1 || events[0] != "keep-update" {
			t.Fatal("refused retirement had effects", events)
		}
		if _, err := st.GetHistoricalAgentProfile(t.Context(), target.ID); err != nil {
			t.Fatal(err)
		}
		if ledger, err := st.GetRetiredAgentProfile(t.Context(), target.ID); err != nil || ledger != nil {
			t.Fatal("stale keep wrote ledger", ledger, err)
		}
	}
}
