package mcpconfig

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestLegacySSERefusalBeforeImportWrites(t *testing.T) {
	for _, entry := range []string{
		`{"type":"sse","url":"http://gateway.invalid/servers/x/sse"}`,
		`{"type":"http","url":"http://gateway.invalid/servers/x/sse/?token=private"}`,
		`{"url":"http://gateway.invalid/servers/x/%73se"}`,
	} {
		t.Run(entry, func(t *testing.T) {
			ctx := context.Background()
			st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close(ctx) })
			_, importErr := Import(ctx, st, []byte(`{"mcpServers":{"a-valid":{"command":"true"},"z-legacy":`+entry+`}}`), nil)
			if !errors.Is(importErr, ErrLegacySSE) {
				t.Fatalf("refusal lost cause: %v", importErr)
			}
			if strings.Contains(importErr.Error(), "private") {
				t.Fatal("refusal exposed URL credential")
			}
			rows, listErr := st.ListMCPServers(ctx)
			if listErr != nil || len(rows) != 0 {
				t.Fatalf("invalid import mutated store: %+v %v", rows, listErr)
			}
		})
	}
}

func TestLegacySSEExportPreservesOperatorConfiguration(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	row := &store.MCPServerConfig{Name: "legacy", TransportType: store.TransportSSE, URL: "http://gateway.invalid/servers/x/sse", Enabled: true}
	if operationErr := st.CreateMCPServer(ctx, row); operationErr != nil {
		t.Fatal(operationErr)
	}
	exported, exportErr := Export(ctx, st)
	if exportErr != nil {
		t.Fatal(exportErr)
	}
	entry := exported.MCPServers["legacy"]
	if entry.Type != "sse" || entry.URL != row.URL {
		t.Fatalf("export silently migrated legacy: %+v", entry)
	}
	data, marshalErr := Marshal(exported)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if _, parseErr := Parse(data); !errors.Is(parseErr, ErrLegacySSE) {
		t.Fatalf("legacy export imported as active config: %v", parseErr)
	}
}

func TestConfigTransportRejectsUnknownAndContradictoryTypes(t *testing.T) {
	for _, entry := range []string{
		`{"type":"websocket","url":"http://gateway.invalid/mcp"}`,
		`{"type":"stdio","url":"http://gateway.invalid/mcp"}`,
		`{"type":"http","command":"true"}`,
	} {
		if _, err := Parse([]byte(`{"mcpServers":{"probe":` + entry + `}}`)); err == nil {
			t.Fatalf("ambiguous type defaulted silently: %s", entry)
		}
	}
	for _, kind := range []string{"", "http"} {
		cfg, err := Parse([]byte(`{"mcpServers":{"probe":{"type":"` + kind + `","url":"http://gateway.invalid/servers/x/mcp"}}}`))
		if err != nil {
			t.Fatal(err)
		}
		rows := ToStoreConfigs(cfg)
		if len(rows) != 1 || rows[0].TransportType != store.TransportStreamable {
			t.Fatalf("HTTP did not select Streamable: %+v", rows)
		}
	}
}
