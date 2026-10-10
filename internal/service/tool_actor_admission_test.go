package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func TestToolActorAdmissionRefusesBeforeBroker(t *testing.T) {
	for _, state := range []string{"host_uuid", "disabled", "empty_receipt"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			st := newKnownToolsTestStore(t)
			prior := &store.AgentProfile{Name: "Prior host-authorized fixture", Slug: "admission"}
			if err := persistTestActor(ctx, st, prior); err != nil {
				t.Fatal(err)
			}
			actor := prior.ID
			switch state {
			case "host_uuid":
				if err := st.DB.QueryRowContext(ctx, `SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?`, actor).Scan(&actor); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := st.DB.ExecContext(ctx, `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, actor); err != nil {
					t.Fatal(err)
				}
			case "empty_receipt":
				if _, err := st.DB.ExecContext(ctx, `UPDATE agent_actor_bindings SET binding_receipt='' WHERE actor_uri=?`, actor); err != nil {
					t.Fatal(err)
				}
			}
			toolID, err := st.UpsertKnownTool(ctx, "private_probe", "builtin", "available", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB.ExecContext(ctx, `UPDATE known_tools SET always_included=1 WHERE id=?`, toolID); err != nil {
				t.Fatal(err)
			}
			client := toolclient.New(nil, st, nil)
			brokerCalls := 0
			client.DeveloperModeFunc = func() bool { brokerCalls++; return true }
			client.RegisterTools([]llmtypes.ToolDefinition{{Name: "private_probe", Description: "probe"}})
			svc := NewToolService(client, nil, st)
			if _, err := svc.SelectForAgent(ctx, "private", actor, "probe", "", 0); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("selection error=%v", err)
			}
			if _, _, err := svc.HandleRequestTools(ctx, actor, map[string]any{"tool_names": []any{"private_probe"}}); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("discovery error=%v", err)
			}
			if _, err := client.SelectToolsAsProvider(ctx, "probe", nil, "", actor); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("direct broker error=%v", err)
			}
			if tools, _ := client.HandleRequestToolsForAgent(ctx, actor, map[string]any{"tool_names": []any{"private_probe"}}); len(tools) != 0 {
				t.Fatal("direct discovery exposed catalog exception")
			}
			if brokerCalls != 0 {
				t.Fatalf("broker effects=%d", brokerCalls)
			}
		})
	}
}
