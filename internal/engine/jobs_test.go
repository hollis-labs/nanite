package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

func TestBackgroundShellOutlivesTurnAndPostsNotice(t *testing.T) {
	launch := toolResponse("background_start")
	launch.Events[0].ToolUse.Input = map[string]any{"command": "printf 'first-line\\n'; while [ ! -f release ]; do sleep 0.05; done; printf job-finished", "timeout_seconds": 10.0}
	p := &watchedProvider{Provider: fake.New(launch, fake.Text("started"), fake.Text("noticed"))}
	e, db, id := setupEngine(t, p, "")
	e.cfg.WorkspaceRoots = []string{e.cfg.Dir}
	e.cfg.Gate = permission.AllowGate

	run, err := e.Send(t.Context(), id, Input{Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}

	history, err := db.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || history[2].Blocks[0].IsError {
		t.Fatalf("background launch failed: %+v", history)
	}
	var started struct{ ID, Status string }
	if err := json.Unmarshal([]byte(history[2].Blocks[0].Content), &started); err != nil || started.Status != "running" {
		t.Fatalf("started %+v, %v", started, err)
	}
	job, err := db.Job(t.Context(), id, "shell", started.ID)
	if err != nil || job.Status != "running" {
		t.Fatalf("job %+v, %v", job, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	check := execution{engine: e, info: store.Conversation{ID: id}}
	for {
		status, err := check.jobStatus(t.Context(), started.ID)
		if err != nil {
			t.Fatal(err)
		}
		body := status.Text
		if strings.Contains(body, "first-line") {
			if !strings.Contains(body, `"status":"running"`) {
				t.Errorf("output appeared after job stopped: %s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live job never exposed its first output line")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-e.Notices():
		if !strings.Contains(n.Body, "succeeded") || n.ConversationID != id {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("background job never posted a notice")
	}
	raw, err := os.ReadFile(job.OutputPath)
	if err != nil || string(raw) != "first-line\njob-finished" {
		t.Fatalf("job output %q, %v", raw, err)
	}
	info, err := os.Stat(job.OutputPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("job output mode: %v, %v", info, err)
	}

	run, err = e.Send(t.Context(), id, Input{Text: "check completion"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	last := p.requests[len(p.requests)-1]
	if !slices.ContainsFunc(last.SlotBlocks, func(b llmtypes.SlotBlock) bool {
		return b.Name == "session" && strings.Contains(b.Content, started.ID) && strings.Contains(b.Content, "succeeded")
	}) {
		t.Fatal("next turn did not receive the completion notice")
	}
}

func subtaskCall(variant, goal string, background bool) fake.Response {
	spawn := toolResponse("subtask")
	spawn.Events[0].ToolUse.Input = map[string]any{"variant": variant, "goal": goal, "return_spec": "a short report"}
	if background {
		// background is only in the schema where the caller can poll the job.
		spawn.Events[0].ToolUse.Input["background"] = true
	}
	return spawn
}

// returnCall is a child's return_result call.
func returnCall(status string, result any) fake.Response {
	call := toolResponse("return_result")
	call.Events[0].ToolUse.ID = "ret-1"
	call.Events[0].ToolUse.Input = map[string]any{"status": status, "result": result}
	return call
}

func TestChildRolesAndPermissionBoundary(t *testing.T) {
	for _, role := range []string{"worker", "planner"} {
		t.Run(role, func(t *testing.T) {
			spawn := subtaskCall(role, "inspect", false)
			p := &watchedProvider{Provider: fake.New(spawn, toolResponse("sensitive"), fake.Text("child done"), fake.Text("parent done"))}
			e, db, id := setupEngine(t, p, "")
			if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: sensitive\n      behavior: deny\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var invoked atomic.Bool
			if err := e.cfg.Tools.SetGroup("host", tools.Tool{Name: "sensitive", InputSchema: json.RawMessage(`{"type":"object"}`),
				Handler: func(context.Context, tools.Call) (tools.Result, error) {
					invoked.Store(true)
					return tools.Result{}, nil
				},
			}); err != nil {
				t.Fatal(err)
			}
			run, err := e.Send(t.Context(), id, Input{Text: "start"})
			if err != nil {
				t.Fatal(err)
			}
			var started, finished bool
			for event := range run.Events() {
				switch event.(type) {
				case events.SubagentStarted:
					started = true
				case events.SubagentFinished:
					finished = true
				}
			}
			if result, err := run.Wait(); err != nil || result.Text != "parent done" {
				t.Fatalf("result %+v, %v", result, err)
			}
			if !started || !finished || invoked.Load() {
				t.Fatalf("child events: start=%v finish=%v, handler=%v", started, finished, invoked.Load())
			}
			childRequest := p.requests[1]
			show := slices.ContainsFunc(childRequest.Tools, func(t llmtypes.ToolDefinition) bool { return t.Name == "sensitive" })
			if show != (role == "worker") {
				t.Fatalf("%s sees non-read-only tool: %v", role, show)
			}
			for _, tool := range childRequest.Tools {
				if tool.Name == "ask_user" || tool.Name == "subtask" || (role == "planner" && (tool.Name == "write_file" || tool.Name == "edit_file" || tool.Name == "bash")) {
					t.Fatalf("child sees %q", tool.Name)
				}
			}
			conversations, err := db.Conversations(t.Context())
			if err != nil || len(conversations) != 2 {
				t.Fatalf("conversations %+v, %v", conversations, err)
			}
			child := conversations[0]
			if child.ID == id {
				child = conversations[1]
			}
			h, err := db.History(t.Context(), child.ID)
			if err != nil || child.ParentID != id || len(h) != 4 || !h[2].Blocks[0].IsError {
				t.Fatalf("child %+v history %+v, %v", child, h, err)
			}
			if role == "worker" && !strings.Contains(h[2].Blocks[0].Content, "permission denied") {
				t.Fatal("worker bypassed the permission decision")
			}
		})
	}
}

type promptProvider struct {
	parent, child *fake.Provider
	requests      atomic.Int32
}

func (p *promptProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if len(req.Messages) > 0 && len(req.Messages[0].ContentBlocks) > 0 && strings.HasPrefix(req.Messages[0].ContentBlocks[0].Text, "Goal: child work") {
		p.requests.Add(1)
		return p.child.StreamChat(ctx, req)
	}
	return p.parent.StreamChat(ctx, req)
}

func (p *promptProvider) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	return p.parent.Complete(ctx, req)
}

func (*promptProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

func TestBackgroundChildFinishesAndPostsResult(t *testing.T) {
	spawn := subtaskCall("worker", "child work", true)
	p := &promptProvider{parent: fake.New(spawn, fake.Text("parent finished")), child: fake.New(fake.Text("child output"))}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "parent work"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "parent finished" {
		t.Fatalf("result %+v, %v", result, err)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || len(history) != 4 || history[2].Blocks[0].IsError {
		t.Fatalf("history %+v, %v", history, err)
	}
	var launched struct{ ID string }
	if err := json.Unmarshal([]byte(history[2].Blocks[0].Content), &launched); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-e.Notices():
		if n.Kind != "subagent" || !strings.Contains(n.Body, launched.ID) {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("background child did not notify parent")
	}
	child, err := db.Conversation(t.Context(), launched.ID)
	if err != nil || child.ParentID != id || child.Role != "worker" {
		t.Fatalf("child %+v, %v", child, err)
	}
	job, err := db.Job(t.Context(), id, "subagent", launched.ID)
	if err != nil || job.Status != "succeeded" {
		t.Fatalf("job %+v, %v", job, err)
	}
	data, err := os.ReadFile(job.OutputPath)
	var out struct{ Status, Result, Reason string }
	// Ending without return_result is partial, with the child's last output.
	if err != nil || json.Unmarshal(data, &out) != nil || out.Status != "partial" || out.Result != "child output" || out.Reason != "no_return_result" || p.requests.Load() != 1 {
		t.Fatalf("output %q, requests=%d, %v", data, p.requests.Load(), err)
	}
}

func TestParentCancellationCancelsForegroundChild(t *testing.T) {
	spawn := subtaskCall("worker", "child work", false)
	p := &promptProvider{parent: fake.New(spawn), child: fake.New(fake.Response{Stall: true})}
	e, db, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "parent work"})
	if err != nil {
		t.Fatal(err)
	}
	var childID string
	limit := time.NewTimer(4 * time.Second)
	defer limit.Stop()
	for childID == "" {
		select {
		case got, ok := <-run.Events():
			if !ok {
				t.Fatal("parent ended before it started the child")
			}
			if started, ok := got.(events.SubagentStarted); ok {
				childID = started.ID
			}
		case <-limit.C:
			t.Fatal("timed out waiting for child start")
		}
	}
	for p.requests.Load() == 0 {
		select {
		case <-limit.C:
			t.Fatal("child did not request model")
		case <-time.After(10 * time.Millisecond):
		}
	}
	run.Cancel()
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation: %v", err)
	}
	e.mu.Lock()
	childRun := e.active[childID]
	e.mu.Unlock()
	if childRun != nil {
		t.Fatal("foreground child stayed active after parent ended")
	}
	h, err := db.History(t.Context(), childID)
	if err != nil || len(h) != 1 {
		t.Fatalf("child history %+v, %v", h, err)
	}
	if status, err := db.TurnStatus(t.Context(), h[0].TurnID); err != nil || status != "cancelled" {
		t.Fatalf("child status %q, %v", status, err)
	}
}

func TestChildAtStepLimitReportsLimit(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "background"}[background], func(t *testing.T) {
			spawn := subtaskCall("worker", "child work", background)
			// The child calls a tool on its only step, so it stops at max_steps.
			p := &promptProvider{parent: fake.New(spawn, fake.Text("parent closing")), child: fake.New(toolResponse("unknown"), fake.Text("child closing"))}
			e, db, id := setupEngine(t, p, "runtime:\n  max_steps: 1\n")
			run, err := e.Send(t.Context(), id, Input{Text: "parent work"})
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
			history, err := db.History(t.Context(), id)
			if err != nil || len(history) < 3 {
				t.Fatalf("history %+v, %v", history, err)
			}
			result := history[2].Blocks[0]
			var launched struct {
				ID, Status, Reason string
				ChildID            string `json:"child_id"`
			}
			if err := json.Unmarshal([]byte(result.Content), &launched); err != nil {
				t.Fatal(err)
			}
			if !background {
				if launched.Status != "limit" || launched.Reason != "max_steps" || !result.IsError {
					t.Fatalf("foreground result %+v is_error=%v", launched, result.IsError)
				}
				if finished == nil || finished.Limit != "max_steps" || finished.Err != nil {
					t.Fatalf("finished event %+v", finished)
				}
				return
			}
			select {
			case n := <-e.Notices():
				if !strings.Contains(n.Body, "limit (max_steps)") {
					t.Fatalf("notice %+v", n)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("background child did not notify parent")
			}
			job, err := db.Job(t.Context(), id, "subagent", launched.ID)
			if err != nil || job.Status != "limit" || job.Reason != "max_steps" {
				t.Fatalf("job %+v, %v", job, err)
			}
			check := execution{engine: e, info: store.Conversation{ID: id}}
			status, err := check.jobStatus(t.Context(), launched.ID)
			if err != nil || !status.IsError || !strings.Contains(status.Text, `"status":"limit"`) {
				t.Fatalf("background_status %+v, %v", status, err)
			}
		})
	}
}
