package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/tools"
)

// panicTool is a host tool whose handler panics with a value that looks
// like a secret, which must not reach the model or the error text.
var panicTool = tools.Tool{Name: "explode", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
	Handler: func(context.Context, tools.Call) (tools.Result, error) { panic("sk-ant-secretvalue0123456789abcdef") }}

func sendAndWait(t *testing.T, e *Engine, id, text string) error {
	t.Helper()
	run, err := e.Send(t.Context(), id, Input{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	_, err = run.Wait()
	return err
}

func TestPanickingHostToolYieldsErrorResult(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(toolResponse("explode"), fake.Text("handled"), fake.Text("next turn")), "")
	var reported []panics.Record
	e.panics = newGuard(e.cfg.Logger, e.scrub, panics.ReporterFunc(func(r panics.Record) { reported = append(reported, r) }))
	if err := e.cfg.Tools.SetGroup("host", panicTool); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWait(t, e, id, "go"); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) < 3 {
		t.Fatalf("history %+v, %v", history, err)
	}
	result := history[2].Blocks[0]
	if !result.IsError || !strings.Contains(result.Content, "internal error (ref pnc_") || strings.Contains(result.Content, "secretvalue") {
		t.Fatalf("tool result %+v", result)
	}
	if len(reported) != 1 || strings.Contains(reported[0].Value, "secretvalue0123456789") || reported[0].Attrs["call"] != "call-1" {
		t.Fatalf("reported %+v", reported)
	}
	if err := sendAndWait(t, e, id, "again"); err != nil {
		t.Fatalf("next turn failed: %v", err)
	}
}

type panicSource struct{}

func (panicSource) Name() string { return "host" }
func (panicSource) Slots(context.Context, slots.TurnInfo) ([]slots.Update, error) {
	panic("slot source exploded")
}

func TestPanickingSlotSourceFailsTheTurnOnly(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("next turn")), "")
	e.cfg.SlotSources = []slots.Source{panicSource{}}
	err := sendAndWait(t, e, id, "go")
	if ref, ok := panics.Ref(err); !ok || !strings.HasPrefix(ref, "pnc_") {
		t.Fatalf("turn error %v", err)
	}
	e.cfg.SlotSources = nil
	if err := sendAndWait(t, e, id, "again"); err != nil {
		t.Fatalf("next turn failed: %v", err)
	}
}

func TestPanickingProviderStreamFailsTheTurn(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(fake.Response{Panic: "stream exploded"}, fake.Text("next turn")), "")
	err := sendAndWait(t, e, id, "go")
	if err == nil || !strings.Contains(err.Error(), "internal error (ref pnc_") {
		t.Fatalf("turn error %v", err)
	}
	history, herr := db.History(t.Context(), id)
	if herr != nil || len(history) == 0 {
		t.Fatalf("history %+v, %v", history, herr)
	}
	if status, err := db.TurnStatus(t.Context(), history[0].TurnID); err != nil || status != "failed" {
		t.Fatalf("turn status %q, %v", status, err)
	}
	if err := sendAndWait(t, e, id, "again"); err != nil {
		t.Fatalf("next turn failed: %v", err)
	}
}

type panicGate struct{}

func (panicGate) Decide(context.Context, permission.PermissionRequest) (permission.PermissionDecision, error) {
	panic("gate exploded")
}

func TestPanickingGateDenies(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(toolResponse("explode"), fake.Text("done")), "")
	e.cfg.Gate = panicGate{}
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: explode\n      behavior: ask\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "explode", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) {
			t.Error("tool ran after a panicking gate")
			return tools.Result{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWait(t, e, id, "go"); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) < 3 || !strings.Contains(history[2].Blocks[0].Content, "permission denied") {
		t.Fatalf("history %+v, %v", history, err)
	}
}

func TestPanickingEventSinkIsContained(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("done")), "")
	e.runtime = events.NewRuntime(runtimeevents.SinkFunc(func(context.Context, runtimeevents.Event) error { panic("sink exploded") }), e.cfg.Logger)
	if err := sendAndWait(t, e, id, "go"); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
}

type panicPDP struct{}

func (panicPDP) Evaluate(context.Context, policy.Request) (policy.Decision, error) {
	panic("pdp exploded")
}

func TestPanickingPDPDenies(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(toolResponse("explode"), fake.Text("done")), "")
	e.cfg.PDP = panicPDP{}
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "explode", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) {
			t.Error("tool ran after a panicking PDP")
			return tools.Result{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	if err := sendAndWait(t, e, id, "go"); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) < 3 {
		t.Fatalf("history %+v, %v", history, err)
	}
	if content := history[2].Blocks[0].Content; !strings.Contains(content, "permission denied") || !strings.Contains(content, "ref pnc_") || strings.Contains(content, "exploded") {
		t.Fatalf("tool result %q", content)
	}
}

type panicSelector struct{}

func (panicSelector) Select(context.Context, slots.TurnInfo, slots.Snapshot, map[slots.Name]int) (slots.Plan, error) {
	panic("selector exploded")
}

func TestPanickingSelectorFailsTheTurnOnly(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(fake.Text("next turn")), "")
	e.cfg.SlotSelector = panicSelector{}
	err := sendAndWait(t, e, id, "go")
	if _, ok := panics.Ref(err); !ok {
		t.Fatalf("turn error %v", err)
	}
	e.cfg.SlotSelector = slots.DefaultSelector(nil)
	if err := sendAndWait(t, e, id, "again"); err != nil {
		t.Fatalf("next turn failed: %v", err)
	}
}
