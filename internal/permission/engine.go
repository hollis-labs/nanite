package permission

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Decision is the outcome of a permission check.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	DecisionAsk   Decision = "ask"
)

// Mode controls the overall permission stance for a session or project.
type Mode string

const (
	ModeDefault     Mode = "default"      // prompt for destructive/write operations
	ModeAcceptEdits Mode = "accept-edits" // auto-accept file edits, prompt for shell
	ModePlan        Mode = "plan"         // read-only, no modifications
	ModeYolo        Mode = "yolo"         // skip all permission prompts
)

// CheckResult holds the decision and metadata from a permission check.
type CheckResult struct {
	Decision   Decision
	RequestID  string // set when Decision == DecisionAsk
	MatchedRule *Rule  // the rule that matched, if any
	Reason     string // human-readable reason
}

// ApprovalRequest represents a pending permission prompt waiting for user input.
type ApprovalRequest struct {
	ID        string
	SessionID string
	ToolName  string
	Input     map[string]any
	Reason    string
	CreatedAt time.Time
	Response  chan ApprovalResponse // closed when response received or timeout
}

// ApprovalResponse is the user's answer to an approval request.
type ApprovalResponse struct {
	Decision Decision
	Scope    Scope
	TimedOut bool // true when denial was due to timeout, not explicit user action
}

// ToolMeta carries tool metadata used by the permission engine.
// Populated from the tool's interface methods (Phase 1).
type ToolMeta struct {
	IsReadOnly    bool
	IsDestructive bool
}

// Engine evaluates permission rules and manages the approval flow.
type Engine struct {
	mu             sync.RWMutex
	mode           Mode
	rules          *RuleSet
	sessionGrants  map[string]map[string]Decision // sessionID -> toolName -> decision
	pendingApprovals sync.Map                      // requestID -> *ApprovalRequest
	approvalTimeout  time.Duration
}

// NewEngine creates a permission engine with the given mode and rules.
func NewEngine(mode Mode, rules *RuleSet) *Engine {
	if rules == nil {
		rules = &RuleSet{}
	}
	return &Engine{
		mode:            mode,
		rules:           rules,
		sessionGrants:   make(map[string]map[string]Decision),
		approvalTimeout: 60 * time.Second,
	}
}

// SetMode changes the permission mode. Thread-safe.
func (e *Engine) SetMode(mode Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = mode
}

// Mode returns the current permission mode.
func (e *Engine) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// SetRules replaces the rule set. Thread-safe.
func (e *Engine) SetRules(rules *RuleSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = rules
}

// SetApprovalTimeout overrides the default 60s timeout.
func (e *Engine) SetApprovalTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.approvalTimeout = d
}

// Check evaluates whether a tool invocation is allowed, denied, or needs approval.
// This is the main entry point called from the chat loop before tool execution.
func (e *Engine) Check(ctx context.Context, sessionID, toolName string, input map[string]any, meta ToolMeta) CheckResult {
	e.mu.RLock()
	mode := e.mode
	rules := e.rules
	grants := e.sessionGrants[sessionID]
	e.mu.RUnlock()

	// Yolo mode: skip everything.
	if mode == ModeYolo {
		return CheckResult{Decision: DecisionAllow, Reason: "yolo mode active"}
	}

	// Plan mode: deny all writes.
	if mode == ModePlan {
		if !meta.IsReadOnly {
			return CheckResult{Decision: DecisionDeny, Reason: "plan mode — write operations blocked"}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "plan mode — read-only operation allowed"}
	}

	// Check session grants (from previous "allow for session" approvals).
	if grants != nil {
		if d, ok := grants[toolName]; ok && d == DecisionAllow {
			return CheckResult{Decision: DecisionAllow, Reason: "session grant"}
		}
	}

	// Evaluate rules (deny > ask > allow, first match wins per priority).
	if rules != nil && len(rules.Rules) > 0 {
		if result := rules.Evaluate(toolName, input); result != nil {
			return *result
		}
	}

	// Default behavior per mode.
	return e.defaultDecision(mode, toolName, meta)
}

// defaultDecision applies mode-based defaults when no rule matches.
func (e *Engine) defaultDecision(mode Mode, toolName string, meta ToolMeta) CheckResult {
	switch mode {
	case ModeAcceptEdits:
		// Auto-accept file edits. Ask for other writes.
		if isFileEditTool(toolName) {
			return CheckResult{Decision: DecisionAllow, Reason: "accept-edits mode — file edit auto-allowed"}
		}
		if meta.IsDestructive {
			return CheckResult{Decision: DecisionAsk, Reason: "accept-edits mode — destructive operation requires approval"}
		}
		if !meta.IsReadOnly {
			return CheckResult{Decision: DecisionAsk, Reason: "accept-edits mode — non-edit write requires approval"}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "accept-edits mode — read-only operation"}

	default: // ModeDefault
		if meta.IsDestructive {
			return CheckResult{Decision: DecisionAsk, Reason: "default mode — destructive operation requires approval"}
		}
		if meta.IsReadOnly {
			return CheckResult{Decision: DecisionAllow, Reason: "default mode — read-only operation"}
		}
		return CheckResult{Decision: DecisionAllow, Reason: "default mode — non-destructive operation"}
	}
}

// RequestApproval creates a pending approval request and returns it.
// The caller should emit this on SSE and then call WaitForApproval.
func (e *Engine) RequestApproval(sessionID, toolName string, input map[string]any, reason string) *ApprovalRequest {
	req := &ApprovalRequest{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		ToolName:  toolName,
		Input:     input,
		Reason:    reason,
		CreatedAt: time.Now(),
		Response:  make(chan ApprovalResponse, 1),
	}
	e.pendingApprovals.Store(req.ID, req)
	log.Printf("permission: approval request %s created for tool %s in session %s",
		req.ID, toolName, sessionID)
	return req
}

// WaitForApproval blocks until the user responds or the timeout expires.
// Returns deny on timeout.
func (e *Engine) WaitForApproval(ctx context.Context, req *ApprovalRequest) ApprovalResponse {
	e.mu.RLock()
	timeout := e.approvalTimeout
	e.mu.RUnlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	defer e.pendingApprovals.Delete(req.ID)

	select {
	case resp := <-req.Response:
		log.Printf("permission: approval %s responded: %s (scope: %s)", req.ID, resp.Decision, resp.Scope)
		return resp
	case <-timer.C:
		log.Printf("permission: approval %s timed out after %s — defaulting to deny", req.ID, timeout)
		return ApprovalResponse{Decision: DecisionDeny, Scope: ScopeOnce, TimedOut: true}
	case <-ctx.Done():
		log.Printf("permission: approval %s cancelled — defaulting to deny", req.ID)
		return ApprovalResponse{Decision: DecisionDeny, Scope: ScopeOnce, TimedOut: true}
	}
}

// Respond delivers a response to a pending approval request.
// Returns false if the request doesn't exist (already timed out or responded).
func (e *Engine) Respond(requestID string, decision Decision, scope Scope, sessionID string) bool {
	val, ok := e.pendingApprovals.Load(requestID)
	if !ok {
		return false
	}
	req := val.(*ApprovalRequest)

	// Validate the approval request belongs to the claimed session.
	if sessionID != "" && req.SessionID != sessionID {
		return false
	}

	// Record session grant if scope is session.
	if decision == DecisionAllow && scope == ScopeSession {
		e.mu.Lock()
		if e.sessionGrants[req.SessionID] == nil {
			e.sessionGrants[req.SessionID] = make(map[string]Decision)
		}
		e.sessionGrants[req.SessionID][req.ToolName] = DecisionAllow
		e.mu.Unlock()
	}

	select {
	case req.Response <- ApprovalResponse{Decision: decision, Scope: scope}:
		return true
	default:
		return false // already responded
	}
}

// ClearSessionGrants removes all session-level grants for a session.
// Called on session end.
func (e *Engine) ClearSessionGrants(sessionID string) {
	e.mu.Lock()
	delete(e.sessionGrants, sessionID)
	e.mu.Unlock()
}

// isFileEditTool returns true for tools that are file edit operations.
func isFileEditTool(name string) bool {
	switch name {
	case "mcp__dev__edit", "mcp__dev__write", "dev_edit", "dev_write":
		return true
	}
	return false
}
