package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/agent/trace"
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
