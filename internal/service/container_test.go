package service

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/config"
	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/storetest"
	pluginsdk "github.com/hollis-labs/plugin-sdk"
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

	for _, name := range []string{"claude", "codex", "gemini", "opencode", "nanite-native"} {
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
	_, err = NewContainer(ContainerConfig{
		Store:                          st,
		Providers:                      provider.NewRegistry(),
		WorkingDir:                     root,
		ManagedConfigRoot:              filepath.Join(root, ".nanite"),
		DurableAgentRecipeCatalogPaths: []string{filepath.Join(root, "missing-recipes.yaml")},
	})
	if err == nil || !strings.Contains(err.Error(), "durable agent recipes") {
		t.Fatalf("NewContainer error = %v, want durable agent recipes failure", err)
	}

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
		Store:             st,
		Providers:         provider.NewRegistry(),
		WorkingDir:        root,
		ManagedConfigRoot: filepath.Join(root, ".nanite"),
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(container.Shutdown)
	if container.Conduit == nil {
		t.Fatal("NewContainer did not open Conduit")
	}

	var openedDB string
	rows, err := container.Conduit.MemoryStore().DB().Query("PRAGMA database_list")
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
