package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpconfig"
	"github.com/hollis-labs/nanite/internal/store"
)

// mcpFakes records, in one ordered log, every store write, registrar call
// and discovery run the service makes.
type mcpFakes struct {
	rows     map[string]store.MCPServerConfig
	getErr   error
	events   []string
	remote   map[string]string // name -> headers passed to AddRemoteServerFromConfig
	stdioArg map[string][]string
}

func newMCPFakes() *mcpFakes {
	return &mcpFakes{rows: map[string]store.MCPServerConfig{}, remote: map[string]string{}, stdioArg: map[string][]string{}}
}

func (f *mcpFakes) ListMCPServers(context.Context) ([]store.MCPServerConfig, error) {
	out := make([]store.MCPServerConfig, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *mcpFakes) GetMCPServer(_ context.Context, name string) (*store.MCPServerConfig, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	r, ok := f.rows[name]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (f *mcpFakes) CreateMCPServer(_ context.Context, cfg *store.MCPServerConfig) error {
	f.events = append(f.events, "store.create "+cfg.Name)
	cfg.ID = "id-" + cfg.Name
	f.rows[cfg.Name] = *cfg
	return nil
}

func (f *mcpFakes) UpdateMCPServer(_ context.Context, cfg *store.MCPServerConfig) error {
	f.events = append(f.events, "store.update "+cfg.Name)
	f.rows[cfg.Name] = *cfg
	return nil
}

func (f *mcpFakes) DeleteMCPServer(_ context.Context, name string) error {
	if _, ok := f.rows[name]; !ok {
		return errors.New("mcp server \"" + name + "\" not found")
	}
	f.events = append(f.events, "store.delete "+name)
	delete(f.rows, name)
	return nil
}

func (f *mcpFakes) AddStdioServer(name, _ string, args, _, _ []string, _ mcp.TrustTier) error {
	f.events = append(f.events, "add "+name)
	f.stdioArg[name] = args
	return nil
}

func (f *mcpFakes) AddRemoteServerFromConfig(name, _, _, headerJSON string, _ mcp.TrustTier) error {
	f.events = append(f.events, "add "+name)
	f.remote[name] = headerJSON
	return nil
}

func (f *mcpFakes) RemoveServer(name string) { f.events = append(f.events, "remove "+name) }

func (f *mcpFakes) discover(context.Context) error {
	f.events = append(f.events, "discover")
	return nil
}

func (f *mcpFakes) service() *MCPServerService { return NewMCPServerService(f, f, f.discover) }

func assertEvents(t *testing.T, f *mcpFakes, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %q, want %q", f.events, want)
	}
	f.events = nil
}

func TestMCPServerService_CreateSequencing(t *testing.T) {
	ctx := context.Background()
	f := newMCPFakes()
	svc := f.service()

	cfg := &store.MCPServerConfig{Name: "local", Command: "run", Args: `["-v"]`}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertEvents(t, f, "store.create local", "add local", "discover")
	if cfg.TransportType != store.TransportStdio || !cfg.Enabled || cfg.ID != "id-local" {
		t.Fatalf("cfg after create = %+v, want stdio, enabled, store-filled ID", cfg)
	}
	if got := f.stdioArg["local"]; !reflect.DeepEqual(got, []string{"-v"}) {
		t.Fatalf("stdio args = %q", got)
	}

	if err := svc.Create(ctx, &store.MCPServerConfig{Name: "local"}); !errors.Is(err, ErrMCPServerExists) {
		t.Fatalf("duplicate create err = %v, want ErrMCPServerExists", err)
	}
	assertEvents(t, f)

	var ve *MCPServerValidationError
	if err := svc.Create(ctx, &store.MCPServerConfig{}); !errors.As(err, &ve) || ve.Msg != "name is required" {
		t.Fatalf("nameless create err = %v", err)
	}
	if err := svc.Create(ctx, &store.MCPServerConfig{Name: "x", TransportType: "grpc"}); !errors.As(err, &ve) || ve.Msg != TransportTypeError {
		t.Fatalf("bad transport err = %v", err)
	}
	assertEvents(t, f)
}

func TestMCPServerService_CreateIgnoresDuplicateLookupError(t *testing.T) {
	f := newMCPFakes()
	f.getErr = errors.New("db down")
	if err := f.service().Create(context.Background(), &store.MCPServerConfig{Name: "x"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertEvents(t, f, "store.create x", "add x", "discover")
}

func TestMCPServerService_RefusesLegacyBeforeEffects(t *testing.T) {
	f := newMCPFakes()
	svc := f.service()
	ctx := context.Background()
	for _, cfg := range []*store.MCPServerConfig{
		{Name: "legacy", TransportType: store.TransportSSE, URL: "http://gateway.invalid/sse"},
		{Name: "legacy-url", TransportType: store.TransportStreamable, URL: "http://gateway.invalid/servers/x/sse"},
	} {
		if err := svc.Create(ctx, cfg); !errors.Is(err, mcpconfig.ErrLegacySSE) {
			t.Fatalf("Create cause: %v", err)
		}
		assertEvents(t, f)
	}
	existing := store.MCPServerConfig{Name: "legacy", TransportType: store.TransportSSE, URL: "http://gateway.invalid/servers/x/sse", Enabled: true, Headers: `{"Authorization":"Bearer retained"}`}
	f.rows[existing.Name] = existing
	if _, err := svc.Update(ctx, &existing, MCPServerPatch{}); !errors.Is(err, mcpconfig.ErrLegacySSE) {
		t.Fatalf("Update cause: %v", err)
	}
	assertEvents(t, f)
	if !reflect.DeepEqual(f.rows[existing.Name], existing) {
		t.Fatal("refusal changed retained config")
	}
	off := false
	quarantined, err := svc.Update(ctx, &existing, MCPServerPatch{Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(t, f, "store.update legacy", "remove legacy", "discover")
	if quarantined.Enabled || quarantined.URL != existing.URL || quarantined.Headers != existing.Headers {
		t.Fatal("quarantine rewrote retained config")
	}
	on := true
	if _, operationErr := svc.Update(ctx, quarantined, MCPServerPatch{Enabled: &on}); !errors.Is(operationErr, mcpconfig.ErrLegacySSE) {
		t.Fatalf("legacy reactivation: %v", operationErr)
	}
	assertEvents(t, f)
	kind := store.TransportStreamable
	endpoint := "http://gateway.invalid/servers/x/mcp"
	if _, operationErr := svc.Update(ctx, quarantined, MCPServerPatch{Enabled: &on, TransportType: &kind, URL: &endpoint}); operationErr != nil {
		t.Fatal(operationErr)
	}
	assertEvents(t, f, "store.update legacy", "remove legacy", "add legacy", "discover")
	if !strings.Contains(f.remote[existing.Name], "retained") {
		t.Fatal("explicit migration lost retained auth header")
	}
}

func TestMCPServerService_UpdateSequencing(t *testing.T) {
	ctx := context.Background()
	f := newMCPFakes()
	existing := store.MCPServerConfig{
		Name: "remote", TransportType: store.TransportStreamable, Enabled: true,
		Headers: `{"Authorization":"Bearer real-token"}`,
	}
	f.rows["remote"] = existing
	svc := f.service()

	// Placeholder header and empty transport: the registrar gets the stored
	// token, and the transport stays streamable.
	placeholder := `{"Authorization":"` + RedactedHeaderValue + `"}`
	empty := ""
	row, err := svc.Update(ctx, &existing, MCPServerPatch{TransportType: &empty, Headers: &placeholder})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertEvents(t, f, "store.update remote", "remove remote", "add remote", "discover")
	if row.TransportType != store.TransportStreamable {
		t.Fatalf("transport = %q, want the stored streamable", row.TransportType)
	}
	if !strings.Contains(f.remote["remote"], "real-token") || !strings.Contains(f.rows["remote"].Headers, "real-token") {
		t.Fatalf("stored token not carried forward: registered %q, stored %q", f.remote["remote"], f.rows["remote"].Headers)
	}

	// Omitted headers: the registrar still gets the stored token.
	if _, err := svc.Update(ctx, &existing, MCPServerPatch{}); err != nil {
		t.Fatalf("Update with no headers: %v", err)
	}
	assertEvents(t, f, "store.update remote", "remove remote", "add remote", "discover")
	if !strings.Contains(f.remote["remote"], "real-token") {
		t.Fatalf("registered headers after an update without headers = %q, want the stored token", f.remote["remote"])
	}

	// Disabled: removed, not re-added, discovery still runs.
	off := false
	if _, err := svc.Update(ctx, &existing, MCPServerPatch{Enabled: &off}); err != nil {
		t.Fatalf("Update disabled: %v", err)
	}
	assertEvents(t, f, "store.update remote", "remove remote", "discover")

	var ve *MCPServerValidationError
	grpc := "grpc"
	if _, err := svc.Update(ctx, &existing, MCPServerPatch{TransportType: &grpc}); !errors.As(err, &ve) || ve.Msg != TransportTypeError {
		t.Fatalf("bad transport err = %v", err)
	}
	assertEvents(t, f)
}

func TestMCPServerService_DeleteSequencing(t *testing.T) {
	f := newMCPFakes()
	f.rows["gone"] = store.MCPServerConfig{Name: "gone"}
	svc := f.service()
	if err := svc.Delete(context.Background(), "gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertEvents(t, f, "store.delete gone", "remove gone")
	if err := svc.Delete(context.Background(), "gone"); err == nil || err.Error() != `mcp server "gone" not found` {
		t.Fatalf("second delete err = %v, want the store's error unwrapped", err)
	}
	assertEvents(t, f)
}

func TestMCPServerService_ImportSequencing(t *testing.T) {
	ctx := context.Background()
	f := newMCPFakes()
	f.rows["old"] = store.MCPServerConfig{Name: "old"}
	svc := f.service()

	data := []byte(`{"mcpServers":{"a":{"command":"x"},"b":{"url":"http://127.0.0.1:1/mcp"},"old":{"command":"y"}}}`)
	res, err := svc.Import(ctx, data)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !reflect.DeepEqual(res.Created, []string{"a", "b"}) || !reflect.DeepEqual(res.Skipped, []string{"old"}) {
		t.Fatalf("result = %+v", res)
	}
	assertEvents(t, f, "store.create a", "store.create b", "add a", "add b", "discover")

	// Nothing created: no registration, no discovery.
	if _, err := svc.Import(ctx, []byte(`{"mcpServers":{"a":{"command":"x"}}}`)); err != nil {
		t.Fatalf("second Import: %v", err)
	}
	assertEvents(t, f)
}

func TestMCPServerService_NoManager(t *testing.T) {
	f := newMCPFakes()
	svc := NewMCPServerService(f, nil, nil)
	ctx := context.Background()
	cfg := &store.MCPServerConfig{Name: "x"}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	on := true
	if _, err := svc.Update(ctx, cfg, MCPServerPatch{Enabled: &on}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := svc.Import(ctx, []byte(`{"mcpServers":{"y":{"command":"z"}}}`)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if err := svc.Delete(ctx, "x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertEvents(t, f, "store.create x", "store.update x", "store.create y", "store.delete x")
}

// A container without an MCP manager must leave the registrar unset, not
// holding a typed-nil *mcp.Manager that would pass the nil checks and panic.
func TestNewContainerMCPServerService_NilManager(t *testing.T) {
	svc := newContainerMCPServerService(nil, nil, nil)
	if svc.registrar != nil || svc.discover != nil {
		t.Fatalf("registrar = %v, discover set = %v; want both unset", svc.registrar, svc.discover != nil)
	}
}

// An empty patch leaves every field of the stored row as it was. The row is
// reflection-populated, so a column added to MCPServerConfig later is
// covered without editing this test.
func TestMCPServerService_EmptyPatchKeepsEveryField(t *testing.T) {
	var existing store.MCPServerConfig
	rv := reflect.ValueOf(&existing).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("value-%d", i))
		case reflect.Bool:
			f.SetBool(true)
		default:
			t.Fatalf("store.MCPServerConfig.%s has kind %s; teach this test about it", rv.Type().Field(i).Name, f.Kind())
		}
	}
	existing.TransportType = store.TransportStreamable
	existing.Headers = `{"Authorization":"Bearer real-token"}`

	f := newMCPFakes()
	f.rows[existing.Name] = existing
	row, err := NewMCPServerService(f, nil, nil).Update(context.Background(), &existing, MCPServerPatch{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !reflect.DeepEqual(*row, existing) {
		t.Fatalf("empty patch changed the row\n got: %+v\nwant: %+v", *row, existing)
	}
	if stored := f.rows[existing.Name]; !reflect.DeepEqual(stored, existing) {
		t.Fatalf("empty patch changed the stored row\n got: %+v\nwant: %+v", stored, existing)
	}
}

// The service's export hides every env value; mcpconfig.Export, the CLI's
// path, keeps them.
func TestMCPServerService_ExportRedactsEnv(t *testing.T) {
	f := newMCPFakes()
	f.rows["s"] = store.MCPServerConfig{Name: "s", TransportType: store.TransportStdio, Command: "x", Env: `["TOKEN=real","DEBUG=1"]`}
	cfg, err := f.service().Export(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := map[string]string{"TOKEN": RedactedHeaderValue, "DEBUG": RedactedHeaderValue}
	if got := cfg.MCPServers["s"].Env; !reflect.DeepEqual(got, want) {
		t.Fatalf("exported env = %v, want %v", got, want)
	}
	if f.rows["s"].Env != `["TOKEN=real","DEBUG=1"]` {
		t.Fatalf("export changed the stored env: %s", f.rows["s"].Env)
	}
}

func TestMCPServerService_QuarantineLegacyEndpointWithoutRewrite(t *testing.T) {
	for _, kind := range []string{store.TransportSSE, store.TransportStreamable} {
		t.Run(kind, func(t *testing.T) {
			f := newMCPFakes()
			existing := store.MCPServerConfig{Name: "legacy", TransportType: kind, URL: "http://gateway.invalid/servers/x/sse", Enabled: true}
			f.rows[existing.Name] = existing
			off := false
			row, err := f.service().Update(context.Background(), &existing, MCPServerPatch{Enabled: &off})
			if err != nil {
				t.Fatal(err)
			}
			if row.Enabled || row.URL != existing.URL || row.TransportType != existing.TransportType {
				t.Fatal("quarantine rewrote protocol or endpoint")
			}
			assertEvents(t, f, "store.update legacy", "remove legacy", "discover")
		})
	}
}
