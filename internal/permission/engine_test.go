package permission

import (
	"context"
	"testing"
	"time"
)

func TestCheck_yoloMode(t *testing.T) {
	e := NewEngine(ModeYolo, nil)
	result := e.Check(context.Background(), "s1", "any_tool", nil, ToolMeta{IsDestructive: true})
	if result.Decision != DecisionAllow {
		t.Errorf("yolo mode should allow everything, got %s", result.Decision)
	}
}

func TestCheck_planMode_blocksWrites(t *testing.T) {
	e := NewEngine(ModePlan, nil)

	result := e.Check(context.Background(), "s1", "dev_write", nil, ToolMeta{IsReadOnly: false})
	if result.Decision != DecisionDeny {
		t.Errorf("plan mode should deny writes, got %s", result.Decision)
	}

	result = e.Check(context.Background(), "s1", "dev_read", nil, ToolMeta{IsReadOnly: true})
	if result.Decision != DecisionAllow {
		t.Errorf("plan mode should allow reads, got %s", result.Decision)
	}
}

func TestCheck_defaultMode_asksForDestructive(t *testing.T) {
	e := NewEngine(ModeDefault, nil)

	result := e.Check(context.Background(), "s1", "shell", nil, ToolMeta{IsDestructive: true})
	if result.Decision != DecisionAsk {
		t.Errorf("default mode should ask for destructive, got %s", result.Decision)
	}

	result = e.Check(context.Background(), "s1", "dev_read", nil, ToolMeta{IsReadOnly: true})
	if result.Decision != DecisionAllow {
		t.Errorf("default mode should allow read-only, got %s", result.Decision)
	}
}

func TestCheck_defaultMode_allowsNonDestructivePathGatedWrite(t *testing.T) {
	e := NewEngine(ModeDefault, nil)

	result := e.Check(context.Background(), "s1", "dev_write",
		map[string]any{"path": "/tmp/output.txt", "content": "hello"}, ToolMeta{})
	if result.Decision != DecisionAllow {
		t.Errorf("default mode should allow non-destructive path-gated writes, got %s", result.Decision)
	}
}

func TestCheck_acceptEditsMode(t *testing.T) {
	e := NewEngine(ModeAcceptEdits, nil)

	// File edits auto-allowed (uniform name post ADR-002).
	result := e.Check(context.Background(), "s1", "dev_edit", nil, ToolMeta{})
	if result.Decision != DecisionAllow {
		t.Errorf("accept-edits should auto-allow file edits, got %s", result.Decision)
	}

	// Destructive non-edit asks.
	result = e.Check(context.Background(), "s1", "shell", nil, ToolMeta{IsDestructive: true})
	if result.Decision != DecisionAsk {
		t.Errorf("accept-edits should ask for destructive non-edits, got %s", result.Decision)
	}
}

func TestCheck_ruleOverride(t *testing.T) {
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "shell", Pattern: "rm -rf", Behavior: DecisionDeny},
			{Tool: "dev_edit", Pattern: "/src/**", Behavior: DecisionAllow},
			{Tool: "task_delete", Behavior: DecisionAsk}, // formerly mcp__engine__task_delete
		},
	}
	e := NewEngine(ModeDefault, rules)

	// Shell with rm -rf: denied by rule.
	result := e.Check(context.Background(), "s1", "shell",
		map[string]any{"command": "rm -rf /tmp/stuff"}, ToolMeta{})
	if result.Decision != DecisionDeny {
		t.Errorf("expected deny for rm -rf, got %s", result.Decision)
	}

	// Edit in /src: allowed by rule.
	result = e.Check(context.Background(), "s1", "dev_edit",
		map[string]any{"path": "/src/main.go"}, ToolMeta{})
	if result.Decision != DecisionAllow {
		t.Errorf("expected allow for /src edit, got %s", result.Decision)
	}

	// Task delete: ask by rule.
	result = e.Check(context.Background(), "s1", "task_delete",
		nil, ToolMeta{})
	if result.Decision != DecisionAsk {
		t.Errorf("expected ask for task delete, got %s", result.Decision)
	}
}

func TestCheck_sessionGrant(t *testing.T) {
	e := NewEngine(ModeDefault, nil)

	// First check: destructive tool asks.
	result := e.Check(context.Background(), "s1", "shell", nil, ToolMeta{IsDestructive: true})
	if result.Decision != DecisionAsk {
		t.Fatalf("expected ask, got %s", result.Decision)
	}

	// Grant session permission.
	e.mu.Lock()
	e.sessionGrants["s1"] = map[string]Decision{"shell": DecisionAllow}
	e.mu.Unlock()

	// Second check: should be allowed by session grant.
	result = e.Check(context.Background(), "s1", "shell", nil, ToolMeta{IsDestructive: true})
	if result.Decision != DecisionAllow {
		t.Errorf("expected allow from session grant, got %s", result.Decision)
	}

	// Clear grants.
	e.ClearSessionGrants("s1")
	result = e.Check(context.Background(), "s1", "shell", nil, ToolMeta{IsDestructive: true})
	if result.Decision != DecisionAsk {
		t.Errorf("expected ask after grant cleared, got %s", result.Decision)
	}
}

func TestRuleSet_denyTakesPriority(t *testing.T) {
	rules := &RuleSet{
		Rules: []Rule{
			{Tool: "shell", Behavior: DecisionAllow},
			{Tool: "shell", Pattern: "rm", Behavior: DecisionDeny},
		},
	}

	// Even though allow comes first in the list, deny wins.
	result := rules.Evaluate("shell", map[string]any{"command": "rm -rf /"})
	if result == nil || result.Decision != DecisionDeny {
		t.Errorf("deny should take priority over allow")
	}
}

func TestApprovalFlow(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(500 * time.Millisecond)

	req := e.RequestApproval("s1", "shell", nil, "test")

	// Respond in a goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.Respond(req.ID, DecisionAllow, ScopeSession, "s1")
	}()

	resp := e.WaitForApproval(context.Background(), req)
	if resp.Decision != DecisionAllow {
		t.Errorf("expected allow, got %s", resp.Decision)
	}
	if resp.Scope != ScopeSession {
		t.Errorf("expected session scope, got %s", resp.Scope)
	}
	if resp.TimedOut {
		t.Error("expected TimedOut=false for user response")
	}

	// Session grant should have been recorded.
	e.mu.RLock()
	d, ok := e.sessionGrants["s1"]["shell"]
	e.mu.RUnlock()
	if !ok || d != DecisionAllow {
		t.Error("expected session grant to be recorded")
	}
}

func TestApprovalTimeout(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(100 * time.Millisecond)

	req := e.RequestApproval("s1", "shell", nil, "test")
	resp := e.WaitForApproval(context.Background(), req)
	if resp.Decision != DecisionDeny {
		t.Errorf("timeout should default to deny, got %s", resp.Decision)
	}
	if !resp.TimedOut {
		t.Error("expected TimedOut=true on timeout")
	}
}

func TestRespondToExpired(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	ok := e.Respond("nonexistent", DecisionAllow, ScopeOnce, "")
	if ok {
		t.Error("responding to nonexistent request should return false")
	}
}

func TestApprovalContextCancel(t *testing.T) {
	e := NewEngine(ModeDefault, nil)
	e.SetApprovalTimeout(10 * time.Second) // long timeout, won't fire

	req := e.RequestApproval("s1", "rm", nil, "destructive")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	resp := e.WaitForApproval(ctx, req)
	if resp.Decision != DecisionDeny {
		t.Errorf("expected deny on cancel, got %s", resp.Decision)
	}
	if resp.TimedOut {
		t.Error("expected TimedOut=false on context cancel (not a timeout)")
	}
}
