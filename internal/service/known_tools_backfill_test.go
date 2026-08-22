package service

import (
	"context"
	"sort"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestBackfillAgentToolsFromLegacyColumns_ExplicitAllowlist(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{
		{Name: "dev_read"},
		{Name: "dev_write"},
		{Name: "dev_bash"},
	}
	SyncKnownTools(ctx, st, catalog, func(name string) bool { return true })

	agent := &store.AgentProfile{
		Name:         "Restricted",
		Slug:         "restricted-agent",
		SystemPrompt: "Test.",
		Tools:        `["dev_read","dev_write"]`,
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// The aggregate grant count is not asserted here: every fresh store
	// already ships with a handful of real seeded builtin agent profiles
	// (analyst/backend/worker/etc., migrations 036/059/061/063) that this
	// same backfill pass also processes against their own tools/
	// tool_permissions columns, so the total is coupled to that seed data.
	// This test's real assertion is the per-agent grant set below.
	if _, err := BackfillAgentToolsFromLegacyColumns(ctx, st); err != nil {
		t.Fatalf("BackfillAgentToolsFromLegacyColumns: %v", err)
	}

	names, err := st.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "dev_read" || names[1] != "dev_write" {
		t.Fatalf("ListAgentToolNames: got %v, want [dev_read dev_write]", names)
	}

	has, err := st.HasAgentToolGrantedVia(ctx, agent.ID, "legacy_backfill")
	if err != nil {
		t.Fatalf("HasAgentToolGrantedVia: %v", err)
	}
	if !has {
		t.Fatal("expected legacy_backfill provenance on backfilled rows")
	}
}

func TestBackfillAgentToolsFromLegacyColumns_EmptyAllowlistGrantsFullCatalog(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{{Name: "dev_read"}, {Name: "dev_write"}}
	SyncKnownTools(ctx, st, catalog, func(name string) bool { return true })

	agent := &store.AgentProfile{
		Name:         "Unrestricted",
		Slug:         "unrestricted-agent",
		SystemPrompt: "Test.",
		// Tools left empty -- "[]" is the CreateAgent column default,
		// meaning "no restriction" per filterToolsByAllowlist's own
		// documented behavior.
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := BackfillAgentToolsFromLegacyColumns(ctx, st); err != nil {
		t.Fatalf("BackfillAgentToolsFromLegacyColumns: %v", err)
	}

	names, err := st.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "dev_read" || names[1] != "dev_write" {
		t.Fatalf("ListAgentToolNames: got %v, want the full available catalog", names)
	}
}

// TestBackfillAgentToolsFromLegacyColumns_ToolPermissionsNoLongerNarrows is
// the regression test for TASKS/adhoc/02-remove-tool-permissions-collapse-
// to-agent-tools.md's change to legacyGrantCandidates: a still-populated
// legacy tool_permissions.deny_list on the agent row must NOT exclude a
// pattern-matched tool anymore -- tool_permissions is inert everywhere else
// in the system now, so this one-time backfill must not be the last place
// that still honors it. (Superseded the deleted
// TestBackfillAgentToolsFromLegacyColumns_DenyListExcludesTool, which
// asserted the opposite, pre-this-task behavior.)
func TestBackfillAgentToolsFromLegacyColumns_ToolPermissionsNoLongerNarrows(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{{Name: "dev_read"}, {Name: "dev_bash"}}
	SyncKnownTools(ctx, st, catalog, func(name string) bool { return true })

	agent := &store.AgentProfile{
		Name:            "Denied",
		Slug:            "denied-agent",
		SystemPrompt:    "Test.",
		ToolPermissions: `{"deny_list":["dev_bash"]}`,
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := BackfillAgentToolsFromLegacyColumns(ctx, st); err != nil {
		t.Fatalf("BackfillAgentToolsFromLegacyColumns: %v", err)
	}

	names, err := st.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "dev_bash" || names[1] != "dev_read" {
		t.Fatalf("ListAgentToolNames: got %v, want [dev_bash dev_read] (tool_permissions.deny_list no longer narrows)", names)
	}
}

func TestBackfillAgentToolsFromLegacyColumns_RunsOncePerAgent(t *testing.T) {
	st := newKnownToolsTestStore(t)
	ctx := context.Background()

	catalog := []llmtypes.ToolDefinition{{Name: "dev_read"}, {Name: "dev_write"}}
	SyncKnownTools(ctx, st, catalog, func(name string) bool { return true })

	agent := &store.AgentProfile{
		Name:         "Once",
		Slug:         "once-agent",
		SystemPrompt: "Test.",
		Tools:        `["dev_read"]`,
	}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := BackfillAgentToolsFromLegacyColumns(ctx, st); err != nil {
		t.Fatalf("BackfillAgentToolsFromLegacyColumns (first run): %v", err)
	}

	// Simulate an operator narrowing the grant set directly (e.g. via a
	// future agent_tools picker UI) after the one-time backfill ran.
	toolID, err := st.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatalf("UpsertKnownTool: %v", err)
	}
	if err := st.RevokeAgentTool(ctx, agent.ID, toolID); err != nil {
		t.Fatalf("RevokeAgentTool: %v", err)
	}

	// A second boot's backfill pass must not re-derive the revoked grant
	// from the still-present legacy tools: column.
	n, err := BackfillAgentToolsFromLegacyColumns(ctx, st)
	if err != nil {
		t.Fatalf("BackfillAgentToolsFromLegacyColumns (second run): %v", err)
	}
	if n != 0 {
		t.Fatalf("BackfillAgentToolsFromLegacyColumns (second run): got %d new grants, want 0 (one-time-per-agent guard)", n)
	}

	names, err := st.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("ListAgentToolNames: got %v, want [] (revoke must survive a second backfill pass)", names)
	}
}
