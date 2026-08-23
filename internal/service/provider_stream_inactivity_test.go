package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	feotel "github.com/hollis-labs/go-otel"
	"github.com/hollis-labs/nanite/internal/chat"
	"go.opentelemetry.io/otel/trace"
)

// CW-20260517-0036 — provider-stream inactivity timeout acceptance.
//
// These tests pin the structural shape of the within-stream inactivity
// watchdog now owned by consumeProviderIteration's `streamLoop`. The bug (a silently
// stalled provider stream consuming the whole subagent-run budget) was a
// `for evt := range provCh` that blocks indefinitely when the provider
// holds the channel open but emits nothing. CW-20260519-0073 only bounds
// the stall at the *next* chat-loop iteration boundary, which a stalled
// streamLoop never reaches. The fix replaces the bare range with a
// `select` over the provider channel AND a resettable inactivity timer.
//
// The AST acceptances pin the load-bearing structure and the focused
// behavioral test below invokes the action with a short run-scoped window.

// parseChatGenerate returns the parsed chat_generate.go AST.
func parseGenerationActions(t *testing.T) (*token.FileSet, *ast.File) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(cwd, "chat_generation_actions.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse chat_generation_actions.go: %v", err)
	}
	return fset, file
}

// consumeProviderIterationBody returns the action's block.
func consumeProviderIterationBody(t *testing.T, file *ast.File) *ast.BlockStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == "consumeProviderIteration" && fn.Body != nil {
			return fn.Body
		}
	}
	t.Fatal("consumeProviderIteration function not found in chat_generation_actions.go")
	return nil
}

// TestAcceptance_StreamLoop_ConsumesViaSelect confirms the provider
// event channel is consumed inside a `select` statement (the watchdog
// shape) and not a bare `for range` that blocks indefinitely on a
// silent stall.
func TestAcceptance_StreamLoop_ConsumesViaSelect(t *testing.T) {
	_, file := parseGenerationActions(t)
	body := consumeProviderIterationBody(t, file)

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
		t.Fatal("streamLoop label not found in consumeProviderIteration — the provider " +
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
	_, file := parseGenerationActions(t)
	body := consumeProviderIterationBody(t, file)

	src := mustReadGenerationActions(t)
	for _, needle := range []string{
		"time.NewTimer(streamInactivityWindow)",    // timer armed
		"streamIdle.Reset(streamInactivityWindow)", // reset on every provider event
		"run.loop.limits.idleTimeout",              // window reuses the chat loop's liveness budget
		"streamStalled",                            // stall flag drives the post-loop handler
		"attempt.cancelStream()",                   // stall tears down the HTTP stream
	} {
		if !strings.Contains(src, needle) {
			t.Errorf("chat_generation_actions.go missing expected inactivity-watchdog token %q", needle)
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

func TestConsumeProviderIteration_InactivityTerminatesAndClosesAttempt(t *testing.T) {
	f := newCharacterizationFixture(t, nil)
	agent := f.svc.agents.(*characterizationAgents).agent
	run := &runState{loop: newLoopState(chat.AgentConstraints{}, nil, false)}
	run.loop.iteration = 1
	run.loop.limits.idleTimeout = 15 * time.Millisecond
	setup := &turnSetup{
		agent: agent, model: "characterization-model", providerName: "characterization",
		slotResult: &SlotAssemblyResult{},
	}
	stalled := make(chan llmtypes.StreamEvent)
	cancelCalls := 0
	_, span := feotel.StartSpan(context.Background(), "test.consume-provider-inactivity")
	attempt := &providerAttempt{events: stalled, cancel: func() { cancelCalls++ }, span: span}
	stream := make(chan chat.StreamEvent, 8)

	started := time.Now()
	result := f.svc.consumeProviderIteration(context.Background(), f.session, "assistant-stalled", setup, run, attempt, stream)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("inactivity termination took %s, want under 1s", elapsed)
	}
	if result.directive != generationTerminate {
		t.Fatalf("directive = %v, want generationTerminate", result.directive)
	}
	if cancelCalls != 1 {
		t.Fatalf("attempt cancel calls = %d, want exactly 1", cancelCalls)
	}
	var sawError bool
	for len(stream) > 0 {
		if evt := <-stream; evt.Type == "error" {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("inactivity termination emitted no error event")
	}
}

type countingAttemptSpan struct {
	trace.Span
	endCalls int
}

func (s *countingAttemptSpan) End(...trace.SpanEndOption) { s.endCalls++ }

func TestProviderAttempt_CloseIsIdempotent(t *testing.T) {
	cancelCalls := 0
	_, baseSpan := feotel.StartSpan(context.Background(), "test.provider-attempt-close")
	span := &countingAttemptSpan{Span: baseSpan}
	attempt := &providerAttempt{cancel: func() { cancelCalls++ }, span: span}

	attempt.cancelStream()
	attempt.cancelStream()
	attempt.close()
	attempt.close()

	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want exactly 1", cancelCalls)
	}
	if span.endCalls != 1 {
		t.Fatalf("span end calls = %d, want exactly 1", span.endCalls)
	}
}

func mustReadGenerationActions(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, "chat_generation_actions.go"))
	if err != nil {
		t.Fatalf("read chat_generation_actions.go: %v", err)
	}
	return string(b)
}
