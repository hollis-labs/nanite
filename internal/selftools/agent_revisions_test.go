package selftools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestAgentRevisionsSelfToolWrites(t *testing.T) {
	st := newSelfTools(t)
	db := fixtureStore(st)
	profile := retainedSelfToolProfile(t, db, "history-tool", "user")
	before := retainedSelfToolState(t, db)
	assertRetiredSelfTool(t, st, "agent_create", map[string]any{"name": "History Tool", "slug": profile.Slug, "system_prompt": "replacement prompt"})
	assertRetiredSelfTool(t, st, "agent_update", map[string]any{"id": profile.ID, "system_prompt": "replacement prompt"})
	assertRetainedSelfToolState(t, db, before)
	row, err := db.GetAgentRevision(t.Context(), profile.ID, "retained-revision-"+profile.ID)
	if err != nil || row.Profile.SystemPrompt != "PRIVATE HISTORICAL PROMPT" {
		t.Fatalf("retained export revision: %+v,%v", row, err)
	}
}

// Retained legacy rows are fixture history only. They are never selected as
// runtime profiles or converted to verified actors through these self tools.
func retainedSelfToolProfile(t *testing.T, db *store.Store, slug, source string) *store.AgentProfile {
	t.Helper()
	profile := &store.AgentProfile{Name: "Retained profile", Slug: slug, Source: source, SystemPrompt: "PRIVATE HISTORICAL PROMPT"}
	if err := storetest.HistoricalProfile(t.Context(), db, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO agent_procedures(agent_id,name,body) VALUES(?,?,?)`, profile.ID, "boot", "PRIVATE HISTORICAL PROCEDURE"); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO agent_profile_revisions(id,agent_id,operation,profile_json) VALUES(?,?,?,?)`, "retained-revision-"+profile.ID, profile.ID, "baseline", string(wire)); err != nil {
		t.Fatal(err)
	}
	return profile
}

func assertRetiredSelfTool(t *testing.T, st *SelfToolsTransport, tool string, args map[string]any) {
	t.Helper()
	result, err := st.CallTool(t.Context(), tool, args)
	if err != nil || result == nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("retired %s result=%+v err=%v", tool, result, err)
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &wire); err != nil {
		t.Fatal(err)
	}
	message, ok := wire["message"].(string)
	if wire["code"] != "unavailable" || !ok || message != store.ErrImmutableAgentProfile.Error() || len(wire) != 2 || strings.Contains(result.Content[0].Text, "PRIVATE HISTORICAL") {
		t.Fatalf("retired %s missing typed safe guidance: %s", tool, result.Content[0].Text)
	}
}

func retainedSelfToolState(t *testing.T, db *store.Store) map[string][][]any {
	t.Helper()
	tables, err := db.DB.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'agent_%' OR name LIKE 'actor_%' OR name='session_actor_bindings' OR name='sessions') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	state := make(map[string][][]any, len(names))
	for _, name := range names {
		rows, err := db.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, name))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		state[name] = make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				if raw, ok := v.([]byte); ok {
					values[i] = string(raw)
				}
			}
			state[name] = append(state[name], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return state
}
func assertRetainedSelfToolState(t *testing.T, db *store.Store, before map[string][][]any) {
	t.Helper()
	after := retainedSelfToolState(t, db)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("refused self tool changed %s", table)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refused self tool changed graph tables")
	}
}
