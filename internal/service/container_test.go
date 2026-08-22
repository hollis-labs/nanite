package service

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

type blockingShutdownChatService struct {
	ChatService
	shutdownCalls atomic.Int32
	started       chan struct{}
	release       chan struct{}
	startedOnce   sync.Once
}

func (s *blockingShutdownChatService) Shutdown() {
	s.shutdownCalls.Add(1)
	s.startedOnce.Do(func() { close(s.started) })
	<-s.release
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

func TestNewContainer_PostReaperFailureStopsReapers(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(context.Background(), filepath.Join(root, "test.db"))
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
