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

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginReflexHTTPApprovedSDKLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newSeededStore(t)
	agent := &store.AgentProfile{Slug: "reflex-http-agent", Name: "HTTP seed target", SystemPrompt: "test"}
	if checkErr := storetest.HistoricalProfile(ctx, st, agent); checkErr != nil {
		t.Fatal(checkErr)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "nanite.feature")
	if checkErr := os.Mkdir(dir, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	block := pluginapi.Block{Registers: pluginapi.Registrations{
		HTTPRoutes:  []pluginapi.Route{{Method: "GET", Path: "items"}, {Method: "POST", Path: "items"}, {Method: "GET", Path: "items/"}, {Method: "PUT", Path: "items/"}, {Method: "PATCH", Path: "items/"}, {Method: "DELETE", Path: "items/"}},
		ReflexSeeds: []pluginapi.ReflexSeed{{ID: "search-first", AgentSlug: agent.Slug, Reminder: "Search the feature before answering.", Trigger: pluginapi.ReflexPredicate{Kind: "tool_calls_window", Window: 2, Op: "=", Value: 0}}},
	}}
	path := writeAPIPluginBundle(t, dir, "nanite.feature", "Feature", block)
	review, err := naniteplugin.BuildInstallReview(ctx, dir)
	if err != nil || len(review.ReflexSeeds) != 1 || review.ReflexSeeds[0].Reminder != block.Registers.ReflexSeeds[0].Reminder {
		t.Fatalf("seed default absent from review: %+v %v", review, err)
	}
	mux := http.NewServeMux()
	host := naniteplugin.NewHost(mux, naniteplugin.NewLogger("reflex-http-test"))
	host.SetReflexSeedRegistrar(service.NewPluginReflexSeeds(st))
	pms := &pluginManagerState{pluginHost: host}
	const query = `SELECT * FROM agent_reflexes ORDER BY id`
	a := &testAPI{store: st}
	before := retiredAPISnapshot(t, a, query)
	if pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("legacy mutable seed activated")
	}
	retiredAPIHistoryUnchanged(t, a, query, before)
	// The HTTP transport remains supported when the plugin makes no retired
	// mutable policy registration. Its identity does not confer actor grants.
	block.Registers.ReflexSeeds = nil
	path = writeAPIPluginBundle(t, dir, "nanite.feature", "Feature", block)
	if !pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("seed-free HTTP load failed")
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("nanite.feature") })
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		target := "/api/plugins/nanite.feature/items/one%2Ftwo?a=1&a=2&empty=&bare"
		if method == "POST" {
			target = "/api/plugins/nanite.feature/items?a=1&a=2&empty=&bare"
		}
		request := httptest.NewRequest(method, target, strings.NewReader(`{"title":"saved"}`))
		request.Header.Set("Authorization", "Bearer private-host-token")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, request)
		var captured struct {
			Method        string `json:"method"`
			ID            string `json:"id"`
			RawQuery      string `json:"raw_query"`
			Body          string `json:"body"`
			Authorization string `json:"authorization"`
		}
		if checkErr := json.Unmarshal(rec.Body.Bytes(), &captured); checkErr != nil || rec.Code != 200 || captured.Method != method || captured.RawQuery != "a=1&a=2&empty=&bare" || captured.Authorization != "" || captured.Body != `{"title":"saved"}` {
			t.Fatalf("HTTP extraction lost request: %d %+v %v", rec.Code, captured, checkErr)
		}
		if method != "POST" && captured.ID != "one/two" {
			t.Fatalf("escaped item ID lost: %+v", captured)
		}
	}
	delimiters := httptest.NewRecorder()
	mux.ServeHTTP(delimiters, httptest.NewRequest("GET", "/api/plugins/nanite.feature/items/one%3Ftwo%23three?real=query", nil))
	var item struct {
		ID       string `json:"id"`
		RawQuery string `json:"raw_query"`
	}
	if checkErr := json.Unmarshal(delimiters.Body.Bytes(), &item); checkErr != nil || delimiters.Code != 200 || item.ID != "one?two#three" || item.RawQuery != "real=query" {
		t.Fatalf("escaped delimiters changed routing: %+v status=%d error=%v", item, delimiters.Code, checkErr)
	}
	bad := httptest.NewRecorder()
	mux.ServeHTTP(bad, httptest.NewRequest("GET", "/api/plugins/nanite.feature/items/invalid-status", nil))
	if bad.Code != 502 {
		t.Fatalf("invalid child status accepted: %d", bad.Code)
	}
	if !pms.unloadPluginFromHost(path) {
		t.Fatal("unload failed")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/plugins/nanite.feature/items", nil))
	if rec.Code != 404 {
		t.Fatalf("unloaded route retained: %d", rec.Code)
	}
	if !pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("reload failed")
	}
	// Stop the SDK child directly: the loader-owned OnUnload must revoke
	// source eligibility even without Host.UnloadPlugin's redundant cleanup.
	loaded, ok := host.GetPlugin("nanite.feature")
	child, childOK := loaded.(*subprocess.SubprocessPlugin)
	if !ok || !childOK {
		t.Fatal("SDK child unavailable")
	}
	if checkErr := child.Unload(); checkErr != nil {
		t.Fatal(checkErr)
	}
	// A later registration conflict leaves durable defaults inactive.
	if !pms.unloadPluginFromHost(path) {
		t.Fatal("second unload failed")
	}
	if checkErr := host.RegisterEnvelope(naniteplugin.EnvelopeRegistryEntry{Type: "occupied-seed-test", PluginID: "other", Component: "Other"}); checkErr != nil {
		t.Fatal(checkErr)
	}
	block.UI = pluginapi.UI{Bundle: "ui/index.js"}
	block.Registers.Envelopes = []pluginapi.Envelope{{Type: "occupied-seed-test", Component: "Card", Version: 1, Schema: "schema.json"}}
	path = writeAPIPluginBundle(t, dir, "nanite.feature", "Feature", block)
	if pms.runPluginLoadIntoHost(path, dir) {
		t.Fatal("conflicting registration succeeded")
	}
	retiredAPIHistoryUnchanged(t, a, query, before)
}
