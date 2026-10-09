package permission

import (
	"context"
	"testing"
	"time"

	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
)

func TestCheck_yoloMode(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeYolo, nil)
	result := e.Check(context.Background(), "s1", "any_tool", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("yolo mode should allow everything, got %s", result.Decision)
	}
}

func TestCheck_planMode_blocksWrites(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModePlan, nil)

	result := e.Check(context.Background(), "s1", "dev_write", nil, permissionlib.ToolMeta{IsReadOnly: false})
	if result.Decision != permissionlib.DecisionDeny {
		t.Errorf("plan mode should deny writes, got %s", result.Decision)
	}

	result = e.Check(context.Background(), "s1", "dev_read", nil, permissionlib.ToolMeta{IsReadOnly: true})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("plan mode should allow reads, got %s", result.Decision)
	}
}

func TestCheck_defaultMode_asksForDestructive(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)

	result := e.Check(context.Background(), "s1", "shell", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAsk {
		t.Errorf("default mode should ask for destructive, got %s", result.Decision)
	}

	result = e.Check(context.Background(), "s1", "dev_read", nil, permissionlib.ToolMeta{IsReadOnly: true})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("default mode should allow read-only, got %s", result.Decision)
	}
}

func TestCheck_defaultMode_allowsNonDestructivePathGatedWrite(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)

	result := e.Check(context.Background(), "s1", "dev_write",
		map[string]any{"path": "/tmp/output.txt", "content": "hello"}, permissionlib.ToolMeta{})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("default mode should allow non-destructive path-gated writes, got %s", result.Decision)
	}
}

func TestCheck_acceptEditsMode(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeAcceptEdits, nil, permissionlib.WithFileEditTools("dev_edit", "dev_write"))

	// File edits auto-allowed (uniform name post ADR-002).
	result := e.Check(context.Background(), "s1", "dev_edit", nil, permissionlib.ToolMeta{})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("accept-edits should auto-allow file edits, got %s", result.Decision)
	}

	// Destructive non-edit asks.
	result = e.Check(context.Background(), "s1", "shell", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAsk {
		t.Errorf("accept-edits should ask for destructive non-edits, got %s", result.Decision)
	}
}

func TestCheck_ruleOverride(t *testing.T) {
	rules := &permissionlib.RuleSet{
		Rules: []permissionlib.Rule{
			{Tool: "shell", Pattern: "rm -rf", Behavior: permissionlib.DecisionDeny},
			{Tool: "dev_edit", Pattern: "/src/**", Behavior: permissionlib.DecisionAllow},
			{Tool: "task_delete", Behavior: permissionlib.DecisionAsk}, // formerly mcp__engine__task_delete
		},
	}
	e := permissionlib.NewEngine(permissionlib.ModeDefault, rules)

	// Shell with rm -rf: denied by rule.
	result := e.Check(context.Background(), "s1", "shell",
		map[string]any{"command": "rm -rf /tmp/stuff"}, permissionlib.ToolMeta{})
	if result.Decision != permissionlib.DecisionDeny {
		t.Errorf("expected deny for rm -rf, got %s", result.Decision)
	}

	// Edit in /src: allowed by rule.
	result = e.Check(context.Background(), "s1", "dev_edit",
		map[string]any{"path": "/src/main.go"}, permissionlib.ToolMeta{})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("expected allow for /src edit, got %s", result.Decision)
	}

	// Task delete: ask by rule.
	result = e.Check(context.Background(), "s1", "task_delete",
		nil, permissionlib.ToolMeta{})
	if result.Decision != permissionlib.DecisionAsk {
		t.Errorf("expected ask for task delete, got %s", result.Decision)
	}
}

func TestCheck_sessionGrant(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)

	// First check: destructive tool asks.
	result := e.Check(context.Background(), "s1", "shell", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAsk {
		t.Fatalf("expected ask, got %s", result.Decision)
	}

	// Grant session permission.
	req := e.RequestApproval("s1", "shell", nil, "test")
	if !e.Respond(req.ID, permissionlib.DecisionAllow, permissionlib.ScopeSession, "s1") {
		t.Fatal("session approval rejected")
	}
	e.WaitForApproval(context.Background(), req)

	// Second check: should be allowed by session grant.
	result = e.Check(context.Background(), "s1", "shell", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAllow {
		t.Errorf("expected allow from session grant, got %s", result.Decision)
	}

	// Clear grants.
	e.ClearSessionGrants("s1")
	result = e.Check(context.Background(), "s1", "shell", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAsk {
		t.Errorf("expected ask after grant cleared, got %s", result.Decision)
	}
}

func TestRuleSet_denyTakesPriority(t *testing.T) {
	rules := &permissionlib.RuleSet{
		Rules: []permissionlib.Rule{
			{Tool: "shell", Behavior: permissionlib.DecisionAllow},
			{Tool: "shell", Pattern: "rm", Behavior: permissionlib.DecisionDeny},
		},
	}

	// Even though allow comes first in the list, deny wins.
	result := rules.Evaluate("shell", map[string]any{"command": "rm -rf /"})
	if result == nil || result.Decision != permissionlib.DecisionDeny {
		t.Errorf("deny should take priority over allow")
	}
}

func TestApprovalFlow(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	e.SetApprovalTimeout(500 * time.Millisecond)

	req := e.RequestApproval("s1", "shell", nil, "test")

	// Respond in a goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.Respond(req.ID, permissionlib.DecisionAllow, permissionlib.ScopeSession, "s1")
	}()

	resp := e.WaitForApproval(context.Background(), req)
	if resp.Decision != permissionlib.DecisionAllow {
		t.Errorf("expected allow, got %s", resp.Decision)
	}
	if resp.Scope != permissionlib.ScopeSession {
		t.Errorf("expected session scope, got %s", resp.Scope)
	}
	if resp.TimedOut {
		t.Error("expected TimedOut=false for user response")
	}

	// Session grant should have been recorded.
	result := e.Check(context.Background(), "s1", "shell", nil, permissionlib.ToolMeta{IsDestructive: true})
	if result.Decision != permissionlib.DecisionAllow || result.Reason != "session grant" {
		t.Error("expected session grant to be recorded")
	}
}

func TestApprovalTimeout(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	e.SetApprovalTimeout(100 * time.Millisecond)

	req := e.RequestApproval("s1", "shell", nil, "test")
	resp := e.WaitForApproval(context.Background(), req)
	if resp.Decision != permissionlib.DecisionDeny {
		t.Errorf("timeout should default to deny, got %s", resp.Decision)
	}
	if !resp.TimedOut {
		t.Error("expected TimedOut=true on timeout")
	}
}

func TestRespondToExpired(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	ok := e.Respond("nonexistent", permissionlib.DecisionAllow, permissionlib.ScopeOnce, "")
	if ok {
		t.Error("responding to nonexistent request should return false")
	}
}

func TestApprovalContextCancel(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	e.SetApprovalTimeout(10 * time.Second) // long timeout, won't fire

	req := e.RequestApproval("s1", "rm", nil, "destructive")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	resp := e.WaitForApproval(ctx, req)
	if resp.Decision != permissionlib.DecisionDeny {
		t.Errorf("expected deny on cancel, got %s", resp.Decision)
	}
	if resp.TimedOut {
		t.Error("expected TimedOut=false on context cancel (not a timeout)")
	}
}
