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

func TestRemindersPublishedReleaseAdoption(t *testing.T) {
	root := approvedPublishedPluginFixture(t, "NANITE_REMINDERS_TEST_BUNDLE", "nanite.reminders", "0.1.0")
	ctx := context.Background()
	st := newSeededStore(t)
	session := &store.Session{Title: "Transferred reminders"}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	message := &store.Message{SessionID: session.ID, Role: "user", Content: "Private core content"}
	if err := st.CreateMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `ALTER TABLE reminders ADD COLUMN future_metadata BLOB`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id    string
		fired any
	}{{"original-reminder", nil}, {"already-fired", "original fired"}} {
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO reminders(id,session_id,scope,text,trigger_json,fired_at,created_at,updated_at,future_metadata) VALUES (?,?,?,?,?,?,?,?,?)`, row.id, session.ID, "session", "Imported reminder", `{"type":"time","at":"2020-01-01T00:00:00Z"}`, row.fired, "original created", "original updated", []byte{0, 255}); err != nil {
			t.Fatal(err)
		}
	}
	sessions := service.NewSessionService(service.SessionServiceDeps{Sessions: st})
	a := New(&service.Container{PluginQueries: service.NewPluginQueryService(st, sessions, service.NewUsageService(st, st), nil)})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", a.handlePluginHostQuery)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(mux, naniteplugin.NewLogger("reminders-release-test"))
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
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM reminders`).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed load lost rows", count, err)
	}
	if receipts, readErr := st.ReadPluginExportReceipts(ctx, "nanite.reminders", 100); readErr != nil || len(receipts) != 0 {
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
	t.Cleanup(func() { _ = host.UnloadPlugin("nanite.reminders") })
	var exists bool
	if err = st.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='reminders')`).Scan(&exists); err != nil || exists {
		t.Fatal("core table remains", exists, err)
	}
	if slots := host.GetSlotEntries(naniteplugin.UISlotName(pluginapi.SlotWorkingDrawer)); len(slots) != 1 || slots[0].PluginID != "nanite.reminders" || slots[0].Component != "RemindersTab" {
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
	if len(packet.Items) != 1 || packet.Items[0].Key != "original-reminder" || !bytes.Contains([]byte(packet.Items[0].Content), []byte("Imported reminder")) {
		t.Fatal("released due reminder missing", packet)
	}
	if packet = fetch(); len(packet.Items) != 1 {
		t.Fatal("retrieval consumed reminder", packet)
	}
	path := "/api/plugins/nanite.reminders/reminders?session_id=" + session.ID
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return w
	}
	if w := call("GET", path, ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("original-reminder")) || bytes.Contains(w.Body.Bytes(), []byte("already-fired")) {
		t.Fatal("released list", w.Code, w.Body.String())
	}
	toolCtx := mcp.WithSessionID(ctx, session.ID)
	if _, err = manager.ExecuteTool(toolCtx, "reminders_ack", map[string]any{"id": "original-reminder"}); err != nil {
		t.Fatal(err)
	}
	if packet = fetch(); len(packet.Items) != 0 {
		t.Fatal("acknowledged reminder injected", packet)
	}
	if _, err = manager.ExecuteTool(toolCtx, "reminders_set", map[string]any{"text": "Native reminder", "trigger": map[string]any{"type": "time", "at": "2020-01-01T00:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	packet = fetch()
	if len(packet.Items) != 1 {
		t.Fatal("native reminder context", packet)
	}
	nativeID := packet.Items[0].Key
	if err = host.UnloadPlugin("nanite.reminders"); err != nil {
		t.Fatal(err)
	}
	if packet = fetch(); len(packet.Items) != 0 {
		t.Fatal("unloaded source retained", packet)
	}
	if w := call("GET", path, ""); w.Code != 404 {
		t.Fatal("unloaded HTTP route retained", w.Code)
	}
	load()
	if packet = fetch(); len(packet.Items) != 1 || packet.Items[0].Key != nativeID {
		t.Fatal("reconnect lost native state", packet)
	}
	if w := call("DELETE", "/api/plugins/nanite.reminders/reminders/"+nativeID+"?session_id="+session.ID, ""); w.Code != 200 {
		t.Fatal("delete", w.Code, w.Body.String())
	}
	if err = host.UnloadPlugin("nanite.reminders"); err != nil {
		t.Fatal(err)
	}
	load()
	if packet = fetch(); len(packet.Items) != 0 {
		t.Fatal("reconnect resurrected ack/deletion", packet)
	}
	if got, getErr := st.GetMessage(ctx, message.ID); getErr != nil || got.Content != message.Content {
		t.Fatal("core message changed", getErr)
	}
	receipts, err := st.ReadPluginExportReceipts(ctx, "nanite.reminders", 100)
	if err != nil || len(receipts) != 1 || receipts[0].RowCount != 2 {
		t.Fatal("receipt lost original rows", receipts, err)
	}
	// The actual SDK tool uses the authoritative MCP session and refuses identity
	// supplied as an argument even when it names that same session.
	result, err := manager.ExecuteTool(toolCtx, "reminders_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || bytes.Contains(raw, []byte(nativeID)) {
		t.Fatal("deleted native row returned", string(raw), err)
	}
}

func TestRemindersCoreRoutesRetired(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, route := range []struct{ method, path string }{{"GET", "/api/sessions/example/reminders"}, {"DELETE", "/api/reminders/example"}, {"PATCH", "/api/reminders/example/scope"}} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired reminder route %s %s: %d", route.method, route.path, response.Code)
		}
	}
}
