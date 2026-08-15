package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// WorkflowRunStore is the narrow persistence surface the built-in engine
// needs. *store.Store satisfies it structurally; tests supply a fake.
type WorkflowRunStore interface {
	CreateWorkflowRun(row *store.WorkflowRunRow) error
	SetWorkflowRunStatus(id, status, errMsg string, completedAt time.Time) error
	GetWorkflowRun(id string) (*store.WorkflowRunRow, error)
	UpsertWorkflowRunStep(row *store.WorkflowRunStepRow) error
	ListWorkflowRunSteps(runID string) ([]*store.WorkflowRunStepRow, error)
}

// BuiltinWorkflowEngine is the DAG-executing agentworkflow.WorkflowEngine
// implementation (design doc, "Built-in engine"): topological leveling
// with goroutine fan-out per level — apps/hadron's internal/pipeline.Runner
// mechanism (TopoSort + per-level sync.WaitGroup fan-out), applied here at
// per-step granularity instead of Hadron's per-blueprint granularity — and
// every step transition persisted immediately for crash durability.
type BuiltinWorkflowEngine struct {
	store WorkflowRunStore
}

// NewBuiltinWorkflowEngine constructs the built-in WorkflowEngine.
func NewBuiltinWorkflowEngine(runStore WorkflowRunStore) *BuiltinWorkflowEngine {
	return &BuiltinWorkflowEngine{store: runStore}
}

var _ agentworkflow.WorkflowEngine = (*BuiltinWorkflowEngine)(nil)

// Name identifies this engine to Chat's dispatch (design doc: "New
// workflow_run MCP self-tool ... route a task to a named, defined
// workflow").
func (e *BuiltinWorkflowEngine) Name() string { return "builtin" }

// Run validates wf, persists a new run (and every step pre-registered as
// pending), and executes as far as the DAG currently allows — which may be
// the whole workflow, or may stop early with RunStatusWaiting if execution
// reaches an unresolved gate.
func (e *BuiltinWorkflowEngine) Run(ctx context.Context, wf agentworkflow.WorkflowDefinition, input agentworkflow.WorkflowInput, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if err := agentworkflow.Validate(wf); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if e.store == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow builtin engine: no store configured")
	}

	inputJSON, err := json.Marshal(input.Params)
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: marshal workflow input: %w", err)
	}
	runID := ulid.Make().String()
	if err := e.store.CreateWorkflowRun(&store.WorkflowRunRow{
		ID: runID, DefinitionName: wf.Name, Status: "running", InputJSON: string(inputJSON),
	}); err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: create run: %w", err)
	}
	for _, s := range wf.Steps {
		if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
			WorkflowRunID: runID, StepID: s.ID, Kind: string(s.Kind), Status: "pending",
		}); err != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: pre-register step %q: %w", s.ID, err)
		}
	}

	return e.execute(ctx, runID, wf, input, exec, map[string]agentworkflow.StepResult{}, map[string]bool{})
}

// Resume continues a previously persisted run from its last completed step
// (design doc: "a crash/restart can resume a run from its last completed
// step"). Not part of the WorkflowEngine interface — external engines own
// their own resumption semantics; this is specific to the built-in
// engine's own persisted state. The caller supplies the same
// WorkflowDefinition the run was originally started with: only run/step
// state is persisted here, not the definition itself.
//
// A step found in "running" status is treated as interrupted mid-flight
// (there's no transactional link between calling StepExecutor and
// persisting its result) and is re-run rather than resumed in place —
// at-least-once, not exactly-once, semantics for the step that was
// actually executing at crash time.
func (e *BuiltinWorkflowEngine) Resume(ctx context.Context, runID string, wf agentworkflow.WorkflowDefinition, exec agentworkflow.StepExecutor) (agentworkflow.WorkflowResult, error) {
	if err := agentworkflow.Validate(wf); err != nil {
		return agentworkflow.WorkflowResult{}, err
	}
	if e.store == nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow builtin engine: no store configured")
	}

	runRow, err := e.store.GetWorkflowRun(runID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: %w", runID, err)
	}
	var params map[string]any
	if runRow.InputJSON != "" {
		if err := json.Unmarshal([]byte(runRow.InputJSON), &params); err != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: unmarshal input: %w", runID, err)
		}
	}
	input := agentworkflow.WorkflowInput{Params: params}

	persisted, err := e.store.ListWorkflowRunSteps(runID)
	if err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: list steps: %w", runID, err)
	}

	results := make(map[string]agentworkflow.StepResult, len(persisted))
	waiting := make(map[string]bool)
	for _, row := range persisted {
		switch row.Status {
		case "pending", "running":
			continue
		case "waiting_on_gate":
			waiting[row.StepID] = true
			continue
		}
		sr, decodeErr := stepResultFromRow(row)
		if decodeErr != nil {
			return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: resume %s: decode step %q: %w", runID, row.StepID, decodeErr)
		}
		results[row.StepID] = sr
	}

	return e.execute(ctx, runID, wf, input, exec, results, waiting)
}

// execute runs wf's steps level by level, mutating results/waiting in
// place as steps resolve. results and waiting may arrive pre-populated
// (Resume) or empty (a fresh Run).
func (e *BuiltinWorkflowEngine) execute(
	ctx context.Context,
	runID string,
	wf agentworkflow.WorkflowDefinition,
	input agentworkflow.WorkflowInput,
	exec agentworkflow.StepExecutor,
	results map[string]agentworkflow.StepResult,
	waiting map[string]bool,
) (agentworkflow.WorkflowResult, error) {
	levels, err := agentworkflow.Levels(wf.Steps)
	if err != nil {
		return agentworkflow.WorkflowResult{}, err // already validated at Run/Resume entry; defensive only
	}
	byID := make(map[string]agentworkflow.StepDefinition, len(wf.Steps))
	for _, s := range wf.Steps {
		byID[s.ID] = s
	}

	type levelOutcome struct {
		stepID  string
		blocked bool
		skipped bool
		outcome stepRunOutcome
	}

	for _, level := range levels {
		outcomes := make([]levelOutcome, len(level))
		var wg sync.WaitGroup

		// Steps within a level share no dependency edges (that's what
		// makes them a level), so it's safe to read the shared results/
		// waiting maps here without a mutex — nothing writes to them
		// until every goroutine in this level has returned (wg.Wait()
		// below), and reads-only concurrent map access is safe in Go.
		for i, step := range level {
			if _, done := results[step.ID]; done {
				continue
			}
			if waiting[step.ID] {
				continue
			}

			switch dependencyState(step.DependsOn, results, waiting) {
			case depFailed:
				outcomes[i] = levelOutcome{stepID: step.ID, skipped: true}
				continue
			case depBlocked:
				outcomes[i] = levelOutcome{stepID: step.ID, blocked: true}
				continue
			}

			i, step := i, step
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes[i] = levelOutcome{stepID: step.ID, outcome: e.runStep(ctx, runID, step, results, input, exec)}
			}()
		}
		wg.Wait()

		for _, oc := range outcomes {
			if oc.stepID == "" {
				continue
			}
			switch {
			case oc.blocked:
				// Left exactly as it was (pending) — nothing to persist.
			case oc.skipped:
				sr := agentworkflow.StepResult{
					StepID: oc.stepID, Kind: byID[oc.stepID].Kind, IsError: true,
					Output: "skipped: an upstream dependency failed or was skipped",
				}
				results[oc.stepID] = sr
				if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
					WorkflowRunID: runID, StepID: oc.stepID, Kind: string(byID[oc.stepID].Kind),
					Status: "skipped", Output: sr.Output, IsError: true, CompletedAt: time.Now().UTC(),
				}); err != nil {
					return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: persist skip for %q: %w", oc.stepID, err)
				}
			case oc.outcome.Err != nil:
				return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: step %q: %w", oc.stepID, oc.outcome.Err)
			case oc.outcome.Kind == outcomeWaiting:
				waiting[oc.stepID] = true
			default:
				results[oc.stepID] = oc.outcome.Result
			}
		}
	}

	return e.finishRun(runID, byID, results, waiting)
}

func (e *BuiltinWorkflowEngine) finishRun(
	runID string,
	byID map[string]agentworkflow.StepDefinition,
	results map[string]agentworkflow.StepResult,
	waiting map[string]bool,
) (agentworkflow.WorkflowResult, error) {
	status := agentworkflow.RunStatusCompleted
	errMsg := ""
	for _, sr := range results {
		if sr.IsError {
			status = agentworkflow.RunStatusFailed
			if errMsg == "" {
				errMsg = fmt.Sprintf("step %q failed: %s", sr.StepID, sr.Output)
			}
		}
	}
	if status == agentworkflow.RunStatusCompleted && len(waiting) > 0 {
		status = agentworkflow.RunStatusWaiting
	}

	completedAt := time.Time{}
	dbStatus := string(status)
	if status == agentworkflow.RunStatusCompleted || status == agentworkflow.RunStatusFailed {
		completedAt = time.Now().UTC()
	}
	if err := e.store.SetWorkflowRunStatus(runID, dbStatus, errMsg, completedAt); err != nil {
		return agentworkflow.WorkflowResult{}, fmt.Errorf("agentworkflow: finalize run %s: %w", runID, err)
	}

	stepResults := make(map[string]agentworkflow.StepResult, len(results))
	for id, sr := range results {
		stepResults[id] = sr
	}
	return agentworkflow.WorkflowResult{RunID: runID, Status: status, StepResults: stepResults, Error: errMsg}, nil
}

// stepOutcomeKind distinguishes a step that reached a terminal result from
// one that's now paused waiting on a gate.
type stepOutcomeKind int

const (
	outcomeCompleted stepOutcomeKind = iota
	outcomeWaiting
)

// stepRunOutcome is runStep's return value. Err is set only for infra-level
// failures (e.g. the store rejecting a write) that should abort the whole
// run — distinct from Result.IsError, which is the step's own semantic
// failure and lets sibling/downstream steps proceed or skip normally.
type stepRunOutcome struct {
	Kind   stepOutcomeKind
	Result agentworkflow.StepResult
	Err    error
}

// runStep executes one step: llm/tool steps resolve their templated config
// and dispatch to the matching StepExecutor method (never for gate steps —
// gate is engine-native pausing, StepExecutor has no gate method); gate
// steps mark themselves waiting_on_gate and stop there. Every transition
// (running, then terminal) is persisted immediately.
func (e *BuiltinWorkflowEngine) runStep(
	ctx context.Context,
	runID string,
	step agentworkflow.StepDefinition,
	results map[string]agentworkflow.StepResult,
	input agentworkflow.WorkflowInput,
	exec agentworkflow.StepExecutor,
) stepRunOutcome {
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		return stepRunOutcome{Err: fmt.Errorf("mark running: %w", err)}
	}

	if step.Kind == agentworkflow.StepKindGate {
		if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
			WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "waiting_on_gate",
		}); err != nil {
			return stepRunOutcome{Err: fmt.Errorf("mark waiting_on_gate: %w", err)}
		}
		return stepRunOutcome{Kind: outcomeWaiting, Result: agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind}}
	}

	sr, verify := e.executeLLMOrTool(ctx, runID, step, results, input, exec)

	status := "completed"
	if sr.IsError {
		status = "failed"
	}
	toolCallsJSON, _ := json.Marshal(sr.ToolCalls)
	verifyJSON := ""
	if verify != nil {
		if b, err := json.Marshal(verify); err == nil {
			verifyJSON = string(b)
		}
	}
	errMsg := ""
	if sr.IsError {
		errMsg = sr.Output
	}
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: status,
		Output: sr.Output, IsError: sr.IsError, ToolCallsJSON: string(toolCallsJSON), VerifyJSON: verifyJSON,
		Error: errMsg, CompletedAt: time.Now().UTC(),
	}); err != nil {
		return stepRunOutcome{Err: fmt.Errorf("persist result: %w", err)}
	}

	return stepRunOutcome{Kind: outcomeCompleted, Result: sr}
}

// executeLLMOrTool resolves the step's templated config, dispatches to the
// matching StepExecutor method, and — if the step has a verify modifier —
// checks the literal result before returning. Verify runs even when the
// underlying execution itself errored: mode=engine's "no_error" check
// exists precisely to catch that (design doc: don't trust what a step says
// it did; that includes not skipping the check just because the step
// already looks like it failed).
func (e *BuiltinWorkflowEngine) executeLLMOrTool(
	ctx context.Context,
	runID string,
	step agentworkflow.StepDefinition,
	results map[string]agentworkflow.StepResult,
	input agentworkflow.WorkflowInput,
	exec agentworkflow.StepExecutor,
) (agentworkflow.StepResult, *agentworkflow.VerifyResult) {
	cfg, err := resolveStepConfig(step.Config, dependencyResults(step.DependsOn, results), input)
	if err != nil {
		return agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: fmt.Sprintf("config resolution: %v", err)}, nil
	}

	var sr agentworkflow.StepResult
	switch step.Kind {
	case agentworkflow.StepKindLLM:
		req, buildErr := buildLLMStepRequest(runID, step, cfg)
		if buildErr != nil {
			return agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: buildErr.Error()}, nil
		}
		res, execErr := exec.ExecuteLLMStep(ctx, req)
		if execErr != nil {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: execErr.Error()}
		} else {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, Output: res.Text, ToolCalls: res.ToolCalls}
		}

	case agentworkflow.StepKindTool:
		req, buildErr := buildToolStepRequest(runID, step, cfg)
		if buildErr != nil {
			return agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: buildErr.Error()}, nil
		}
		res, execErr := exec.ExecuteToolStep(ctx, req)
		if execErr != nil {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, IsError: true, Output: execErr.Error()}
		} else {
			sr = agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind, Output: res.Output, IsError: res.IsError}
		}
	}

	if step.Verify == nil {
		return sr, nil
	}

	vres, verr := exec.Verify(ctx, agentworkflow.VerifyRequest{
		WorkflowRunID: runID,
		StepID:        step.ID + ":verify",
		SubjectStepID: step.ID,
		VerifySpec:    *step.Verify,
		Subject: agentworkflow.VerifySubject{
			StepKind: step.Kind, Output: sr.Output, IsError: sr.IsError, ToolCalls: sr.ToolCalls,
		},
	})
	if verr != nil {
		sr.IsError = true
		sr.Output += "\n\nverify error: " + verr.Error()
		return sr, nil
	}
	sr.VerifyResult = &vres
	if !vres.Passed {
		sr.IsError = true
		sr.Output += "\n\nverify failed: " + vres.Reason
	}
	return sr, &vres
}

// dependencyResults returns the subset of results the caller is entitled
// to see for template resolution: exactly its declared DependsOn, not
// every step that happens to have completed so far in the run. Without
// this restriction a step could reference a step that completed earlier
// due to topology but was never listed as a dependency — undeclared
// coupling that contradicts resolveTemplateRef's own "must be listed in
// depends_on" error message.
func dependencyResults(dependsOn []string, results map[string]agentworkflow.StepResult) map[string]agentworkflow.StepResult {
	scoped := make(map[string]agentworkflow.StepResult, len(dependsOn))
	for _, id := range dependsOn {
		if r, ok := results[id]; ok {
			scoped[id] = r
		}
	}
	return scoped
}

// depState classifies a step's readiness against its DependsOn.
type depState int

const (
	depReady depState = iota
	depBlocked
	depFailed
)

// dependencyState reports whether step's dependencies are all resolved
// successfully (depReady), still waiting on a gate or not yet resolved
// (depBlocked — level ordering means "not yet resolved" shouldn't happen
// in practice, but it's the safe default rather than depFailed), or
// include a failure/skip (depFailed, which propagates as a skip).
func dependencyState(deps []string, results map[string]agentworkflow.StepResult, waiting map[string]bool) depState {
	for _, id := range deps {
		if waiting[id] {
			return depBlocked
		}
		r, ok := results[id]
		if !ok {
			return depBlocked
		}
		if r.IsError {
			return depFailed
		}
	}
	return depReady
}

// stepResultFromRow reconstructs a StepResult from persisted state — the
// typed inter-step data flow a later step's template resolution reads
// from directly, and what Resume rebuilds its in-memory results map from.
func stepResultFromRow(row *store.WorkflowRunStepRow) (agentworkflow.StepResult, error) {
	var toolCalls []agentworkflow.ToolCallRecord
	if row.ToolCallsJSON != "" && row.ToolCallsJSON != "[]" {
		if err := json.Unmarshal([]byte(row.ToolCallsJSON), &toolCalls); err != nil {
			return agentworkflow.StepResult{}, fmt.Errorf("unmarshal tool_calls_json: %w", err)
		}
	}
	var verifyResult *agentworkflow.VerifyResult
	if row.VerifyJSON != "" {
		var vr agentworkflow.VerifyResult
		if err := json.Unmarshal([]byte(row.VerifyJSON), &vr); err != nil {
			return agentworkflow.StepResult{}, fmt.Errorf("unmarshal verify_json: %w", err)
		}
		verifyResult = &vr
	}
	return agentworkflow.StepResult{
		StepID:       row.StepID,
		Kind:         agentworkflow.StepKind(row.Kind),
		Output:       row.Output,
		IsError:      row.IsError,
		ToolCalls:    toolCalls,
		VerifyResult: verifyResult,
	}, nil
}

// --- step config: templating + typed request builders ---

var templateRefPattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// resolveStepConfig returns a deep copy of cfg with every {{ ... }} string
// reference resolved against completed prior-step results and the run's
// input params — the typed inter-step data flow the design doc calls for
// (StepDefinition.Config's doc comment: "the built-in engine ... should
// design that shape against real workflow definitions").
//
// Unlike apps/hadron's resolveTemplate (which silently drops an
// unresolvable reference), an unresolvable reference here is a hard error:
// a step author who mistypes a step id, or references one outside
// depends_on, gets a clear failure rather than a step silently running
// against an empty string.
func resolveStepConfig(cfg map[string]any, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (map[string]any, error) {
	if cfg == nil {
		return nil, nil
	}
	resolved, err := resolveTemplateValue(cfg, results, input)
	if err != nil {
		return nil, err
	}
	m, _ := resolved.(map[string]any)
	return m, nil
}

func resolveTemplateValue(v any, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (any, error) {
	switch val := v.(type) {
	case string:
		return resolveTemplateString(val, results, input)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, sub := range val {
			rv, err := resolveTemplateValue(sub, results, input)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, sub := range val {
			rv, err := resolveTemplateValue(sub, results, input)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

func resolveTemplateString(s string, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (string, error) {
	var resolveErr error
	out := templateRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		if resolveErr != nil {
			return match
		}
		sub := templateRefPattern.FindStringSubmatch(match)
		val, err := resolveTemplateRef(sub[1], results, input)
		if err != nil {
			resolveErr = err
			return match
		}
		return val
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}

func resolveTemplateRef(ref string, results map[string]agentworkflow.StepResult, input agentworkflow.WorkflowInput) (string, error) {
	switch {
	case strings.HasPrefix(ref, "steps."):
		parts := strings.SplitN(strings.TrimPrefix(ref, "steps."), ".", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("agentworkflow: malformed step reference %q", ref)
		}
		stepID, field := parts[0], parts[1]
		sr, ok := results[stepID]
		if !ok {
			return "", fmt.Errorf("agentworkflow: reference to step %q, which has not completed (must be listed in depends_on)", stepID)
		}
		switch field {
		case "output":
			return sr.Output, nil
		case "is_error":
			return fmt.Sprintf("%t", sr.IsError), nil
		default:
			return "", fmt.Errorf("agentworkflow: unknown step field %q in reference %q", field, ref)
		}
	case strings.HasPrefix(ref, "input."):
		key := strings.TrimPrefix(ref, "input.")
		val, ok := input.Params[key]
		if !ok {
			// CW-20260815-0022: workflow_run's pre-launch check
			// (agentworkflow.RequiredInputs, internal/mcp/self_tools_workflow_run.go)
			// catches this for the common case, but that check is
			// best-effort and skipped when WorkflowRegistry isn't wired —
			// this is the backstop, so name what WAS supplied to make the
			// gap between "what params has" and "what this step needs"
			// concrete rather than a bare unknown-key message.
			return "", fmt.Errorf("agentworkflow: step references {{input.%s}}, but params has no %q key (params supplied: %v) — pass it via workflow_run(..., params: {%q: \"...\"})",
				key, key, paramKeys(input.Params), key)
		}
		if s, ok := val.(string); ok {
			return s, nil
		}
		return fmt.Sprintf("%v", val), nil
	default:
		return "", fmt.Errorf("agentworkflow: unrecognized template reference %q (expected steps.<id>.<field> or input.<key>)", ref)
	}
}

// paramKeys returns m's keys, sorted, for use in an error message — a
// deterministic "here's what you actually supplied" listing.
func paramKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func configString(cfg map[string]any, key string) string {
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func configStringSlice(cfg map[string]any, key string) []string {
	switch vv := cfg[key].(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func configInt(cfg map[string]any, key string) int {
	switch vv := cfg[key].(type) {
	case int:
		return vv
	case int64:
		return int(vv)
	case float64:
		return int(vv)
	default:
		return 0
	}
}

func configMap(cfg map[string]any, key string) map[string]any {
	if m, ok := cfg[key].(map[string]any); ok {
		return m
	}
	return nil
}

func buildLLMStepRequest(runID string, step agentworkflow.StepDefinition, cfg map[string]any) (agentworkflow.LLMStepRequest, error) {
	provider := configString(cfg, "provider")
	if provider == "" {
		return agentworkflow.LLMStepRequest{}, fmt.Errorf("agentworkflow: llm step %q config requires \"provider\"", step.ID)
	}
	prompt := configString(cfg, "prompt")
	if prompt == "" {
		return agentworkflow.LLMStepRequest{}, fmt.Errorf("agentworkflow: llm step %q config requires \"prompt\"", step.ID)
	}
	return agentworkflow.LLMStepRequest{
		WorkflowRunID:     runID,
		StepID:            step.ID,
		SessionID:         configString(cfg, "session_id"),
		AgentID:           configString(cfg, "agent_id"),
		Provider:          provider,
		Model:             configString(cfg, "model"),
		SystemPrompt:      configString(cfg, "system_prompt"),
		Messages:          []llmtypes.ChatMessage{{Role: "user", Content: prompt}},
		Tools:             configStringSlice(cfg, "tools"),
		MaxToolIterations: configInt(cfg, "max_tool_iterations"),
	}, nil
}

func buildToolStepRequest(runID string, step agentworkflow.StepDefinition, cfg map[string]any) (agentworkflow.ToolStepRequest, error) {
	tool := configString(cfg, "tool")
	if tool == "" {
		return agentworkflow.ToolStepRequest{}, fmt.Errorf("agentworkflow: tool step %q config requires \"tool\"", step.ID)
	}
	return agentworkflow.ToolStepRequest{
		WorkflowRunID: runID,
		StepID:        step.ID,
		AgentID:       configString(cfg, "agent_id"),
		Tool:          tool,
		Args:          configMap(cfg, "args"),
	}, nil
}
