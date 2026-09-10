package agentimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	adapterclaude "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-claude"
)

// TestConcurrentImportsShareOneRegistry probes the shape the REST surface
// actually creates and no other test here does: many simultaneous imports,
// each with its OWN Importer (per-request, as internal/api builds them), all
// reading through ONE shared *agent.AdapterRegistry held on the service
// container for the process's lifetime.
//
// Two things are being checked, and neither is provable by inspection:
//
//   - AdapterRegistry.ImportAll takes a read lock only to copy the adapter
//     slice and then does filesystem I/O outside it. That is the shape
//     DiscoverAll had, but ImportAll rewrote the body, so the property is
//     asserted here rather than inherited.
//   - A registered adapter is reachable from every request goroutine at
//     once. adapterclaude.Adapter carries no per-call state, which is easy
//     to say and easy to break later; run under -race, this test says it.
//
// Each goroutine imports a distinct slug, so the pipeline's own
// create-or-refuse decision is not what is under test — the shared reads are.
func TestConcurrentImportsShareOneRegistry(t *testing.T) {
	st := newTestStore(t)

	registry := agent.NewAdapterRegistry()
	registry.Register(adapterclaude.New().Adapter())

	const n = 24
	root := t.TempDir()
	for i := range n {
		dir := filepath.Join(root, fmt.Sprintf("proj-%02d", i), adapterclaude.SubagentsDir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		body := fmt.Sprintf("---\nname: agent-%02d\ndescription: fixture %d\n---\nPrompt %d.\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release together, so the reads genuinely overlap
			imp := &Importer{
				Store: st,
				Parse: ChainParser{Parsers: []Parser{
					NativeParser{},
					RegistryParser{Registry: registry},
				}},
			}
			_, err := imp.Import(context.Background(), Source{
				Path: filepath.Join(root, fmt.Sprintf("proj-%02d", i)),
			})
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("import %d: %v", i, err)
		}
	}
	for i := range n {
		slug := fmt.Sprintf("agent-%02d", i)
		row, err := st.GetAgentBySlug(context.Background(), slug)
		if err != nil {
			t.Errorf("%s missing after concurrent import: %v", slug, err)
			continue
		}
		if row.Source != SourceProvenance || row.OriginSystem != adapterclaude.AdapterName {
			t.Errorf("%s: source=%q origin=%q", slug, row.Source, row.OriginSystem)
		}
	}
}

// TestConcurrentRegisterDuringImport is the harsher case: an adapter being
// registered while imports are in flight. ImportAll must not observe a
// half-sorted slice — Register re-sorts under the write lock, and Adapters()
// copies under the read lock before any I/O begins.
func TestConcurrentRegisterDuringImport(t *testing.T) {
	st := newTestStore(t)
	registry := agent.NewAdapterRegistry()
	registry.Register(adapterclaude.New().Adapter())

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "solo.md"),
		[]byte("---\nname: solo\n---\nPrompt.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 50 {
			registry.Register(adapterclaude.New().Adapter())
		}
	}()
	go func() {
		defer wg.Done()
		imp := &Importer{Store: st, Parse: RegistryParser{Registry: registry}}
		for range 50 {
			if _, err := imp.Import(context.Background(), Source{Path: dir}); err != nil {
				t.Errorf("import during Register: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if _, err := st.GetAgentBySlug(context.Background(), "solo"); err != nil {
		t.Errorf("solo missing: %v", err)
	}
}
