package service

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestNewRuntimeAdapterRegistry_RegistersBuiltins(t *testing.T) {
	reg := newRuntimeAdapterRegistry()

	for _, name := range []string{"claude", "codex", "gemini", "opencode", "nanite-native"} {
		if _, ok := reg.GetAdapter(name); !ok {
			t.Fatalf("expected adapter %q to be registered", name)
		}
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
