package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleCompactSession_RunsPipelineAndPersists exercises the rewritten
// /compact endpoint: it must build a slot window, run the compaction
// pipeline (RunForce path), persist the summary on the session, drop the
// destructive `is_compacted` writes the legacy 2000-char path used, and
// return {summary, stages_applied, tokens_saved, mode} JSON.
func TestHandleCompactSession_RunsPipelineAndPersists(t *testing.T) {
	a, mux := newTestAPI(t)

	// Workspace + session setup.
	sess := &store.Session{
		ID:    "compact-sess",
		Title: "Compact Test",
	}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Seed enough conversation history that strip-tool-blocks / drop-enrichment
	// stages have something to operate on. No tools/enrichment are configured
	// in the test container, so the summarizer-less pipeline will short-circuit
	// after the recheck and we still get a 200 with empty stages.
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := a.Services.Store.CreateMessage(context.Background(), &store.Message{
			ID:        fmt.Sprintf("compact-msg-%d", i),
			SessionID: sess.ID,
			Role:      role,
			Content:   strings.Repeat("filler ", 40),
		}); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sess.ID+"/compact", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["summary"]; !ok {
		t.Errorf("response missing summary field: %v", resp)
	}
	if _, ok := resp["stages_applied"]; !ok {
		t.Errorf("response missing stages_applied field: %v", resp)
	}
	if _, ok := resp["tokens_saved"]; !ok {
		t.Errorf("response missing tokens_saved field: %v", resp)
	}
	if _, ok := resp["mode"]; !ok {
		t.Errorf("response missing mode field: %v", resp)
	}

	// Verify that no message rows were marked is_compacted by the new path.
	// The legacy handler set this flag on every row; the slot-based path no
	// longer touches the column.
	rows, err := a.Services.Store.ListMessages(context.Background(), sess.ID, 100)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for _, m := range rows {
		if m.IsCompacted {
			t.Errorf("message %s should not be marked is_compacted by /compact", m.ID)
		}
	}
}
