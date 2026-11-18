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
	"github.com/hollis-labs/nanite/internal/tools"
)

func TestBackgroundShellOutlivesTurnAndPostsNotice(t *testing.T) {
	launch := toolResponse("background_start")
	launch.Events[0].ToolUse.Input = map[string]any{"command": "while [ ! -f release ]; do sleep 0.05; done; printf job-finished", "timeout_seconds": 10.0}
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
	if err != nil || string(raw) != "job-finished" {
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

func TestChildRolesAndPermissionBoundary(t *testing.T) {
	for _, role := range []string{"worker", "planner"} {
		t.Run(role, func(t *testing.T) {
			spawn := toolResponse("subagent_spawn")
			spawn.Events[0].ToolUse.Input = map[string]any{"role": role, "task": "inspect"}
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
				if tool.Name == "ask_user" || strings.HasPrefix(tool.Name, "subagent_") {
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
	if len(req.Messages) > 0 && len(req.Messages[0].ContentBlocks) > 0 && req.Messages[0].ContentBlocks[0].Text == "child work" {
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
	spawn := toolResponse("subagent_spawn")
	spawn.Events[0].ToolUse.Input = map[string]any{"role": "worker", "task": "child work", "background": true}
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
	if err != nil || string(data) != "child output" || p.requests.Load() != 1 {
		t.Fatalf("output %q, requests=%d, %v", data, p.requests.Load(), err)
	}
}

func TestParentCancellationCancelsForegroundChild(t *testing.T) {
	spawn := toolResponse("subagent_spawn")
	spawn.Events[0].ToolUse.Input = map[string]any{"role": "worker", "task": "child work"}
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
