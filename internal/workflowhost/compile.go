package workflowhost

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/diagnostic"
	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

type compiledWorkflow struct {
	material    PlanMaterial
	projections []PlanNodeProjection
}

const hostInvocationKeyInput = "workflow-invocation-key"

// CompileSource runs go-workflow's public graph-native source pipeline in the
// required order and returns immutable material suitable for persistence.
func CompileSource(ctx context.Context, locator string, source []byte, registry stepkind.Registry) (PlanMaterial, error) {
	if strings.TrimSpace(locator) == "" {
		locator = "nanite.workflow.yaml"
	}
	loaded := compile.LoadBytes(locator, source)
	if loaded.Source == nil || hasDiagnosticErrors(loaded.Diagnostics) {
		return PlanMaterial{}, diagnosticsError("load go-workflow source", loaded.Diagnostics)
	}
	compiled := compile.Compile(loaded.Source)
	if compiled.Plan == nil || hasDiagnosticErrors(compiled.Diagnostics) {
		return PlanMaterial{}, diagnosticsError("compile go-workflow source", compiled.Diagnostics)
	}
	material, err := finalizeCompiledPlan(ctx, *compiled.Plan, compile.ValueVisibilityPlan{}, locator, graph.SourceWorkflow, loaded.Source.Bytes(), registry)
	if err != nil {
		return PlanMaterial{}, err
	}
	// Graph.ID is intentionally normalized for execution. Preserve the exact
	// authored workflow name separately for Nanite's product/API identity.
	if name, ok := loaded.Source.Node("workflow", "name"); ok && strings.TrimSpace(name.Value) != "" {
		material.ProductDefinitionName = name.Value
	} else if id, ok := loaded.Source.Node("workflow", "id"); ok && strings.TrimSpace(id.Value) != "" {
		material.ProductDefinitionName = id.Value
	}
	if err := validatePlanMaterial(material); err != nil {
		return PlanMaterial{}, fmt.Errorf("freeze exact go-workflow product identity: %w", err)
	}
	return material, nil
}

func compileWorkflowDefinition(ctx context.Context, definition agentworkflow.WorkflowDefinition, registry stepkind.Registry) (compiledWorkflow, error) {
	if err := agentworkflow.Validate(definition); err != nil {
		return compiledWorkflow{}, err
	}
	if agentworkflow.IsExternalEngine(definition.Engine) {
		return compileExternalWorkflowDefinition(ctx, definition, registry)
	}
	value := graph.Graph{
		ID: graph.NormalizeID(definition.Name), Version: "1.0.0",
		Inputs: []graph.InputSpec{
			{Name: "params", Schema: graph.Schema{"type": "object"}, Required: true},
			{Name: "session", Schema: graph.Schema{"type": "string"}, Required: true},
			{Name: hostInvocationKeyInput, Schema: graph.Schema{"type": "string"}, Required: true},
		},
	}
	if value.ID == "" {
		value.ID = "nanite-workflow"
	}
	projections := make([]PlanNodeProjection, 0, len(definition.Steps))
	for _, step := range definition.Steps {
		nodeID := graph.NormalizeID(step.ID)
		kind, waitClass, err := workflowKindForDefinition(step)
		if err != nil {
			return compiledWorkflow{}, err
		}
		config, err := cloneConfigMap(step.Config)
		if err != nil {
			return compiledWorkflow{}, fmt.Errorf("go-workflow step %q config: %w", step.ID, err)
		}
		if config == nil {
			config = make(map[string]any)
		}
		config["product_step_id"] = step.ID
		config["product_kind"] = string(step.Kind)
		if step.Verify != nil {
			encoded, marshalErr := json.Marshal(step.Verify)
			if marshalErr != nil {
				return compiledWorkflow{}, fmt.Errorf("go-workflow step %q verify: %w", step.ID, marshalErr)
			}
			var verify map[string]any
			if decodeErr := decodeJSON(encoded, &verify); decodeErr != nil {
				return compiledWorkflow{}, fmt.Errorf("go-workflow step %q verify: %w", step.ID, decodeErr)
			}
			config["verify"] = verify
		}
		bindings := map[string]graph.Binding{
			"workflow-input": {
				Kind:       graph.BindingExpression,
				Expression: &graph.Expression{Text: "inputs.params"},
			},
			"workflow-session-id": {
				Kind:       graph.BindingExpression,
				Expression: &graph.Expression{Text: "inputs.session"},
			},
		}
		dependencySlots := make(map[string]any, len(step.DependsOn))
		needs := make([]graph.Need, 0, len(step.DependsOn))
		for index, dependency := range step.DependsOn {
			dependencyNode := graph.NormalizeID(dependency)
			slot := fmt.Sprintf("dependency-%d", index)
			dependencySlots[dependency] = slot
			bindings[slot] = graph.Binding{
				Kind:       graph.BindingExpression,
				Expression: &graph.Expression{Text: "steps[" + strconv.Quote(dependencyNode) + "].outputs.result"},
			}
			needs = append(needs, graph.Need{Node: dependencyNode, Kind: graph.EdgeControl})
		}
		config["dependency_slots"] = dependencySlots
		node := graph.Node{
			ID: nodeID, DisplayName: step.ID, Kind: kind, KindVersion: StepKindVersion,
			Needs: needs, Config: graph.Config(config), InputBindings: bindings,
			Outputs: []graph.OutputSpec{{Name: "result", Schema: graph.Schema{"type": "object"}}},
		}
		applyHostCrashRecovery(&node)
		value.Nodes = append(value.Nodes, node)
		projections = append(projections, PlanNodeProjection{
			NodeID: nodeID, ProductStepID: step.ID, ProductKind: string(step.Kind), WaitClass: waitClass,
		})
	}

	canonicalGraph, err := json.Marshal(value)
	if err != nil {
		return compiledWorkflow{}, fmt.Errorf("marshal generated go-workflow graph: %w", err)
	}
	var validatedGraph graph.Graph
	if decodeErr := decodeJSON(canonicalGraph, &validatedGraph); decodeErr != nil {
		return compiledWorkflow{}, fmt.Errorf("validate generated go-workflow graph JSON: %w", decodeErr)
	}
	if enumErr := validatedGraph.ValidateEnums(); enumErr != nil {
		return compiledWorkflow{}, fmt.Errorf("validate generated go-workflow graph enums: %w", enumErr)
	}
	canonicalGraph, err = json.Marshal(validatedGraph)
	if err != nil {
		return compiledWorkflow{}, fmt.Errorf("canonicalize generated go-workflow graph: %w", err)
	}
	sourceDigest := values.SHA256Digest(canonicalGraph)
	locator := "nanite-agent:" + value.ID + "@" + value.Version
	compiled := compile.CompileGraph(validatedGraph, compile.GraphCompileOptions{
		SourceFormat: graph.SourceAgent, SourceDigest: sourceDigest,
		Definition: graph.DefinitionRef{
			Authority: "nanite", Kind: "workflow", ID: value.ID,
			Locator: locator, Version: value.Version, Digest: sourceDigest,
		},
	})
	if compiled.Plan == nil || hasDiagnosticErrors(compiled.Diagnostics) {
		return compiledWorkflow{}, diagnosticsError("compile generated go-workflow graph", compiled.Diagnostics)
	}
	material, err := finalizeCompiledPlan(ctx, *compiled.Plan, compile.ValueVisibilityPlan{}, locator, graph.SourceAgent, canonicalGraph, registry)
	if err != nil {
		return compiledWorkflow{}, err
	}
	material.ProductDefinitionName = definition.Name
	return compiledWorkflow{material: material, projections: projections}, nil
}

func compileExternalWorkflowDefinition(ctx context.Context, definition agentworkflow.WorkflowDefinition, registry stepkind.Registry) (compiledWorkflow, error) {
	graphID := graph.NormalizeID(definition.Name)
	if graphID == "" {
		graphID = "nanite-external-workflow"
	}
	value := graph.Graph{
		ID: graphID, Version: "1.0.0",
		Inputs: []graph.InputSpec{
			{Name: "params", Schema: graph.Schema{"type": "object"}, Required: true},
			{Name: "session", Schema: graph.Schema{"type": "string"}, Required: true},
			{Name: hostInvocationKeyInput, Schema: graph.Schema{"type": "string"}, Required: true},
		},
		Nodes: []graph.Node{{
			ID: "external-run", DisplayName: definition.Name, Kind: StepKindExternalEngine, KindVersion: StepKindVersion,
			Config: graph.Config{"external_engine": definition.Engine, "workflow_name": definition.Name, "product_step_id": "run", "product_kind": "tool"},
			InputBindings: map[string]graph.Binding{
				"workflow-input":      {Kind: graph.BindingExpression, Expression: &graph.Expression{Text: "inputs.params"}},
				"workflow-session-id": {Kind: graph.BindingExpression, Expression: &graph.Expression{Text: "inputs.session"}},
			},
			Outputs: []graph.OutputSpec{{Name: "result", Schema: graph.Schema{"type": "object"}}},
		}},
	}
	applyHostCrashRecovery(&value.Nodes[0])
	canonicalGraph, err := json.Marshal(value)
	if err != nil {
		return compiledWorkflow{}, fmt.Errorf("marshal generated external graph: %w", err)
	}
	var validated graph.Graph
	if decodeErr := decodeJSON(canonicalGraph, &validated); decodeErr != nil {
		return compiledWorkflow{}, fmt.Errorf("validate generated external graph: %w", decodeErr)
	}
	if enumErr := validated.ValidateEnums(); enumErr != nil {
		return compiledWorkflow{}, fmt.Errorf("validate generated external graph enums: %w", enumErr)
	}
	sourceDigest := values.SHA256Digest(canonicalGraph)
	locator := "nanite-external:" + graphID + "@" + value.Version
	compiled := compile.CompileGraph(validated, compile.GraphCompileOptions{
		SourceFormat: graph.SourceAgent, SourceDigest: sourceDigest,
		Definition: graph.DefinitionRef{Authority: "nanite", Kind: "workflow", ID: graphID, Locator: locator, Version: value.Version, Digest: sourceDigest},
	})
	if compiled.Plan == nil || hasDiagnosticErrors(compiled.Diagnostics) {
		return compiledWorkflow{}, diagnosticsError("compile generated external graph", compiled.Diagnostics)
	}
	material, err := finalizeCompiledPlan(ctx, *compiled.Plan, compile.ValueVisibilityPlan{}, locator, graph.SourceAgent, canonicalGraph, registry)
	if err != nil {
		return compiledWorkflow{}, err
	}
	material.ProductDefinitionName = definition.Name
	return compiledWorkflow{material: material, projections: []PlanNodeProjection{{NodeID: "external-run", ProductStepID: "run", ProductKind: "tool"}}}, nil
}

func applyHostCrashRecovery(node *graph.Node) {
	if node == nil || (node.Kind != StepKindLoop && node.Kind != StepKindExternalEngine) {
		return
	}
	node.Idempotency = &graph.IdempotencySpec{
		Mode:  graph.IdempotencyKeyed,
		Key:   &graph.Expression{Text: `inputs["` + hostInvocationKeyInput + `"]`},
		Scope: "nanite-workflow-run",
	}
	node.Retry = &graph.RetryPolicy{
		Attempts: 3,
		Backoff:  graph.BackoffPolicy{Strategy: graph.BackoffNone},
		On:       []string{"crashed"},
	}
}

func finalizeCompiledPlan(
	ctx context.Context,
	plan compile.ExecutionPlan,
	_ compile.ValueVisibilityPlan,
	locator string,
	sourceFormat graph.SourceFormat,
	source []byte,
	registry stepkind.Registry,
) (PlanMaterial, error) {
	inferred := compile.InferValueDependencies(&plan, compile.DependencyOptions{})
	if inferred.Plan == nil || hasDiagnosticErrors(inferred.Diagnostics) {
		return PlanMaterial{}, diagnosticsError("infer go-workflow value dependencies", inferred.Diagnostics)
	}
	verifiers, verifierSpecs, verifierDigest, err := currentVerifierRegistry()
	if err != nil {
		return PlanMaterial{}, err
	}
	findings := compile.ValidatePlan(ctx, inferred.Plan, compile.ValidationOptions{StepKinds: registry, Verifiers: verifiers})
	if hasDiagnosticErrors(findings) {
		return PlanMaterial{}, diagnosticsError("validate go-workflow execution plan", findings)
	}
	specs := registry.List()
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Name == specs[j].Name {
			return specs[i].Version < specs[j].Version
		}
		return specs[i].Name < specs[j].Name
	})
	catalogJSON, err := json.Marshal(specs)
	if err != nil {
		return PlanMaterial{}, fmt.Errorf("marshal frozen go-workflow StepKind catalog: %w", err)
	}
	hostContract, hostContractDigest, err := currentHostContract()
	if err != nil {
		return PlanMaterial{}, err
	}
	material := PlanMaterial{
		Plan: *inferred.Plan, Visibility: inferred.Visibility,
		SourceLocator: locator, SourceFormat: sourceFormat,
		SourceDigest: values.SHA256Digest(source), SourceContent: append([]byte(nil), source...),
		ProductDefinitionName: inferred.Plan.Graph.ID,
		StepKindCatalog:       specs, StepKindCatalogDigest: values.SHA256Digest(catalogJSON),
		VerifierCatalog: verifierSpecs, VerifierCatalogDigest: verifierDigest,
		HostContract: hostContract, HostContractDigest: hostContractDigest,
		CreatedAt: time.Now().UTC(),
	}
	if err := validatePlanMaterial(material); err != nil {
		return PlanMaterial{}, fmt.Errorf("freeze exact go-workflow plan material: %w", err)
	}
	return material, nil
}

func workflowKindForDefinition(step agentworkflow.StepDefinition) (name, waitClass string, err error) {
	switch step.Kind {
	case agentworkflow.StepKindLLM:
		role := strings.ToLower(configStringValue(step.Config, "role"))
		mode := strings.ToLower(configStringValue(step.Config, "mode"))
		switch {
		case role == "worker" || mode == "worker":
			return StepKindWorker, "", nil
		case role == "reviewer" || mode == "reviewer":
			return StepKindReviewer, "", nil
		case mode == "turn":
			return StepKindTurn, "", nil
		default:
			return StepKindLLM, "", nil
		}
	case agentworkflow.StepKindTool:
		return StepKindTool, "", nil
	case agentworkflow.StepKindGate:
		return StepKindApproval, "gate", nil
	case agentworkflow.StepKindFlex:
		return StepKindTeam, "flex", nil
	case agentworkflow.StepKindLoop:
		return StepKindLoop, "loop", nil
	default:
		return "", "", fmt.Errorf("unsupported Nanite step kind %q", step.Kind)
	}
}

func projectionsForPlan(plan compile.ExecutionPlan) ([]PlanNodeProjection, error) {
	result := make([]PlanNodeProjection, 0, len(plan.Graph.Nodes))
	for _, node := range plan.Graph.Nodes {
		kind, waitClass := productKindForWorkflow(node.Kind)
		productID := node.ID
		if configured := configStringValue(node.Config, "product_step_id"); configured != "" {
			productID = configured
		}
		if configured := configStringValue(node.Config, "product_kind"); configured != "" {
			kind = configured
		}
		projection := PlanNodeProjection{NodeID: node.ID, ProductStepID: productID, ProductKind: kind, WaitClass: waitClass}
		if err := validatePlanNodeProjection(projection); err != nil {
			return nil, fmt.Errorf("project go-workflow node %q: %w", node.ID, err)
		}
		result = append(result, projection)
	}
	return result, nil
}

func productKindForWorkflow(kind string) (productKind, waitClass string) {
	switch kind {
	case StepKindLLM, StepKindTurn, StepKindWorker, StepKindReviewer:
		return "llm", ""
	case StepKindTool:
		return "tool", ""
	case StepKindTeam:
		return "flex", "flex"
	case StepKindLoop:
		return "loop", "loop"
	case StepKindApproval:
		return "gate", "gate"
	case StepKindExternalCallback:
		return "gate", "gate"
	default:
		return "tool", ""
	}
}

func cloneConfigMap(input map[string]any) (map[string]any, error) {
	if input == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := decodeJSON(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func hasDiagnosticErrors(findings []diagnostic.Diagnostic) bool {
	for _, finding := range findings {
		if finding.Severity == diagnostic.SeverityError {
			return true
		}
	}
	return false
}

func diagnosticsError(operation string, findings []diagnostic.Diagnostic) error {
	if len(findings) == 0 {
		return fmt.Errorf("%s produced no executable plan", operation)
	}
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, fmt.Sprintf("%s: %s", finding.Code, finding.Message))
	}
	return fmt.Errorf("%s: %s", operation, strings.Join(parts, "; "))
}

func planRef(plan compile.ExecutionPlan) workflowruntime.PlanRef {
	return workflowruntime.PlanRef{
		ID: plan.ID, Version: plan.Graph.Version, Digest: plan.Digest,
		SchemaVersion: plan.SchemaVersion,
	}
}
