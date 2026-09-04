package workflowhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-workflow/diagnostic"
	"github.com/hollis-labs/go-workflow/graph"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"
	workflowwait "github.com/hollis-labs/go-workflow/wait"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

const (
	StepKindVersion          = "v1"
	StepKindLLM              = "nanite-llm"
	StepKindTurn             = "nanite-turn"
	StepKindTool             = "nanite-tool"
	StepKindWorker           = "nanite-worker"
	StepKindReviewer         = "nanite-reviewer"
	StepKindApproval         = "nanite-approval"
	StepKindExternalCallback = "nanite-external-callback"
	StepKindExternalEngine   = "nanite-external-engine"
	StepKindLoop             = "nanite-loop"
	StepKindTeam             = "nanite-team"
)

type frozenRegistry struct {
	kinds map[string]stepkind.StepKind
	specs []stepkind.StepKindSpec
	pilot bool
}

func newFrozenRegistry(exec agentworkflow.StepExecutor, bindings ...hostBindings) (*frozenRegistry, error) {
	if exec == nil {
		return nil, errors.New("go-workflow host requires a StepExecutor")
	}
	var bound hostBindings
	if len(bindings) != 0 {
		bound = bindings[0]
	}
	mutable := stepkind.NewRegistry()
	kinds := []stepkind.StepKind{
		&naniteExecutionKind{name: StepKindLLM, mode: executionLLM, exec: exec},
		&naniteExecutionKind{name: StepKindTurn, mode: executionLLM, exec: exec},
		&naniteExecutionKind{name: StepKindTool, mode: executionTool, exec: exec},
		&naniteExecutionKind{name: StepKindWorker, mode: executionLLM, exec: exec},
		&naniteExecutionKind{name: StepKindReviewer, mode: executionLLM, exec: exec},
		&naniteWaitKind{name: StepKindApproval, waitKind: workflowwait.KindGate, wakeSource: workflowwait.WakeGate},
		&naniteWaitKind{name: StepKindExternalCallback, waitKind: workflowwait.KindCallback, wakeSource: workflowwait.WakeCallback},
		&naniteExternalKind{host: bound.external, store: bound.store},
		&naniteLoopKind{host: bound.loop, store: bound.store},
		&naniteTeamKind{host: bound.team},
	}
	for _, kind := range kinds {
		if err := mutable.Register(kind); err != nil {
			return nil, fmt.Errorf("register %s: %w", kind.Spec().Name, err)
		}
	}
	result := &frozenRegistry{kinds: make(map[string]stepkind.StepKind), specs: mutable.List()}
	for _, spec := range result.specs {
		kind, ok := mutable.Lookup(spec.Name, spec.Version)
		if !ok {
			return nil, fmt.Errorf("freeze step kind %s@%s: missing implementation", spec.Name, spec.Version)
		}
		result.kinds[catalogKey(spec.Name, spec.Version)] = kind
	}
	return result, nil
}

func (r *frozenRegistry) Register(stepkind.StepKind) error {
	return errors.New("nanite go-workflow StepKind registry is frozen")
}

// newPilotRegistry reconstructs exactly the v0.5.0-beta.2 catalog. It exists
// only for recovery of rows carrying the frozen pilot engine identity.
func newPilotRegistry(exec agentworkflow.StepExecutor) (*frozenRegistry, error) {
	if exec == nil {
		return nil, errors.New("go-workflow host requires a StepExecutor")
	}
	mutable := stepkind.NewRegistry()
	kinds := []stepkind.StepKind{
		&naniteExecutionKind{name: StepKindLLM, mode: executionLLM, exec: exec},
		&naniteExecutionKind{name: StepKindTurn, mode: executionLLM, exec: exec},
		&naniteExecutionKind{name: StepKindTool, mode: executionTool, exec: exec},
		&naniteExecutionKind{name: StepKindWorker, mode: executionLLM, exec: exec},
		&naniteExecutionKind{name: StepKindReviewer, mode: executionLLM, exec: exec},
		&naniteWaitKind{name: StepKindApproval, waitKind: workflowwait.KindGate, wakeSource: workflowwait.WakeGate},
		&naniteWaitKind{name: StepKindExternalCallback, waitKind: workflowwait.KindCallback, wakeSource: workflowwait.WakeCallback},
		&naniteWaitKind{name: StepKindLoop, waitKind: workflowwait.KindChildRun, wakeSource: workflowwait.WakeChildRun},
		&naniteWaitKind{name: StepKindTeam, waitKind: workflowwait.KindSignal, wakeSource: workflowwait.WakeSignal},
	}
	for _, kind := range kinds {
		if err := mutable.Register(kind); err != nil {
			return nil, fmt.Errorf("register pilot %s: %w", kind.Spec().Name, err)
		}
	}
	result := &frozenRegistry{kinds: make(map[string]stepkind.StepKind), specs: mutable.List(), pilot: true}
	for _, spec := range result.specs {
		kind, ok := mutable.Lookup(spec.Name, spec.Version)
		if !ok {
			return nil, fmt.Errorf("freeze pilot step kind %s@%s", spec.Name, spec.Version)
		}
		result.kinds[catalogKey(spec.Name, spec.Version)] = kind
	}
	return result, nil
}

func (r *frozenRegistry) Lookup(name, version string) (stepkind.StepKind, bool) {
	kind, ok := r.kinds[catalogKey(name, version)]
	return kind, ok
}

func (r *frozenRegistry) List() []stepkind.StepKindSpec {
	encoded, _ := json.Marshal(r.specs)
	var specs []stepkind.StepKindSpec
	_ = json.Unmarshal(encoded, &specs)
	return specs
}

func (r *frozenRegistry) snapshot() ([]stepkind.StepKindSpec, string, error) {
	specs := r.List()
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Name == specs[j].Name {
			return specs[i].Version < specs[j].Version
		}
		return specs[i].Name < specs[j].Name
	})
	encoded, err := json.Marshal(specs)
	if err != nil {
		return nil, "", err
	}
	return specs, values.SHA256Digest(encoded), nil
}

func catalogKey(name, version string) string { return name + "\x00" + version }

type executionMode int

const (
	executionLLM executionMode = iota
	executionTool
)

type naniteExecutionKind struct {
	name string
	mode executionMode
	exec agentworkflow.StepExecutor
}

func (k *naniteExecutionKind) Spec() stepkind.StepKindSpec {
	// Every executable adapter may be externally consequential: direct tools
	// mutate by definition, and an LLM/Turn can invoke its bounded tool list.
	// Retry therefore requires both a stable runtime key and Nanite's explicit
	// config-aware retry authorization; the static catalog never claims an
	// intrinsic guarantee it cannot provide.
	spec := baseKindSpec(k.name, graph.EffectMutate, false)
	spec.Idempotency = graph.IdempotencyKeyed
	spec.RetrySafety = stepkind.RetryRequiresIdempotency
	return spec
}

func (*naniteExecutionKind) ValidateConfig(context.Context, graph.Config) []diagnostic.Diagnostic {
	return nil
}

func (k *naniteExecutionKind) Execute(ctx context.Context, prepared stepkind.PreparedInvocation) (stepkind.StepResult, error) {
	if prepared.Invocation.Continuation != nil {
		return stepkind.StepResult{}, &stepkind.ExecutionError{
			Code: "nanite_unexpected_continuation", Message: "execution step cannot receive a wait continuation",
			Classification: stepkind.RetryPermanent,
		}
	}
	cfg, err := resolvedNaniteConfig(prepared.Invocation)
	if err != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_config_resolution", err)
	}
	stepID := configStringValue(cfg, "product_step_id")
	if stepID == "" {
		stepID = prepared.Invocation.Identity.NodeID
	}
	kind := agentworkflow.StepKind(configStringValue(cfg, "product_kind"))
	if kind == "" {
		kind = agentworkflow.StepKindLLM
		if k.mode == executionTool {
			kind = agentworkflow.StepKindTool
		}
	}
	sessionID := configStringValue(cfg, "session_id")
	if sessionID == "" {
		sessionID = inputString(prepared.Invocation.Inputs, "workflow-session-id")
	}

	var result agentworkflow.StepResult
	switch k.mode {
	case executionLLM:
		provider := configStringValue(cfg, "provider")
		prompt := configStringValue(cfg, "prompt")
		if provider == "" || prompt == "" {
			return stepkind.StepResult{}, permanentExecutionError("nanite_invalid_llm_config", errors.New("provider and prompt are required"))
		}
		llmResult, executeErr := k.exec.ExecuteLLMStep(ctx, agentworkflow.LLMStepRequest{
			WorkflowRunID: prepared.Invocation.Identity.RunID,
			StepID:        stepID, SessionID: sessionID, AgentID: configStringValue(cfg, "agent_id"),
			Provider: provider, Model: configStringValue(cfg, "model"),
			SystemPrompt:      configStringValue(cfg, "system_prompt"),
			Messages:          []llmtypes.ChatMessage{{Role: "user", Content: prompt}},
			Tools:             configStringSliceValue(cfg, "tools"),
			MaxToolIterations: configIntValue(cfg, "max_tool_iterations"),
		})
		if executeErr != nil {
			return stepkind.StepResult{}, adapterExecutionError("nanite_llm_failed", executeErr)
		}
		result = agentworkflow.StepResult{StepID: stepID, Kind: kind, Output: llmResult.Text, ToolCalls: llmResult.ToolCalls}
	case executionTool:
		tool := configStringValue(cfg, "tool")
		if tool == "" {
			return stepkind.StepResult{}, permanentExecutionError("nanite_invalid_tool_config", errors.New("tool is required"))
		}
		toolResult, executeErr := k.exec.ExecuteToolStep(ctx, agentworkflow.ToolStepRequest{
			WorkflowRunID: prepared.Invocation.Identity.RunID,
			StepID:        stepID, SessionID: sessionID, AgentID: configStringValue(cfg, "agent_id"),
			Tool: tool, Args: configMapValue(cfg, "args"),
		})
		if executeErr != nil {
			return stepkind.StepResult{}, adapterExecutionError("nanite_tool_failed", executeErr)
		}
		result = agentworkflow.StepResult{StepID: stepID, Kind: kind, Output: toolResult.Output, IsError: toolResult.IsError}
	}
	if verifyMap := configMapValue(cfg, "verify"); verifyMap != nil {
		var verify agentworkflow.VerifySpec
		encoded, marshalErr := json.Marshal(verifyMap)
		if marshalErr != nil || json.Unmarshal(encoded, &verify) != nil {
			return stepkind.StepResult{}, permanentExecutionError("nanite_invalid_verify", errors.New("verify config is invalid"))
		}
		verified, verifyErr := k.exec.Verify(ctx, agentworkflow.VerifyRequest{
			WorkflowRunID: prepared.Invocation.Identity.RunID, StepID: stepID + ":verify",
			SubjectStepID: stepID, VerifySpec: verify,
			Subject: agentworkflow.VerifySubject{StepKind: result.Kind, Output: result.Output, IsError: result.IsError, ToolCalls: result.ToolCalls},
		})
		if verifyErr != nil {
			return stepkind.StepResult{}, permanentExecutionError("nanite_verify_failed", verifyErr)
		}
		result.VerifyResult = &verified
		if !verified.Passed {
			return stepkind.StepResult{}, permanentExecutionError("nanite_verify_rejected", errors.New(verified.Reason))
		}
	}
	if result.IsError {
		return stepkind.StepResult{}, permanentExecutionError("nanite_step_failed", errors.New(result.Output))
	}
	value, err := resultValue(result, prepared.Invocation.Identity.NodeID)
	if err != nil {
		return stepkind.StepResult{}, permanentExecutionError("nanite_output_invalid", err)
	}
	return stepkind.StepResult{Outcome: stepkind.StepCompleted, Outputs: values.ValueSet{"result": value}}, nil
}

type naniteWaitKind struct {
	name       string
	waitKind   workflowwait.Kind
	wakeSource workflowwait.WakeSource
}

func (k *naniteWaitKind) Spec() stepkind.StepKindSpec {
	return baseKindSpec(k.name, graph.EffectRead, true)
}

func (*naniteWaitKind) ValidateConfig(context.Context, graph.Config) []diagnostic.Diagnostic {
	return nil
}

func (k *naniteWaitKind) Execute(_ context.Context, prepared stepkind.PreparedInvocation) (stepkind.StepResult, error) {
	if continuation := prepared.Invocation.Continuation; continuation != nil {
		resumed := any(nil)
		if value, ok := continuation.Values["resume"]; ok {
			resumed = value.Inline
		}
		result := agentworkflow.StepResult{
			StepID: configStringValue(prepared.Invocation.Config, "product_step_id"),
			Kind:   agentworkflow.StepKind(configStringValue(prepared.Invocation.Config, "product_kind")),
			Output: inlineOutput(resumed),
		}
		if result.StepID == "" {
			result.StepID = prepared.Invocation.Identity.NodeID
		}
		value, err := resultValue(result, prepared.Invocation.Identity.NodeID)
		if err != nil {
			return stepkind.StepResult{}, permanentExecutionError("nanite_wait_output_invalid", err)
		}
		return stepkind.StepResult{Outcome: stepkind.StepCompleted, Outputs: values.ValueSet{"result": value}}, nil
	}
	schema, err := workflowwait.NewSchemaRef(graph.Schema{})
	if err != nil {
		return stepkind.StepResult{}, err
	}
	correlation := configStringValue(prepared.Invocation.Config, "correlation")
	if correlation == "" {
		correlation = prepared.Invocation.Identity.RunID + ":" + prepared.Invocation.Identity.NodeID
	}
	authorityRef := configStringValue(prepared.Invocation.Config, "authority_ref")
	if authorityRef == "" {
		authorityRef = configStringValue(prepared.Invocation.Config, "agent_id")
	}
	if authorityRef == "" {
		if workflowInput, ok := prepared.Invocation.Inputs["workflow-input"]; ok {
			if input, ok := workflowInput.Inline.(map[string]any); ok {
				authorityRef, _ = input["_nanite_a2a_task_id"].(string)
			}
		}
	}
	if authorityRef == "" {
		authorityRef = prepared.Invocation.Identity.RunID
	}
	return stepkind.StepResult{Outcome: stepkind.StepWaiting, Wait: &stepkind.WaitResult{
		ID: fmt.Sprintf("wait-%s-%s-%d", prepared.Invocation.Identity.RunID, prepared.Invocation.Identity.NodeID, prepared.Invocation.Identity.Attempt),
		Record: workflowwait.Record{
			Kind: k.waitKind, Correlation: correlation, ResumeSchema: schema,
			Visibility: workflowwait.VisibilityPrivate,
			Authority:  workflowwait.ResponderAuthority{Kind: "nanite", Reference: authorityRef},
			WakeSource: k.wakeSource, Status: workflowwait.StatusOpen,
		},
	}}, nil
}

func baseKindSpec(name string, effect graph.Effect, suspend bool) stepkind.StepKindSpec {
	return stepkind.StepKindSpec{
		Name: name, Version: StepKindVersion,
		ConfigSchema: graph.Schema{"type": "object"},
		InputSchema:  graph.Schema{"type": "object"},
		OutputSchema: graph.Schema{
			"type": "object", "required": []any{"result"},
			"properties": map[string]any{"result": map[string]any{"type": "object"}},
		},
		Effects: graph.EffectSet{effect}, Idempotency: graph.IdempotencyNone,
		RetrySafety:  stepkind.RetryUnsupported,
		Cancellation: stepkind.CancellationSpec{Mode: stepkind.CancellationContext},
		Observation:  stepkind.ObservationSpec{Mode: stepkind.ObservationNone},
		CanSuspend:   suspend, EmbeddedModeSupported: true,
	}
}

func resultValue(result agentworkflow.StepResult, nodeID string) (values.Value, error) {
	toolCalls := make([]any, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		toolCalls = append(toolCalls, map[string]any{
			"tool": call.Tool, "input": call.Input, "output": call.Output, "is_error": call.IsError,
		})
	}
	payload := map[string]any{
		"step_id": result.StepID, "kind": string(result.Kind), "output": result.Output,
		"is_error": result.IsError, "tool_calls": toolCalls,
	}
	if result.VerifyResult != nil {
		encoded, _ := json.Marshal(result.VerifyResult)
		var verify any
		_ = decodeJSON(encoded, &verify)
		payload["verify"] = verify
	}
	return values.NewInline(payload, values.Metadata{
		Producer:  values.Producer{Kind: "nanite-step", Reference: nodeID, Output: "result"},
		MediaType: "application/json", Redaction: values.RedactionPrivate, Retention: values.RetentionRun,
	})
}

func permanentExecutionError(code string, err error) error {
	return classifiedExecutionError(code, err, stepkind.RetryPermanent)
}

func adapterExecutionError(code string, err error) error {
	classification := stepkind.ClassifyError(err)
	if classification == stepkind.RetryUnspecified {
		classification = stepkind.RetryPermanent
	}
	return classifiedExecutionError(code, err, classification)
}

func classifiedExecutionError(code string, err error, classification stepkind.RetryClassification) error {
	message := err.Error()
	if strings.TrimSpace(message) == "" {
		message = code
	}
	return &stepkind.ExecutionError{Code: code, Message: message, Classification: classification, Cause: err}
}

var naniteTemplatePattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

func resolvedNaniteConfig(invocation stepkind.Invocation) (map[string]any, error) {
	encoded, err := json.Marshal(invocation.Config)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if decodeErr := decodeJSON(encoded, &cfg); decodeErr != nil {
		return nil, decodeErr
	}
	workflowInput := map[string]any{}
	if value, ok := invocation.Inputs["workflow-input"]; ok {
		workflowInput, _ = value.Inline.(map[string]any)
	}
	dependencies := make(map[string]map[string]any)
	if slots, ok := cfg["dependency_slots"].(map[string]any); ok {
		for stepID, rawSlot := range slots {
			slot, _ := rawSlot.(string)
			if value, exists := invocation.Inputs[slot]; exists {
				dependencies[stepID], _ = value.Inline.(map[string]any)
			}
		}
	}
	resolved, err := resolveNaniteValue(cfg, workflowInput, dependencies)
	if err != nil {
		return nil, err
	}
	return resolved.(map[string]any), nil
}

func resolveNaniteValue(value any, input map[string]any, dependencies map[string]map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		var resolutionErr error
		result := naniteTemplatePattern.ReplaceAllStringFunc(typed, func(match string) string {
			if resolutionErr != nil {
				return match
			}
			parts := naniteTemplatePattern.FindStringSubmatch(match)
			resolved, err := resolveNaniteReference(parts[1], input, dependencies)
			if err != nil {
				resolutionErr = err
				return match
			}
			return resolved
		})
		return result, resolutionErr
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := resolveNaniteValue(item, input, dependencies)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			resolved, err := resolveNaniteValue(item, input, dependencies)
			if err != nil {
				return nil, err
			}
			result[index] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}

func resolveNaniteReference(ref string, input map[string]any, dependencies map[string]map[string]any) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "input.") {
		key := strings.TrimPrefix(ref, "input.")
		value, ok := input[key]
		if !ok {
			return "", fmt.Errorf("unknown workflow input %q", key)
		}
		return inlineOutput(value), nil
	}
	if strings.HasPrefix(ref, "steps.") {
		parts := strings.SplitN(strings.TrimPrefix(ref, "steps."), ".", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("malformed step reference %q", ref)
		}
		result, ok := dependencies[parts[0]]
		if !ok {
			return "", fmt.Errorf("step %q is not a declared dependency", parts[0])
		}
		value, ok := result[parts[1]]
		if !ok {
			return "", fmt.Errorf("unknown step result field %q", parts[1])
		}
		return inlineOutput(value), nil
	}
	return "", fmt.Errorf("unrecognized template reference %q", ref)
}

func inlineOutput(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func inputString(set values.ValueSet, key string) string {
	if value, ok := set[key]; ok {
		if result, ok := value.Inline.(string); ok {
			return result
		}
	}
	return ""
}

func configStringValue(cfg map[string]any, key string) string {
	value, _ := cfg[key].(string)
	return value
}

func configStringSliceValue(cfg map[string]any, key string) []string {
	items, _ := cfg[key].([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

func configMapValue(cfg map[string]any, key string) map[string]any {
	value, _ := cfg[key].(map[string]any)
	return value
}

func configIntValue(cfg map[string]any, key string) int {
	switch value := cfg[key].(type) {
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func decodeJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	return decoder.Decode(target)
}
