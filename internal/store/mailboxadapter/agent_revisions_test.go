package mailboxadapter

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestAgentRevisionsRegistryWrites(t *testing.T) {
	st := newTestStore(t)
	retained := &store.AgentProfile{ID: "CLI.Host/History", Name: "Historical CLI", Slug: "cli-host-history", Source: "auto", SystemPrompt: "Retained prompt"}
	// This raw historical row simulates retained mail registration, not enrollment.
	if err := storetest.HistoricalProfile(t.Context(), st, retained); err != nil {
		t.Fatal(err)
	}
	before := registryBoundarySnapshot(t, st)
	registry := &messagingAgentRegistry{store: st}
	for _, claimedID := range []string{retained.ID, "msg://agent/claimed-host/agent", "01846846-13b6-4fcd-998a-48c9c8607cda"} {
		for _, kind := range []string{"cli", "internal", "external"} {
			if err := registry.RegisterAgent(t.Context(), claimedID, kind); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("RegisterAgent(%q,%q) = %v; want verified issuer refusal", claimedID, kind, err)
			}
			exists, err := registry.AgentExists(t.Context(), claimedID)
			if err != nil || exists {
				t.Fatalf("unbound identity became visible: %q, %v, %v", claimedID, exists, err)
			}
			if after := registryBoundarySnapshot(t, st); !reflect.DeepEqual(before, after) {
				t.Fatalf("registration changed history or authority: %#v -> %#v", before, after)
			}
		}
	}
	revision, err := st.GetAgentRevision(t.Context(), retained.ID, retained.Revision)
	if err != nil || revision.Profile.ID != retained.ID || revision.Profile.Source != "auto" {
		t.Fatalf("retained revision = %+v, %v", revision, err)
	}
}

// registryBoundarySnapshot captures retained history and both authority partitions in the private DB.
func registryBoundarySnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, query := range []struct{ table, sql string }{
		{"agent_profiles", "SELECT * FROM agent_profiles ORDER BY rowid"},
		{"agent_profile_revisions", "SELECT * FROM agent_profile_revisions ORDER BY rowid"},
		{"agent_procedures", "SELECT * FROM agent_procedures ORDER BY rowid"},
		{"agent_reflexes", "SELECT * FROM agent_reflexes ORDER BY rowid"},
		{"agent_tools", "SELECT * FROM agent_tools ORDER BY rowid"},
		{"agent_known_skills", "SELECT * FROM agent_known_skills ORDER BY rowid"},
		{"agent_definitions", "SELECT * FROM agent_definitions ORDER BY rowid"},
		{"agent_definition_resources", "SELECT * FROM agent_definition_resources ORDER BY rowid"},
		{"agent_definition_resource_refs", "SELECT * FROM agent_definition_resource_refs ORDER BY rowid"},
		{"agent_host_settings", "SELECT * FROM agent_host_settings ORDER BY rowid"},
		{"agent_actor_bindings", "SELECT * FROM agent_actor_bindings ORDER BY rowid"},
		{"actor_granted_tools", "SELECT * FROM actor_granted_tools ORDER BY rowid"},
		{"actor_known_skills", "SELECT * FROM actor_known_skills ORDER BY rowid"},
	} {
		rows, err := st.DB.QueryContext(t.Context(), query.sql)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			cells := make([]any, len(columns))
			refs := make([]any, len(columns))
			for i := range cells {
				refs[i] = &cells[i]
			}
			if err := rows.Scan(refs...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for i, cell := range cells {
				if raw, ok := cell.([]byte); ok {
					cells[i] = append([]byte(nil), raw...)
				}
			}
			out[query.table] = append(out[query.table], cells)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
