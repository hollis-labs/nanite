package skill

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// Static proof: this file never spawns a subprocess directly.
// ---------------------------------------------------------------------

// TestExec_NoDirectSubprocessSpawn is this task's own Done-means
// requirement made concrete: "a test confirms no execution path in this
// file calls exec.Command (or equivalent) directly, bypassing the gate."
// Parses exec.go's own AST and asserts it imports neither "os/exec" nor
// "syscall" — a structural guarantee (not a string grep, which a comment
// mentioning "exec.Command" for documentation purposes would trip) that
// this file cannot construct a subprocess itself, gated or not. All real
// execution goes through the injected GatedExecutor interface instead.
func TestExec_NoDirectSubprocessSpawn(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "exec.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse exec.go: %v", err)
	}

	forbidden := map[string]bool{
		`"os/exec"`: true,
		`"syscall"`: true,
	}
	for _, imp := range f.Imports {
		if forbidden[imp.Path.Value] {
			t.Fatalf("exec.go imports %s — this file must never spawn a subprocess directly, only through GatedExecutor", imp.Path.Value)
		}
	}

	// Belt-and-braces: also confirm no identifier in the whole file is
	// literally "exec" resolving to a package selector like exec.Command —
	// walk the full AST (not just imports) for a SelectorExpr whose X is
	// an Ident named "exec". Since the import above is already absent,
	// this would only ever fire if a local variable/package alias were
	// named "exec" some other way — defense in depth against future edits
	// that might reintroduce a direct spawn under a different import
	// alias.
	full, err := parser.ParseFile(fset, "exec.go", nil, 0)
	if err != nil {
		t.Fatalf("parse exec.go (full): %v", err)
	}
	ast.Inspect(full, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "exec" && sel.Sel.Name == "Command" {
			t.Fatalf("found exec.Command call in exec.go")
		}
		return true
	})
}

// ---------------------------------------------------------------------
// Fence-awareness — the single most important regression test in this
// task, per its own Done-means: a real marker outside a fence executes;
// the identical literal text inside a fenced code block does not.
// ---------------------------------------------------------------------

func TestFindInlineMarkers_CodeFenceAwareness(t *testing.T) {
	body := "Intro text.\n" +
		"\n" +
		"!`echo real-marker`\n" +
		"\n" +
		"Here is an example of the marker syntax:\n" +
		"\n" +
		"```\n" +
		"Example: !`some command`\n" +
		"```\n" +
		"\n" +
		"Trailing prose."

	markers := FindInlineMarkers(body)
	if len(markers) != 1 {
		t.Fatalf("FindInlineMarkers found %d marker(s), want exactly 1 (the fenced literal must not be reported): %+v", len(markers), markers)
	}
	if markers[0].Command != "echo real-marker" {
		t.Errorf("Command = %q, want %q", markers[0].Command, "echo real-marker")
	}
	// The reported span must point at the real marker's own location in
	// the original body, not the fenced one.
	if got := body[markers[0].Start:markers[0].End]; got != "!`echo real-marker`" {
		t.Errorf("marker span = %q, want the real marker's exact text", got)
	}
}

func TestFindInlineMarkers_TildeFence(t *testing.T) {
	body := "!`echo real`\n" +
		"~~~\n" +
		"!`echo fenced`\n" +
		"~~~\n"

	markers := FindInlineMarkers(body)
	if len(markers) != 1 || markers[0].Command != "echo real" {
		t.Fatalf("markers = %+v, want exactly one real marker", markers)
	}
}

func TestFindInlineMarkers_LongerClosingFenceRequired(t *testing.T) {
	// A closing fence must be at least as long as the opener. A shorter
	// run of the same character does not close the fence, so a marker
	// after it is still inside the (still-open) fence and must not be
	// reported.
	body := "````\n" +
		"```\n" + // shorter run — does not close a 4-backtick fence
		"!`echo still-fenced`\n" +
		"````\n" +
		"!`echo now-real`\n"

	markers := FindInlineMarkers(body)
	if len(markers) != 1 || markers[0].Command != "echo now-real" {
		t.Fatalf("markers = %+v, want exactly the post-fence marker", markers)
	}
}

func TestFindInlineMarkers_NoMarkers(t *testing.T) {
	if got := FindInlineMarkers("Nothing dynamic here."); len(got) != 0 {
		t.Errorf("expected no markers, got %+v", got)
	}
}

func TestFindInlineMarkers_MultipleRealMarkers(t *testing.T) {
	body := "A: !`echo aaa`\nB: !`echo bbb`"
	markers := FindInlineMarkers(body)
	if len(markers) != 2 {
		t.Fatalf("expected 2 markers, got %d: %+v", len(markers), markers)
	}
	if markers[0].Command != "echo aaa" || markers[1].Command != "echo bbb" {
		t.Errorf("unexpected commands: %+v", markers)
	}
}

// TestFindInlineMarkers_RequiresLineStartOrPrecedingWhitespace is the
// regression test for the fresh-reviewer-found bug (TASKS/skills/08's "Fix
// required" section): the real Agent-Skills-spec
// (https://code.claude.com/docs/en/skills) states the inline form is only
// recognized when `!` starts a line or immediately follows whitespace — a
// `!` preceded by any other character (its own spec example: `` KEY=!`cmd` ``)
// must be left as literal text and never reported as a marker. Three cases,
// exactly as the fix instructions specify: the spec's own "does not run"
// example (no preceding whitespace), the spec's own "still runs" case
// (preceded by whitespace), and a marker at the very start of a line with no
// preceding character at all (already covered implicitly by other tests in
// this file, confirmed explicitly here since the precedence check is now
// conditional logic that could regress it).
func TestFindInlineMarkers_RequiresLineStartOrPrecedingWhitespace(t *testing.T) {
	t.Run("no preceding whitespace is left as literal text", func(t *testing.T) {
		body := "KEY=!`echo should-not-run-per-spec`"
		markers := FindInlineMarkers(body)
		if len(markers) != 0 {
			t.Fatalf("markers = %+v, want none — `!` immediately follows `=`, not whitespace or line-start, per the spec's own KEY=!`cmd` example", markers)
		}
	})

	t.Run("preceded by whitespace is still a real marker", func(t *testing.T) {
		body := "VALUE = !`echo should-run`"
		markers := FindInlineMarkers(body)
		if len(markers) != 1 {
			t.Fatalf("markers = %+v, want exactly 1 — `!` immediately follows a space, which the spec explicitly allows", markers)
		}
		if markers[0].Command != "echo should-run" {
			t.Errorf("Command = %q, want %q", markers[0].Command, "echo should-run")
		}
		if got := body[markers[0].Start:markers[0].End]; got != "!`echo should-run`" {
			t.Errorf("marker span = %q, want the marker's exact text", got)
		}
	})

	t.Run("start of line with no preceding character is still a real marker", func(t *testing.T) {
		body := "!`echo at-line-start`\nAfter."
		markers := FindInlineMarkers(body)
		if len(markers) != 1 {
			t.Fatalf("markers = %+v, want exactly 1 — `!` at the very start of a line has nothing preceding it and must be treated as a real marker", markers)
		}
		if markers[0].Command != "echo at-line-start" {
			t.Errorf("Command = %q, want %q", markers[0].Command, "echo at-line-start")
		}
	})
}

// ---------------------------------------------------------------------
// Test double GatedExecutor — the injected implementation this file's own
// instructions require in place of task 09's not-yet-landed real gate.
// Calls exec.Command directly (fine here: this is test-only code, never a
// production execution path within internal/skill itself).
// ---------------------------------------------------------------------

type fakeGate struct {
	calls   int32
	sleep   time.Duration // if set, ExecuteGated blocks (respecting ctx) this long
	lastReq ExecRequest
	err     error // if set, ExecuteGated always returns this error
}

func (g *fakeGate) ExecuteGated(ctx context.Context, req ExecRequest) (ExecResult, error) {
	atomic.AddInt32(&g.calls, 1)
	g.lastReq = req
	if g.err != nil {
		return ExecResult{}, g.err
	}
	if g.sleep > 0 {
		select {
		case <-time.After(g.sleep):
		case <-ctx.Done():
			return ExecResult{}, ctx.Err()
		}
	}

	if len(req.Command) == 0 {
		return ExecResult{}, errors.New("fakeGate: empty command")
	}
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return ExecResult{Stdout: out.String(), Stderr: errBuf.String()}, err
	}
	return ExecResult{Stdout: out.String(), Stderr: errBuf.String()}, nil
}

func (g *fakeGate) callCount() int {
	return int(atomic.LoadInt32(&g.calls))
}

// ---------------------------------------------------------------------
// ResolveInlineMarkers — substitution, fence-awareness end to end, nil
// gate, and failure attribution.
// ---------------------------------------------------------------------

func TestResolveInlineMarkers_SubstitutesRealMarkerOnly(t *testing.T) {
	def := Definition{Slug: "example-skill"}
	gate := &fakeGate{}

	body := "Before.\n" +
		"!`echo hello-world`\n" +
		"After.\n" +
		"\n" +
		"```\n" +
		"!`echo should-not-run`\n" +
		"```\n"

	got, err := ResolveInlineMarkers(context.Background(), gate, def, "agent-1", "", body, ExecOptions{})
	if err != nil {
		t.Fatalf("ResolveInlineMarkers: %v", err)
	}

	if !strings.Contains(got, "hello-world") {
		t.Errorf("expected substituted output in result, got: %q", got)
	}
	if strings.Contains(got, "!`echo hello-world`") {
		t.Errorf("real marker should have been replaced, got: %q", got)
	}
	if !strings.Contains(got, "!`echo should-not-run`") {
		t.Errorf("fenced literal marker must survive unexecuted, got: %q", got)
	}
	if gate.callCount() != 1 {
		t.Errorf("gate called %d times, want exactly 1 (only the real marker)", gate.callCount())
	}
	if gate.lastReq.SkillSlug != "example-skill" {
		t.Errorf("SkillSlug = %q, want %q", gate.lastReq.SkillSlug, "example-skill")
	}
	if gate.lastReq.Kind != ExecKindMarker {
		t.Errorf("Kind = %q, want %q", gate.lastReq.Kind, ExecKindMarker)
	}
}

func TestResolveInlineMarkers_NoMarkersNeverCallsGate(t *testing.T) {
	gate := &fakeGate{}
	body := "Plain text, no markers."
	got, err := ResolveInlineMarkers(context.Background(), gate, Definition{Slug: "s"}, "a", "", body, ExecOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != body {
		t.Errorf("body changed with no markers present: %q", got)
	}
	if gate.callCount() != 0 {
		t.Errorf("gate called %d times, want 0", gate.callCount())
	}
}

func TestResolveInlineMarkers_NilGateWithMarkerIsConfigError(t *testing.T) {
	_, err := ResolveInlineMarkers(context.Background(), nil, Definition{Slug: "s"}, "a", "", "!`echo x`", ExecOptions{})
	if err == nil {
		t.Fatal("expected an error when a marker is present but no gate is configured")
	}
}

func TestResolveInlineMarkers_FailureIsAttributedNotSilent(t *testing.T) {
	gate := &fakeGate{err: errors.New("boom")}
	_, err := ResolveInlineMarkers(context.Background(), gate, Definition{Slug: "my-skill"}, "a", "", "prefix !`false-command` suffix", ExecOptions{})
	if err == nil {
		t.Fatal("expected an error, got nil (a failing marker must never silently substitute empty output)")
	}
	var execErr *ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("expected *ExecutionError, got %T: %v", err, err)
	}
	if execErr.Skill != "my-skill" {
		t.Errorf("Skill = %q, want %q", execErr.Skill, "my-skill")
	}
	if execErr.Kind != ExecKindMarker {
		t.Errorf("Kind = %q, want %q", execErr.Kind, ExecKindMarker)
	}
	if execErr.Label != "false-command" {
		t.Errorf("Label = %q, want %q", execErr.Label, "false-command")
	}
	if execErr.Line != 1 {
		t.Errorf("Line = %d, want 1", execErr.Line)
	}
}

func TestResolveInlineMarkers_TimeoutIsEnforcedAndAttributed(t *testing.T) {
	gate := &fakeGate{sleep: 500 * time.Millisecond}
	opts := ExecOptions{Timeout: 20 * time.Millisecond}

	start := time.Now()
	_, err := ResolveInlineMarkers(context.Background(), gate, Definition{Slug: "slow-skill"}, "a", "", "!`sleep 5`", opts)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("ResolveInlineMarkers took %s, want it to return promptly at the configured timeout (~20ms), not wait for the slow gate", elapsed)
	}
	var execErr *ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("expected *ExecutionError, got %T: %v", err, err)
	}
	if !strings.Contains(execErr.Error(), "timed out") {
		t.Errorf("error message doesn't mention timeout: %v", execErr)
	}
	if execErr.Skill != "slow-skill" || execErr.Label != "sleep 5" {
		t.Errorf("unexpected attribution: skill=%q label=%q", execErr.Skill, execErr.Label)
	}
}

// ---------------------------------------------------------------------
// ExecuteScript
// ---------------------------------------------------------------------

func TestExecuteScript_RunsDeclaredScriptThroughGate(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(scriptsDir, "hello.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho script-output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := Definition{Slug: "scripty", Scripts: []string{"scripts/hello.sh"}}
	gate := &fakeGate{}

	out, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, "scripts/hello.sh",
		[]string{"/bin/sh", "scripts/hello.sh"}, ExecOptions{})
	if err != nil {
		t.Fatalf("ExecuteScript: %v", err)
	}
	if strings.TrimSpace(out) != "script-output" {
		t.Errorf("output = %q, want %q", out, "script-output")
	}
	if gate.callCount() != 1 {
		t.Errorf("gate called %d times, want 1", gate.callCount())
	}
	if gate.lastReq.Kind != ExecKindScript {
		t.Errorf("Kind = %q, want %q", gate.lastReq.Kind, ExecKindScript)
	}
	if gate.lastReq.WorkDir != dir {
		t.Errorf("WorkDir = %q, want %q", gate.lastReq.WorkDir, dir)
	}
	if gate.lastReq.SkillSlug != "scripty" {
		t.Errorf("SkillSlug = %q, want %q", gate.lastReq.SkillSlug, "scripty")
	}
}

func TestExecuteScript_UndeclaredScriptRejected(t *testing.T) {
	dir := t.TempDir()
	def := Definition{Slug: "scripty", Scripts: []string{"scripts/other.sh"}}
	gate := &fakeGate{}

	_, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, "scripts/hello.sh",
		[]string{"/bin/sh", "scripts/hello.sh"}, ExecOptions{})
	if err == nil {
		t.Fatal("expected an error for an undeclared script path")
	}
	if gate.callCount() != 0 {
		t.Errorf("gate should never be called for an undeclared script, got %d call(s)", gate.callCount())
	}
}

func TestExecuteScript_MissingFileRejected(t *testing.T) {
	dir := t.TempDir()
	def := Definition{Slug: "scripty", Scripts: []string{"scripts/hello.sh"}}
	gate := &fakeGate{}

	_, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, "scripts/hello.sh",
		[]string{"/bin/sh", "scripts/hello.sh"}, ExecOptions{})
	if err == nil {
		t.Fatal("expected an error when the declared script doesn't actually exist on disk")
	}
	if gate.callCount() != 0 {
		t.Errorf("gate should never be called for a missing script, got %d call(s)", gate.callCount())
	}
}

func TestExecuteScript_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	// Write a file outside dir that a traversal attempt would target.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.sh"), []byte("echo leaked"), 0o644); err != nil {
		t.Fatal(err)
	}

	traversal := "../" + filepath.Base(outside) + "/secret.sh"
	def := Definition{Slug: "scripty", Scripts: []string{traversal}}
	gate := &fakeGate{}

	_, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, traversal,
		[]string{"/bin/sh", traversal}, ExecOptions{})
	if err == nil {
		t.Fatal("expected a path-traversal rejection")
	}
	if gate.callCount() != 0 {
		t.Errorf("gate should never be called for a path-traversal attempt, got %d call(s)", gate.callCount())
	}
}

func TestExecuteScript_EmptyCommandRejected(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "hello.sh"), []byte("echo hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	def := Definition{Slug: "scripty", Scripts: []string{"scripts/hello.sh"}}
	gate := &fakeGate{}

	_, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, "scripts/hello.sh", nil, ExecOptions{})
	if err == nil {
		t.Fatal("expected an error for an empty command")
	}
	if gate.callCount() != 0 {
		t.Errorf("gate should never be called for an empty command, got %d call(s)", gate.callCount())
	}
}

func TestExecuteScript_FailureIsAttributedNotSilent(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "fails.sh"), []byte("exit 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	def := Definition{Slug: "scripty", Scripts: []string{"scripts/fails.sh"}}
	gate := &fakeGate{}

	_, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, "scripts/fails.sh",
		[]string{"/bin/sh", "scripts/fails.sh"}, ExecOptions{})
	if err == nil {
		t.Fatal("expected an error for a failing script")
	}
	var execErr *ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("expected *ExecutionError, got %T: %v", err, err)
	}
	if execErr.Kind != ExecKindScript || execErr.Label != "scripts/fails.sh" {
		t.Errorf("unexpected attribution: kind=%q label=%q", execErr.Kind, execErr.Label)
	}
}

func TestExecuteScript_TimeoutIsEnforcedAndAttributed(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "slow.sh"), []byte("sleep 5"), 0o644); err != nil {
		t.Fatal(err)
	}
	def := Definition{Slug: "scripty", Scripts: []string{"scripts/slow.sh"}}
	gate := &fakeGate{sleep: 500 * time.Millisecond}

	start := time.Now()
	_, err := ExecuteScript(context.Background(), gate, def, "agent-1", dir, "scripts/slow.sh",
		[]string{"/bin/sh", "scripts/slow.sh"}, ExecOptions{Timeout: 20 * time.Millisecond})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("ExecuteScript took %s, want it to return promptly at the configured timeout", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error message doesn't mention timeout: %v", err)
	}
}

func TestExecuteScript_DefaultTimeoutDiffersFromMarkerDefault(t *testing.T) {
	if DefaultScriptTimeout <= DefaultMarkerTimeout {
		t.Errorf("DefaultScriptTimeout (%s) should be configured longer than DefaultMarkerTimeout (%s) — scripts may legitimately need more time than a one-line marker command",
			DefaultScriptTimeout, DefaultMarkerTimeout)
	}
}
