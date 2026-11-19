package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

// routedProvider answers each conversation from its own script, chosen by the
// first message: a child's starts "Goal: <goal>". Anything else is the root.
type routedProvider struct {
	root     *fake.Provider
	children map[string]*fake.Provider
	mu       sync.Mutex
	requests map[string][]llmtypes.ChatRequest
}

func (p *routedProvider) route(req llmtypes.ChatRequest) (string, *fake.Provider) {
	if len(req.Messages) > 0 && len(req.Messages[0].ContentBlocks) > 0 {
		text := req.Messages[0].ContentBlocks[0].Text
		for goal, child := range p.children {
			if strings.HasPrefix(text, "Goal: "+goal+"\n") {
				return goal, child
			}
		}
	}
	return "", p.root
}

func (p *routedProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	name, target := p.route(req)
	p.mu.Lock()
	if p.requests == nil {
		p.requests = make(map[string][]llmtypes.ChatRequest)
	}
	p.requests[name] = append(p.requests[name], req)
	p.mu.Unlock()
	return target.StreamChat(ctx, req)
}

func (p *routedProvider) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	return p.root.Complete(ctx, req)
}

func (*routedProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

func (p *routedProvider) offered(name string, request int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var names []string
	for _, tool := range p.requests[name][request].Tools {
		names = append(names, tool.Name)
	}
	return names
}

func writeVariant(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "variants", name+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func toolResults(t *testing.T, db *store.Store, id string) []llmtypes.ContentBlock {
	t.Helper()
	history, err := db.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []llmtypes.ContentBlock
	for _, m := range history {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" {
				out = append(out, b)
			}
		}
	}
	return out
}

func TestSubtaskRequiresAVariant(t *testing.T) {
	t.Setenv("NANITE_AUTO_REPAIR", "false") // The model must choose; nothing may fill a variant in.
	for _, tc := range []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"missing", map[string]any{"goal": "x", "return_spec": "y"}, "variant"},
		{"unknown", map[string]any{"goal": "x", "return_spec": "y", "variant": "nope"}, "variant"},
		{"no return spec", map[string]any{"goal": "x", "variant": "code"}, "return_spec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := toolResponse("subtask")
			call.Events[0].ToolUse.Input = tc.input
			e, db, id := setupEngine(t, fake.New(call, fake.Text("done")), "")
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			results := toolResults(t, db, id)
			if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, tc.want) {
				t.Fatalf("results %+v", results)
			}
			conversations, err := db.Conversations(t.Context())
			if err != nil || len(conversations) != 1 {
				t.Fatalf("a child was started: %+v, %v", conversations, err)
			}
		})
	}
}

func TestAuthoredVariantNarrowsTools(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("scout", "look around", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"look around": fake.New(fake.Text("found it"))}}
	e, db, id := setupEngine(t, p, "")
	// Allow names a tool the agent does not have (narrowing cannot add it) and
	// a host tool the deny list then removes.
	writeVariant(t, e.cfg.Dir, "scout", "---\ndescription: Looks, never touches.\ntools:\n  allow: [read_file, list_dir, \"host_*\", invented_tool]\n  deny: [host_write]\n---\nYou are a scout.\n")
	for _, name := range []string{"host_read", "host_write"} {
		if err := e.cfg.Tools.SetGroup(name, tools.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`),
			Handler: func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{}, nil }}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	got := p.offered("look around", 0)
	slices.Sort(got)
	if want := []string{"host_read", "list_dir", "read_file"}; !slices.Equal(got, want) {
		t.Fatalf("scout offered %v, want %v", got, want)
	}
	if root := p.offered("", 0); !slices.Contains(root, "subtask") || !slices.Contains(root, "host_write") {
		t.Fatalf("root lost tools: %v", root)
	}
	var out struct{ Status, Result, Variant string }
	if results := toolResults(t, db, id); len(results) != 1 || json.Unmarshal([]byte(results[0].Content), &out) != nil || out.Status != "ok" || out.Result != "found it" || out.Variant != "scout" {
		t.Fatalf("result %+v", results)
	}
	if requests := p.requests["look around"]; !slices.ContainsFunc(requests[0].SlotBlocks, func(b llmtypes.SlotBlock) bool {
		return b.Name == "mode" && strings.Contains(b.Content, "You are a scout.")
	}) {
		t.Fatal("the variant's role prompt was not in the child's mode slot")
	}
}

func TestPlannerCannotWrite(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("planner", "make a plan", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"make a plan": fake.New(toolResponse("write_file"), fake.Text("plan"))}}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, name := range p.offered("make a plan", 0) {
		if name == "write_file" || name == "edit_file" || name == "bash" || name == "background_start" {
			t.Fatalf("planner offered %s", name)
		}
	}
	child := childOf(t, db, id)
	results := toolResults(t, db, child.ID)
	if len(results) != 1 || !strings.Contains(results[0].Content, "tool unavailable: write_file") {
		t.Fatalf("planner write was not refused: %+v", results)
	}
}

func childOf(t *testing.T, db *store.Store, parent string) store.Conversation {
	t.Helper()
	conversations, err := db.Conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conversations {
		if c.ParentID == parent {
			return c
		}
	}
	t.Fatalf("no child of %s in %+v", parent, conversations)
	return store.Conversation{}
}

func TestLeadVariantReachesDepthTwoNotThree(t *testing.T) {
	deep := toolResponse("subtask")
	deep.Events[0].ToolUse.Input = map[string]any{"variant": "worker", "goal": "too deep", "return_spec": "x"}
	p := &routedProvider{
		root: fake.New(subtaskCall("lead", "run a council", false), fake.Text("root done")),
		children: map[string]*fake.Provider{
			"run a council": fake.New(subtaskCall("worker", "one view", false), fake.Text("council done")),
			"one view":      fake.New(deep, fake.Text("view done")),
			"too deep":      fake.New(fake.Text("must never run")),
		},
	}
	e, db, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead a council.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "root done" {
		t.Fatalf("root %+v, %v", result, err)
	}
	if !slices.Contains(p.offered("run a council", 0), "subtask") {
		t.Fatal("the lead (depth 1, can_spawn) was not offered subtask")
	}
	if slices.Contains(p.offered("one view", 0), "subtask") {
		t.Fatal("a depth-2 child was offered subtask")
	}
	lead := childOf(t, db, id)
	worker := childOf(t, db, lead.ID)
	if lead.Role != "lead" || worker.Role != "worker" {
		t.Fatalf("lead %+v worker %+v", lead, worker)
	}
	results := toolResults(t, db, worker.ID)
	if len(results) != 1 || !strings.Contains(results[0].Content, "tool unavailable: subtask") {
		t.Fatalf("depth 3 was not refused: %+v", results)
	}
	if _, ok := p.requests["too deep"]; ok {
		t.Fatal("a depth-3 child ran")
	}
	// The handler refuses on its own as well, should the tool ever be offered.
	x := execution{engine: e, info: worker, depth: 2, variant: subagent.Builtins()["worker"], variants: subagent.Builtins()}
	if _, err := x.subtask(t.Context(), subagent.Request{Goal: "x", Variant: "worker", ReturnSpec: json.RawMessage(`"y"`)}); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth-2 subtask call: %v", err)
	}
	plain := execution{engine: e, info: lead, depth: 1, variant: subagent.Builtins()["worker"], variants: subagent.Builtins()}
	if _, err := plain.subtask(t.Context(), subagent.Request{Goal: "x", Variant: "worker", ReturnSpec: json.RawMessage(`"y"`)}); err == nil {
		t.Fatal("a depth-1 child without can_spawn started a subtask")
	}
}

func TestSubtaskBudgetsStopAtLimit(t *testing.T) {
	usage := func(name string) fake.Response {
		r := toolResponse(name)
		r.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 600, OutputTokens: 500}}}, r.Events...)
		return r
	}
	for _, tc := range []struct{ name, variant, want string }{
		{"steps", "---\nbudgets:\n  steps: 1\n---\nWork.\n", events.StopMaxSteps},
		{"tokens", "---\nbudgets:\n  tokens: 1000\n---\nWork.\n", events.StopTokenBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &routedProvider{root: fake.New(subtaskCall("bounded", "child work", false), fake.Text("parent done")),
				children: map[string]*fake.Provider{"child work": fake.New(usage("unknown"), usage("unknown"), fake.Text("never"))}}
			e, db, id := setupEngine(t, p, "")
			writeVariant(t, e.cfg.Dir, "bounded", tc.variant)
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			var finished *events.SubagentFinished
			for event := range run.Events() {
				if f, ok := event.(events.SubagentFinished); ok {
					finished = &f
				}
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			results := toolResults(t, db, id)
			var out struct{ Status, Reason string }
			if len(results) != 1 || !results[0].IsError || json.Unmarshal([]byte(results[0].Content), &out) != nil || out.Status != "limit" || out.Reason != tc.want {
				t.Fatalf("result %+v", results)
			}
			if finished == nil || finished.Status != "limit" || finished.Limit != tc.want || finished.Variant != "bounded" {
				t.Fatalf("finished %+v", finished)
			}
		})
	}
}

func TestSubtaskUsageRollsUpIntoParent(t *testing.T) {
	withUsage := func(r fake.Response, in, out int) fake.Response {
		r.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: in, OutputTokens: out}}}, r.Events...)
		return r
	}
	p := &routedProvider{
		root:     fake.New(withUsage(subtaskCall("worker", "child work", false), 100, 10), withUsage(fake.Text("parent done"), 200, 20)),
		children: map[string]*fake.Provider{"child work": fake.New(withUsage(fake.Text("child done"), 30, 3))},
	}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var rolled []events.Usage
	var in, out int
	for event := range run.Events() {
		if u, ok := event.(events.Usage); ok {
			in, out = in+u.InputTokens, out+u.OutputTokens
			if u.Subtask != "" {
				rolled = append(rolled, u)
			}
		}
	}
	result, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	if len(rolled) != 1 || rolled[0].Subtask != child.ID || rolled[0].InputTokens != 30 || rolled[0].OutputTokens != 3 {
		t.Fatalf("rolled-up usage %+v", rolled)
	}
	if result.Usage.InputTokens != 330 || result.Usage.OutputTokens != 33 || in != 330 || out != 33 {
		t.Fatalf("turn usage %+v; events summed to %d/%d", result.Usage, in, out)
	}
	var reported struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}
	}
	if results := toolResults(t, db, id); json.Unmarshal([]byte(results[0].Content), &reported) != nil || reported.Usage.InputTokens != 30 {
		t.Fatalf("subtask result usage %+v", results)
	}
}

func TestSubtaskStatus(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		result events.Result
		err    error
		want   subagent.Status
	}{
		{"ok", t.Context(), events.Result{StopReason: llmtypes.StopReasonEndTurn}, nil, subagent.StatusOK},
		{"limit", t.Context(), events.Result{StopReason: events.StopWallBudget}, nil, subagent.StatusLimit},
		{"partial", t.Context(), events.Result{StopReason: "refusal"}, nil, subagent.StatusPartial},
		{"error", t.Context(), events.Result{}, os.ErrInvalid, subagent.StatusError},
		{"cancelled", cancelled, events.Result{}, context.Canceled, subagent.StatusCancelled},
	} {
		if got, _ := subtaskStatus(tc.ctx, tc.ctx, tc.result, tc.err); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestLeadCannotWidenItsChildrensTools(t *testing.T) {
	denied := []string{"write_file", "edit_file", "bash", "todo_write", "scratchpad_write"}
	p := &routedProvider{
		root: fake.New(subtaskCall("lead", "run a council", false), fake.Text("root done")),
		children: map[string]*fake.Provider{
			"run a council": fake.New(subtaskCall("code", "write things", false), fake.Text("council done")),
			"write things":  fake.New(fake.Text("wrote nothing")),
		},
	}
	e, _, id := setupEngine(t, p, "")
	e.cfg.WorkspaceRoots = []string{e.cfg.Dir}
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\ntools:\n  deny: [write_file, edit_file, bash, todo_write, scratchpad_write]\n---\nYou lead.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	root := p.offered("", 0)
	for _, name := range denied {
		if !slices.Contains(root, name) {
			t.Fatalf("root lacks %s: %v", name, root)
		}
	}
	child := p.offered("write things", 0)
	for _, name := range denied {
		if slices.Contains(child, name) {
			t.Fatalf("the lead's code child was offered %s, which the lead denies: %v", name, child)
		}
	}
	if !slices.Contains(child, "read_file") {
		t.Fatalf("the code child lost tools the lead allows: %v", child)
	}
}

func TestOnlyARootRunsBackgroundSubtasks(t *testing.T) {
	e, db, id := setupEngine(t, fake.New(), "")
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead.\n")
	variants, err := subagent.Load(e.cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	lead := execution{engine: e, info: store.Conversation{ID: id, ParentID: "parent"}, depth: 1, variant: variants["lead"], variants: variants}
	_, err = lead.subtask(t.Context(), subagent.Request{Goal: "x", Variant: "worker", ReturnSpec: json.RawMessage(`"y"`), Background: true})
	if err == nil || !strings.Contains(err.Error(), "background") {
		t.Fatalf("a lead started a background subtask: %v", err)
	}
	if conversations, _ := db.Conversations(t.Context()); len(conversations) != 1 {
		t.Fatalf("a child was created: %+v", conversations)
	}
}

func schemaHasBackground(t *testing.T, req llmtypes.ChatRequest) bool {
	t.Helper()
	for _, tool := range req.Tools {
		if tool.Name == "subtask" {
			props, _ := tool.InputSchema["properties"].(map[string]any)
			_, ok := props["background"]
			return ok
		}
	}
	t.Fatal("subtask not offered")
	return false
}

func TestSubtaskBackgroundOnlyWhenPollable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups []tools.BuiltinGroup
		want   bool
	}{
		{"all groups", nil, true},
		{"no background group", []tools.BuiltinGroup{tools.BuiltinSubtasks, tools.BuiltinFilesRead}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &routedProvider{root: fake.New(fake.Text("done"))}
			e, _, id := setupEngine(t, p, "")
			builtins, err := tools.NewBuiltins(tc.groups)
			if err != nil {
				t.Fatal(err)
			}
			e.cfg.Builtins = builtins
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			if got := schemaHasBackground(t, p.requests[""][0]); got != tc.want {
				t.Fatalf("background offered: %v", got)
			}
		})
	}
	// A lead's own subtask tool never offers background.
	p := &routedProvider{root: fake.New(subtaskCall("lead", "lead it", false), fake.Text("done")),
		children: map[string]*fake.Provider{"lead it": fake.New(fake.Text("led"))}}
	e, _, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "lead", "---\ncan_spawn: true\n---\nYou lead.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if schemaHasBackground(t, p.requests["lead it"][0]) {
		t.Fatal("a lead was offered background subtasks")
	}
}

func TestWallBudgetStopsAChildMidStep(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("quick", "child work", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"child work": fake.New(fake.Response{Stall: true})}}
	e, db, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "quick", "---\nbudgets:\n  wall: 200ms\n---\nBe quick.\n")
	started := time.Now()
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "parent done" {
		t.Fatalf("parent %+v, %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the wall budget did not stop a stalled step: %v", elapsed)
	}
	var out struct{ Status, Reason string }
	results := toolResults(t, db, id)
	if len(results) != 1 || !results[0].IsError || json.Unmarshal([]byte(results[0].Content), &out) != nil || out.Status != "limit" || out.Reason != "wall_time" {
		t.Fatalf("result %+v", results)
	}
}

func TestStepBudgetOnlyNarrows(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("roomy", "child work", false)),
		children: map[string]*fake.Provider{"child work": fake.New(toolResponse("unknown"), toolResponse("unknown"), fake.Text("never"))}}
	e, db, id := setupEngine(t, p, "runtime:\n  max_steps: 1\n")
	writeVariant(t, e.cfg.Dir, "roomy", "---\nbudgets:\n  steps: 5\n---\nTake your time.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Status, Reason string
		Steps          int
	}
	results := toolResults(t, db, id)
	if len(results) != 1 || json.Unmarshal([]byte(results[0].Content), &out) != nil || out.Status != "limit" || out.Reason != "max_steps" || out.Steps != 1 {
		t.Fatalf("a variant raised the agent's step limit: %+v", results)
	}
}

func TestTokenBudgetCountsCachedTokens(t *testing.T) {
	cached := toolResponse("unknown")
	cached.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 800, CacheCreationTokens: 100}}}, cached.Events...)
	p := &routedProvider{root: fake.New(subtaskCall("bounded", "child work", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"child work": fake.New(cached, fake.Text("never"))}}
	e, db, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "bounded", "---\nbudgets:\n  tokens: 1000\n---\nWork.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var out struct{ Status, Reason string }
	results := toolResults(t, db, id)
	if len(results) != 1 || json.Unmarshal([]byte(results[0].Content), &out) != nil || out.Status != "limit" || out.Reason != "token_budget" {
		t.Fatalf("150 fresh + 900 cached tokens did not spend a 1000-token budget: %+v", results)
	}
}

func TestBackgroundSubtaskReportsInSubtaskTerms(t *testing.T) {
	// The parent's second step calls a host tool that holds its turn open until
	// the background child's notice arrives, so the child finishes while the
	// parent still has a running turn to report usage on.
	wait := toolResponse("await_notice")
	wait.Events[0].ToolUse.ID = "call-2"
	child := fake.Text("child output")
	child.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 30, OutputTokens: 3}}}, child.Events...)
	p := &routedProvider{root: fake.New(subtaskCall("worker", "child work", true), wait, fake.Text("parent finished")),
		children: map[string]*fake.Provider{"child work": fake.New(child)}}
	e, db, id := setupEngine(t, p, "")
	notices := make(chan store.Notice, 1)
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "await_notice", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			select {
			case n := <-e.Notices():
				notices <- n
				return tools.Result{Text: n.Body}, nil
			case <-time.After(4 * time.Second):
				return tools.Result{}, context.DeadlineExceeded
			}
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var rolled []events.Usage
	for event := range run.Events() {
		if u, ok := event.(events.Usage); ok && u.Subtask != "" {
			rolled = append(rolled, u)
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	n := <-notices
	launched := childOf(t, db, id)
	if len(rolled) != 1 || rolled[0].Subtask != launched.ID || rolled[0].InputTokens != 30 || !rolled[0].Background {
		t.Fatalf("background usage events %+v", rolled)
	}
	if !strings.Contains(n.Body, "ended ok") || !strings.Contains(n.Body, "30 input") {
		t.Fatalf("notice %q", n.Body)
	}
	check := execution{engine: e, info: store.Conversation{ID: id}}
	status, err := check.jobStatus(t.Context(), launched.ID)
	if err != nil || status.IsError || !strings.Contains(status.Text, `"status":"ok"`) || !strings.Contains(status.Text, `"kind":"subtask"`) {
		t.Fatalf("background_status %+v, %v", status, err)
	}
}

func TestCancelledBackgroundSubtaskIsAnError(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "child work", true), fake.Text("parent finished")),
		children: map[string]*fake.Provider{"child work": fake.New(fake.Response{Stall: true})}}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	launched := childOf(t, db, id)
	check := execution{engine: e, info: store.Conversation{ID: id}}
	if _, err := check.cancelJob(t.Context(), launched.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-e.Notices():
		if !strings.Contains(n.Body, "ended cancelled") {
			t.Fatalf("notice %q", n.Body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no notice after cancellation")
	}
	status, err := check.jobStatus(t.Context(), launched.ID)
	if err != nil || !status.IsError || !strings.Contains(status.Text, `"status":"cancelled"`) {
		t.Fatalf("background_status %+v, %v", status, err)
	}
}

// A background subtask's usage can arrive while its parent's turn is ending.
// The terminal event must stay the last one reported, with no race on the
// run's sequence. Run with -race.
func TestBackgroundUsageRacesTheTerminalEvent(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	for range 50 {
		var mu sync.Mutex
		var seen []events.Event
		r := &Run{id: "turn", events: make(chan events.Event, 65), done: make(chan struct{}), cancel: func() {}}
		r.observe = func(_ context.Context, ev events.Event) { mu.Lock(); seen = append(seen, ev); mu.Unlock() }
		e.mu.Lock()
		e.active[id] = r
		e.mu.Unlock()
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.reportUsage(t.Context(), id, "child", events.Usage{InputTokens: 1})
			}()
		}
		terminal := r.terminal(events.Result{}, nil)
		r.report(t.Context(), terminal)
		wg.Wait()
		e.mu.Lock()
		delete(e.active, id)
		e.mu.Unlock()
		mu.Lock()
		last := seen[len(seen)-1]
		mu.Unlock()
		if _, ok := last.(events.RunFinished); !ok {
			t.Fatalf("an event followed the terminal event: %+v", seen)
		}
		for _, ev := range seen {
			if u, ok := ev.(events.Usage); ok && (!u.Background || u.Subtask != "child") {
				t.Fatalf("usage %+v", u)
			}
		}
	}
}

func TestVariantSelectsItsOwnProvider(t *testing.T) {
	srv, models := streamingCompatServer(t, "variant")
	p := &routedProvider{root: fake.New(subtaskCall("local", "child work", false), subtaskCall("named", "other work", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"other work": fake.New(fake.Text("named done"))}}
	e, db, id := setupEngine(t, p, "")
	e.cfg.Providers = map[string]provider.Spec{"small": {Name: "small", Wire: provider.WireOpenAIChat, BaseURL: srv.URL + "/v1", Auth: provider.AuthNone}}
	writeVariant(t, e.cfg.Dir, "local", "---\nprovider: small\nmodel: tiny-model\n---\nRun small.\n")
	writeVariant(t, e.cfg.Dir, "named", "---\nmodel: other-model\n---\nModel only.\n")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := models(); len(got) != 1 || got[0] != "tiny-model" {
		t.Fatalf("the variant's provider saw models %v", got)
	}
	results := toolResults(t, db, id)
	if len(results) != 2 || !strings.Contains(results[0].Content, "hello from variant") {
		t.Fatalf("results %+v", results)
	}
	// Model alone stays on the subagent provider (here the host's injected one).
	if got := p.requests["other work"][0].Model; got != "other-model" {
		t.Fatalf("model-only variant asked for %q", got)
	}
}

func TestBackgroundSubtaskEndingPartial(t *testing.T) {
	refusal := fake.Text("I will not")
	refusal.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{StopReason: "refusal"}}}, refusal.Events...)
	p := &routedProvider{root: fake.New(subtaskCall("worker", "child work", true), fake.Text("parent finished")),
		children: map[string]*fake.Provider{"child work": fake.New(refusal)}}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-e.Notices():
		if !strings.Contains(n.Body, "ended partial (refusal)") {
			t.Fatalf("notice %q", n.Body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no notice")
	}
	launched := childOf(t, db, id)
	check := execution{engine: e, info: store.Conversation{ID: id}}
	status, err := check.jobStatus(t.Context(), launched.ID)
	if err != nil || !status.IsError || !strings.Contains(status.Text, `"status":"partial"`) || !strings.Contains(status.Text, `"reason":"refusal"`) {
		t.Fatalf("background_status %+v, %v", status, err)
	}
}

// The panic path finishes a run without terminal(). A background usage
// report arriving afterwards is dropped, not sent on the closed channel.
func TestEmitAfterPanicFinishIsDropped(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(), "")
	r := &Run{id: "turn", events: make(chan events.Event, 65), done: make(chan struct{}), cancel: func() {}}
	e.mu.Lock()
	e.active[id] = r
	e.mu.Unlock()
	// As Engine.Send's panic handler does: release with finish, no terminal().
	e.release(id, r, func() {
		r.finish(events.Result{}, os.ErrClosed, events.RunFailed{EventMeta: events.EventMeta{RunID: r.id}, Err: os.ErrClosed})
	})
	e.mu.Lock()
	e.active[id] = r // A lookup that raced the release still holds the run.
	e.mu.Unlock()
	e.reportUsage(t.Context(), id, "child", events.Usage{InputTokens: 1})
	e.mu.Lock()
	delete(e.active, id)
	e.mu.Unlock()
	var got []events.Event
	for ev := range r.Events() {
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("events after finish: %+v", got)
	}
	if _, ok := got[0].(events.RunFailed); !ok {
		t.Fatalf("events %+v", got)
	}
}
