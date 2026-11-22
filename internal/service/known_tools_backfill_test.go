package service

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestBackfillAgentToolsFromLegacyColumnsCannotReplayAuthority(t *testing.T) {
	for _, patterns := range []string{"", `[]`, `["dev_read"]`, `["dev_*"]`} {
		t.Run(patterns, func(t *testing.T) {
			st := newKnownToolsTestStore(t)
			ctx := t.Context()
			SyncKnownTools(ctx, st, []llmtypes.ToolDefinition{{Name: "dev_read"}, {Name: "dev_write"}}, func(string) bool { return true })
			historical := &store.AgentProfile{Name: "Historical", Slug: "historical-backfill", SystemPrompt: "Retained instructions."}
			if err := storetest.HistoricalProfile(ctx, st, historical); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.ExecContext(ctx, `UPDATE agent_profiles SET tools=?,role_tools='["dev_write"]',tool_permissions='{"deny_list":[]}' WHERE id=?`, patterns, historical.ID); err != nil {
				t.Fatal(err)
			}
			actor := &store.AgentProfile{Name: "Prior actor", Slug: "prior-backfill", SystemPrompt: "Fresh pinned instructions."}
			if err := persistTestActor(ctx, st, actor); err != nil {
				t.Fatal(err)
			}
			tool, err := st.GetKnownToolByName(ctx, "dev_read")
			if err != nil {
				t.Fatal(err)
			}
			if err = grantTestActorTool(ctx, st, actor.ID, tool.ID, "private-prior-grant"); err != nil {
				t.Fatal(err)
			}
			if err = st.RevokeAgentTool(ctx, actor.ID, tool.ID); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if n, operationErr := BackfillAgentToolsFromLegacyColumns(ctx, st); n != 0 || !errors.Is(operationErr, store.ErrVerifiedActorRequired) {
					t.Fatal(n, operationErr)
				}
			}
			names, err := st.ListAgentToolNames(ctx, actor.ID)
			if err != nil || len(names) != 0 {
				t.Fatal("backfill revived grant", names, err)
			}
			var retained string
			if err := st.DB.QueryRowContext(ctx, `SELECT tools FROM agent_profiles WHERE id=?`, historical.ID).Scan(&retained); err != nil || retained != patterns {
				t.Fatal("historical selection changed", retained, err)
			}
			var markers int
			if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_tools_legacy_backfill WHERE agent_id=?`, historical.ID).Scan(&markers); err != nil || markers != 0 {
				t.Fatal("backfill wrote old marker", markers, err)
			}
		})
	}
}
