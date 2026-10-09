package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginsdk "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

type blockingShutdownChatService struct {
	ChatService
	shutdownCalls atomic.Int32
	shutdownErr   error
	started       chan struct{}
	release       chan struct{}
	finished      chan struct{}
	startedOnce   sync.Once
}

type shutdownOrderingPlugin struct {
	unloaded      atomic.Bool
	unloadStarted chan struct{}
	unloadRelease chan struct{}
}

func (*shutdownOrderingPlugin) ID() string                { return "shutdown-ordering" }
func (*shutdownOrderingPlugin) Name() string              { return "shutdown-ordering" }
func (*shutdownOrderingPlugin) Version() string           { return "test" }
func (*shutdownOrderingPlugin) Description() string       { return "test" }
func (*shutdownOrderingPlugin) Dependencies() []string    { return nil }
func (*shutdownOrderingPlugin) Load(pluginsdk.Host) error { return nil }
func (p *shutdownOrderingPlugin) Unload() error {
	if p.unloadStarted != nil {
		close(p.unloadStarted)
	}
	if p.unloadRelease != nil {
		<-p.unloadRelease
	}
	p.unloaded.Store(true)
	return nil
}
func (*shutdownOrderingPlugin) Status() pluginsdk.PluginStatus {
	return pluginsdk.PluginStatus{Loaded: true}
}

func (s *blockingShutdownChatService) Shutdown() error {
	s.shutdownCalls.Add(1)
	s.startedOnce.Do(func() { close(s.started) })
	<-s.release
	if s.finished != nil {
		close(s.finished)
	}
	return s.shutdownErr
}

func TestNewRuntimeAdapterRegistry_RegistersBuiltins(t *testing.T) {
	reg := newRuntimeAdapterRegistry()

	for _, name := range []string{"claude", "codex", "opencode", "nanite-native"} {
		if _, ok := reg.GetAdapter(name); !ok {
			t.Fatalf("expected adapter %q to be registered", name)
		}
	}
}

func TestContainer_ShutdownIsIdempotent(t *testing.T) {
	chatService := &blockingShutdownChatService{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	container := &Container{Chat: chatService}

	firstDone := make(chan struct{})
	go func() {
		container.Shutdown()
		close(firstDone)
	}()

	select {
	case <-chatService.started:
	case <-time.After(time.Second):
		t.Fatal("first Shutdown did not reach the chat subsystem")
	}

	secondDone := make(chan struct{})
	go func() {
		container.Shutdown()
		close(secondDone)
	}()

	select {
	case <-secondDone:
		t.Fatal("concurrent Shutdown returned before the in-flight shutdown completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(chatService.release)
	for call, done := range map[string]<-chan struct{}{
		"first":  firstDone,
		"second": secondDone,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s Shutdown did not complete", call)
		}
	}

	sequentialDone := make(chan struct{})
	go func() {
		container.Shutdown()
		close(sequentialDone)
	}()
	select {
	case <-sequentialDone:
	case <-time.After(time.Second):
		t.Fatal("sequential Shutdown did not return")
	}

	if got := chatService.shutdownCalls.Load(); got != 1 {
		t.Fatalf("chat Shutdown calls = %d, want 1", got)
	}
}

func TestContainer_ShutdownDrainsChatBeforeUnloadingPlugins(t *testing.T) {
	chatService := &blockingShutdownChatService{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	host := hostplugin.NewHost(nil, hostplugin.NewLogger("shutdown-order-test"))
	p := &shutdownOrderingPlugin{}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	container := &Container{Chat: chatService, Plugins: host}

	done := make(chan struct{})
	go func() { container.Shutdown(); close(done) }()
	select {
	case <-chatService.started:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not reach chat")
	}
	time.Sleep(25 * time.Millisecond)
	if p.unloaded.Load() {
		t.Fatal("plugin unloaded before chat lifecycle drained")
	}

	close(chatService.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("container shutdown did not complete")
	}
	if !p.unloaded.Load() {
		t.Fatal("plugin host was not shut down after chat drained")
	}
}

func TestContainer_ChatDrainFailureSkipsPluginUnload(t *testing.T) {
	chatService := &blockingShutdownChatService{
		started:     make(chan struct{}),
		release:     make(chan struct{}),
		shutdownErr: errors.New("lifecycle did not drain"),
	}
	close(chatService.release)
	host := hostplugin.NewHost(nil, hostplugin.NewLogger("shutdown-failed-drain-test"))
	p := &shutdownOrderingPlugin{}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	t.Cleanup(func() { _ = host.Shutdown() })

	container := &Container{Chat: chatService, Plugins: host}
	container.Shutdown()
	if p.unloaded.Load() {
		t.Fatal("plugin unloaded after chat reported a failed lifecycle drain")
	}
}

func TestContainer_ShutdownJoinsPluginUnloadOnceStarted(t *testing.T) {
	chatService := &blockingShutdownChatService{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	close(chatService.release)
	host := hostplugin.NewHost(nil, hostplugin.NewLogger("shutdown-join-unload-test"))
	p := &shutdownOrderingPlugin{
		unloadStarted: make(chan struct{}),
		unloadRelease: make(chan struct{}),
	}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	container := &Container{Chat: chatService, Plugins: host}

	done := make(chan struct{})
	go func() { container.Shutdown(); close(done) }()
	select {
	case <-p.unloadStarted:
	case <-time.After(time.Second):
		t.Fatal("plugin unload did not start after chat drained")
	}
	select {
	case <-done:
		t.Fatal("Container.Shutdown returned while plugin unload was blocked")
	case <-time.After(25 * time.Millisecond):
	}
	close(p.unloadRelease)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Container.Shutdown did not join completed plugin unload")
	}
}

func TestContainer_ShutdownTimeoutDoesNotUnloadPluginsLater(t *testing.T) {
	chatService := &blockingShutdownChatService{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	host := hostplugin.NewHost(nil, hostplugin.NewLogger("shutdown-timeout-order-test"))
	p := &shutdownOrderingPlugin{}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	t.Cleanup(func() { _ = host.Shutdown() })
	container := &Container{Chat: chatService, Plugins: host}

	done := make(chan struct{})
	go func() { container.shutdownWithMaxWait(25 * time.Millisecond); close(done) }()
	select {
	case <-chatService.started:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not reach chat")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("container shutdown did not honor its timeout")
	}
	if p.unloaded.Load() {
		t.Fatal("plugin unloaded while chat shutdown was blocked")
	}

	close(chatService.release)
	select {
	case <-chatService.finished:
	case <-time.After(time.Second):
		t.Fatal("late chat shutdown did not finish after release")
	}
	time.Sleep(50 * time.Millisecond)
	if p.unloaded.Load() {
		t.Fatal("plugin unloaded after Container.Shutdown returned")
	}
}

func TestNewContainer_PostReaperFailureStopsReapers(t *testing.T) {
	root := t.TempDir()
	st, err := storetest.New(t, context.Background(), filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	beforeSubagent, beforeRuntime := reaperGoroutineCounts(t)
	beforeCatalog := settledModelCatalogGoroutineCount(t, 300*time.Millisecond)
	beforeDecay := tesseractDecayGoroutineCount(t)
	_, err = NewContainer(ContainerConfig{
		// The refresher's fetch completes only 200ms after its cancel, so a
		// failed build that cancels without waiting leaves it visible below
		// (CW-20260930-0105; before this seam the check caught 0/20).
		ModelCatalogOptions:            modelsdevtest.LingerAfterCancel(t, 200*time.Millisecond),
		Store:                          st,
		Providers:                      provider.NewRegistry(),
		WorkingDir:                     root,
		DurableAgentRecipeCatalogPaths: []string{filepath.Join(root, "missing-recipes.yaml")},
	})
	if err == nil || !strings.Contains(err.Error(), "durable agent recipes") {
		t.Fatalf("NewContainer error = %v, want durable agent recipes failure", err)
	}

	// No polling for the refresher: catalogDone closes only after Run has
	// returned, so its frame is gone once NewContainer's wait completes
	// (CW-20260930-0103).
	if afterCatalog := modelCatalogGoroutineCount(t); afterCatalog > beforeCatalog {
		t.Fatalf("model catalog refresher survived failed construction: %d -> %d", beforeCatalog, afterCatalog)
	}
	// Close joins Tesseract's workers, and the decay loop ticks hourly, so an
	// unclosed instance stays visible indefinitely.
	if afterDecay := tesseractDecayGoroutineCount(t); afterDecay > beforeDecay {
		t.Fatalf("Tesseract decay goroutine survived failed construction: %d -> %d", beforeDecay, afterDecay)
	}

	// The reapers' Stop returns once the loop's deferred closeDone runs,
	// which is before the loop frame itself unwinds, so their goroutines
	// can still be visible for an instant.
	deadline := time.Now().Add(2 * time.Second)
	for {
		afterSubagent, afterRuntime := reaperGoroutineCounts(t)
		if afterSubagent <= beforeSubagent && afterRuntime <= beforeRuntime {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reaper goroutines survived failed construction: subagent %d -> %d, runtime %d -> %d",
				beforeSubagent, afterSubagent, beforeRuntime, afterRuntime)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Shutdown must not return while the model catalog refresher is still
// running: its OnRefresh hook writes the models table, and the process
// owner closes the store as soon as Shutdown returns (CW-20260930-0103).
func TestContainer_ShutdownWaitsForModelCatalogRefresher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var exited atomic.Bool
	go func() {
		defer close(done)
		<-ctx.Done()
		// Stand-in for a refresh that finished its fetch as the cancel
		// landed and is still syncing.
		time.Sleep(100 * time.Millisecond)
		exited.Store(true)
	}()
	container := &Container{stopModelCatalog: cancel, modelCatalogDone: done}

	container.Shutdown()
	if !exited.Load() {
		t.Fatal("Shutdown returned before the model catalog refresher exited")
	}
}

// The wait is bounded by the shutdown deadline, like every other subsystem.
func TestContainer_ShutdownModelCatalogWaitIsBounded(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	container := &Container{stopModelCatalog: cancel, modelCatalogDone: make(chan struct{})}

	returned := make(chan struct{})
	go func() {
		container.shutdownWithMaxWait(50 * time.Millisecond)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown blocked past its deadline on a refresher that never exits")
	}
}

func TestNewContainer_TesseractDBIsPackageTempIsolated(t *testing.T) {
	layout, err := config.ResolveTesseractLayout()
	if err != nil {
		t.Fatalf("ResolveTesseractLayout: %v", err)
	}
	for label, path := range map[string]string{
		"data":    layout.DataDir(),
		"state":   layout.StateDir(),
		"cache":   layout.CacheDir(),
		"config":  layout.ConfigDir(),
		"main DB": layout.MainDB(),
	} {
		assertServiceTestPathUnder(t, serviceTestRoot, label, path)
	}

	root := t.TempDir()
	st, err := storetest.New(t, context.Background(), filepath.Join(root, "nanite.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	container, err := NewContainer(ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               st,
		Providers:           provider.NewRegistry(),
		WorkingDir:          root,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(container.Shutdown)
	if container.Tesseract == nil {
		t.Fatal("NewContainer did not open Tesseract")
	}

	var openedDB string
	rows, err := container.Tesseract.MemoryStore().DB().Query("PRAGMA database_list")
	if err != nil {
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			t.Fatalf("scan database_list: %v", err)
		}
		if name == "main" {
			openedDB = path
			break
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("database_list rows: %v", err)
	}
	if openedDB == "" {
		t.Fatal("Tesseract main database path not found")
	}
	assertServiceTestPathUnder(t, serviceTestRoot, "opened main DB", openedDB)
	if canonicalTestPath(t, openedDB) != canonicalTestPath(t, layout.MainDB()) {
		t.Fatalf("opened Tesseract DB = %q, resolved DB = %q", openedDB, layout.MainDB())
	}
}

func TestNewContainer_ExternalTesseractDoesNotOpenEmbeddedOwner(t *testing.T) {
	root := t.TempDir()
	st, err := storetest.New(t, context.Background(), filepath.Join(root, "nanite.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	container, err := NewContainer(ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               st, Providers: provider.NewRegistry(), WorkingDir: root,
		DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(container.Shutdown)
	if container.Tesseract != nil || container.Memory != nil {
		t.Fatalf("external mode opened embedded owner: tesseract=%v memory=%v", container.Tesseract, container.Memory)
	}
}

func TestNewContainer_MissingLegacySourceWithJournalDisablesEmbeddedTesseract(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	targetDB := filepath.Join(root, "xdg", "data", "tesseract", "workspaces", "default", "main.db")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg", "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg", "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg", "config"))
	t.Setenv("TESSERACT_DB_PATH", targetDB)
	t.Setenv("TESSERACT_WORKSPACE", "default")

	layout, err := config.ResolveTesseractLayout()
	if err != nil {
		t.Fatalf("ResolveTesseractLayout: %v", err)
	}
	targetRecords := filepath.Join(layout.StateDir(), "records")
	if mkdirErr := os.MkdirAll(filepath.Dir(targetDB), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	journalData, err := json.Marshal(map[string]string{
		"source_db":      filepath.Join(home, ".conduit", "data", "index", "context.db"),
		"source_records": filepath.Join(home, ".conduit", "data", "records"),
		"target_db":      targetDB,
		"target_records": targetRecords,
	})
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.json")
	if writeErr := os.WriteFile(journal, journalData, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	st, err := storetest.New(t, context.Background(), filepath.Join(root, "nanite.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	container, err := NewContainer(ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               st, Providers: provider.NewRegistry(), WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(container.Shutdown)
	if container.Tesseract != nil || container.Memory != nil {
		t.Fatalf("interrupted migration opened an empty embedded store: tesseract=%v memory=%v", container.Tesseract, container.Memory)
	}
	if _, statErr := os.Lstat(targetDB); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("interrupted migration created target DB: %v", statErr)
	}
	if _, statErr := os.Lstat(journal); statErr != nil {
		t.Fatalf("interrupted migration removed its recovery journal: %v", statErr)
	}
}

func TestNewContainer_EmptyUnjournaledTargetDBDisablesEmbeddedTesseract(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	targetDB := filepath.Join(root, "xdg", "data", "tesseract", "workspaces", "default", "main.db")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg", "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg", "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg", "config"))
	t.Setenv("TESSERACT_DB_PATH", targetDB)
	t.Setenv("TESSERACT_WORKSPACE", "default")
	if err := os.MkdirAll(filepath.Dir(targetDB), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetDB, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := storetest.New(t, context.Background(), filepath.Join(root, "nanite.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	container, err := NewContainer(ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               st, Providers: provider.NewRegistry(), WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(container.Shutdown)
	if container.Tesseract != nil || container.Memory != nil {
		t.Fatalf("empty unjournaled target opened an embedded store: tesseract=%v memory=%v", container.Tesseract, container.Memory)
	}
	info, err := os.Lstat(targetDB)
	if err != nil {
		t.Fatalf("empty target DB was removed: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("empty target DB was initialized during validation: size=%d", info.Size())
	}
}

func assertServiceTestPathUnder(t *testing.T, root, label, path string) {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return
	}
	canonicalRoot := canonicalTestPath(t, root)
	canonicalPath := canonicalTestPath(t, path)
	rel, err = filepath.Rel(canonicalRoot, canonicalPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("%s path %q escapes service test root %q", label, path, root)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize test path %q: %v", path, err)
	}
	return canonical
}

func modelCatalogGoroutineCount(t *testing.T) int {
	t.Helper()
	var stacks bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
		t.Fatalf("write goroutine profile: %v", err)
	}
	return strings.Count(stacks.String(), "go-modelsdev/modelsdev.(*Client).Run")
}

// settledModelCatalogGoroutineCount returns the refresher count once it has
// held steady for window. A refresher an earlier test or iteration left
// lingering (see modelsdevtest.LingerAfterCancel) would otherwise inflate
// the baseline and mask a new leak under -count.
func settledModelCatalogGoroutineCount(t *testing.T, window time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	count, steadySince := modelCatalogGoroutineCount(t), time.Now()
	for time.Since(steadySince) < window && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		if now := modelCatalogGoroutineCount(t); now != count {
			count, steadySince = now, time.Now()
		}
	}
	return count
}

func tesseractDecayGoroutineCount(t *testing.T) int {
	t.Helper()
	var stacks bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
		t.Fatalf("write goroutine profile: %v", err)
	}
	return strings.Count(stacks.String(), "tesseract/internal/memory.(*DecayJob).Run")
}

func reaperGoroutineCounts(t *testing.T) (subagent, runtime int) {
	t.Helper()
	var stacks bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
		t.Fatalf("write goroutine profile: %v", err)
	}
	profile := stacks.String()
	return strings.Count(profile, "internal/subagent.(*Reaper).loop"),
		strings.Count(profile, "internal/recovery/orphansweep.(*RuntimeReaper).loop")
}
