package toolclient

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

type recordingGate struct {
	allow bool
	asked []string
}

func (g *recordingGate) AuthorizeNativeTool(_ context.Context, tool string) (bool, string) {
	g.asked = append(g.asked, tool)
	return g.allow, "gate said so"
}

func TestNativeGateDecidesTheCallAndNothingFallsThrough(t *testing.T) {
	// With no store the fabric check default-permits ("machinery not wired").
	// A gate that says no must still be final.
	tb := New(nil, nil, DefaultConfig())
	ctx := context.Background()
	if !tb.isToolGrantedToAgent(ctx, "agent-x", "todo_create") {
		t.Fatal("setup: the ungated, store-less path should default-permit")
	}
	deny := &recordingGate{allow: false}
	if tb.isToolGrantedToAgent(WithNativeToolGate(ctx, deny), "agent-x", "todo_create") {
		t.Fatal("a denying gate did not stop the call")
	}
	allow := &recordingGate{allow: true}
	if !tb.isToolGrantedToAgent(WithNativeToolGate(ctx, allow), "agent-x", "todo_create") {
		t.Fatal("an allowing gate did not permit the call")
	}
	if len(deny.asked) != 1 || deny.asked[0] != "todo_create" || len(allow.asked) != 1 {
		t.Fatalf("the gate was not asked exactly once per call: %v %v", deny.asked, allow.asked)
	}
	if _, ok := nativeToolGateFrom(ctx); ok {
		t.Fatal("a gate appeared in a context that never had one")
	}
}

func TestNativeGateGatesEveryCall(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	// A denying gate is refused before any execution is attempted.
	_, err := tb.CallTool(WithNativeToolGate(context.Background(), &recordingGate{allow: false}), "agent-x", "todo_create", nil)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("denying gate: %v", err)
	}
	// An allowing gate reaches the execution step (which has no manager here).
	_, err = tb.CallTool(WithNativeToolGate(context.Background(), &recordingGate{allow: true}), "agent-x", "todo_create", nil)
	if err == nil || !strings.Contains(err.Error(), "no MCP manager") {
		t.Fatalf("allowing gate did not reach execution: %v", err)
	}
}

func nameList(tools []llmtypes.ToolDefinition) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}
