package service

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md):
// end-to-end verification that a DB-configured cmd-kind resolver's
// output is verifiably folded into an agent's assembled launch content
// — the same code path driveBootSession's cold-boot branch exercises
// (resolveAgentContextForBoot → Options.DynamicContext →
// resolveBootPrompt/ResolveSystemPrompt → BuildCLAUDEMD), against a
// real (temp-dir-isolated) *store.Store, not a mock.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newContextResolverTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "context-resolver.db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	return st
}

func TestRetiredBootContextResolverCannotExecuteHistoricalCommand(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := t.Context()
	p := &store.AgentProfile{Name: "Historical resolver", Slug: "historical-resolver", SystemPrompt: "history"}
	if err := storetest.HistoricalProfile(ctx, st, p); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-execute")
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO agent_context_resolvers(id,agent_id,slot_name,kind,run,enabled) VALUES('retired-fixture',?,'dangerous','cmd',?,1)`, p.ID, "touch "+marker); err != nil {
		t.Fatal(err)
	}
	svc := &chatServiceImpl{store: st}
	if content, err := svc.resolveAgentContextForBoot(ctx, p.ID, ""); !errors.Is(err, store.ErrImmutableAgentProfile) || len(content) != 0 {
		t.Fatal(content, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("historical command executed", err)
	}
}
