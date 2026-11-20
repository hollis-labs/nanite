package engine

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

// subtaskCallSpec is a subtask call with its own return spec.
func subtaskCallSpec(variant, goal string, spec any) fake.Response {
	call := subtaskCall(variant, goal, false)
	call.Events[0].ToolUse.Input["return_spec"] = spec
	return call
}

type parentView struct {
	Status          string          `json:"status"`
	Result          json.RawMessage `json:"result"`
	Artifacts       []string        `json:"artifacts"`
	Notes           string          `json:"notes"`
	Reason          string          `json:"reason"`
	ResultRef       string          `json:"result_ref"`
	ResultBytes     int             `json:"result_bytes"`
	ResultExpiresAt string          `json:"result_expires_at"`
	Preview         string          `json:"preview"`
	ChildID         string          `json:"child_id"`
	Steps           int             `json:"steps"`
}

// runSubtask runs a root turn whose single subtask is answered by child and
// returns what the parent model received.
func runSubtask(t *testing.T, call fake.Response, goal string, child *fake.Provider, setup func(*Engine)) (parentView, bool, *Engine, *store.Store, string, *routedProvider) {
	t.Helper()
	p := &routedProvider{root: fake.New(call, fake.Text("parent done")), children: map[string]*fake.Provider{goal: child}}
	e, db, id := setupEngine(t, p, "")
	if setup != nil {
		setup(e)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	if len(results) != 1 {
		t.Fatalf("results %+v", results)
	}
	var view parentView
	if err := json.Unmarshal([]byte(results[0].Content), &view); err != nil {
		t.Fatalf("result %q: %v", results[0].Content, err)
	}
	return view, results[0].IsError, e, db, id, p
}

var reportSchema = map[string]any{"type": "object", "required": []any{"files"},
	"properties": map[string]any{"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}

func TestReturnResultMatchesItsSchema(t *testing.T) {
	ret := returnCall("ok", map[string]any{"files": []any{"a.go", "b.go"}})
	ret.Events[0].ToolUse.Input["artifacts"] = []any{"a.go"}
	ret.Events[0].ToolUse.Input["notes"] = "two files"
	view, failed, _, _, _, p := runSubtask(t, subtaskCallSpec("worker", "list files", reportSchema), "list files", fake.New(ret), nil)
	if failed || view.Status != "ok" || string(view.Result) != `{"files":["a.go","b.go"]}` || !slices.Equal(view.Artifacts, []string{"a.go"}) || view.Notes != "two files" || view.Steps != 1 {
		t.Fatalf("view %+v (error %v)", view, failed)
	}
	// The child's turn ended at return_result: one model request, no free-text step after it.
	if n := len(p.requests["list files"]); n != 1 {
		t.Fatalf("child made %d requests", n)
	}
	if offered := p.offered("", 0); slices.Contains(offered, "return_result") {
		t.Fatal("a root conversation was offered return_result")
	}
}

func TestReturnResultGetsOneRepair(t *testing.T) {
	bad := returnCall("ok", map[string]any{"files": "a.go"})
	good := returnCall("ok", map[string]any{"files": []any{"a.go"}})
	good.Events[0].ToolUse.ID = "ret-2"
	view, failed, _, db, _, _ := runSubtask(t, subtaskCallSpec("worker", "list files", reportSchema), "list files", fake.New(bad, good), nil)
	if failed || view.Status != "ok" || string(view.Result) != `{"files":["a.go"]}` {
		t.Fatalf("view %+v", view)
	}
	repair := toolResults(t, db, view.ChildID)
	if len(repair) != 2 || !repair[0].IsError || !strings.Contains(repair[0].Content, "does not match the return spec") || !strings.Contains(repair[0].Content, "one repair") {
		t.Fatalf("child tool results %+v", repair)
	}
}

func TestReturnResultFailingTwiceIsPartial(t *testing.T) {
	bad := returnCall("ok", map[string]any{"files": "a.go"})
	worse := returnCall("ok", map[string]any{"nothing": true})
	worse.Events[0].ToolUse.ID = "ret-2"
	view, failed, _, _, _, p := runSubtask(t, subtaskCallSpec("worker", "list files", reportSchema), "list files", fake.New(bad, worse), nil)
	if !failed || view.Status != "partial" || !strings.HasPrefix(view.Reason, "return_spec: ") || string(view.Result) != `{"nothing":true}` {
		t.Fatalf("view %+v (error %v)", view, failed)
	}
	if n := len(p.requests["list files"]); n != 2 {
		t.Fatalf("child made %d requests, want 2 (the second failure ends it)", n)
	}
}

func TestChildReportedErrorReachesTheParent(t *testing.T) {
	ret := returnCall("error", "the repository is missing")
	view, failed, _, _, _, _ := runSubtask(t, subtaskCall("worker", "fix it", false), "fix it", fake.New(ret), nil)
	if !failed || view.Status != "error" || view.Reason != "reported error by the subtask" || string(view.Result) != `"the repository is missing"` {
		t.Fatalf("view %+v (error %v)", view, failed)
	}
}

func TestMissingReturnResultIsPartialWithBoundedOutput(t *testing.T) {
	long := strings.Repeat("x", 10<<10) + "THE END"
	view, failed, _, _, _, _ := runSubtask(t, subtaskCall("worker", "ramble", false), "ramble", fake.New(fake.Text(long)), nil)
	var text string
	if !failed || view.Status != "partial" || view.Reason != noReturnReason || json.Unmarshal(view.Result, &text) != nil {
		t.Fatalf("view %+v (error %v)", view, failed)
	}
	// A bounded summary of the last output, never the whole transcript.
	if len(text) > maxLastOutput+len("…") || !strings.HasSuffix(text, "THE END") || !strings.HasPrefix(text, "…") {
		t.Fatalf("summary of %d bytes: %.40q…", len(text), text)
	}
}

func TestResultOverTheAgentCapBecomesAPointer(t *testing.T) {
	big := strings.Repeat("finding ", 500) // About 4KB.
	view, failed, e, db, id, _ := runSubtask(t, subtaskCall("worker", "dig", false), "dig", fake.New(returnCall("ok", big)),
		func(e *Engine) { e.cfg.SubtaskResultCap = 300 }) // 1,200 bytes for the whole envelope.
	raw := lastResultText(t, db, id)
	if failed || view.Status != "ok" || string(view.Result) != "null" || !strings.HasPrefix(view.ResultRef, "tool_result://") ||
		view.ResultBytes <= len(big) || view.Preview == "" || !slices.Contains(view.Artifacts, view.ResultRef) || len(raw) > 1200 {
		t.Fatalf("view %+v (%d bytes)", view, len(raw))
	}
	// Stored, not cached for an hour: a background parent may read it days later.
	if expires, err := time.Parse(time.RFC3339, view.ResultExpiresAt); err != nil || time.Until(expires) < 7*24*time.Hour {
		t.Fatalf("result expires %q", view.ResultExpiresAt)
	}
	// The parent reads the full result with fetch_tool_result.
	text, fetchFailed := e.cache.HandleFetch(t.Context(), id, map[string]any{"id": strings.TrimPrefix(view.ResultRef, "tool_result://")}, 0)
	if fetchFailed || !strings.Contains(text, "finding finding") {
		t.Fatalf("fetch %q (failed %v)", text, fetchFailed)
	}
}

// lastResultText is the raw text of the parent's only tool result.
func lastResultText(t *testing.T, db *store.Store, id string) string {
	t.Helper()
	results := toolResults(t, db, id)
	return results[len(results)-1].Content
}

func TestEnvelopeStaysValidJSONUnderTheToolResultBudget(t *testing.T) {
	// A result just under the default cap, plus long notes and many long
	// artifacts: together they are well over the tool result budget.
	ret := returnCall("ok", strings.Repeat("r", 23<<10))
	ret.Events[0].ToolUse.Input["notes"] = strings.Repeat("n", 6<<10)
	var artifacts []any
	for i := range 80 {
		artifacts = append(artifacts, "file-"+strings.Repeat("a", 900)+"-"+string(rune('a'+i%26)))
	}
	ret.Events[0].ToolUse.Input["artifacts"] = artifacts
	view, _, e, db, id, _ := runSubtask(t, subtaskCall("worker", "flood", false), "flood", fake.New(ret), nil)
	raw := lastResultText(t, db, id)
	if !json.Valid([]byte(raw)) || len(raw) > subtaskEnvelopeLimit || strings.Contains(raw, "PARTIAL PREVIEW") {
		t.Fatalf("parent got %d bytes, valid JSON %v: %.120q", len(raw), json.Valid([]byte(raw)), raw)
	}
	if view.ResultRef == "" || len(view.Artifacts) > maxArtifacts || view.Artifacts[len(view.Artifacts)-1] != view.ResultRef {
		t.Fatalf("artifacts %d, ref %q", len(view.Artifacts), view.ResultRef)
	}
	for _, a := range view.Artifacts {
		if len(a) > maxArtifactBytes+len("…") {
			t.Fatalf("an artifact of %d bytes was shown", len(a))
		}
	}
	// Everything cut from the envelope is in the stored body.
	text, failed := e.cache.HandleFetch(t.Context(), id, map[string]any{"id": strings.TrimPrefix(view.ResultRef, "tool_result://"), "json_pointer": "/artifacts/79"}, 0)
	if failed || !strings.Contains(text, strings.Repeat("a", 900)+"-b") {
		t.Fatalf("stored artifacts %.200q (failed %v)", text, failed)
	}
}

func TestErrorEnvelopeIsBoundedToo(t *testing.T) {
	// An error result skips the cache preview, so the envelope must be bounded itself.
	ret := returnCall("error", strings.Repeat("e", 40<<10))
	ret.Events[0].ToolUse.Input["notes"] = strings.Repeat("n", 20<<10)
	view, failed, _, db, id, _ := runSubtask(t, subtaskCall("worker", "fail hard", false), "fail hard", fake.New(ret), nil)
	raw := lastResultText(t, db, id)
	if !failed || view.Status != "error" || !json.Valid([]byte(raw)) || len(raw) > subtaskEnvelopeLimit || view.ResultRef == "" {
		t.Fatalf("error envelope %d bytes: %+v", len(raw), view)
	}
}

func TestStoredResultIsScrubbed(t *testing.T) {
	secret := "sk-ant-secretvalue0123456789abcdef" // #nosec G101 -- A fake key the redactor must catch.
	view, _, e, _, id, _ := runSubtask(t, subtaskCall("worker", "leak", false), "leak", fake.New(returnCall("ok", strings.Repeat("x ", 15<<10)+" token "+secret+" end")), nil)
	text, failed := e.cache.HandleFetch(t.Context(), id, map[string]any{"id": strings.TrimPrefix(view.ResultRef, "tool_result://"), "pattern": "secretvalue"}, 0)
	stored, _ := e.cfg.Store.Results().Get(t.Context(), id, strings.TrimPrefix(view.ResultRef, "tool_result://"))
	if view.ResultRef == "" || strings.Contains(stored.Body, "secretvalue0123456789") {
		t.Fatalf("stored body kept the secret (fetch %q, failed %v)", text, failed)
	}
}

func TestCyclicReturnSpecIsRefusedBeforeAnyChild(t *testing.T) {
	t.Setenv("NANITE_AUTO_REPAIR", "false")
	call := subtaskCallSpec("worker", "loop", map[string]any{"$ref": "#"})
	e, db, id := setupEngine(t, fake.New(call, fake.Text("parent done")), "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "return_spec") {
		t.Fatalf("results %+v", results)
	}
	if conversations, err := db.Conversations(t.Context()); err != nil || len(conversations) != 1 {
		t.Fatalf("a child was started: %d, %v", len(conversations), err)
	}
}

func TestReturnResultRunsAlone(t *testing.T) {
	// A step that writes and returns: the write must not run after the result.
	step := returnCall("ok", "done")
	write := toolResponse("host_write")
	write.Events[0].ToolUse.ID = "write-1"
	step.Events = append(write.Events[:1:1], step.Events...)
	var writes int
	view, _, _, db, _, _ := runSubtask(t, subtaskCall("worker", "write and go", false), "write and go", fake.New(step), func(e *Engine) {
		if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "host_write", InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(context.Context, tools.Call) (tools.Result, error) {
				writes++
				return tools.Result{Text: "wrote"}, nil
			}}); err != nil {
			t.Fatal(err)
		}
	})
	if writes != 0 || view.Status != "ok" {
		t.Fatalf("writes %d, view %+v", writes, view)
	}
	child := toolResults(t, db, view.ChildID)
	if len(child) != 2 || !strings.HasPrefix(child[0].Content, "ignored:") || !child[0].IsError {
		t.Fatalf("child tool results %+v", child)
	}
}

func TestTwoReturnsInOneStepStillGetTheRepair(t *testing.T) {
	first := returnCall("ok", map[string]any{"files": "a.go"})
	second := returnCall("ok", map[string]any{"nothing": true})
	second.Events[0].ToolUse.ID = "ret-2"
	both := fake.Response{Events: append(first.Events[:1:1], second.Events...)}
	fixed := returnCall("ok", map[string]any{"files": []any{"a.go"}})
	fixed.Events[0].ToolUse.ID = "ret-3"
	view, _, _, db, _, _ := runSubtask(t, subtaskCallSpec("worker", "list files", reportSchema), "list files", fake.New(both, fixed), nil)
	if view.Status != "ok" || string(view.Result) != `{"files":["a.go"]}` {
		t.Fatalf("view %+v", view)
	}
	child := toolResults(t, db, view.ChildID)
	if len(child) != 3 || !strings.Contains(child[0].Content, "one repair") || !strings.HasPrefix(child[1].Content, "ignored:") {
		t.Fatalf("child tool results %+v", child)
	}
}

func TestBackgroundErrorOutputIsJustTheResult(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "bg fail", true), fake.Text("parent finished")),
		children: map[string]*fake.Provider{"bg fail": fake.New(returnCall("error", "it broke"))}}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.Notices():
	case <-time.After(4 * time.Second):
		t.Fatal("no notice")
	}
	child := childOf(t, db, id)
	job, err := db.Job(t.Context(), id, subtaskJobKind, child.ID)
	if err != nil || job.Status != "failed" {
		t.Fatalf("job %+v, %v", job, err)
	}
	data, err := os.ReadFile(job.OutputPath)
	var out parentView
	if err != nil || json.Unmarshal(data, &out) != nil || out.Status != "error" {
		t.Fatalf("output %q is not just the result JSON: %v", data, err)
	}
	check := execution{engine: e, info: store.Conversation{ID: id}}
	status, err := check.jobStatus(t.Context(), child.ID)
	var wrapped struct {
		Output string
		Result parentView
	}
	if err != nil || !status.IsError || json.Unmarshal([]byte(status.Text), &wrapped) != nil || wrapped.Output != "" || wrapped.Result.Status != "error" {
		t.Fatalf("background_status %+v, %v", status, err)
	}
}

func TestBackgroundStatusEmbedsTheResultAsJSON(t *testing.T) {
	// About 18KB of short strings: mostly quotes, which an escaped string
	// would nearly double past the tool result budget.
	var many []any
	for range 4500 {
		many = append(many, "a")
	}
	p := &routedProvider{root: fake.New(subtaskCall("worker", "quotes", true), fake.Text("parent finished")),
		children: map[string]*fake.Provider{"quotes": fake.New(returnCall("ok", many))}}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.Notices():
	case <-time.After(4 * time.Second):
		t.Fatal("no notice")
	}
	child := childOf(t, db, id)
	check := execution{engine: e, info: store.Conversation{ID: id}}
	status, err := check.jobStatus(t.Context(), child.ID)
	var wrapped struct{ Result parentView }
	if err != nil || status.IsError || len(status.Text) > toolResultBudget || json.Unmarshal([]byte(status.Text), &wrapped) != nil {
		t.Fatalf("background_status of %d bytes, error %v: %.120q", len(status.Text), err, status.Text)
	}
	var got []string
	if wrapped.Result.Status != "ok" || wrapped.Result.ResultRef != "" || json.Unmarshal(wrapped.Result.Result, &got) != nil || len(got) != 4500 {
		t.Fatalf("result %+v", wrapped.Result.Status)
	}
}

func TestVariantCapAboveTheLimitIsClamped(t *testing.T) {
	big := strings.Repeat("y", 30<<10)
	view, _, _, db, id, _ := runSubtask(t, subtaskCall("huge", "dig", false), "dig", fake.New(returnCall("ok", big)), func(e *Engine) {
		writeVariant(t, e.cfg.Dir, "huge", "---\nresult_cap: 100000\n---\nReturn everything.\n")
	})
	raw := lastResultText(t, db, id)
	if !json.Valid([]byte(raw)) || len(raw) > subtaskEnvelopeLimit || view.ResultRef == "" || string(view.Result) != "null" {
		t.Fatalf("clamped envelope %d bytes: %+v", len(raw), view.Status)
	}
}

func TestOversizedReturnSpecIsRefusedBeforeAnyChild(t *testing.T) {
	t.Setenv("NANITE_AUTO_REPAIR", "false")
	spec := map[string]any{"type": "object", "description": strings.Repeat("d", 17<<10)}
	e, db, id := setupEngine(t, fake.New(subtaskCallSpec("worker", "big spec", spec), fake.Text("parent done")), "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, id)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "limit") {
		t.Fatalf("results %+v", results)
	}
	if conversations, err := db.Conversations(t.Context()); err != nil || len(conversations) != 1 {
		t.Fatalf("a child was started: %d, %v", len(conversations), err)
	}
}

func TestManySmallArtifactsAreCapped(t *testing.T) {
	// Small enough to fit inline: only the artifact count cap stores them.
	ret := returnCall("ok", "done")
	var artifacts []any
	for i := range 70 {
		artifacts = append(artifacts, "f"+strings.Repeat("0", 3)+string(rune('a'+i%26)))
	}
	ret.Events[0].ToolUse.Input["artifacts"] = artifacts
	view, _, e, _, id, _ := runSubtask(t, subtaskCall("worker", "many", false), "many", fake.New(ret), nil)
	if len(view.Artifacts) != maxArtifacts || view.ResultRef == "" || view.Artifacts[maxArtifacts-1] != view.ResultRef {
		t.Fatalf("%d artifacts shown, ref %q", len(view.Artifacts), view.ResultRef)
	}
	text, failed := e.cache.HandleFetch(t.Context(), id, map[string]any{"id": strings.TrimPrefix(view.ResultRef, "tool_result://"), "json_pointer": "/artifacts"}, 0)
	var stored []string
	_, body, _ := strings.Cut(text, "\n\n")
	if failed || json.Unmarshal([]byte(strings.TrimSpace(body)), &stored) != nil || len(stored) != 70 {
		t.Fatalf("stored artifacts %d from %.160q (failed %v)", len(stored), text, failed)
	}
}

func TestVariantResultCapOverridesTheAgent(t *testing.T) {
	big := strings.Repeat("finding ", 200)
	view, _, _, _, _, _ := runSubtask(t, subtaskCall("roomy", "dig", false), "dig", fake.New(returnCall("ok", big)), func(e *Engine) {
		e.cfg.SubtaskResultCap = 100
		writeVariant(t, e.cfg.Dir, "roomy", "---\nresult_cap: 1000\n---\nYou have room.\n")
	})
	var text string
	if view.Status != "ok" || view.ResultRef != "" || json.Unmarshal(view.Result, &text) != nil || text != big {
		t.Fatalf("view %+v", view)
	}
	view, _, _, _, _, _ = runSubtask(t, subtaskCall("tight", "dig", false), "dig", fake.New(returnCall("ok", big)), func(e *Engine) {
		writeVariant(t, e.cfg.Dir, "tight", "---\nresult_cap: 50\n---\nBe brief.\n")
	})
	if view.ResultRef == "" || string(view.Result) != "null" {
		t.Fatalf("a variant cap under the default was not applied: %+v", view)
	}
}
