package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

// recordingGate answers each request with decide and records it.
type recordingGate struct {
	mu       sync.Mutex
	requests []permission.PermissionRequest
	decide   func(context.Context, permission.PermissionRequest) permission.PermissionDecision
}

func (g *recordingGate) Decide(ctx context.Context, req permission.PermissionRequest) (permission.PermissionDecision, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.mu.Unlock()
	return g.decide(ctx, req), nil
}

func (g *recordingGate) asked() []permission.PermissionRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]permission.PermissionRequest(nil), g.requests...)
}

func allowFor(scope permission.PermissionScope) func(context.Context, permission.PermissionRequest) permission.PermissionDecision {
	return func(context.Context, permission.PermissionRequest) permission.PermissionDecision {
		return permission.PermissionDecision{Allow: true, Scope: scope, Reason: "test allow"}
	}
}

// askedTool sets up a host tool "deploy" whose calls the rules always ask
// about, and counts its runs.
func askedTool(t *testing.T, e *Engine) *atomic.Int32 {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: deploy\n      behavior: ask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int32
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "deploy", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) {
			ran.Add(1)
			return tools.Result{Text: "deployed"}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	return &ran
}

// callAs is a tool call with its own ID and no input.
func callAs(name, id string) fake.Response {
	r := toolResponse(name)
	r.Events[0].ToolUse.ID = id
	return r
}

func subtaskResultOf(t *testing.T, db *store.Store, id string, index int) (out struct{ Status, Reason, Result, ChildID string }, isError bool) {
	t.Helper()
	results := toolResults(t, db, id)
	var found []llmtypes.ContentBlock
	for _, r := range results {
		if strings.Contains(r.Content, `"child_id"`) {
			found = append(found, r)
		}
	}
	if len(found) <= index {
		t.Fatalf("no subtask result %d in %+v", index, results)
	}
	if err := json.Unmarshal([]byte(found[index].Content), &out); err != nil {
		t.Fatal(err)
	}
	return out, found[index].IsError
}

func TestRootGrantCoversChild(t *testing.T) {
	p := &routedProvider{root: fake.New(callAs("deploy", "root-1"), subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	gate := &recordingGate{decide: allowFor(permission.ScopeConversation)}
	e.cfg.Gate = gate
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if asked := gate.asked(); len(asked) != 1 || asked[0].Subtask != nil {
		t.Fatalf("the child was asked about a tool the root granted: %+v", asked)
	}
	if ran.Load() != 2 {
		t.Fatalf("deploy ran %d times, want 2", ran.Load())
	}
	// Under the result contract a child that ends without return_result is
	// partial; what matters here is that it was not denied.
	if out, _ := subtaskResultOf(t, db, id, 0); out.Status != "partial" {
		t.Fatalf("subtask %+v", out)
	}
}

func TestChildAskRelaysToRootWithAttribution(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	gate := &recordingGate{decide: allowFor(permission.ScopeOnce)}
	e.cfg.Gate = gate
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var asked []events.PermissionAsked
	var resolved []events.PermissionResolved
	for event := range run.Events() {
		switch v := event.(type) {
		case events.PermissionAsked:
			asked = append(asked, v)
		case events.PermissionResolved:
			if strings.Contains(v.ID, ":") { // a relayed ask's ID is "<child>:<call>"
				resolved = append(resolved, v)
			}
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	// CallID is the root's subtask call ("call-1" in subtaskCall).
	want := permission.SubtaskOrigin{ChildID: child.ID, Variant: "worker", Goal: "ship it", Depth: 1, CallID: "call-1"}
	requests := gate.asked()
	if len(requests) != 1 || requests[0].ConversationID != id || requests[0].Subtask == nil || *requests[0].Subtask != want ||
		requests[0].Tool != "deploy" || requests[0].ID != child.ID+":child-1" {
		t.Fatalf("gate requests %+v", requests)
	}
	// The root's own run reports the relayed ask and its outcome.
	if len(asked) != 1 || asked[0].Request.Subtask == nil || *asked[0].Request.Subtask != want || asked[0].Request.ConversationID != id {
		t.Fatalf("PermissionAsked on the root run: %+v", asked)
	}
	if len(resolved) != 1 || resolved[0].ID != requests[0].ID || !resolved[0].Decision.Allow {
		t.Fatalf("PermissionResolved on the root run: %+v", resolved)
	}
	if ran.Load() != 1 {
		t.Fatalf("deploy ran %d times", ran.Load())
	}
	// A once allow is not a grant: the root conversation has none.
	e.mu.Lock()
	granted := e.grants[id]["deploy"]
	e.mu.Unlock()
	if granted {
		t.Fatal("an allow-once became a conversation grant")
	}
}

// A conversation-scope allow on a subtask's relayed ask covers that subtask
// only: not a sibling, not the root.
func TestSubtaskGrantStaysWithTheSubtask(t *testing.T) {
	second := subtaskCall("worker", "ship again", false)
	second.Events[0].ToolUse.ID = "call-2"
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), second, callAs("deploy", "root-1"), fake.Text("parent done")),
		children: map[string]*fake.Provider{
			"ship it":    fake.New(callAs("deploy", "child-1"), callAs("deploy", "child-2"), fake.Text("shipped")),
			"ship again": fake.New(callAs("deploy", "child-3"), fake.Text("shipped again")),
		}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	gate := &recordingGate{decide: allowFor(permission.ScopeConversation)}
	e.cfg.Gate = gate
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	asked := gate.asked()
	// The first subtask is asked once for two calls; its sibling and the
	// root are each asked again.
	if len(asked) != 3 || asked[0].Subtask == nil || asked[1].Subtask == nil || asked[1].Subtask.Goal != "ship again" || asked[2].Subtask != nil {
		t.Fatalf("asks %+v", asked)
	}
	if ran.Load() != 4 {
		t.Fatalf("deploy ran %d times, want 4", ran.Load())
	}
	for i := range 2 {
		if out, _ := subtaskResultOf(t, db, id, i); out.Status != "partial" {
			t.Fatalf("subtask %d %+v", i, out)
		}
	}
}

func TestDeniedRelayYieldsDenied(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("could not ship"))}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	e.cfg.Gate = &recordingGate{decide: func(context.Context, permission.PermissionRequest) permission.PermissionDecision {
		return permission.PermissionDecision{Reason: "not today", Human: true}
	}}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 0 {
		t.Fatal("a denied tool ran")
	}
	child := childOf(t, db, id)
	results := toolResults(t, db, child.ID)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "not today") {
		t.Fatalf("the child's tool result %+v", results)
	}
	out, isError := subtaskResultOf(t, db, id, 0)
	if out.Status != "denied" || out.Reason != "approval denied for deploy: not today" || out.Result != "could not ship" || !isError {
		t.Fatalf("subtask %+v (error %v)", out, isError)
	}
	e.mu.Lock()
	left := len(e.denials)
	e.mu.Unlock()
	if left != 0 {
		t.Fatal("denials were not cleared")
	}
}

// A background child's ask waits for the root's next turn when the turn that
// started it has ended, and is asked again if a turn ends with it open.
func TestBackgroundRelayWaitsForTheRootsNextTurn(t *testing.T) {
	hold := callAs("hold", "root-hold")
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", true), fake.Text("started"), hold, fake.Text("second done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	holding := make(chan struct{}) // closed once the root's second turn is in hold
	answered := make(chan struct{})
	var once sync.Once
	gate := &recordingGate{decide: func(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
		// Nobody answers until the second turn: an ask open when the first
		// turn ends comes back to the second.
		select {
		case <-holding:
			once.Do(func() { close(answered) })
			return permission.PermissionDecision{Allow: true, Reason: "answered on the second turn"}
		case <-ctx.Done():
			return permission.PermissionDecision{Reason: "the turn ended"}
		}
	}}
	e.cfg.Gate = gate
	if err := e.cfg.Tools.SetGroup("hold", tools.Tool{Name: "hold", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			close(holding)
			select {
			case <-answered:
				return tools.Result{Text: "held"}, nil
			case <-time.After(5 * time.Second):
				return tools.Result{}, context.DeadlineExceeded
			}
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	// Give the child time to relay into the gap between the root's turns.
	time.Sleep(50 * time.Millisecond)
	run, err = e.Send(t.Context(), id, Input{Text: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var waiting, ended string
	for ended == "" {
		select {
		case n := <-e.Notices():
			if n.Kind == "subtask_ask" {
				waiting = n.Body
			} else {
				ended = n.Body
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no completion notice")
		}
	}
	if !strings.Contains(waiting, child.ID) || !strings.Contains(waiting, "waiting for an answer") {
		t.Fatalf("waiting notice %q", waiting)
	}
	if !strings.Contains(ended, "ended partial") || ran.Load() != 1 {
		t.Fatalf("notice %q, deploy ran %d", ended, ran.Load())
	}
	for _, req := range gate.asked() {
		if req.ConversationID != id || req.Subtask == nil || req.Subtask.ChildID != child.ID {
			t.Fatalf("relayed request %+v", req)
		}
	}
}

func TestChildElicitationRelaysToRoot(t *testing.T) {
	hold := callAs("hold", "root-hold")
	p := &routedProvider{root: fake.New(hold, fake.Text("done"))}
	e, _, id := setupEngine(t, p, "")
	release := make(chan struct{})
	if err := e.cfg.Tools.SetGroup("hold", tools.Tool{Name: "hold", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			select {
			case <-release:
				return tools.Result{Text: "held"}, nil
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
		}}); err != nil {
		t.Fatal(err)
	}
	root, err := e.cfg.Store.Conversation(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	origin := permission.SubtaskOrigin{ChildID: "child", Variant: "research", Goal: "look it up", Depth: 1}
	child := execution{engine: e, info: store.Conversation{ID: "child", ParentID: id, Role: "research"}, root: root, origin: &origin}
	type reply struct {
		value json.RawMessage
		err   error
	}
	got := make(chan reply, 1)
	go func() {
		v, err := child.relayQuestion(t.Context(), "Which index?", json.RawMessage(`{"type":"string"}`))
		got <- reply{v, err}
	}()
	for event := range run.Events() {
		if q, ok := event.(events.Question); ok {
			if q.Subtask == nil || *q.Subtask != origin || q.Prompt != "Which index?" {
				t.Errorf("relayed question %+v", q)
			}
			if err := run.Answer(q.ID, events.Answer{Value: json.RawMessage(`"main"`)}); err != nil {
				t.Fatal(err)
			}
			r := <-got
			if r.err != nil || string(r.value) != `"main"` {
				t.Errorf("the child got %s, %v", r.value, r.err)
			}
			close(release)
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-release:
	default:
		t.Fatal("the root run never saw the child's question")
	}
}

// A refusal no person gave (here the default DenyGate) is the tool's error
// result with the gate's own reason; the subtask's status is not "denied".
func TestDefaultGateRefusalIsNotADenial(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("x"))}}
	e, db, id := setupEngine(t, p, "")
	askedTool(t, e)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	if results := toolResults(t, db, child.ID); len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "no interactive approval gate configured") {
		t.Fatalf("the child's tool result %+v", results)
	}
	if out, _ := subtaskResultOf(t, db, id, 0); out.Status != "partial" || strings.Contains(out.Reason, "denied") {
		t.Fatalf("subtask %+v", out)
	}
}

// A secret in the goal is scrubbed from the relayed origin, the events and
// the child's stored title.
func TestSubtaskGoalIsScrubbed(t *testing.T) {
	secret := "sk-ant-secretvalue0123456789abcdef" //nolint:gosec // A fake key, to check that it is scrubbed.
	goal := "use " + secret
	p := &routedProvider{root: fake.New(subtaskCall("worker", goal, false), fake.Text("parent done")),
		// The child's own first message is scrubbed too.
		children: map[string]*fake.Provider{"use [REDACTED]": fake.New(callAs("deploy", "child-1"), fake.Text("x"))}}
	e, db, id := setupEngine(t, p, "")
	askedTool(t, e)
	gate := &recordingGate{decide: allowFor(permission.ScopeOnce)}
	e.cfg.Gate = gate
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for event := range run.Events() {
		switch v := event.(type) {
		case events.PermissionAsked:
			if v.Request.Subtask != nil {
				seen = append(seen, v.Request.Subtask.Goal)
			}
		case events.SubagentStarted:
			seen = append(seen, v.Goal)
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, req := range gate.asked() {
		if req.Subtask != nil {
			seen = append(seen, req.Subtask.Goal)
		}
	}
	seen = append(seen, childOf(t, db, id).Title)
	if len(seen) != 4 {
		t.Fatalf("goals seen %q", seen)
	}
	for _, s := range seen {
		if strings.Contains(s, secret) {
			t.Fatalf("the secret reached %q", s)
		}
	}
}

// One ask is open at a time per root conversation: a background child's
// relayed ask and the root's own ask never reach the gate together.
func TestAsksAreOneAtATimePerRoot(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", true), callAs("deploy", "root-1"), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, _, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	var inflight, peak, calls atomic.Int32
	e.cfg.Gate = &recordingGate{decide: func(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
		calls.Add(1)
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for m := peak.Load(); n > m && !peak.CompareAndSwap(m, n); m = peak.Load() {
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return permission.PermissionDecision{Reason: "ended"}
		}
		return permission.PermissionDecision{Allow: true, Human: true}
	}}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.Notices():
	case <-time.After(5 * time.Second):
	}
	if peak.Load() != 1 {
		t.Fatalf("%d gate calls were open at once", peak.Load())
	}
	if calls.Load() < 1 || ran.Load() < 1 {
		t.Fatalf("gate calls %d, deploy ran %d", calls.Load(), ran.Load())
	}
}

// A refusal a person gives as the root's turn ends stands; it is not asked
// again on the next turn.
func TestHumanDenialAtTurnEndStands(t *testing.T) {
	// The root's turn holds until the child's ask is open, then ends.
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", true), callAs("hold", "root-hold"), fake.Text("started")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, db, id := setupEngine(t, p, "")
	ran := askedTool(t, e)
	var calls atomic.Int32
	open := make(chan struct{})
	e.cfg.Gate = &recordingGate{decide: func(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
		if calls.Add(1) == 1 {
			close(open)
		}
		<-ctx.Done() // the owner clicks deny just as the turn ends
		return permission.PermissionDecision{Reason: "owner said no", Human: true}
	}}
	if err := e.cfg.Tools.SetGroup("hold", tools.Tool{Name: "hold", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			select {
			case <-open:
				return tools.Result{Text: "held"}, nil
			case <-time.After(5 * time.Second):
				return tools.Result{}, context.DeadlineExceeded
			}
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var ended string
	for ended == "" {
		select {
		case n := <-e.Notices():
			if n.Kind != "subtask_ask" {
				ended = n.Body
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the subtask is still waiting; the owner's denial was discarded")
		}
	}
	if !strings.Contains(ended, "ended denied") || calls.Load() != 1 || ran.Load() != 0 {
		t.Fatalf("notice %q, gate calls %d, deploy ran %d", ended, calls.Load(), ran.Load())
	}
	check := execution{engine: e, info: store.Conversation{ID: id}}
	status, err := check.jobStatus(t.Context(), childOf(t, db, id).ID)
	if err != nil || !status.IsError || !strings.Contains(status.Text, `"status":"denied"`) {
		t.Fatalf("background_status %+v, %v", status, err)
	}
}

// A background child's ask that waits AskWait without a root turn ends
// denied, "unanswered".
func TestUnansweredAskEndsAfterAskWait(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", true), fake.Text("started")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("gave up"))}}
	e, db, id := setupEngine(t, p, "")
	e.cfg.AskWait = 150 * time.Millisecond
	ran := askedTool(t, e)
	e.cfg.Gate = &recordingGate{decide: func(ctx context.Context, req permission.PermissionRequest) permission.PermissionDecision {
		<-ctx.Done()
		return permission.PermissionDecision{Reason: "the turn ended"}
	}}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var ended string
	for ended == "" {
		select {
		case n := <-e.Notices():
			if n.Kind != "subtask_ask" {
				ended = n.Body
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the ask never gave up")
		}
	}
	child := childOf(t, db, id)
	results := toolResults(t, db, child.ID)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "unanswered") || ran.Load() != 0 {
		t.Fatalf("the child's tool result %+v, deploy ran %d", results, ran.Load())
	}
	if !strings.Contains(ended, "ended partial") {
		t.Fatalf("notice %q", ended)
	}
	// The "is waiting" notice is withdrawn: no root turn will show it.
	pending, err := db.PendingNotices(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range pending {
		if n.Kind == "subtask_ask" {
			t.Fatalf("a stale waiting notice is still pending: %+v", n)
		}
	}
}

// chatLikeGate behaves like chatrunner's gate: the host's event reader waits
// on each PermissionAsked until the gate has been entered for that ID, and
// the gate answers only once the reader has shown the card.
type chatLikeGate struct {
	mu      sync.Mutex
	settled map[string]chan struct{}
	shown   map[string]chan struct{}
}

func (g *chatLikeGate) ch(m map[string]chan struct{}, id string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m[id] == nil {
		m[id] = make(chan struct{})
	}
	return m[id]
}

func (g *chatLikeGate) Decide(ctx context.Context, req permission.PermissionRequest) (permission.PermissionDecision, error) {
	close(g.ch(g.settled, req.ID))
	select {
	case <-g.ch(g.shown, req.ID):
		return permission.PermissionDecision{Allow: true, Human: true}, nil
	case <-ctx.Done():
		return permission.PermissionDecision{Reason: "ended"}, nil
	}
}

// A root ask and a relayed ask racing for the root's slot: each holds the
// slot from its PermissionAsked to its PermissionResolved, so the order the
// stream shows asks is the order the gate gets them, and a chat-style host
// never waits on a card whose gate call is queued behind another.
func TestAskHoldsTheSlotFromAskedToResolved(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", true), callAs("deploy", "root-1"), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("hold", "c-hold"), callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, _, id := setupEngine(t, p, "")
	askedTool(t, e)
	askGap = 300 * time.Millisecond // widens the window between reporting an ask and deciding it
	t.Cleanup(func() { askGap = 0 })
	g := &chatLikeGate{settled: map[string]chan struct{}{}, shown: map[string]chan struct{}{}}
	e.cfg.Gate = g
	release := make(chan struct{})
	if err := e.cfg.Tools.SetGroup("hold", tools.Tool{Name: "hold", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return tools.Result{Text: "held"}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	run, err := e.Send(ctx, id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for ev := range run.Events() {
		if a, ok := ev.(events.PermissionAsked); ok {
			order = append(order, a.Request.ID)
			if a.Request.Subtask == nil {
				close(release) // the root's card is out: let the child ask now
			}
			select {
			case <-g.ch(g.settled, a.Request.ID):
			case <-ctx.Done():
				t.Fatalf("deadlock: the stream waited on %s; order %v", a.Request.ID, order)
			}
			close(g.ch(g.shown, a.Request.ID))
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(order) == 0 {
		t.Fatal("no ask reached the root's stream")
	}
}

// pdpRecorder is a host PDP that records each request and asks for deploy.
type pdpRecorder struct {
	mu       sync.Mutex
	requests []policy.Request
}

func (p *pdpRecorder) Evaluate(_ context.Context, req policy.Request) (policy.Decision, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return policy.Decision{Effect: policy.Allow}, nil
}

// A subtask's call reaches the host PDP as the root's: the root's scope and
// role, request ID <child>:<call>, and the variant in subtask_variant.
func TestSubtaskPDPRequestIsTheRootsWithAPrefixedID(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, db, id := setupEngine(t, p, "")
	askedTool(t, e)
	host := &pdpRecorder{}
	e.cfg.PDP = host
	e.cfg.Gate = &recordingGate{decide: allowFor(permission.ScopeOnce)}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	child := childOf(t, db, id)
	host.mu.Lock()
	defer host.mu.Unlock()
	var found bool
	for _, req := range host.requests {
		if req.Resource.ID != "deploy" {
			continue
		}
		found = true
		if req.RequestID != child.ID+":child-1" || req.Scope != id || req.Context["role"] != "chat" ||
			req.Context["subtask_variant"] != "worker" || req.Context["subtask_conversation"] != child.ID || req.Context["subtask_depth"] != 1 {
			t.Fatalf("the PDP saw %+v", req)
		}
	}
	if !found {
		t.Fatal("the PDP never saw the subtask's call")
	}
}

// A grant given on a subtask's relayed ask lives only as long as the
// subtask: it is pruned when the child ends.
func TestSubtaskGrantIsPrunedWhenItEnds(t *testing.T) {
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
	e, db, id := setupEngine(t, p, "")
	askedTool(t, e)
	var during atomic.Bool
	e.cfg.Gate = &recordingGate{decide: allowFor(permission.ScopeConversation)}
	if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "deploy", InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, tools.Call) (tools.Result, error) {
			// The grant was stored on the child by the time its call runs.
			e.mu.Lock()
			for conv, g := range e.grants {
				if conv != id && g["deploy"] {
					during.Store(true)
				}
			}
			e.mu.Unlock()
			return tools.Result{Text: "deployed"}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if !during.Load() {
		t.Fatal("no grant was stored on the child")
	}
	child := childOf(t, db, id)
	e.mu.Lock()
	_, kept := e.grants[child.ID]
	_, mapped := e.subtaskCalls[child.ID]
	e.mu.Unlock()
	if kept || mapped {
		t.Fatalf("the ended child's state was kept: grants %v, call mapping %v", kept, mapped)
	}
}

// A child cannot claim "denied" through return_result: only the engine sets
// it, from a person's refusal.
func TestReturnResultRefusesDenied(t *testing.T) {
	x := execution{info: store.Conversation{ID: "child"}}
	_, err := x.returnResult(t.Context(), subagent.Return{Status: subagent.StatusDenied, Result: json.RawMessage(`"x"`)})
	if err == nil || !strings.Contains(err.Error(), "set by the engine") {
		t.Fatalf("return_result accepted or misreported denied: %v", err)
	}
}

// A person's refusal makes the subtask denied even when the child then
// returned ok through return_result.
func TestDenialOverridesAReturnedOK(t *testing.T) {
	ret := toolResponse("return_result")
	ret.Events[0].ToolUse.ID = "ret-1"
	ret.Events[0].ToolUse.Input = map[string]any{"status": "ok", "result": "all fine"}
	p := &routedProvider{root: fake.New(subtaskCall("worker", "ship it", false), fake.Text("parent done")),
		children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), ret, fake.Text("x"))}}
	e, db, id := setupEngine(t, p, "")
	askedTool(t, e)
	e.cfg.Gate = &recordingGate{decide: func(context.Context, permission.PermissionRequest) permission.PermissionDecision {
		return permission.PermissionDecision{Reason: "no", Human: true}
	}}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	out, isError := subtaskResultOf(t, db, id, 0)
	if out.Status != "denied" || out.Reason != "approval denied for deploy: no" || !isError {
		t.Fatalf("subtask %+v (error %v)", out, isError)
	}
}
