package mcpconfig

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestScopedStdioProjection_ArgvEnvAndExistingNameSkip(t *testing.T) {
	// This is a wire-config fixture; it neither executes Tether nor claims that
	// an installed binary supports the reviewed draft flag contract.
	input := []byte(`{"mcpServers":{"tether-mux":{"command":"tether","args":["mcp","--proxy","--no-discover","--servers","torque"],"env":{"TETHER_MCP_TOOLS":"[\"torque_task_get\",\"torque_task_list\"]"}}}}`)
	config, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	configs := ToStoreConfigs(config)
	wantArgs := []string{"mcp", "--proxy", "--no-discover", "--servers", "torque"}
	wantEnv := []string{`TETHER_MCP_TOOLS=["torque_task_get","torque_task_list"]`}
	assertConfig := func(row store.MCPServerConfig) {
		t.Helper()
		var args, env []string
		if operationErr := json.Unmarshal([]byte(row.Args), &args); operationErr != nil {
			t.Fatal(operationErr)
		}
		if operationErr := json.Unmarshal([]byte(row.Env), &env); operationErr != nil {
			t.Fatal(operationErr)
		}
		if row.Name != "tether-mux" || row.Command != "tether" || row.TransportType != store.TransportStdio || !reflect.DeepEqual(args, wantArgs) || !reflect.DeepEqual(env, wantEnv) {
			t.Fatalf("projection changed execution config: %+v", row)
		}
	}
	assertConfig(configs[0])
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	result, err := Import(ctx, st, input, nil)
	if err != nil || !reflect.DeepEqual(result.Created, []string{"tether-mux"}) {
		t.Fatalf("import %+v %v", result, err)
	}
	row, err := st.GetMCPServer(ctx, "tether-mux")
	if err != nil {
		t.Fatal(err)
	}
	assertConfig(*row)
	row.Command = "existing-command"
	row.Args = `["existing-argument"]`
	row.Env = `["EXISTING=value"]`
	if operationErr := st.UpdateMCPServer(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	original := *row
	result, err = Import(ctx, st, input, nil)
	if err != nil || !reflect.DeepEqual(result.Skipped, []string{"tether-mux"}) || len(result.Created) != 0 {
		t.Fatalf("existing import %+v %v", result, err)
	}
	after, err := st.GetMCPServer(ctx, "tether-mux")
	if err != nil || !reflect.DeepEqual(original, *after) {
		t.Fatalf("existing name overwritten: %+v -> %+v: %v", original, after, err)
	}
}
