package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// skipIfNoOSSandbox skips a test on Linux when bwrap is unavailable and the
// AD-01 degraded-mode opt-in (TASKS/audit-remediation/
// ARCHITECT-DECISIONS.md) is not set. sandbox.AgentExec (which ShellStep
// calls) now fails closed by default in that case — previously it silently
// fell back to Tier 1 only (GO-SEC4-001, the finding this fix closes).
// darwin (seatbelt always present) and a Linux host WITH bwrap installed
// are unaffected.
func skipIfNoOSSandbox(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC"))) {
	case "1", "true", "yes":
		return
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed and NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC not set — sandbox.AgentExec now fails closed (AD-01); see internal/sandbox/os_linux_test.go for the dedicated fail-closed/degraded regression tests")
	}
}

func TestPipeline_LinearSequence(t *testing.T) {
	var order []string
	var mu sync.Mutex
	record := func(id string) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
	}

	p := &Pipeline{
		ID:   "linear",
		Name: "Linear Pipeline",
		Steps: []Step{
			{
				ID: "a", Name: "Step A", Required: true,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					record("a")
					return &StepOutput{Data: map[string]any{"val": 1}}, nil
				}},
			},
			{
				ID: "b", Name: "Step B", Required: true, DependsOn: []string{"a"},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					record("b")
					// Verify prior results are passed.
					if input.PriorResults["a"] == nil {
						return nil, fmt.Errorf("missing prior result from a")
					}
					return &StepOutput{Data: map[string]any{"val": 2}}, nil
				}},
			},
			{
				ID: "c", Name: "Step C", Required: true, DependsOn: []string{"b"},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					record("c")
					return &StepOutput{Data: map[string]any{"val": 3}}, nil
				}},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s", state.Status)
	}
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("unexpected order: %v", order)
	}
	for _, id := range []string{"a", "b", "c"} {
		if state.StepStates[id].Status != StepCompleted {
			t.Errorf("step %s: expected completed, got %s", id, state.StepStates[id].Status)
		}
	}
}

func TestPipeline_DependencyResolution(t *testing.T) {
	// Diamond dependency: a -> b, a -> c, b+c -> d
	var order []string
	var mu sync.Mutex
	record := func(id string) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
	}

	mkStep := func(id string, deps []string) Step {
		return Step{
			ID: id, Name: id, Required: true, DependsOn: deps,
			Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
				record(id)
				return &StepOutput{Data: map[string]any{"id": id}}, nil
			}},
		}
	}

	p := &Pipeline{
		ID: "diamond",
		Steps: []Step{
			mkStep("a", nil),
			mkStep("b", []string{"a"}),
			mkStep("c", []string{"a"}),
			mkStep("d", []string{"b", "c"}),
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s", state.Status)
	}

	// a must come before b and c, d must come last.
	aIdx := indexOf(order, "a")
	bIdx := indexOf(order, "b")
	cIdx := indexOf(order, "c")
	dIdx := indexOf(order, "d")
	if aIdx >= bIdx || aIdx >= cIdx {
		t.Errorf("a must come before b and c, got: %v", order)
	}
	if dIdx <= bIdx || dIdx <= cIdx {
		t.Errorf("d must come after b and c, got: %v", order)
	}
}

func TestPipeline_CyclicDependency(t *testing.T) {
	p := &Pipeline{
		ID: "cyclic",
		Steps: []Step{
			{ID: "a", Required: true, DependsOn: []string{"c"}, Handler: &FuncStep{Fn: noop}},
			{ID: "b", Required: true, DependsOn: []string{"a"}, Handler: &FuncStep{Fn: noop}},
			{ID: "c", Required: true, DependsOn: []string{"b"}, Handler: &FuncStep{Fn: noop}},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err == nil {
		t.Fatal("expected error for cyclic dependency")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
	if state.Status != RunFailed {
		t.Fatalf("expected failed status, got %s", state.Status)
	}
}

func TestPipeline_OptionalStepFailure(t *testing.T) {
	p := &Pipeline{
		ID: "optional-fail",
		Steps: []Step{
			{
				ID: "a", Required: true,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{Data: map[string]any{"ok": true}}, nil
				}},
			},
			{
				ID: "b", Required: false, DependsOn: []string{"a"},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return nil, fmt.Errorf("optional failure")
				}},
			},
			{
				ID: "c", Required: true, DependsOn: []string{"a"},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{Data: map[string]any{"ok": true}}, nil
				}},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s (error: %s)", state.Status, state.Error)
	}
	if state.StepStates["b"].Status != StepFailed {
		t.Errorf("step b: expected failed, got %s", state.StepStates["b"].Status)
	}
	if state.StepStates["c"].Status != StepCompleted {
		t.Errorf("step c: expected completed, got %s", state.StepStates["c"].Status)
	}
}

func TestPipeline_RequiredStepFailure(t *testing.T) {
	p := &Pipeline{
		ID: "required-fail",
		Steps: []Step{
			{
				ID: "a", Required: true,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return nil, fmt.Errorf("required failure")
				}},
			},
			{
				ID: "b", Required: true, DependsOn: []string{"a"},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{}, nil
				}},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunFailed {
		t.Fatalf("expected failed, got %s", state.Status)
	}
	if state.StepStates["a"].Status != StepFailed {
		t.Errorf("step a: expected failed, got %s", state.StepStates["a"].Status)
	}
	if state.StepStates["b"].Status != StepSkipped {
		t.Errorf("step b: expected skipped, got %s", state.StepStates["b"].Status)
	}
}

func TestPipeline_GateSkip(t *testing.T) {
	p := &Pipeline{
		ID: "gate-skip",
		Steps: []Step{
			{
				ID: "a", Required: true,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{Data: map[string]any{"skip_next": true}}, nil
				}},
			},
			{
				ID: "b", Required: true, DependsOn: []string{"a"},
				Gate: func(ctx context.Context, pr map[string]*StepOutput) (bool, string) {
					if pr["a"] != nil {
						if skip, ok := pr["a"].Data["skip_next"].(bool); ok && skip {
							return false, "prior step requested skip"
						}
					}
					return true, ""
				},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{}, nil
				}},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.StepStates["b"].Status != StepSkipped {
		t.Errorf("step b: expected skipped, got %s", state.StepStates["b"].Status)
	}
	if state.StepStates["b"].SkipReason != "prior step requested skip" {
		t.Errorf("unexpected skip reason: %s", state.StepStates["b"].SkipReason)
	}
}

func TestPipeline_Timeout(t *testing.T) {
	p := &Pipeline{
		ID: "timeout",
		Steps: []Step{
			{
				ID: "slow", Required: true, Timeout: 50 * time.Millisecond,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(5 * time.Second):
						return &StepOutput{}, nil
					}
				}},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunFailed {
		t.Fatalf("expected failed, got %s", state.Status)
	}
	if state.StepStates["slow"].Status != StepFailed {
		t.Errorf("step slow: expected failed, got %s", state.StepStates["slow"].Status)
	}
	if !strings.Contains(state.StepStates["slow"].Error, "deadline exceeded") {
		t.Errorf("expected deadline exceeded error, got: %s", state.StepStates["slow"].Error)
	}
}

func TestPipeline_Retry(t *testing.T) {
	var attempts int32

	p := &Pipeline{
		ID: "retry",
		Steps: []Step{
			{
				ID: "flaky", Required: true,
				Retry: &RetryPolicy{MaxAttempts: 2, Delay: 1 * time.Millisecond, Backoff: 1.0},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					n := atomic.AddInt32(&attempts, 1)
					if n < 3 {
						return nil, fmt.Errorf("attempt %d failed", n)
					}
					return &StepOutput{Data: map[string]any{"ok": true}}, nil
				}},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s (error: %s)", state.Status, state.Error)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestPipeline_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	p := &Pipeline{
		ID: "cancel",
		Steps: []Step{
			{
				ID: "blocker", Required: true,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					close(started)
					<-ctx.Done()
					return nil, ctx.Err()
				}},
			},
			{
				ID: "after", Required: true, DependsOn: []string{"blocker"},
				Handler: &FuncStep{Fn: noop},
			},
		},
	}

	exec := NewExecutor()
	done := make(chan struct{})
	var state *RunState
	var runErr error

	go func() {
		state, runErr = exec.Run(ctx, p, nil)
		close(done)
	}()

	<-started
	cancel()
	<-done

	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if state.Status != RunFailed {
		// The blocker step fails with context.Canceled, which is a required step failure.
		// OR it could be RunCanceled depending on timing.
		if state.Status != RunCanceled {
			t.Fatalf("expected failed or canceled, got %s", state.Status)
		}
	}
}

func TestPipeline_ParallelStep(t *testing.T) {
	p := &Pipeline{
		ID: "parallel",
		Steps: []Step{
			{
				ID: "par", Required: true,
				Handler: &ParallelStep{
					FailPolicy: FailPolicyAll,
					Steps: []Step{
						{
							ID: "sub-a",
							Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
								return &StepOutput{Data: map[string]any{"from": "a"}}, nil
							}},
						},
						{
							ID: "sub-b",
							Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
								return &StepOutput{Data: map[string]any{"from": "b"}}, nil
							}},
						},
					},
				},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s", state.Status)
	}
	out := state.StepStates["par"].Output
	if out == nil {
		t.Fatal("expected output from parallel step")
	}
	passed, _ := out.Data["passed"].(int)
	if passed != 2 {
		t.Errorf("expected 2 passed, got %d", passed)
	}
}

func TestPipeline_LoopStep(t *testing.T) {
	var count int32

	p := &Pipeline{
		ID: "loop",
		Steps: []Step{
			{
				ID: "looper", Required: true,
				Handler: &LoopStep{
					MaxIter: 10,
					Body: Step{
						ID: "increment",
						Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
							n := atomic.AddInt32(&count, 1)
							return &StepOutput{Data: map[string]any{"count": n}}, nil
						}},
					},
					Gate: func(ctx context.Context, pr map[string]*StepOutput) (bool, string) {
						// Check if any iteration result has count >= 3.
						for _, out := range pr {
							if out != nil {
								if c, ok := out.Data["count"].(int32); ok && c >= 3 {
									return false, "count reached 3"
								}
							}
						}
						return true, ""
					},
				},
			},
		},
	}

	exec := NewExecutor()
	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s (error: %s)", state.Status, state.Error)
	}
	if atomic.LoadInt32(&count) != 3 {
		t.Errorf("expected 3 iterations, got %d", count)
	}
}

func TestPipeline_Events(t *testing.T) {
	var events []string
	var mu sync.Mutex

	exec := NewExecutor(WithEventHandler(func(e Event) {
		mu.Lock()
		events = append(events, e.Type)
		mu.Unlock()
	}))

	p := &Pipeline{
		ID: "events",
		Steps: []Step{
			{
				ID: "a", Required: true,
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{}, nil
				}},
			},
			{
				ID: "b", Required: true, DependsOn: []string{"a"},
				Handler: &FuncStep{Fn: func(ctx context.Context, input StepInput) (*StepOutput, error) {
					return &StepOutput{}, nil
				}},
			},
		},
	}

	state, err := exec.Run(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != RunCompleted {
		t.Fatalf("expected completed, got %s", state.Status)
	}

	// Expected: pipeline.started, step.started(a), step.completed(a), step.started(b), step.completed(b), pipeline.completed
	expected := []string{
		"pipeline.started",
		"step.started", "step.completed",
		"step.started", "step.completed",
		"pipeline.completed",
	}
	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(events), events)
	}
	for i, e := range expected {
		if events[i] != e {
			t.Errorf("event[%d]: expected %q, got %q", i, e, events[i])
		}
	}
}

func TestLoadYAML_ValidWorkflow(t *testing.T) {
	yaml := `
name: test-workflow
description: A test workflow
default_timeout: 45s
env:
  FOO: bar
steps:
  - id: lint
    name: Run linter
    type: shell
    required: true
    command: echo
    args: [hello]
    timeout: 60s
    retry:
      max_attempts: 2
      delay: 5s
      backoff: 1.5

  - id: test
    name: Run tests
    type: shell
    required: true
    depends_on: [lint]
    command: echo
    args: [tests]
    timeout: 120s

  - id: review
    name: Code review
    type: skill
    required: false
    depends_on: [test]
    skill: code-review
    prompt: "Review: {{.test.stdout}}"
`
	p, err := Load([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Name != "test-workflow" {
		t.Errorf("expected name test-workflow, got %s", p.Name)
	}
	if p.DefaultTimeout != 45*time.Second {
		t.Errorf("expected 45s default timeout, got %s", p.DefaultTimeout)
	}
	if p.Env["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %s", p.Env["FOO"])
	}
	if len(p.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(p.Steps))
	}

	// Check lint step.
	lint := p.Steps[0]
	if lint.ID != "lint" {
		t.Errorf("step 0: expected id lint, got %s", lint.ID)
	}
	if lint.Timeout != 60*time.Second {
		t.Errorf("lint timeout: expected 60s, got %s", lint.Timeout)
	}
	if lint.Retry == nil || lint.Retry.MaxAttempts != 2 {
		t.Error("lint retry: expected max_attempts=2")
	}
	if _, ok := lint.Handler.(*ShellStep); !ok {
		t.Error("lint handler: expected ShellStep")
	}

	// Check test step depends on lint.
	test := p.Steps[1]
	if len(test.DependsOn) != 1 || test.DependsOn[0] != "lint" {
		t.Errorf("test depends_on: expected [lint], got %v", test.DependsOn)
	}

	// Check review step is a skill.
	review := p.Steps[2]
	if !review.Required {
		t.Log("review.Required correctly false") // required defaults to true but yaml has false
	}
	if review.Required {
		t.Error("review should not be required")
	}
	skill, ok := review.Handler.(*SkillStep)
	if !ok {
		t.Fatal("review handler: expected SkillStep")
	}
	if skill.SkillSlug != "code-review" {
		t.Errorf("review skill: expected code-review, got %s", skill.SkillSlug)
	}
}

func TestLoadYAML_InvalidDependency(t *testing.T) {
	yaml := `
name: bad-deps
steps:
  - id: a
    type: shell
    command: echo
    depends_on: [nonexistent]
`
	_, err := Load([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid dependency")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected error mentioning nonexistent, got: %v", err)
	}
}

func TestShellStep_BasicCommand(t *testing.T) {
	skipIfNoOSSandbox(t)
	step := &ShellStep{
		Command: "echo",
		Args:    []string{"hello", "world"},
	}

	input := StepInput{
		PipelineID: "test-pipeline",
		StepID:     "echo-step",
	}

	out, err := step.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Stdout, "hello world") {
		t.Errorf("expected stdout to contain 'hello world', got: %q", out.Stdout)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", out.ExitCode)
	}
}

// --- helpers ---

func noop(ctx context.Context, input StepInput) (*StepOutput, error) {
	return &StepOutput{Data: map[string]any{}}, nil
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
