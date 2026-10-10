package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestImmutableAgentInstallDeclarationsCannotInitializeOrReplayHistoricalGrants(t *testing.T) {
	st := newKnownToolsTestStore(t)
	p := immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Imported history", Slug: "retained-install", SystemPrompt: "Retained prompt", Source: "import"})
	toolID, err := st.UpsertKnownTool(t.Context(), "declared_tool", "mcp", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET tools='["*"]',role_tools='["declared_tool"]' WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO agent_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,'explicit','retained-created')`, p.ID, toolID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(t.Context(), `INSERT INTO agent_tools_legacy_backfill(agent_id,created_at) VALUES(?,'retained-created')`, p.ID); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_tools ORDER BY agent_id,tool_id`, `SELECT * FROM agent_tools_legacy_backfill ORDER BY agent_id`, `SELECT * FROM agent_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM agent_procedures ORDER BY agent_id,name`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`, `SELECT * FROM actor_known_skills ORDER BY agent_id,skill_name`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	def := &agent.Definition{Slug: "retained-install", Tools: []string{"*"}}
	for _, created := range []bool{true, false} {
		if err = SeedImportedAgentChildren(st)(t.Context(), p.ID, def, created); !errors.Is(err, store.ErrImmutableAgentProfile) {
			t.Fatalf("legacy child seeding created=%v: %v", created, err)
		}
	}
	for _, id := range []string{p.ID, "missing-profile"} {
		for _, grants := range [][]store.InitialAgentToolGrant{nil, {{ToolID: toolID, GrantedVia: "role_seed"}}, {{ToolID: "missing-tool", GrantedVia: "explicit"}}} {
			if err = st.InitializeAgentToolGrants(t.Context(), id, grants); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("unverified initial grants: %v", err)
			}
		}
		if err = st.GrantAgentTool(t.Context(), id, toolID, "explicit"); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("unverified grant: %v", err)
		}
		marked, guardErr := st.HasLegacyToolsBackfillRun(t.Context(), id)
		if marked || !errors.Is(guardErr, store.ErrVerifiedActorRequired) {
			t.Fatalf("historical marker became actor authority: %v %v", marked, guardErr)
		}
	}
	// Boot backfill sees the fresh partition only; old declarations and markers
	// remain data and cannot populate the fresh grant relation.
	n, err := BackfillAgentToolsFromLegacyColumns(t.Context(), st)
	if !errors.Is(err, store.ErrVerifiedActorRequired) || n != 0 {
		t.Fatalf("backfill replayed old declarations: %d %v", n, err)
	}
	names, err := st.ListAgentToolNames(t.Context(), p.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("historical grant exposed as fresh authority: %v %v", names, err)
	}
	for i, q := range queries {
		immutableConfigUnchanged(t, st, q, before[i])
	}
}

func TestImmutableAgentInstallRealImporterRefusesBeforeChildEffects(t *testing.T) {
	st := newKnownToolsTestStore(t)
	immutableConfigHistoricalFixture(t, st, store.AgentProfile{Name: "Imported history", Slug: "retained-real-import", SystemPrompt: "Private retained prompt", Source: "import"})
	queries := []string{`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM agent_profile_revisions ORDER BY sequence`, `SELECT * FROM agent_tools ORDER BY agent_id,tool_id`, `SELECT * FROM agent_tools_legacy_backfill ORDER BY agent_id`, `SELECT * FROM agent_procedures ORDER BY agent_id,name`, `SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`, `SELECT * FROM actor_granted_tools ORDER BY agent_id,tool_id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = immutableConfigSnapshot(t, st, q)
	}
	for _, slug := range []string{"new-real-import", "retained-real-import"} {
		for _, declarations := range []string{"", "tools: [declared_tool]\nroleTools: [dev_read]\nprocedures:\n  - name: triage\n    body: Never seeded.\n"} {
			body := "---\nname: Refused install\nslug: " + slug + "\n" + declarations + "---\nNever installed prompt.\n"
			path := filepath.Join(t.TempDir(), "agent.md")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			seedCalled := false
			importer := &agentimport.Importer{Store: st, SeedChildren: func(context.Context, string, *agent.Definition, bool) error {
				seedCalled = true
				return errors.New("child seeder must not run")
			}}
			result, err := importer.Import(t.Context(), agentimport.Source{Path: path})
			if !errors.Is(err, store.ErrImmutableAgentProfile) || seedCalled || importer.State() != agentimport.StateFailed {
				t.Fatalf("legacy install did not stop before child effects: %+v %v seeded=%v state=%s", result, err, seedCalled, importer.State())
			}
			data, readErr := os.ReadFile(path) //nolint:gosec // private fixture created above
			if readErr != nil || string(data) != body {
				t.Fatalf("refused importer changed source: %q %v", data, readErr)
			}
			for i, q := range queries {
				immutableConfigUnchanged(t, st, q, before[i])
			}
		}
	}
}

// Keep this tool-selection diagnostic control unchanged except for supplying
// its old profile explicitly as private history rather than creating it live.
func TestZeroAgentGrants_VisibleAtInfoAndDeniesTools(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	row := &store.AgentProfile{Name: "Empty", Slug: "empty"}
	if operationErr := storetest.HistoricalProfile(ctx, st, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(original)
	tools := filterToolsByAgentTools(ctx, st, row.ID, []llmtypes.ToolDefinition{{Name: "write_tool"}})
	if len(tools) != 0 {
		t.Fatal("zero grants allowed tool")
	}
	if !strings.Contains(logs.String(), "zero agent_tools grants") || !strings.Contains(logs.String(), "level=INFO") {
		t.Fatalf("missing visible zero grant diagnostic: %s", logs.String())
	}
}
