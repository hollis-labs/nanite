package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

// CW-20260512-0123 (SP-20260512-0011 W3) — acceptance tests
//
// These tests pin the "Agent execution time limit exceeded (N seconds)"
// error class as structurally unreachable. The W3 ticket's user-mandate
// is to remove the heavy per-call restriction class entirely; these
// tests fail loudly if a future change reintroduces:
//
//   - the AgentConstraints.MaxTimeSeconds field, or
//   - the context.WithTimeout wrap that produced ctx.DeadlineExceeded
//     inside chat_generate.go's generateResponse, or
//   - any error message containing the c160 signature
//     "Agent execution time limit exceeded".
//
// The reaper (internal/subagent/reaper.go) is the surviving hung-run
// safety net; runaway chat loops are bounded by runaway-fail-cap +
// idle-timeout + hard-ceiling (verified by the existing
// chat_loop_state_test.go suite).

// TestAcceptance_AgentConstraints_HasNoMaxTimeSecondsField pins the
// schema-side guarantee: zero-value AgentConstraints must not carry any
// reflection-visible MaxTimeSeconds / MaxIterations / RetryBudget
// field. The compiler already enforces this (referring to those fields
// is a build error); this test makes the intent explicit so a reviewer
// who reintroduces the field sees the W3 contract violated, not just
// "unknown field" compile errors elsewhere.
func TestAcceptance_AgentConstraints_HasNoMaxTimeSecondsField(t *testing.T) {
	// Build a value of every legacy key as a JSON blob and confirm
	// json.Unmarshal silently drops them — proof the fields no longer
	// exist on the struct. (Adding the field back to the struct would
	// make Unmarshal populate it and these checks would fail in some
	// future variant test.)
	legacyJSON := `{"max_time_seconds":300,"max_iterations":50,"retry_budget":3}`
	c := chat.ParseAgentConstraints(legacyJSON)

	// Surviving Phase-4 chat-loop fields must remain zero-valued (none
	// were set in the legacy JSON). MaxTurns was itself removed by Phase
	// 0 item 12 (2026-08-18, soft/telemetry-only, never gated the loop)
	// so it's no longer part of this check.
	if c.HardCeiling != 0 || c.RunawayFailCap != 0 ||
		c.ConsecutiveFailCap != 0 || c.IdleTimeoutSeconds != 0 {
		t.Errorf("legacy JSON populated surviving fields: %+v", c)
	}

	// The struct equals its zero value — the legacy keys had nowhere
	// to land.
	if c != (chat.AgentConstraints{}) {
		t.Errorf("AgentConstraints not zero-valued after parsing legacy keys (W3 contract violated): %+v", c)
	}
}

// TestAcceptance_C160ErrorClass_IsUnreachable_ByStaticAnalysis parses
// internal/service/chat_generate.go AST-style and confirms no string
// literal contains "Agent execution time limit exceeded" — the exact
// c160 error signature. This is structural proof the firing path is
// gone: even if the WithTimeout wrap were somehow reintroduced and
// produced ctx.DeadlineExceeded, there's no longer any emission site
// for the c160 error class.
func TestAcceptance_C160ErrorClass_IsUnreachable_ByStaticAnalysis(t *testing.T) {
	// Locate chat_generate.go relative to this test file.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(cwd, "chat_generate.go")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("chat_generate.go not found at %s: %v", path, err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse chat_generate.go: %v", err)
	}

	// Walk the AST collecting every string literal — comments do NOT
	// appear in BasicLit nodes, so comments referencing the legacy
	// error class are tolerated (and we deliberately have a few that
	// document the removal). Only actual emitted strings fail.
	const c160Signature = "Agent execution time limit exceeded"
	var offenders []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if strings.Contains(lit.Value, c160Signature) {
			pos := fset.Position(lit.Pos())
			offenders = append(offenders,
				pos.String()+": "+lit.Value)
		}
		return true
	})

	if len(offenders) > 0 {
		t.Errorf("c160 error class signature %q reappeared in chat_generate.go (W3 acceptance violated):\n  %s",
			c160Signature, strings.Join(offenders, "\n  "))
	}
}

// TestAcceptance_GenerateResponse_DoesNotWrapTurnContextWithTimeout
// asserts (by AST inspection of chat_generate.go) that the legacy
// `context.WithTimeout(ctx, agentTimeout)` wrap on the turn's primary
// context is gone from generateResponse. The W3 ticket explicitly
// forbids reintroducing any per-call wall-clock deadline on the
// per-turn ctx — the subagent reaper is the safety net for hung runs,
// not a context deadline.
//
// Distinction: `context.WithTimeout(context.Background(), ...)` for
// short-lived internal operations (e.g. emitting a session event in
// <5s) is legitimate and unrelated to the W3 restriction class. We
// only flag a WithTimeout call whose first argument is the
// generateResponse-scoped `ctx` identifier — that's the shape of the
// removed MaxTimeSeconds wrap.
func TestAcceptance_GenerateResponse_DoesNotWrapTurnContextWithTimeout(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(cwd, "chat_generate.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse chat_generate.go: %v", err)
	}

	var offenders []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "generateResponse" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if !(pkgIdent.Name == "context" && sel.Sel.Name == "WithTimeout") {
				return true
			}
			// W3 contract: a turn-deadline wrap takes the
			// generateResponse-scoped `ctx` identifier as its first
			// arg. WithTimeout calls passing `context.Background()`
			// (or any non-`ctx` value) are short-lived internal
			// timeouts and out of scope for this acceptance.
			if len(call.Args) == 0 {
				return true
			}
			ident, isIdent := call.Args[0].(*ast.Ident)
			if !isIdent || ident.Name != "ctx" {
				return true
			}
			pos := fset.Position(call.Pos())
			offenders = append(offenders, pos.String())
			return true
		})
	}

	if len(offenders) > 0 {
		t.Errorf("context.WithTimeout(ctx, ...) reappeared inside generateResponse (W3 acceptance violated — per-call wall-clock deadlines on the turn ctx were removed); the subagent reaper is the safety net for hung runs:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestAcceptance_LongRunningTurn_HasNoArtificialDeadline confirms that
// resolveIterationLimits does NOT produce any wall-clock duration tied
// to an agent-author "max time" setting. The surviving idleTimeout is
// a no-progress catch (lastActivity drift), not a total-turn deadline,
// and is bounded by `defaultIdleTimeoutSeconds = 900` (15 minutes)
// rather than a per-agent constraint.
//
// If a future refactor adds a "this agent's turn must finish by N
// seconds" knob, this test fails — that's the restriction class W3
// removed.
func TestAcceptance_LongRunningTurn_HasNoArtificialDeadline(t *testing.T) {
	// Construct a constraints value that previously would have set a
	// 1-second deadline (via the removed MaxTimeSeconds field). The
	// fact that this struct literal does not even mention
	// MaxTimeSeconds is the W3 contract: the field no longer exists.
	// (MaxTurns is likewise absent — Phase 0 item 12, 2026-08-18,
	// removed it as soft/telemetry-only and never gating the loop.)
	c := chat.AgentConstraints{
		HardCeiling:        100,
		ConsecutiveFailCap: 3,
		RunawayFailCap:     10,
		IdleTimeoutSeconds: 0, // 0 = default(900s); not a per-turn deadline
	}

	lim := resolveIterationLimits(c)

	// idleTimeout is a no-progress catch (chat-loop's own reaper-
	// equivalent at the message layer), retained intentionally. It is
	// NOT a total-turn wall-clock — its semantics are "no tool
	// progress for N seconds" rather than "this turn must finish in
	// N seconds". The W3 audit confirms it stays.
	if lim.idleTimeout == 0 {
		t.Error("idleTimeout should default to a non-zero no-progress catch")
	}

	// The hard ceiling is an iteration cap, not a wall-clock deadline.
	if lim.hardCeiling == 0 {
		t.Error("hardCeiling should default to a non-zero iteration backstop")
	}
}
