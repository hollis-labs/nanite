package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

type compactionFailingContext struct {
	service.ContextService
	received context.Context
}

func (c *compactionFailingContext) AssembleSlots(ctx context.Context, _ *store.Session, _ *store.AgentProfile, _ []llmtypes.ToolDefinition, _ string, _ int, _ string) (*service.SlotAssemblyResult, error) {
	c.received = ctx
	return nil, sql.ErrNoRows
}

// A downstream missing row is an assembly failure, not a missing session.
// It must not emit compaction events or mask the error with a 404 response.
func TestCompactSession_AssemblyFailureUsesCallerContext(t *testing.T) {
	a, mux := newTestAPI(t)
	const sid = "compact-assembly-failure"
	seedCompactSession(t, a, sid, 0)
	assembly := &compactionFailingContext{ContextService: a.Services.Context}
	a.Services.Context = assembly
	rec := &compactRecorder{EventEmitter: a.Services.Events}
	a.Services.Events = rec
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+"/compact", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || errorBody(t, w) != sql.ErrNoRows.Error() {
		t.Fatalf("assembly failure: %d %s", w.Code, w.Body.String())
	}
	if assembly.received != ctx {
		t.Fatal("assembly did not receive caller context")
	}
	if len(rec.pre) != 0 || len(rec.post) != 0 {
		t.Fatalf("assembly failure emitted events: pre=%v post=%v", rec.pre, rec.post)
	}
}

func TestCompactSession_ServiceErrors(t *testing.T) {
	a, _ := newTestAPI(t)
	result, err := a.Services.CompactSession(context.Background(), "missing")
	if result != nil || !errors.Is(err, service.ErrCompactionSessionNotFound) {
		t.Fatalf("missing session = %+v, %v", result, err)
	}
	const sid = "compact-canceled"
	seedCompactSession(t, a, sid, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = a.Services.CompactSession(ctx, sid)
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compaction = %+v, %v", result, err)
	}
}
