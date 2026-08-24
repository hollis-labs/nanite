package service

import (
	"context"
	"path/filepath"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newKnownToolsTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "known-tools.db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	return st
}

func TestSyncKnownTools_UpsertsAndClassifiesSource(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{
		{Name: "dev_read", Description: "Reads a file."},
		{Name: "acme_widget_list", Description: "Lists widgets from an MCP server."},
	}
	isBuiltin := func(name string) bool { return name == "dev_read" }

	result := SyncKnownTools(ctx, st, catalog, isBuiltin)
	if result.Upserted != 2 {
		t.Fatalf("SyncKnownTools: got %d upserted, want 2", result.Upserted)
	}

	builtin, err := st.GetKnownToolByName(ctx, "dev_read")
	if err != nil {
		t.Fatalf("GetKnownToolByName(dev_read): %v", err)
	}
	if builtin.Source != "builtin" || builtin.Status != "available" {
		t.Fatalf("dev_read: got source=%q status=%q, want builtin/available", builtin.Source, builtin.Status)
	}

	mcpTool, err := st.GetKnownToolByName(ctx, "acme_widget_list")
	if err != nil {
		t.Fatalf("GetKnownToolByName(acme_widget_list): %v", err)
	}
	if mcpTool.Source != "mcp" || mcpTool.Status != "available" {
		t.Fatalf("acme_widget_list: got source=%q status=%q, want mcp/available", mcpTool.Source, mcpTool.Status)
	}
}

func TestSyncKnownTools_MarksDisappearedToolsUnavailableNotDeleted(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	first := []llmtypes.ToolDefinition{{Name: "acme_widget_list", Description: "v1"}}
	SyncKnownTools(ctx, st, first, nil)

	// Second boot: the MCP server providing acme_widget_list is gone.
	second := []llmtypes.ToolDefinition{{Name: "dev_read", Description: "builtin"}}
	result := SyncKnownTools(ctx, st, second, func(name string) bool { return name == "dev_read" })
	if result.MarkedUnavailable != 1 {
		t.Fatalf("SyncKnownTools: got %d marked unavailable, want 1", result.MarkedUnavailable)
	}

	row, err := st.GetKnownToolByName(ctx, "acme_widget_list")
	if err != nil {
		t.Fatalf("GetKnownToolByName(acme_widget_list) — row must survive, not be deleted: %v", err)
	}
	if row.Status != "unavailable" {
		t.Fatalf("acme_widget_list: got status=%q, want unavailable", row.Status)
	}
}

func TestSyncKnownTools_SeedsAlwaysIncludedDefaults(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	// Migration 110 seeds request_tools/tool_list/tool_describe as
	// always_included even before any SyncKnownTools call.
	list, err := st.ListAlwaysIncludedKnownTools(ctx)
	if err != nil {
		t.Fatalf("ListAlwaysIncludedKnownTools: %v", err)
	}
	if len(list) < 3 {
		t.Fatalf("expected at least 3 always-included seed rows, got %d: %+v", len(list), list)
	}
}
