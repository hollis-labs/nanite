package workflow

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"text/template"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/sandbox"
)

// FuncStep runs a Go function. For deterministic, programmatic steps.
type FuncStep struct {
	Fn func(ctx context.Context, input StepInput) (*StepOutput, error)
}

func (f *FuncStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	if f.Fn == nil {
		return nil, fmt.Errorf("func step: nil function")
	}
	return f.Fn(ctx, input)
}

// ShellStep runs a shell command in the sandbox.
type ShellStep struct {
	Command string
	Args    []string
	Dir     string // working directory (defaults to sandbox dir)
}

func (s *ShellStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	result, err := sandbox.AgentExec(sandbox.AgentExecOpts{
		SessionID: input.PipelineID,
		Command:   s.Command,
		Args:      s.Args,
		Env:       input.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("shell step: %w", err)
	}

	out := &StepOutput{
		Data: map[string]any{
			"stdout":    result.Stdout,
			"stderr":    result.Stderr,
			"exit_code": result.ExitCode,
			"timed_out": result.TimedOut,
		},
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}

	if result.ExitCode != 0 {
		return out, fmt.Errorf("shell step: non-zero exit code %d", result.ExitCode)
	}
	return out, nil
}

// SkillStep runs a Nanite skill (LLM-driven, non-deterministic).
// The step is deterministic in sequencing; the skill execution is not.
//
// TODO: Full integration with the chat service for actual skill invocation.
// For now, this validates the skill slug and renders the prompt template,
// returning the rendered prompt as output data. This lets the workflow
// track what WOULD run without requiring the chat service dependency.
type SkillStep struct {
	SkillSlug string
	Prompt    string // supports Go template interpolation from prior results
}

func (s *SkillStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	if s.SkillSlug == "" {
		return nil, fmt.Errorf("skill step: empty skill slug")
	}

	// Build template data from prior results.
	tplData := make(map[string]any)
	for id, result := range input.PriorResults {
		if result != nil {
			entry := map[string]any{
				"stdout":    result.Stdout,
				"stderr":    result.Stderr,
				"exit_code": result.ExitCode,
			}
			for k, v := range result.Data {
				entry[k] = v
			}
			tplData[id] = entry
		}
	}

	// Render the prompt template.
	rendered := s.Prompt
	if s.Prompt != "" {
		tmpl, err := template.New("prompt").Parse(s.Prompt)
		if err != nil {
			return nil, fmt.Errorf("skill step: parse prompt template: %w", err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, tplData); err != nil {
			return nil, fmt.Errorf("skill step: render prompt template: %w", err)
		}
		rendered = buf.String()
	}

	return &StepOutput{
		Data: map[string]any{
			"skill":           s.SkillSlug,
			"rendered_prompt": rendered,
			"status":          "stub", // TODO: replace with actual execution status
		},
	}, nil
}

// FailPolicy defines how ParallelStep handles sub-step failures.
type FailPolicy string

const (
	FailPolicyAll  FailPolicy = "all_must_pass" // any failure = step failure
	FailPolicyAny  FailPolicy = "any_pass"      // at least one must pass
	FailPolicyNone FailPolicy = "ignore"         // failures don't fail the parent
)

// ParallelStep runs N sub-steps concurrently and collects results.
type ParallelStep struct {
	Steps      []Step
	FailPolicy FailPolicy
}

func (p *ParallelStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	if len(p.Steps) == 0 {
		return &StepOutput{Data: map[string]any{}}, nil
	}

	type result struct {
		id  string
		out *StepOutput
		err error
	}

	results := make([]result, len(p.Steps))
	var wg sync.WaitGroup

	for i, step := range p.Steps {
		wg.Add(1)
		idx := i
		s := step
		safego.Go(ctx, "workflow.parallel.step", func() {
			defer wg.Done()
			subInput := StepInput{
				PipelineID:   input.PipelineID,
				StepID:       s.ID,
				Params:       input.Params,
				PriorResults: input.PriorResults,
				Env:          input.Env,
			}
			out, err := s.Handler.Execute(ctx, subInput)
			results[idx] = result{id: s.ID, out: out, err: err}
		})
	}
	wg.Wait()

	// Aggregate results.
	aggregated := map[string]any{}
	var passed, failed int
	var firstErr error

	for _, r := range results {
		if r.err != nil {
			failed++
			if firstErr == nil {
				firstErr = r.err
			}
			aggregated[r.id] = map[string]any{"error": r.err.Error()}
			if r.out != nil {
				aggregated[r.id] = map[string]any{"error": r.err.Error(), "output": r.out.Data}
			}
		} else {
			passed++
			if r.out != nil {
				aggregated[r.id] = r.out.Data
			}
		}
	}

	out := &StepOutput{
		Data: map[string]any{
			"results": aggregated,
			"passed":  passed,
			"failed":  failed,
		},
	}

	// Apply fail policy.
	switch p.FailPolicy {
	case FailPolicyAll:
		if failed > 0 {
			return out, fmt.Errorf("parallel step: %d of %d sub-steps failed: %w", failed, len(p.Steps), firstErr)
		}
	case FailPolicyAny:
		if passed == 0 {
			return out, fmt.Errorf("parallel step: all %d sub-steps failed: %w", failed, firstErr)
		}
	case FailPolicyNone:
		// Failures don't fail the parent.
	default:
		if failed > 0 {
			return out, fmt.Errorf("parallel step: %d of %d sub-steps failed: %w", failed, len(p.Steps), firstErr)
		}
	}

	return out, nil
}

// LoopStep repeats a step until a gate passes or max iterations reached.
type LoopStep struct {
	Body    Step
	Gate    GateFunc // check after each iteration; (true, _) = keep looping, (false, reason) = done
	MaxIter int      // safety ceiling (default 5)
}

func (l *LoopStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	maxIter := l.MaxIter
	if maxIter <= 0 {
		maxIter = 5
	}

	iterations := make([]map[string]any, 0, maxIter)
	priorResults := make(map[string]*StepOutput)
	// Copy existing prior results.
	for k, v := range input.PriorResults {
		priorResults[k] = v
	}

	for i := 0; i < maxIter; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("loop step: %w", ctx.Err())
		default:
		}

		subInput := StepInput{
			PipelineID:   input.PipelineID,
			StepID:       l.Body.ID,
			Params:       input.Params,
			PriorResults: priorResults,
			Env:          input.Env,
		}

		out, err := l.Body.Handler.Execute(ctx, subInput)
		iterData := map[string]any{"iteration": i + 1}
		if err != nil {
			iterData["error"] = err.Error()
		}
		if out != nil {
			iterData["output"] = out.Data
			// Store iteration result for gate evaluation.
			iterKey := fmt.Sprintf("%s_iter_%d", l.Body.ID, i)
			priorResults[iterKey] = out
		}
		iterations = append(iterations, iterData)

		if err != nil {
			return &StepOutput{
				Data: map[string]any{"iterations": iterations, "completed": false},
			}, fmt.Errorf("loop step: body failed on iteration %d: %w", i+1, err)
		}

		// Check gate: (false, reason) means "done, condition met".
		if l.Gate != nil {
			keepGoing, reason := l.Gate(ctx, priorResults)
			if !keepGoing {
				iterData["gate_reason"] = reason
				return &StepOutput{
					Data: map[string]any{
						"iterations":  iterations,
						"completed":   true,
						"gate_reason": reason,
					},
				}, nil
			}
		}
	}

	return &StepOutput{
		Data: map[string]any{"iterations": iterations, "completed": false},
	}, fmt.Errorf("loop step: max iterations (%d) reached without gate passing", maxIter)
}
