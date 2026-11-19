package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/tools"
)

func TestTurnWritesOneTraceRecord(t *testing.T) {
	// The first request overflows, forcing a compaction; the retry calls a
	// host tool; the last step answers.
	p := overflowThen(toolResponse("lookup"), fake.Text("done"))
	e, db, id := setupEngine(t, p, "")
	seedLongHistory(t, db, id)
	e.cfg.TraceDir = filepath.Join(t.TempDir(), "traces")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	e.cfg.Gate = permission.AllowGate
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "lookup", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{Text: "found"}, nil }}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(e.cfg.TraceDir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("trace files %v, %v", files, err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var records []trace.Turn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var rec trace.Turn
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	if len(records) != 1 || len(sunk) != 1 {
		t.Fatalf("records in file %d, in sink %d", len(records), len(sunk))
	}
	rec := records[0]
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	if rec.ConversationID != id || rec.RunID != run.ID() || rec.Source != trace.SourceUser || rec.Role != "chat" || rec.Model == "" {
		t.Errorf("identity %+v", rec)
	}
	if len(rec.Slots) == 0 || rec.Slots[0].Name != "universal" || rec.Slots[0].Tokens == 0 {
		t.Errorf("slots %+v", rec.Slots)
	}
	found := false
	for _, name := range rec.ToolsOffered {
		found = found || name == "lookup"
	}
	if !found {
		t.Errorf("tools offered %v", rec.ToolsOffered)
	}
	if len(rec.ToolCalls) != 1 || rec.ToolCalls[0].Tool != "lookup" || rec.ToolCalls[0].Permission != trace.PermissionAllow || rec.ToolCalls[0].ErrorKind != "" {
		t.Errorf("tool calls %+v", rec.ToolCalls)
	}
	if len(rec.Compactions) != 1 || !rec.Compactions[0].Forced || rec.Compactions[0].Failed || rec.Compactions[0].After >= rec.Compactions[0].Before {
		t.Errorf("compactions %+v", rec.Compactions)
	}
	if rec.Outcome.Status != trace.StatusCompleted || rec.Outcome.FinishReason != "end_turn" || rec.Outcome.Steps != 2 {
		t.Errorf("outcome %+v", rec.Outcome)
	}
	if rec.Situation != nil || rec.Classified != nil || rec.Why != "" || rec.Fired != nil || rec.HooksRun != nil {
		t.Errorf("reserved fields filled: %+v", rec)
	}
}

func TestFailedTurnStillWritesATraceRecord(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Response{Panic: "stream exploded"}), "")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	if err := sendAndWait(t, e, id, "go"); err == nil {
		t.Fatal("turn should fail")
	}
	if len(sunk) != 1 || sunk[0].Outcome.Status != trace.StatusFailed || sunk[0].Outcome.Error == "" {
		t.Fatalf("records %+v", sunk)
	}
}

func TestPanicOutsideTheModelLoopIsRecordedAsFailed(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("unused")), "")
	e.cfg.TraceDir = filepath.Join(t.TempDir(), "traces")
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	e.testTurnHook = func() { panic("before the model loop") }
	if err := sendAndWait(t, e, id, "go"); err == nil {
		t.Fatal("turn should fail")
	}
	if len(sunk) != 1 || sunk[0].Outcome.Status != trace.StatusFailed || !strings.Contains(sunk[0].Outcome.Error, "ref pnc_") {
		t.Fatalf("records %+v", sunk)
	}
	files, _ := filepath.Glob(filepath.Join(e.cfg.TraceDir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("trace files %v", files)
	}
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("trace file mode %v, %v", info, err)
	}
}

func TestToolCallClassification(t *testing.T) {
	calls := fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "c-ok", Name: "lookup"}},
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "c-deny", Name: "sensitive"}},
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "c-unknown", Name: "nowhere"}},
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "c-panic", Name: "explode"}},
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "c-fail", Name: "failing"}},
		{Type: llmtypes.EventDone},
	}}
	e, _, id := setupEngine(t, fake.New(calls, fake.Text("done")), "")
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: sensitive\n      behavior: deny\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	schema := json.RawMessage(`{"type":"object"}`)
	ok := func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{Text: "fine"}, nil }
	if err := e.cfg.Tools.SetGroup("host",
		tools.Tool{Name: "lookup", ReadOnly: true, InputSchema: schema, Handler: ok},
		tools.Tool{Name: "sensitive", ReadOnly: true, InputSchema: schema, Handler: ok},
		tools.Tool{Name: "failing", ReadOnly: true, InputSchema: schema, Handler: func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{}, errors.New("no") }},
		panicTool,
	); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWait(t, e, id, "go"); err != nil {
		t.Fatal(err)
	}
	if len(sunk) != 1 {
		t.Fatalf("records %d", len(sunk))
	}
	got := map[string]string{}
	for _, c := range sunk[0].ToolCalls {
		got[c.ToolCallID] = string(c.Permission) + "/" + string(c.ErrorKind)
	}
	want := map[string]string{"c-ok": "allow/", "c-deny": "deny/denied", "c-unknown": "none/unknown_tool", "c-panic": "allow/panic", "c-fail": "allow/tool_error"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}
