package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CW-20260517-0036 — provider-stream inactivity timeout acceptance.
//
// These tests pin the structural shape of the within-stream inactivity
// watchdog added to generateResponse's `streamLoop`. The bug (a silently
// stalled provider stream consuming the whole subagent-run budget) was a
// `for evt := range provCh` that blocks indefinitely when the provider
// holds the channel open but emits nothing. CW-20260519-0073 only bounds
// the stall at the *next* chat-loop iteration boundary, which a stalled
// streamLoop never reaches. The fix replaces the bare range with a
// `select` over the provider channel AND a resettable inactivity timer.
//
// A full behavioral test would have to drive generateResponse with a
// stalled fake provider for the entire inactivity window (300s for a
// subagent dispatch) — too slow for a unit test, and generateResponse
// has no test-injection seam for the window. These AST acceptances pin
// the load-bearing structure instead, mirroring the
// chat_generate_no_max_time_seconds_test.go precedent.

// parseChatGenerate returns the parsed chat_generate.go AST.
func parseChatGenerate(t *testing.T) (*token.FileSet, *ast.File) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(cwd, "chat_generate.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse chat_generate.go: %v", err)
	}
	return fset, file
}

// generateResponseBody returns the *ast.BlockStmt for generateResponse.
func generateResponseBody(t *testing.T, file *ast.File) *ast.BlockStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == "generateResponse" && fn.Body != nil {
			return fn.Body
		}
	}
	t.Fatal("generateResponse function not found in chat_generate.go")
	return nil
}

// TestAcceptance_StreamLoop_ConsumesViaSelect confirms the provider
// event channel is consumed inside a `select` statement (the watchdog
// shape) and not a bare `for range` that blocks indefinitely on a
// silent stall.
func TestAcceptance_StreamLoop_ConsumesViaSelect(t *testing.T) {
	_, file := parseChatGenerate(t)
	body := generateResponseBody(t, file)

	var sawLabeledStreamLoop bool
	var streamLoopHasSelect bool
	ast.Inspect(body, func(n ast.Node) bool {
		labeled, ok := n.(*ast.LabeledStmt)
		if !ok || labeled.Label == nil || labeled.Label.Name != "streamLoop" {
			return true
		}
		sawLabeledStreamLoop = true
		// The streamLoop label must front a `for` whose body contains a
		// `select` — that select is the inactivity watchdog.
		forStmt, ok := labeled.Stmt.(*ast.ForStmt)
		if !ok || forStmt.Body == nil {
			return true
		}
		ast.Inspect(forStmt.Body, func(inner ast.Node) bool {
			if _, ok := inner.(*ast.SelectStmt); ok {
				streamLoopHasSelect = true
				return false
			}
			return true
		})
		return false
	})

	if !sawLabeledStreamLoop {
		t.Fatal("streamLoop label not found in generateResponse — the provider " +
			"stream consume loop structure changed; update this acceptance")
	}
	if !streamLoopHasSelect {
		t.Error("streamLoop is not a `select`-based loop — the provider-stream " +
			"inactivity watchdog (CW-20260517-0036) appears to have been removed; " +
			"a bare `for evt := range provCh` blocks indefinitely on a silent stall")
	}
}

// TestAcceptance_StreamLoop_ArmsInactivityTimer confirms a time.Timer is
// constructed for the streamLoop window and reset on provider events —
// the reset-on-activity is what prevents the watchdog from killing a
// legitimate stream that is merely slow but still emitting.
func TestAcceptance_StreamLoop_ArmsInactivityTimer(t *testing.T) {
	_, file := parseChatGenerate(t)
	body := generateResponseBody(t, file)

	src := mustReadChatGenerate(t)
	for _, needle := range []string{
		"time.NewTimer(streamInactivityWindow)", // timer armed
		"streamIdle.Reset(streamInactivityWindow)", // reset on every provider event
		"ls.limits.idleTimeout",                    // window reuses the chat loop's liveness budget
		"streamStalled",                            // stall flag drives the post-loop handler
		"provStreamCancel()",                       // stall tears down the HTTP stream
	} {
		if !strings.Contains(src, needle) {
			t.Errorf("chat_generate.go missing expected inactivity-watchdog token %q", needle)
		}
	}
	_ = body
}

// TestTimerResetIdiom is a self-contained sanity check on the
// stop-drain-reset Timer idiom used by the streamLoop watchdog: after a
// reset the timer fires on the NEW window, and an event arriving before
// the window elapses keeps the loop alive.
func TestTimerResetIdiom(t *testing.T) {
	const window = 40 * time.Millisecond
	timer := time.NewTimer(window)
	defer timer.Stop()

	// Simulate three events arriving within the window — each resets it.
	for i := 0; i < 3; i++ {
		time.Sleep(window / 2)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(window)
		select {
		case <-timer.C:
			t.Fatalf("timer fired after reset %d despite an event within the window", i)
		default:
		}
	}

	// Now go silent past the window — the watchdog must fire.
	select {
	case <-timer.C:
		// expected
	case <-time.After(window * 4):
		t.Fatal("timer did not fire after the inactivity window elapsed")
	}
}

func mustReadChatGenerate(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, "chat_generate.go"))
	if err != nil {
		t.Fatalf("read chat_generate.go: %v", err)
	}
	return string(b)
}
