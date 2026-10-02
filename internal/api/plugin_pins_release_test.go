package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/mcp"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPinsPublishedReleaseAdoption(t *testing.T) {
	root := approvedPublishedPluginFixture(t, "NANITE_PINS_TEST_BUNDLE", "nanite.pins", "0.1.0")
	ctx := context.Background()
	st := newSeededStore(t)
	project := &store.Project{ID: "pins-project", Name: "Pins project", RepoPath: t.TempDir()}
	if err := st.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	session := &store.Session{Title: "Transferred pins", ProjectID: project.ID}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	message := &store.Message{SessionID: session.ID, Role: "user", Content: "Private core content"}
	if err := st.CreateMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `ALTER TABLE pinned_content ADD COLUMN future_metadata BLOB`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, scope, content string
		session, project   any
	}{
		{"original-pin", "session", "Imported pin", session.ID, nil},
		{"null-origin", "project", "Project pin", nil, project.ID},
		{"legacy-turn", "turn", "Turn pin", session.ID, nil},
	} {
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO pinned_content(id,session_id,scope,project_id,content,agent_id,created_at,updated_at,future_metadata) VALUES (?,?,?,?,?,?,?,?,?)`, row.id, row.session, row.scope, row.project, row.content, "original-agent", "original created", "original updated", []byte{0, 255}); err != nil {
			t.Fatal(err)
		}
	}

	sessions := service.NewSessionService(service.SessionServiceDeps{Sessions: st})
	a := New(&service.Container{PluginQueries: service.NewPluginQueryService(st, sessions, service.NewUsageService(st, st), nil)})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", a.handlePluginHostQuery)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(mux, naniteplugin.NewLogger("pins-release-test"))
	host.SetStore(st)
	host.SetCoreDataAdopter(service.NewPluginCoreData(st))
	manager := mcp.NewManager()
	host.SetMCPRegistrar(service.NewPluginToolRegistrar(manager, st))
	a.SetPluginHost(host)
	if err := host.SetHostQueryURL(server.URL); err != nil {
		t.Fatal(err)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	// Missing context registration refuses activation after the subprocess starts;
	// the core table/rows and receipt must remain untouched.
	rejected, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(rejected) != 0 || len(failures) == 0 {
		t.Fatal("missing context registrar did not refuse load")
	}
	var count int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM pinned_content`).Scan(&count); err != nil || count != 3 {
		t.Fatal("failed load lost rows", count, err)
	}
	if receipts, readErr := st.ReadPluginExportReceipts(ctx, "nanite.pins", 100); readErr != nil || len(receipts) != 0 {
		t.Fatal("failed load committed receipt", receipts, readErr)
	}
	registry := service.NewPluginContextSources()
	host.SetContextSourceRegistrar(registry)
	load := func() {
		t.Helper()
		loaded, loadErrors := naniteplugin.LoadDiscovered(host, discovered)
		if len(loaded) != 1 || len(loadErrors) != 0 {
			t.Fatal("published load", loaded, loadErrors)
		}
	}
	load()
	t.Cleanup(func() { _ = host.UnloadPlugin("nanite.pins") })
	var exists bool
	if err = st.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='pinned_content')`).Scan(&exists); err != nil || exists {
		t.Fatal("core table remains", exists, err)
	}
	if slots := host.GetSlotEntries(naniteplugin.UISlotName(pluginapi.SlotWorkingDrawer)); len(slots) != 1 || slots[0].PluginID != "nanite.pins" || slots[0].Component != "PinsTab" {
		t.Fatal("released drawer declaration lost", slots)
	}
	broker := contextbroker.New(contextbroker.DefaultBudget(), registry)
	fetch := func() *contextbroker.ContextPacket {
		t.Helper()
		packet, fetchErr := broker.Fetch(ctx, contextbroker.Intent{SessionID: session.ID})
		if fetchErr != nil {
			t.Fatal(fetchErr)
		}
		return packet
	}
	packet := fetch()
	if len(packet.Items) != 2 {
		t.Fatal("released session/project pins missing", packet)
	}
	for _, item := range packet.Items {
		if item.Key == "legacy-turn" {
			t.Fatal("turn pin injected")
		}
	}
	if packet = fetch(); len(packet.Items) != 2 {
		t.Fatal("retrieval consumed pins", packet)
	}
	path := "/api/plugins/nanite.pins/pins?session_id=" + session.ID
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return w
	}
	if w := call("GET", path, ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("legacy-turn")) || !bytes.Contains(w.Body.Bytes(), []byte("original-agent")) {
		t.Fatal("released list", w.Code, w.Body.String())
	}
	if w := call("PATCH", "/api/plugins/nanite.pins/pins/null-origin/scope?session_id="+session.ID, `{"scope":"session"}`); w.Code != 400 {
		t.Fatal("invented null origin", w.Code, w.Body.String())
	}
	toolCtx := mcp.WithSessionID(ctx, session.ID)
	if _, err = manager.ExecuteTool(toolCtx, "pins_set", map[string]any{"content": "forged", "session_id": session.ID}); err == nil {
		t.Fatal("forged identity accepted")
	}
	if _, err = manager.ExecuteTool(toolCtx, "pins_update", map[string]any{"id": "original-pin", "content": "Operator edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ExecuteTool(toolCtx, "pins_delete", map[string]any{"id": "null-origin"}); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ExecuteTool(toolCtx, "pins_set", map[string]any{"content": "Native pin"}); err != nil {
		t.Fatal(err)
	}
	packet = fetch()
	if len(packet.Items) != 2 {
		t.Fatal("native context", packet)
	}
	nativeID := ""
	for _, item := range packet.Items {
		if item.Key != "original-pin" {
			nativeID = item.Key
		}
	}
	if nativeID == "" {
		t.Fatal("native ID missing")
	}
	if err = host.UnloadPlugin("nanite.pins"); err != nil {
		t.Fatal(err)
	}
	if packet = fetch(); len(packet.Items) != 0 {
		t.Fatal("unloaded source retained", packet)
	}
	if w := call("GET", path, ""); w.Code != 404 {
		t.Fatal("unloaded route retained", w.Code)
	}
	load()
	if packet = fetch(); len(packet.Items) != 2 {
		t.Fatal("reconnect lost state", packet)
	}
	for _, item := range packet.Items {
		if item.Key == "original-pin" && !bytes.Contains([]byte(item.Content), []byte("Operator edit")) {
			t.Fatal("reconnect lost edit", item)
		}
	}
	if w := call("DELETE", "/api/plugins/nanite.pins/pins/"+nativeID+"?session_id="+session.ID, ""); w.Code != 200 {
		t.Fatal("delete", w.Code, w.Body.String())
	}
	if err = host.UnloadPlugin("nanite.pins"); err != nil {
		t.Fatal(err)
	}
	load()
	if packet = fetch(); len(packet.Items) != 1 || packet.Items[0].Key != "original-pin" {
		t.Fatal("replay resurrected deletion", packet)
	}
	if got, getErr := st.GetMessage(ctx, message.ID); getErr != nil || got.Content != message.Content {
		t.Fatal("core message changed", getErr)
	}
	receipts, err := st.ReadPluginExportReceipts(ctx, "nanite.pins", 100)
	if err != nil || len(receipts) != 1 || receipts[0].RowCount != 3 {
		t.Fatal("receipt lost original rows", receipts, err)
	}
	// The actual SDK tool uses the authoritative MCP session and refuses identity
	// supplied as an argument even when it names that same session.
	result, err := manager.ExecuteTool(toolCtx, "pins_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || bytes.Contains(raw, []byte(nativeID)) {
		t.Fatal("deleted native row returned", string(raw), err)
	}
}

func TestPinsCoreRoutesRetired(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, route := range []struct{ method, path string }{{"GET", "/api/sessions/example/pins"}, {"DELETE", "/api/pins/example"}, {"PATCH", "/api/pins/example/scope"}} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired pin route %s %s: %d", route.method, route.path, response.Code)
		}
	}
}
