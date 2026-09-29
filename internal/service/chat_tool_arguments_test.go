package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/tool"
)

// D-35: the executor persists redacted, bounded arguments for every executed
// tool call, against the real migrated schema, and never for calls that did
// not run.
func TestChatToolArguments_PersistedRedactedAndBounded(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "args.db"))
	if err != nil {
		t.Fatalf("storetest.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })

	svc := makeErrorHonestyService()
	svc.resultCache = tool.NewResultCache(st.DB, tool.ResultCacheConfig{ArgumentBudgetBytes: 400})

	ran := llmtypes.ToolUseBlock{ID: "ran", Name: "http_request", Input: map[string]any{
		"url":     "https://example.com/audit-me",
		"headers": map[string]any{"Authorization": "Bearer live-secret-token-value"},
		"body":    strings.Repeat("payload ", 500),
	}}
	blocked := llmtypes.ToolUseBlock{ID: "blocked", Name: "http_request", Input: map[string]any{"url": "https://never-ran.example"}}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 32)
	svc.postProcessToolResults(ctx,
		[]toolPlan{
			{tu: ran, status: toolPlanReady},
			{tu: blocked, status: toolPlanBlocked},
		},
		[]toolExecResult{{rawOutput: "ok", ref: chat.ToolCallRef{ID: ran.ID, Name: ran.Name}}, {}},
		ls, ch, "session-args", "agent", "message", "")

	got, err := svc.resultCache.ListArguments("session-args")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("persisted %d records, want exactly the executed call: %+v", len(got), got)
	}
	rec := got[0]
	if rec.ToolCallID != "ran" || rec.ToolName != "http_request" {
		t.Errorf("record = %+v", rec)
	}
	if strings.Contains(rec.Body, "live-secret-token-value") {
		t.Errorf("secret persisted: %s", rec.Body)
	}
	if !strings.Contains(rec.Body, "audit-me") || rec.RedactedCount != 1 {
		t.Errorf("audit value lost or redaction uncounted: %+v", rec)
	}
	if !rec.WasTruncated || len(rec.Body) > 400+200 {
		t.Errorf("not bounded: truncated=%v len=%d", rec.WasTruncated, len(rec.Body))
	}
}

// Persist failure must never fail or alter the tool call.
func TestChatToolArguments_PersistFailureDoesNotAffectResult(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "argfail.db"))
	if err != nil {
		t.Fatalf("storetest.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	if _, err := st.DB.Exec(`DROP TABLE tool_call_arguments`); err != nil {
		t.Fatal(err)
	}
	svc := makeErrorHonestyService()
	svc.resultCache = tool.NewResultCache(st.DB, tool.ResultCacheConfig{})
	tu := llmtypes.ToolUseBlock{ID: "x", Name: "http_request", Input: map[string]any{"token": "t"}}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 32)
	blocks, _ := svc.postProcessToolResults(ctx,
		[]toolPlan{{tu: tu, status: toolPlanReady}},
		[]toolExecResult{{rawOutput: "result-body", ref: chat.ToolCallRef{ID: tu.ID, Name: tu.Name}}},
		ls, ch, "s", "agent", "message", "")
	if len(blocks) != 1 || blocks[0].Content != "result-body" {
		t.Fatalf("tool result affected by persistence failure: %+v", blocks)
	}
}
