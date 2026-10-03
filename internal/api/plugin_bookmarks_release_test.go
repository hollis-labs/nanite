package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// This integration uses the verified published binary, not a second embedded
// implementation. Set NANITE_BOOKMARKS_TEST_BUNDLE to the extracted v0.1.0
// release directory. The ordinary suite tests the transfer independently.
func TestBookmarksPublishedReleaseAdoption(t *testing.T) {
	published := os.Getenv("NANITE_BOOKMARKS_TEST_BUNDLE")
	if published == "" {
		t.Skip("requires verified bookmarks/v0.1.0 release bundle")
	}
	ctx := context.Background()
	st := newSeededStore(t)
	session := &store.Session{Title: "Transferred bookmarks"}
	if checkErr := st.CreateSession(ctx, session); checkErr != nil {
		t.Fatal(checkErr)
	}
	message := &store.Message{SessionID: session.ID, Role: "assistant", Content: "Private content is never exported"}
	if checkErr := st.CreateMessage(ctx, message); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.DB.Exec(`INSERT INTO bookmarks(id,message_id,session_id,note,tags,created_at) VALUES (?,?,?,?,?,?)`, "original-bookmark", message.ID, session.ID, "Original title", `["kept"]`, "2026-01-02 03:04:05.678+02:00"); checkErr != nil {
		t.Fatal(checkErr)
	}
	root := t.TempDir()
	directory := filepath.Join(root, "nanite.bookmarks")
	if checkErr := os.Mkdir(directory, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	input, err := os.OpenRoot(published) // #nosec G703 -- opt-in integration fixture selects an externally verified release directory.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	if checkErr := fs.WalkDir(input.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return output.MkdirAll(relative, 0700)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		raw, readErr := input.ReadFile(relative)
		if readErr != nil {
			return readErr
		}
		return output.WriteFile(relative, raw, info.Mode().Perm())
	}); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if review.Version != "0.1.0" {
		t.Fatal("test requires published v0.1.0")
	}
	if err = naniteplugin.SaveInstallApproval(root, review, review.Digest()); err != nil {
		t.Fatal(err)
	}
	sessions := service.NewSessionService(service.SessionServiceDeps{Sessions: st})
	a := New(&service.Container{PluginQueries: service.NewPluginQueryService(st, sessions, nil, nil)})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin-host/query/{resource}", a.handlePluginHostQuery)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(mux, naniteplugin.NewLogger("bookmarks-release-test"))
	host.SetStore(st)
	host.SetCoreDataAdopter(service.NewPluginCoreData(st))
	manager := mcp.NewManager()
	host.SetMCPRegistrar(service.NewPluginToolRegistrar(manager, st))
	commands := chat.NewCommandRegistry()
	host.SetCommandRegistry(commands)
	a.SetPluginHost(host)
	if err = host.SetHostQueryURL(server.URL); err != nil {
		t.Fatal(err)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	load := func() {
		t.Helper()
		loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
		if len(loaded) != 1 || len(failures) != 0 {
			t.Fatalf("load: %v %v", loaded, failures)
		}
	}
	// A late panel collision must not authorize a destructive transfer.
	if checkErr := host.RegisterPanel(naniteplugin.PanelEntry{ID: "bookmark-list", PluginID: "collision-owner", Component: "Other"}); checkErr != nil {
		t.Fatal(checkErr)
	}
	rejected, rejections := naniteplugin.LoadDiscovered(host, discovered)
	if len(rejected) != 0 || len(rejections) == 0 {
		t.Fatal("conflicting registration loaded")
	}
	var count int
	if checkErr := st.DB.QueryRow(`SELECT count(*) FROM bookmarks`).Scan(&count); checkErr != nil || count != 1 {
		t.Fatal("failed registration retired core rows")
	}
	if receipts, checkErr := st.ReadPluginExportReceipts(ctx, "nanite.bookmarks", 100); checkErr != nil || len(receipts) != 0 {
		t.Fatal("failed registration committed receipt")
	}
	host.UnregisterPluginPanels("collision-owner")
	load()
	t.Cleanup(func() { _ = host.UnloadPlugin("nanite.bookmarks") })
	var exists bool
	if err = st.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='bookmarks')`).Scan(&exists); err != nil || exists {
		t.Fatal("core copy remains")
	}
	path := "/api/plugins/nanite.bookmarks/bookmarks?session_id=" + session.ID
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return w
	}
	response := call("GET", path, "")
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte("Original title")) || bytes.Contains(response.Body.Bytes(), []byte("Private content")) {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	if w := call("PATCH", "/api/plugins/nanite.bookmarks/bookmarks/original-bookmark", `{"note":"Edited after import"}`); w.Code != 200 {
		t.Fatalf("edit: %s", w.Body.String())
	}
	if err = host.UnloadPlugin("nanite.bookmarks"); err != nil {
		t.Fatal(err)
	}
	load()
	response = call("GET", path, "")
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte("Edited after import")) {
		t.Fatal("reconnect lost edits")
	}
	if w := call("DELETE", "/api/plugins/nanite.bookmarks/bookmarks/original-bookmark", ""); w.Code != 200 {
		t.Fatalf("delete: %s", w.Body.String())
	}
	if err = host.UnloadPlugin("nanite.bookmarks"); err != nil {
		t.Fatal(err)
	}
	load()
	response = call("GET", path, "")
	if response.Code != 200 || bytes.Contains(response.Body.Bytes(), []byte("original-bookmark")) {
		t.Fatal("reconnect resurrected deletion")
	}
	if _, checkErr := commands.Execute(ctx, "bookmark", session.ID, message.ID+" Native title"); checkErr != nil {
		t.Fatal(checkErr)
	}
	toolResult, toolErr := manager.ExecuteTool(mcp.WithSessionID(ctx, session.ID), "bookmarks_list", map[string]any{"session_id": "argument-session"})
	if toolErr != nil {
		t.Fatal(toolErr)
	}
	toolRaw, marshalErr := json.Marshal(toolResult)
	if marshalErr != nil || !bytes.Contains(toolRaw, []byte("Native title")) {
		t.Fatalf("canonical tool session lost: %s %v", toolRaw, marshalErr)
	}
	// The core session and message were references, never moved or copied.
	if got, getErr := st.GetMessage(ctx, message.ID); getErr != nil || got.Content != message.Content {
		t.Fatal("core message changed")
	}
	receipts, err := st.ReadPluginExportReceipts(ctx, "nanite.bookmarks", 100)
	if err != nil || len(receipts) != 1 {
		t.Fatal("receipt missing")
	}
	directory, err = brand.PluginDataDir("nanite.bookmarks")
	if err != nil {
		t.Fatal(err)
	}
	dataRoot, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dataRoot.Close() }()
	file, err := dataRoot.Open(receipts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := pluginapi.DecodeDataExport(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if exported.Snapshot.Rows[0][0].Text != "original-bookmark" || exported.Snapshot.Rows[0][3].Text != "Original title" || exported.Snapshot.Rows[0][4].Text != `["kept"]` || exported.Snapshot.Rows[0][5].Text != "2026-01-02 03:04:05.678+02:00" {
		t.Fatal("export changed original columns")
	}
	raw, err := json.Marshal(exported.Snapshot)
	if err != nil || bytes.Contains(raw, []byte(message.Content)) {
		t.Fatal("copied core content")
	}
}

func TestBookmarksCoreRoutesRetired(t *testing.T) {
	_, mux := newTestAPI(t)
	for _, route := range []struct{ method, path string }{{"GET", "/api/sessions/example/bookmarks"}, {"POST", "/api/bookmarks"}, {"DELETE", "/api/bookmarks/example"}, {"POST", "/api/messages/example/bookmark"}, {"POST", "/api/bookmarks/example/autotitle"}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("retired route %s %s: %d", route.method, route.path, w.Code)
		}
	}
}
