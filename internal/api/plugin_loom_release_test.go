package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type rejectLoomActivation struct{ *service.PluginReflexSeeds }

func (*rejectLoomActivation) ActivatePluginReflexSeeds(string) error {
	return errors.New("test late activation refusal")
}

// NANITE_LOOM_TEST_BUNDLE selects the externally verified extracted
// loom/v0.1.0 release. The ordinary suite tests ownership transfer separately.
func TestLoomPublishedReleaseAdoption(t *testing.T) {
	published := os.Getenv("NANITE_LOOM_TEST_BUNDLE")
	if published == "" {
		t.Skip("requires verified loom/v0.1.0 release bundle")
	}
	ctx := context.Background()
	st := newSeededStore(t)
	agents := map[string]*store.AgentProfile{}
	for _, slug := range []string{"loom-curator", "loom-weaver"} {
		profile := &store.AgentProfile{Slug: slug, Name: slug, SystemPrompt: "Curate fragments.", Source: "user", Durable: true}
		if err := st.CreateAgent(ctx, profile); err != nil {
			t.Fatal(err)
		}
		agents[slug] = profile
	}
	root := t.TempDir()
	directory := filepath.Join(root, "nanite.loom")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	input, err := os.OpenRoot(published) // #nosec G703 -- opt-in fixture selects an externally verified release directory.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	if err = fs.WalkDir(input.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
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
	}); err != nil {
		t.Fatal(err)
	}
	review, err := naniteplugin.BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if review.Version != "0.1.0" {
		t.Fatal("requires published Loom v0.1.0")
	}
	if err = naniteplugin.SaveInstallApproval(root, review, review.Digest()); err != nil {
		t.Fatal(err)
	}
	manifest, err := naniteplugin.ParseManifest(filepath.Join(directory, "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	block, err := pluginapi.DecodeBlock(manifest.Shared.Nanite)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy records are fixture state, not a second runtime seeder. Their
	// complete persisted bytes and identity must survive the ownership change.
	originals := map[string]*store.AgentReflex{}
	for _, seed := range block.Registers.ReflexSeeds {
		name := "capture_on_discovery"
		if seed.ID == "check-before-answer" {
			name = "check_before_answer"
		}
		trigger, marshalErr := json.Marshal(seed.Trigger)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		action, marshalErr := json.Marshal(map[string]string{"body": "Edited " + seed.Reminder, "urgency": "info"})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		id, insertErr := st.InsertAgentReflex(ctx, store.AgentReflex{AgentID: agents[seed.AgentSlug].ID, Name: name, TriggerKind: store.ReflexTriggerPredicate, TriggerSpec: string(trigger), ActionKind: store.ReflexActionInjectReminder, ActionSpec: string(action), CreatedBy: "system", OptOutAllowed: true, Priority: 77, FiredCount: 9, LastFiredAt: "2026-10-01"})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		original, getErr := st.GetAgentReflex(ctx, id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		originals[seed.ID] = original
	}
	check := originals["check-before-answer"]
	if _, err = st.DB.ExecContext(ctx, `UPDATE agent_reflexes SET status='paused' WHERE id=?`, check.ID); err != nil {
		t.Fatal(err)
	}
	check.Status = store.ReflexStatusPaused
	capture := originals["weaver-capture-on-discovery"]
	if err = st.SetAgentReflexOptOut(ctx, capture.AgentID, capture.ID); err != nil {
		t.Fatal(err)
	}
	runtime := &wakeRuntimeCapture{}
	durable := service.NewDurableAgentServiceWithRuntime(st, runtime)
	instance := &store.DurableAgentInstance{Name: "Curator", Slug: "loom-curator", ProfileID: agents["loom-curator"].ID, LifecycleClass: store.DurableAgentClassProcess, RuntimeKind: "api", LaunchSourceType: store.DurableAgentLaunchProcessTick, LaunchSourceID: "loom-curator", Status: store.DurableAgentStatusSleeping}
	if err = durable.Create(ctx, instance); err != nil {
		t.Fatal(err)
	}
	a := New(&service.Container{DurableAgents: durable, DurableWake: service.NewDurableAgentWakeService(st, durable)})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugin-host/durable-wake", a.handlePluginHostDurableWake)
	server := httptest.NewServer(mux)
	defer server.Close()
	host := naniteplugin.NewHost(mux, naniteplugin.NewLogger("loom-release-test"))
	host.SetStore(st)
	a.SetPluginHost(host)
	seeds := service.NewPluginReflexSeeds(st)
	host.SetReflexSeedRegistrar(&rejectLoomActivation{seeds})
	if err = host.SetHostQueryURL(server.URL); err != nil {
		t.Fatal(err)
	}
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	rejected, failures := naniteplugin.LoadDiscovered(host, discovered)
	if len(rejected) != 0 || len(failures) == 0 {
		t.Fatal("late activation refusal did not reject plugin")
	}
	var count int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_reflexes WHERE created_by='system' AND provenance_tier='system'`).Scan(&count); err != nil || count != 3 {
		t.Fatal("failed load changed core definitions", count, err)
	}
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_reflex_seed_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed load committed bindings", count, err)
	}
	host.SetReflexSeedRegistrar(seeds)
	load := func() {
		t.Helper()
		loaded, failures := naniteplugin.LoadDiscovered(host, discovered)
		if len(loaded) != 1 || len(failures) != 0 {
			t.Fatalf("load: %v %v", loaded, failures)
		}
	}
	load()
	t.Cleanup(func() { _ = host.UnloadPlugin("nanite.loom") })
	for _, original := range originals {
		got, getErr := st.GetAgentReflex(ctx, original.ID)
		if getErr != nil || got.ID != original.ID || got.Name != original.Name || got.TriggerSpec != original.TriggerSpec || got.ActionSpec != original.ActionSpec || got.Status != original.Status || got.Priority != original.Priority || got.FiredCount != original.FiredCount || got.LastFiredAt != original.LastFiredAt || got.CreatedAt != original.CreatedAt || got.CreatedBy != "plugin:nanite.loom" || got.ProvenanceTier != "plugin" {
			t.Fatal("handoff changed persisted definition", got, getErr)
		}
	}
	candidates, err := st.ListAgentReflexesForAgent(ctx, agents["loom-weaver"].ID, "")
	if err != nil || len(candidates) != 0 {
		t.Fatal("pause or opt-out lost", candidates, err)
	}
	candidates, err = st.ListAgentReflexesForAgent(ctx, agents["loom-curator"].ID, "")
	if err != nil || len(candidates) != 1 {
		t.Fatal("active contribution unavailable", candidates, err)
	}
	callback := []byte(`{"generator":"wiki_page","fragment":{"id":"release-fragment","title":"A finding"}}`)
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/plugins/nanite.loom/curator-wake", bytes.NewReader(callback)))
		return w
	}
	response := call()
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"queued"`)) {
		t.Fatalf("published callback: %d %s", response.Code, response.Body.String())
	}
	runtime.mu.Lock()
	if runtime.calls != 1 || !strings.Contains(runtime.prompt, "fragment_id: release-fragment") || !strings.Contains(runtime.prompt, "classify_and_compile_fragment") {
		t.Error("published callback did not deliver real prompt")
	}
	runtime.mu.Unlock()
	if err = host.UnloadPlugin("nanite.loom"); err != nil {
		t.Fatal(err)
	}
	candidates, err = st.ListAgentReflexesForAgent(ctx, agents["loom-curator"].ID, "")
	if err != nil || len(candidates) != 0 {
		t.Fatal("unloaded contribution executed", candidates, err)
	}
	if response = call(); response.Code != 404 {
		t.Fatal("unload retained callback", response.Code)
	}
	if err = st.DeleteAgentReflex(ctx, capture.ID); err != nil {
		t.Fatal(err)
	}
	load()
	if _, err = st.GetAgentReflex(ctx, capture.ID); !errors.Is(err, store.ErrAgentReflexNotFound) {
		t.Fatal("reload recreated deleted definition", err)
	}
	if response = call(); response.Code != 502 {
		t.Fatal("active wake claimed queued success", response.Code)
	}
	// The capture controller does not run a turn. Simulate completion's sleeping
	// state before asserting a second wake through the reloaded real plugin.
	if _, err = st.SetDurableAgentInstanceStatus(ctx, instance.ID, store.DurableAgentStatusSleeping); err != nil {
		t.Fatal(err)
	}
	if response = call(); response.Code != 200 {
		t.Fatal("reload lost callback", response.Code, response.Body.String())
	}
}
