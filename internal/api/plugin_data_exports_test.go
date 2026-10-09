package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/dataexport"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginHostExportReceiptsAndCoreReferences(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "exports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	session := &store.Session{Title: "Referenced session"}
	if checkErr := st.CreateSession(ctx, session); checkErr != nil {
		t.Fatal(checkErr)
	}
	message := &store.Message{SessionID: session.ID, Role: "user", Content: "private message content"}
	if checkErr := st.CreateMessage(ctx, message); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.DB.ExecContext(ctx, `INSERT INTO bookmarks(id,session_id,message_id,note,tags) VALUES (?,?,?,?,?)`, "export-fixture-bookmark", session.ID, message.ID, "Saved", `["star"]`); checkErr != nil {
		t.Fatal(checkErr)
	}
	sessions := service.NewSessionService(service.SessionServiceDeps{Sessions: st})
	a := New(&service.Container{PluginQueries: service.NewPluginQueryService(st, sessions, nil, nil)})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", a.handlePluginHostQuery)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(nil, naniteplugin.NewLogger("export-test"))
	a.SetPluginHost(host)
	if checkErr := host.SetHostQueryURL(server.URL); checkErr != nil {
		t.Fatal(checkErr)
	}
	root := t.TempDir()
	directory := filepath.Join(root, "exports-reader")
	if checkErr := os.Mkdir(directory, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	path := writeAPIPluginBundle(t, directory, "exports-reader", "Export reader", pluginapi.Block{})
	raw, err := os.ReadFile(path) // #nosec G304 -- private fixture-generated manifest under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := manifest.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	scope := pluginapi.QueryScope{Resources: []pluginapi.QueryResource{pluginapi.QueryDataExports, pluginapi.QueryMessageReferences}, AllSessions: true}
	metadata, _ := json.Marshal(scope)
	declaration.Capabilities = []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityReadOnlyQuery, Metadata: metadata}}
	var encoded strings.Builder
	if checkErr := manifest.Encode(&encoded, declaration); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(path, []byte(encoded.String()), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := naniteplugin.SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("load: %v %v", loaded, failures)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("exports-reader") })
	child, ok := host.GetPlugin("exports-reader")
	if !ok {
		t.Fatal("child missing")
	}
	identity, err := child.(*subprocess.SubprocessPlugin).CallTool(ctx, &sdkprocess.MCPCallRequest{ToolName: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := pluginapi.QueryGrantFromIdentity(identity.Content)
	if err != nil {
		t.Fatal(err)
	}
	client, err := pluginapi.NewQueryClient(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := client.ExportReceipts(ctx, 100)
	if err != nil || len(before.Exports) != 0 {
		t.Fatal("uncommitted receipt exposed")
	}
	reference, err := client.ResolveReference(ctx, pluginapi.CoreReference{SessionID: session.ID, MessageID: message.ID})
	if err != nil || reference.Role != "user" {
		t.Fatalf("reference: %+v %v", reference, err)
	}
	response, err := client.Query(ctx, pluginapi.QueryRequest{Resource: pluginapi.QueryMessageReferences, SessionID: session.ID})
	if err != nil || strings.Contains(string(response.Data), "private message") {
		t.Fatal("message projection leaked content")
	}
	if _, checkErr := client.ResolveReference(ctx, pluginapi.CoreReference{SessionID: session.ID, MessageID: "missing"}); checkErr == nil {
		t.Fatal("missing reference resolved")
	}
	dataDir := t.TempDir()
	source, err := dataexport.SourceID(st.DBPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := dataexport.ExportAndDrop(ctx, tx, dataexport.Spec{PluginID: "exports-reader", Feature: "bookmarks", SourceID: source, Table: "bookmarks"}, dataDir)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
	// A different owner's committed receipt remains private to that owner.
	if _, checkErr := st.DB.ExecContext(ctx, `CREATE TABLE other_feature(id TEXT PRIMARY KEY)`); checkErr != nil {
		t.Fatal(checkErr)
	}
	tx, err = st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := dataexport.ExportAndDrop(ctx, tx, dataexport.Spec{PluginID: "other.plugin", Feature: "other", SourceID: source, Table: "other_feature"}, dataDir); checkErr != nil {
		_ = tx.Rollback()
		t.Fatal(checkErr)
	}
	if checkErr := tx.Commit(); checkErr != nil {
		t.Fatal(checkErr)
	}
	committed, err := client.ExportReceipts(ctx, 100)
	if err != nil || len(committed.Exports) != 1 || committed.Exports[0] != receipt {
		t.Fatalf("owned committed receipts: %+v %v", committed, err)
	}
	file, err := os.Open(filepath.Join(dataDir, receipt.Path)) // #nosec G304 -- host-generated receipt in isolated fixture DataDir.
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := pluginapi.DecodeDataExport(file)
	_ = file.Close()
	if err != nil || receipt.Verify(snapshot) != nil || snapshot.Snapshot.Rows[0][0].Text != "export-fixture-bookmark" {
		t.Fatal("bookmark export differs from actual store row")
	}
	expected := map[string]string{"id": "export-fixture-bookmark", "session_id": session.ID, "message_id": message.ID, "note": "Saved", "tags": `["star"]`}
	for index, column := range snapshot.Snapshot.Columns {
		if value, known := expected[column]; known && snapshot.Snapshot.Rows[0][index].Text != value {
			t.Fatalf("bookmark field %s differs", column)
		}
	}
	for _, path := range []string{"/api/plugin-host/query/data_exports?plugin_id=other.plugin", "/api/plugin-host/query/data_exports?message_id=missing", "/api/plugin-host/query/message_refs?session_id=" + session.ID + "&message_id=", "/api/plugin-host/query/message_refs?session_id=" + session.ID + "&message_id=one&message_id=two"} {
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+grant.Token)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("malformed query accepted: %s %d", path, recorder.Code)
		}
	}
	if _, checkErr := client.ResolveReference(ctx, pluginapi.CoreReference{SessionID: session.ID, MessageID: message.ID}); checkErr != nil {
		t.Fatal("export dropped a core referenced message")
	}
	if checkErr := host.UnloadPlugin("exports-reader"); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := client.ExportReceipts(ctx, 100); checkErr == nil {
		t.Fatal("unloaded credential retained export access")
	}
}
