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

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
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
		if got, _ := subtaskStatus(tc.ctx, tc.result, tc.err); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
