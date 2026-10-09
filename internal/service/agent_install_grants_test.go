package service

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/store"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestAgentInstallGrants_DeclaredToolsAndSyncPreservesRevocation(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	SyncKnownTools(ctx, st, []llmtypes.ToolDefinition{{Name: "torque_task_get"}, {Name: "torque_task_list"}, {Name: "dev_read"}, {Name: "diagnostic_status"}}, nil)
	path := filepath.Join(t.TempDir(), "agent.md")
	body := "---\nname: Installed\nslug: installed\ntools: [torque_task_*]\nroleTools: [dev_read]\n---\nPrompt.\n"
	if operationErr := os.WriteFile(path, []byte(body), 0o600); operationErr != nil {
		t.Fatal(operationErr)
	}
	importer := &agentimport.Importer{Store: st, SeedChildren: SeedImportedAgentChildren(st)}
	result, err := importer.Import(ctx, agentimport.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	row := result.Outcomes[0].Profile
	names, err := st.ListAgentToolNames(ctx, row.ID)
	if err != nil || !reflect.DeepEqual(names, []string{"dev_read", "torque_task_get", "torque_task_list"}) {
		t.Fatalf("installed grants %v: %v", names, err)
	}
	for _, name := range names {
		known, lookupErr := st.GetKnownToolByName(ctx, name)
		if lookupErr != nil {
			t.Fatal(lookupErr)
		}
		if operationErr := st.RevokeAgentTool(ctx, row.ID, known.ID); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	if operationErr := os.WriteFile(path, []byte(strings.Replace(body, "Prompt.", "Changed.", 1)), 0o600); operationErr != nil {
		t.Fatal(operationErr)
	}
	if _, operationErr := importer.Import(ctx, agentimport.Source{Path: path}); operationErr != nil {
		t.Fatal(operationErr)
	}
	if _, operationErr := BackfillAgentToolsFromLegacyColumns(ctx, st); operationErr != nil {
		t.Fatal(operationErr)
	}
	names, err = st.ListAgentToolNames(ctx, row.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("sync/boot replayed revocations: %v %v", names, err)
	}
	after, err := st.GetAgentBySlug(ctx, "installed")
	if err != nil || after.SystemPrompt != "Changed." {
		t.Fatalf("content not synced: %+v %v", after, err)
	}
}

func TestAgentInstallGrants_EmptyDeclarationsDoNotGrantCatalog(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	SyncKnownTools(ctx, st, []llmtypes.ToolDefinition{{Name: "powerful_write"}}, nil)
	row, _ := importFixture(t, st, "---\nname: Empty\nslug: imported-reviewer\n---\nPrompt.\n")
	if _, operationErr := BackfillAgentToolsFromLegacyColumns(ctx, st); operationErr != nil {
		t.Fatal(operationErr)
	}
	names, err := st.ListAgentToolNames(ctx, row.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("empty declaration grants %v: %v", names, err)
	}
}

func TestInitialAgentToolGrants_RollsBackMarkerWithFailedGrant(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	row := &store.AgentProfile{Name: "Rollback", Slug: "rollback"}
	if operationErr := st.CreateAgent(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := st.InitializeAgentToolGrants(ctx, row.ID, []store.InitialAgentToolGrant{{ToolID: "missing", GrantedVia: "explicit"}}); operationErr == nil {
		t.Fatal("missing FK grant succeeded")
	}
	marked, err := st.HasLegacyToolsBackfillRun(ctx, row.ID)
	if err != nil || marked {
		t.Fatalf("failed grant committed marker: %v %v", marked, err)
	}
	known, err := st.UpsertKnownTool(ctx, "actual_tool", "mcp", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	if operationErr := st.InitializeAgentToolGrants(ctx, row.ID, []store.InitialAgentToolGrant{{ToolID: known, GrantedVia: "explicit"}}); operationErr != nil {
		t.Fatal(operationErr)
	}
	names, err := st.ListAgentToolNames(ctx, row.ID)
	if err != nil || !reflect.DeepEqual(names, []string{"actual_tool"}) {
		t.Fatalf("recovered grants %v: %v", names, err)
	}
}

func TestZeroAgentGrants_VisibleAtInfoAndDeniesTools(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	row := &store.AgentProfile{Name: "Empty", Slug: "empty"}
	if operationErr := st.CreateAgent(ctx, row); operationErr != nil {
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

func TestAgentInstallGrants_PartialFailureIsVisibleAndSyncDoesNotReplay(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()
	SyncKnownTools(ctx, st, []llmtypes.ToolDefinition{{Name: "declared_tool"}}, nil)
	if _, operationErr := st.DB.ExecContext(ctx, `CREATE TRIGGER fixture_fail_initial_grant BEFORE INSERT ON agent_tools BEGIN SELECT RAISE(ABORT, 'fixture grant failure'); END`); operationErr != nil {
		t.Fatal(operationErr)
	}
	path := filepath.Join(t.TempDir(), "partial.md")
	if operationErr := os.WriteFile(path, []byte("---\nname: Partial\nslug: partial\ntools: [declared_tool]\n---\nPrompt.\n"), 0o600); operationErr != nil {
		t.Fatal(operationErr)
	}
	importer := &agentimport.Importer{Store: st, SeedChildren: SeedImportedAgentChildren(st)}
	if _, operationErr := importer.Import(ctx, agentimport.Source{Path: path}); operationErr == nil || !strings.Contains(operationErr.Error(), "fixture grant failure") {
		t.Fatalf("install lost grant failure: %v", operationErr)
	}
	row, err := st.GetAgentBySlug(ctx, "partial")
	if err != nil {
		t.Fatalf("created profile must remain discoverable: %v", err)
	}
	marked, err := st.HasLegacyToolsBackfillRun(ctx, row.ID)
	if err != nil || marked {
		t.Fatalf("failed grant snapshot committed marker: %v %v", marked, err)
	}
	if _, operationErr := st.DB.ExecContext(ctx, `DROP TRIGGER fixture_fail_initial_grant`); operationErr != nil {
		t.Fatal(operationErr)
	}
	// A retry is a content sync over the retained row, not renewed authority.
	if _, operationErr := importer.Import(ctx, agentimport.Source{Path: path}); operationErr != nil {
		t.Fatal(operationErr)
	}
	if _, operationErr := BackfillAgentToolsFromLegacyColumns(ctx, st); operationErr != nil {
		t.Fatal(operationErr)
	}
	names, err := st.ListAgentToolNames(ctx, row.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("retry/boot silently granted tools: %v %v", names, err)
	}
}
