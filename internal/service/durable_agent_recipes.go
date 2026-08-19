package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	"github.com/hollis-labs/nanite/internal/store"
	"gopkg.in/yaml.v3"
)

const DurableAgentRecipeSchemaVersion = 1

const (
	DurableAgentRecipeKindProjectAdvisor    = "project_advisor"
	DurableAgentRecipeKindManagedCLIHarness = "managed_cli_harness"
	DurableAgentRecipeKindProcessMonitor    = "process_monitor"
	DurableAgentRecipeKindTemplateWorker    = "template_worker"
	DurableAgentRecipeKindOrchestrator      = "orchestrator"
	// DurableAgentRecipeKindConductor (CW-20260816-0066) is the chat-facing
	// entry point: delegates down to per-project execution (Orchestrator/
	// Torque) and out to cross-app coordination (Tether/mux). Deliberately
	// distinct from DurableAgentRecipeKindOrchestrator — Conductor never
	// calls subagent_spawn/workflow_run itself, so reusing the orchestrator
	// kind would misrepresent its dispatch authority. See
	// docs/architecture/conductor-console-design.md.
	DurableAgentRecipeKindConductor = "conductor"
)

var (
	ErrDurableAgentRecipeNotFound       = errors.New("durable agent recipe not found")
	ErrDurableAgentRecipeInvalid        = errors.New("durable agent recipe invalid")
	ErrDurableAgentRecipeMissingInputs  = errors.New("durable agent recipe missing required inputs")
	ErrDurableAgentRecipeApplyNotReady  = errors.New("durable agent recipe cannot be applied")
	ErrDurableAgentRecipeUnsupportedRun = errors.New("durable agent recipe unsupported runtime")
	ErrDurableAgentRecipeDuplicateID    = errors.New("durable agent recipe duplicate id")
)

const (
	DurableAgentRecipeInputTypeString      = "string"
	DurableAgentRecipeInputTypeTextarea    = "textarea"
	DurableAgentRecipeInputTypeBoolean     = "boolean"
	DurableAgentRecipeInputTypeSelect      = "select"
	DurableAgentRecipeInputTypePath        = "path"
	DurableAgentRecipeInputTypeProfile     = "profile"
	DurableAgentRecipeInputTypeProvider    = "provider"
	DurableAgentRecipeInputTypeModel       = "model"
	DurableAgentRecipeInputTypeRuntimeKind = "runtime_kind"
)

type DurableAgentRecipe struct {
	ID               string                    `json:"id" yaml:"id"`
	SchemaVersion    int                       `json:"schema_version" yaml:"schema_version"`
	Kind             string                    `json:"kind" yaml:"kind"`
	Name             string                    `json:"name" yaml:"name"`
	Description      string                    `json:"description" yaml:"description"`
	LifecycleClass   string                    `json:"lifecycle_class" yaml:"lifecycle_class"`
	ProfileID        string                    `json:"profile_id,omitempty" yaml:"profile_id,omitempty"`
	ProfileRule      string                    `json:"profile_rule,omitempty" yaml:"profile_rule,omitempty"`
	Provider         string                    `json:"provider" yaml:"provider"`
	Model            string                    `json:"model" yaml:"model"`
	RuntimeKind      string                    `json:"runtime_kind" yaml:"runtime_kind"`
	LaunchSourceType string                    `json:"launch_source_type" yaml:"launch_source_type"`
	LaunchSourceID   string                    `json:"launch_source_id,omitempty" yaml:"launch_source_id,omitempty"`
	WorkRoot         string                    `json:"work_root,omitempty" yaml:"work_root,omitempty"`
	WakeDefaults     DurableAgentWakePayload   `json:"wake_defaults" yaml:"wake_defaults"`
	Metadata         map[string]string         `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Tags             []string                  `json:"tags,omitempty" yaml:"tags,omitempty"`
	Injections       []RecipeInjectionPlan     `json:"injections,omitempty" yaml:"injections,omitempty"`
	Inputs           []DurableAgentRecipeInput `json:"inputs,omitempty" yaml:"inputs,omitempty"`
}

type DurableAgentRecipeInput struct {
	ID          string                          `json:"id" yaml:"id"`
	Label       string                          `json:"label" yaml:"label"`
	Description string                          `json:"description,omitempty" yaml:"description,omitempty"`
	Help        string                          `json:"help,omitempty" yaml:"help,omitempty"`
	Type        string                          `json:"type" yaml:"type"`
	Required    bool                            `json:"required,omitempty" yaml:"required,omitempty"`
	Default     any                             `json:"default,omitempty" yaml:"default,omitempty"`
	Placeholder string                          `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
	Options     []DurableAgentRecipeInputOption `json:"options,omitempty" yaml:"options,omitempty"`
	Secret      bool                            `json:"secret,omitempty" yaml:"secret,omitempty"`
	MapsTo      string                          `json:"maps_to,omitempty" yaml:"maps_to,omitempty"`
}

type DurableAgentRecipeInputOption struct {
	Value string `json:"value" yaml:"value"`
	Label string `json:"label" yaml:"label"`
}

type RecipeInjectionPlan struct {
	ID          string `json:"id" yaml:"id"`
	Kind        string `json:"kind" yaml:"kind"`
	Target      string `json:"target" yaml:"target"`
	Description string `json:"description" yaml:"description"`
	Secret      bool   `json:"secret" yaml:"secret"`
}

type DurableAgentRecipeRequest struct {
	Name        string                  `json:"name"`
	Slug        string                  `json:"slug"`
	ProfileID   string                  `json:"profile_id"`
	Provider    string                  `json:"provider"`
	Model       string                  `json:"model"`
	RuntimeKind string                  `json:"runtime_kind"`
	WorkRoot    string                  `json:"work_root"`
	ProjectID   string                  `json:"project_id"`
	WakePayload DurableAgentWakePayload `json:"wake_payload"`
	Metadata    map[string]string       `json:"metadata"`
	Start       bool                    `json:"start"`
}

type DurableAgentRecipePlan struct {
	RecipeID            string                     `json:"recipe_id"`
	RecipeSchemaVersion int                        `json:"recipe_schema_version"`
	Instance            store.DurableAgentInstance `json:"instance"`
	LaunchPolicy        DurableAgentLaunchPolicy   `json:"launch_policy"`
	WakePayload         DurableAgentWakePayload    `json:"wake_payload"`
	SessionPolicy       string                     `json:"session_policy"`
	WouldCreateSession  bool                       `json:"would_create_session"`
	WouldReuseSession   bool                       `json:"would_reuse_session"`
	MissingRequirements []string                   `json:"missing_requirements,omitempty"`
	Unsupported         []string                   `json:"unsupported,omitempty"`
	Injections          []RecipeInjectionPlan      `json:"injections,omitempty"`
	Ready               bool                       `json:"ready"`
}

type DurableAgentRecipeApplyResult struct {
	Plan         DurableAgentRecipePlan      `json:"plan"`
	Instance     *store.DurableAgentInstance `json:"instance"`
	LaunchResult *DurableAgentLaunchResult   `json:"launch_result,omitempty"`
}

type DurableAgentRecipeService interface {
	List(ctx context.Context) ([]DurableAgentRecipe, error)
	Get(ctx context.Context, id string) (*DurableAgentRecipe, error)
	DryRun(ctx context.Context, id string, req DurableAgentRecipeRequest) (*DurableAgentRecipePlan, error)
	Apply(ctx context.Context, id string, req DurableAgentRecipeRequest) (*DurableAgentRecipeApplyResult, error)
}

type durableAgentRecipeService struct {
	recipes []DurableAgentRecipe
	byID    map[string]DurableAgentRecipe
	agents  DurableAgentService
}

func NewDurableAgentRecipeService(agents DurableAgentService, catalogPaths ...string) (DurableAgentRecipeService, error) {
	recipes, err := loadDurableAgentRecipes(catalogPaths...)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]DurableAgentRecipe, len(recipes))
	for _, recipe := range recipes {
		byID[recipe.ID] = recipe
	}
	return &durableAgentRecipeService{recipes: recipes, byID: byID, agents: agents}, nil
}

func (s *durableAgentRecipeService) List(_ context.Context) ([]DurableAgentRecipe, error) {
	out := append([]DurableAgentRecipe(nil), s.recipes...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *durableAgentRecipeService) Get(_ context.Context, id string) (*DurableAgentRecipe, error) {
	recipe, ok := s.byID[id]
	if !ok {
		return nil, ErrDurableAgentRecipeNotFound
	}
	return &recipe, nil
}

func (s *durableAgentRecipeService) DryRun(_ context.Context, id string, req DurableAgentRecipeRequest) (*DurableAgentRecipePlan, error) {
	recipe, ok := s.byID[id]
	if !ok {
		return nil, ErrDurableAgentRecipeNotFound
	}
	plan := compileDurableAgentRecipe(recipe, req)
	return &plan, nil
}

func (s *durableAgentRecipeService) Apply(ctx context.Context, id string, req DurableAgentRecipeRequest) (*DurableAgentRecipeApplyResult, error) {
	recipe, ok := s.byID[id]
	if !ok {
		return nil, ErrDurableAgentRecipeNotFound
	}
	plan := compileDurableAgentRecipe(recipe, req)
	if len(plan.MissingRequirements) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrDurableAgentRecipeMissingInputs, strings.Join(plan.MissingRequirements, ", "))
	}
	if len(plan.Unsupported) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrDurableAgentRecipeApplyNotReady, strings.Join(plan.Unsupported, ", "))
	}
	inst := plan.Instance
	if err := s.agents.Create(ctx, &inst); err != nil {
		return nil, err
	}
	result := &DurableAgentRecipeApplyResult{Plan: plan, Instance: &inst}
	if req.Start {
		launch, err := s.agents.Start(ctx, inst.ID, DurableAgentStartRequest{
			ProjectID:   req.ProjectID,
			WakePayload: plan.WakePayload,
		})
		if err != nil {
			return nil, err
		}
		result.Instance = launch.Instance
		result.LaunchResult = launch
	}
	return result, nil
}

func compileDurableAgentRecipe(recipe DurableAgentRecipe, req DurableAgentRecipeRequest) DurableAgentRecipePlan {
	plan := DurableAgentRecipePlan{
		RecipeID:            recipe.ID,
		RecipeSchemaVersion: recipe.SchemaVersion,
		Injections:          append([]RecipeInjectionPlan(nil), recipe.Injections...),
	}
	missing := validateDurableAgentRecipe(recipe)
	if req.ProfileID == "" && recipe.ProfileID == "" {
		missing = append(missing, "profile_id")
	}
	missing = append(missing, missingRecipeInputs(recipe, req)...)

	inst := store.DurableAgentInstance{
		Name:             firstNonEmpty(req.Name, recipe.Name),
		Slug:             req.Slug,
		ProfileID:        firstNonEmpty(req.ProfileID, recipe.ProfileID),
		LifecycleClass:   recipe.LifecycleClass,
		Provider:         firstNonEmpty(req.Provider, recipe.Provider),
		Model:            firstNonEmpty(req.Model, recipe.Model),
		RuntimeKind:      firstNonEmpty(req.RuntimeKind, recipe.RuntimeKind),
		LaunchSourceType: firstNonEmpty(recipe.LaunchSourceType, store.DurableAgentLaunchDurableAdvisor),
		LaunchSourceID:   firstNonEmpty(recipe.LaunchSourceID, recipe.ID),
		WorkRoot:         firstNonEmpty(req.WorkRoot, recipe.WorkRoot),
		MetadataJSON:     recipeMetadataJSON(recipe, req),
	}
	if inst.Slug == "" && inst.Name != "" {
		inst.Slug = durableAgentRecipeSlug(inst.Name)
	}
	wake := mergeWakePayload(recipe.WakeDefaults, req.WakePayload)
	policy, err := durableAgentLaunchPolicyFor(&inst, wake)
	if err != nil {
		plan.Unsupported = append(plan.Unsupported, err.Error())
	}
	for _, injection := range plan.Injections {
		if injection.Secret {
			plan.Unsupported = append(plan.Unsupported, "recipe injections must be non-secret: "+injection.ID)
		}
	}
	plan.Instance = inst
	plan.LaunchPolicy = policy
	plan.WakePayload = wake
	plan.SessionPolicy = policy.SessionPolicy
	plan.WouldCreateSession = req.Start
	plan.WouldReuseSession = false
	plan.MissingRequirements = missing
	plan.Ready = len(plan.MissingRequirements) == 0 && len(plan.Unsupported) == 0
	return plan
}

func validateDurableAgentRecipe(recipe DurableAgentRecipe) []string {
	var missing []string
	if recipe.SchemaVersion != DurableAgentRecipeSchemaVersion {
		missing = append(missing, "supported schema_version")
	}
	if recipe.ID == "" {
		missing = append(missing, "id")
	}
	if recipe.Kind == "" {
		missing = append(missing, "kind")
	}
	if recipe.Name == "" {
		missing = append(missing, "name")
	}
	if recipe.LifecycleClass == "" {
		missing = append(missing, "lifecycle_class")
	}
	if recipe.RuntimeKind == "" {
		missing = append(missing, "runtime_kind")
	} else if !runtimekind.IsManagedAutomation(runtimekind.Parse(recipe.RuntimeKind)) {
		missing = append(missing, "managed runtime_kind")
	}
	if recipe.LaunchSourceType == "" {
		missing = append(missing, "launch_source")
	}
	seenInputs := make(map[string]struct{}, len(recipe.Inputs))
	for _, input := range recipe.Inputs {
		if input.ID == "" {
			missing = append(missing, "inputs.id")
			continue
		}
		if _, ok := seenInputs[input.ID]; ok {
			missing = append(missing, "duplicate input id: "+input.ID)
			continue
		}
		seenInputs[input.ID] = struct{}{}
		if input.Label == "" {
			missing = append(missing, "inputs."+input.ID+".label")
		}
		if !isSupportedRecipeInputType(input.Type) {
			missing = append(missing, "inputs."+input.ID+".type")
		}
		if input.Type == DurableAgentRecipeInputTypeSelect {
			if len(input.Options) == 0 {
				missing = append(missing, "inputs."+input.ID+".options")
			}
			for _, option := range input.Options {
				if option.Value == "" || option.Label == "" {
					missing = append(missing, "inputs."+input.ID+".options.value_label")
					break
				}
			}
		}
	}
	return missing
}

func builtinDurableAgentRecipes() []DurableAgentRecipe {
	return []DurableAgentRecipe{
		{
			ID:               "architect-advisor",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProjectAdvisor,
			Name:             "Architect Advisor",
			Description:      "A reusable system-design and architecture partner scoped to one project or system.",
			LifecycleClass:   store.DurableAgentClassAdvisor,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "architect_advisor",
				},
			},
			Metadata: map[string]string{
				"product_story":      "architect_advisor",
				"exposure_surface":   "internal_chat_and_harness_v1",
				"integration_status": "operator_managed",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "architecture-brief",
				Kind:        "planned_context",
				Target:      "wake_payload.facts",
				Description: "Future architecture summary or repo/system notes surfaced as non-secret wake facts.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Architect Advisor"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "architect-advisor"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("project_scope", "Project scope", "metadata.project_scope", true, "nanite"),
				recipeStringInput("system_scope", "System scope", "metadata.system_scope", true, "session runtime and durable agent control plane"),
				recipeTextareaInput("agent_network_notes", "Agent network notes", "metadata.agent_network_notes", false, "Optional notes about other durable agents, humans, or services this architect should understand."),
				recipeTextareaInput("boot_knowledge_hint", "Boot knowledge hint", "metadata.boot_knowledge_hint", false, "Preview-only non-secret boot knowledge or procedure hints for later configuration."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff instructions for the architecture advisor."),
			},
			Tags: []string{"advisor", "architecture", "product"},
		},
		{
			// Profile pairing: operator selects the `conductor` AgentProfile
			// (.nanite/agents/conductor.md) at apply-time. Conductor
			// (CW-20260816-0066) is the chat-facing entry point across the
			// user's concurrent projects — it delegates *down* to existing
			// per-project execution (Orchestrator/Torque) and *out* to
			// cross-app coordination (Tether/mux) rather than
			// reimplementing either. It deliberately does NOT get
			// subagent_spawn/workflow_run — those stay exclusive to the
			// `orchestrator` recipe/profile above. Foundation task for
			// CW-20260816-0067/-0068/-0069/-0070; see
			// docs/architecture/conductor-console-design.md for the full
			// design.
			ID:               "conductor",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindConductor,
			Name:             "Conductor",
			Description:      "Chat-facing entry point across concurrent projects: delegates to per-project execution (Orchestrator/Torque) and cross-app coordination (Tether/mux), relays distilled status, captures durable notes, and surfaces approvals/blockers — never does the underlying work itself.",
			LifecycleClass:   store.DurableAgentClassHarness,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchAPIChat,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "conductor",
				},
			},
			Metadata: map[string]string{
				"product_story":      "conductor",
				"exposure_surface":   "internal_chat_and_harness_v1",
				"integration_status": "operator_managed",
				"role_boundary":      "delegates_and_relays_never_dispatches_or_executes",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "pending-digest-brief",
				Kind:        "planned_context",
				Target:      "wake_payload.facts",
				Description: "Future pull-based pending-approvals/completions digest surfaced as non-secret wake facts when the operator asks 'what's pending?'.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Conductor"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "conductor"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev"),
				recipeTextareaInput("project_roster_notes", "Project roster notes", "metadata.project_roster_notes", false, "Which projects/apps Conductor tracks (Torque project IDs, Tether registry names) and any per-project routing notes."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff instructions for Conductor (e.g. which projects to check in on at first boot)."),
			},
			Tags: []string{"harness", "conductor", "concierge", "product"},
		},
		{
			ID:               "external-company-agent",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProjectAdvisor,
			Name:             "External Company Agent Endpoint",
			Description:      "A durable API-backed advisor prepared for another company system to drive through the harness v1 API.",
			LifecycleClass:   store.DurableAgentClassAdvisor,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "external_company_agent",
				},
			},
			Metadata: map[string]string{
				"product_story":       "external_company_agent",
				"exposure_surface":    "harness_v1",
				"integration_status":  "company_adapter_operator_managed",
				"permissions_posture": "review_required",
				"adapter_status":      "recipe_prepares_agent_only",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "endpoint-contract",
				Kind:        "planned_context",
				Target:      "metadata.endpoint_purpose",
				Description: "Operator-supplied endpoint purpose and contract notes retained as non-secret metadata for the later adapter layer.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "External Company Agent"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "external-company-agent"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/company-agent"),
				recipeStringInput("company_name", "Company / system name", "metadata.company_name", true, "Partner API"),
				recipeTextareaInput("endpoint_purpose", "Endpoint purpose", "metadata.endpoint_purpose", true, "What this external company endpoint should do and what requests it should accept."),
				recipeSelectInput("permissions_posture", "Permissions posture", "metadata.permissions_posture", false, "default", []DurableAgentRecipeInputOption{
					{Value: "default", Label: "Default"},
					{Value: "accept-edits", Label: "Accept edits"},
					{Value: "plan", Label: "Plan"},
					{Value: "yolo", Label: "YOLO"},
				}),
				recipeTextareaInput("tool_posture_notes", "Tool posture notes", "metadata.tool_posture_notes", false, "Notes about tool posture, approvals, and operator expectations for the external caller."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff prompt sent when the operator starts the endpoint agent."),
			},
			Tags: []string{"advisor", "external", "endpoint", "product"},
		},
		{
			ID:               "managed-cli-harness",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindManagedCLIHarness,
			Name:             "Managed CLI Harness",
			Description:      "A durable managed CLI harness session for local tool-backed work controlled through Nanite chat and harness APIs.",
			LifecycleClass:   store.DurableAgentClassHarness,
			ProfileRule:      "operator_selected",
			Provider:         "pty-claude",
			Model:            "claude-cli",
			RuntimeKind:      string(runtimekind.StreamingStdio),
			LaunchSourceType: store.DurableAgentLaunchCLIHarness,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts:  map[string]string{"substrate": "managed-cli", "control_surface": "chat_and_harness_v1"},
			},
			Metadata: map[string]string{
				"product_story":      "managed_cli_harness",
				"tui_support":        "not_supported",
				"exposure_surface":   "chat_and_harness_v1",
				"integration_status": "operator_managed",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "workspace-brief",
				Kind:        "planned_native_file",
				Target:      "prompts/workspace-brief.md",
				Description: "Future non-secret workspace briefing planted before CLI boot.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Managed CLI Harness"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "managed-cli-harness"),
				recipeProfileInput(true),
				recipeProviderInput(false, "pty-claude"),
				recipeModelInput(false, "claude-cli"),
				recipeRuntimeKindInput(false, string(runtimekind.StreamingStdio)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("boot_profile_id", "Boot profile ID", "metadata.boot_profile_id", false, "bootprofile:claude-smoke"),
				recipeTextareaInput("kickoff_prompt", "Kickoff prompt", "wake_payload.prompt", false, "Optional kickoff instructions for the managed harness."),
				recipeTextareaInput("boot_plan_hint", "Boot plan hint", "metadata.boot_plan_hint", false, "Preview-only non-secret note about files, prompts, or setup the harness should eventually plant."),
			},
			Tags: []string{"cli", "harness"},
		},
		{
			// Profile pairing: operator selects the `orchestrator` AgentProfile
			// (.nanite/agents/orchestrator.md) at apply-time. Its roleTools are
			// the one place in this whole role family that DOES include
			// subagent_spawn/workflow_run — by design, this is the only role
			// permitted to dispatch (Planner/PM/Reviewer must never get these).
			ID:               "orchestrator",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindOrchestrator,
			Name:             "Orchestrator",
			Description:      "Long-lived executive: polls Torque task state and dispatches ready work via workflow_run/subagent_spawn, waiting on Reviewer/gate clearance before advancing dependents.",
			LifecycleClass:   store.DurableAgentClassHarness,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchAPIChat,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "orchestrator",
				},
			},
			Metadata: map[string]string{
				"product_story":      "orchestrator",
				"exposure_surface":   "internal_chat_and_harness_v1",
				"integration_status": "operator_managed",
				"role_boundary":      "polls_torque_task_status_dispatches_via_workflow_run_or_subagent_spawn",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "dispatch-scope-brief",
				Kind:        "planned_context",
				Target:      "wake_payload.facts",
				Description: "Future Torque project/tag scope filter or workflow-mapping policy surfaced as non-secret wake facts.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Orchestrator"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "orchestrator"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("torque_project_id", "Torque project ID", "metadata.torque_project_id", true, "PRJ-20260417-0002"),
				recipeTextareaInput("dispatch_scope_notes", "Dispatch scope notes", "metadata.dispatch_scope_notes", false, "Which tags/task shapes map to which shipped WorkflowDefinition (workflow_run) vs freeform dispatch (subagent_spawn); any project/tag filter to scope polling."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff instructions for the orchestrator (e.g. which project/plan to start walking)."),
			},
			Tags: []string{"harness", "orchestrator", "dispatch", "product"},
		},
		{
			// Profile pairing: operator selects the `task-planner` AgentProfile
			// (.nanite/agents/task-planner.md) at apply-time — NOT the existing
			// `planner` AgentProfile slug. That slug is reserved for the
			// Phase-6 cognition-arc stub (internal/agent/builtin/profiles/planner.md,
			// deliberately tool-less, resolved via dispatch.PlannerRoleSlug for
			// reflex-routed decomposition) and is a different role entirely —
			// reusing it here would silently overwrite that migration-tracked
			// identity at boot-time upsert. `task-planner`'s roleTools grant
			// full Torque task-lifecycle write access but exclude
			// subagent_spawn/workflow_run (Planner produces the plan; it never
			// dispatches it — that's the Orchestrator's job).
			ID:               "planner",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindTemplateWorker,
			Name:             "Planner",
			Description:      "A one-shot sequencing pass: given a scoped goal or a design doc, produces a dependency-ordered set of Torque tasks with embedded boot prompts.",
			LifecycleClass:   store.DurableAgentClassTemplate,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchTaskTemplateRun,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeLifecycleStart,
				Facts: map[string]string{
					"story": "planner",
				},
			},
			Metadata: map[string]string{
				"product_story":      "planner",
				"run_shape":          "fresh_template_run",
				"integration_status": "operator_managed",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "plan-brief",
				Kind:        "planned_native_file",
				Target:      "tasks/plan-brief.md",
				Description: "Future non-secret goal/design-doc brief planted for planner runs.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Planner"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "planner"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("torque_project_id", "Torque project ID", "metadata.torque_project_id", true, "PRJ-20260417-0002"),
				recipeTextareaInput("goal_or_design_doc", "Goal or design doc", "wake_payload.prompt", true, "The scoped goal to sequence, or a pointer to an Architect's design doc (file path or Torque task ID) to turn into a dependency-ordered task set."),
				recipeTextareaInput("boot_knowledge_hint", "Boot knowledge hint", "metadata.boot_knowledge_hint", false, "Preview-only non-secret notes about conventions, prior sprints, or related tasks this Planner should be aware of."),
			},
			Tags: []string{"template", "planner", "sequencing", "product"},
		},
		{
			ID:               "process-monitor",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProcessMonitor,
			Name:             "Process Monitor",
			Description:      "A process agent placeholder that wakes with fresh fact-oriented sessions.",
			LifecycleClass:   store.DurableAgentClassProcess,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchProcessTick,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeProcessTick,
				Facts:  map[string]string{"cadence": "external_or_manual_tick"},
			},
			Metadata: map[string]string{
				"product_story":      "process_monitor_substrate",
				"monitor_mode":       "manual_or_external_tick",
				"integration_status": "substrate_level",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "tick-facts",
				Kind:        "planned_context",
				Target:      "wake_payload.facts",
				Description: "Future process probes summarized as structured wake facts.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Process Monitor"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "process-monitor"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "/tmp/process-monitor"),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional monitor instructions."),
			},
			Tags: []string{"process", "monitor"},
		},
		{
			ID:               "project-advisor",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProjectAdvisor,
			Name:             "Project Advisor",
			Description:      "A project or topic advisor with a reusable API-backed session.",
			LifecycleClass:   store.DurableAgentClassAdvisor,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts:  map[string]string{"scope": "project_or_topic"},
			},
			Metadata: map[string]string{
				"product_story":      "project_advisor",
				"exposure_surface":   "chat_and_harness_v1",
				"integration_status": "operator_managed",
			},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Project Advisor"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "project-advisor"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("scope_topic", "Scope / topic", "metadata.scope_topic", true, "nanite frontend/runtime API"),
				recipeTextareaInput("boot_knowledge_hint", "Boot knowledge hint", "metadata.boot_knowledge_hint", false, "Preview-only non-secret notes, procedures, or knowledge to configure later."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional advisor kickoff prompt."),
			},
			Tags: []string{"advisor", "project"},
		},
		{
			// Profile pairing: operator selects the `project-manager` AgentProfile
			// (.nanite/agents/project-manager.md) at apply-time — its roleTools
			// already excludes subagent_spawn/workflow_run, which must stay
			// excluded (PM coordinates; it never dispatches). The older
			// `agridd-project-manager` profile is the POC this was generalized
			// from and is slated for retirement; new applies should use this one.
			ID:               "project-manager",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProjectAdvisor,
			Name:             "Project Manager",
			Description:      "A reusable work-coordination advisor that monitors task/dependency state, surfaces blockers and stalls, and recommends dispatch timing — coordinates, never executes or dispatches.",
			LifecycleClass:   store.DurableAgentClassAdvisor,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "project_manager",
				},
			},
			Metadata: map[string]string{
				"product_story":      "project_manager",
				"exposure_surface":   "internal_chat_and_harness_v1",
				"integration_status": "operator_managed",
				"role_boundary":      "coordinates_only_no_dispatch",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "workstate-brief",
				Kind:        "planned_context",
				Target:      "wake_payload.facts",
				Description: "Future Torque work-state snapshot or blocker summary surfaced as non-secret wake facts.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Project Manager"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "project-manager"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("project_scope", "Project scope", "metadata.project_scope", true, "nanite"),
				recipeStringInput("torque_project_filter", "Torque project ID / filter", "metadata.torque_project_filter", false, "PRJ-20260417-0002"),
				recipeStringInput("schedule_hint", "Schedule / cadence hint", "metadata.schedule_hint", false, "cron: 0 9,17 * * 1-5 (twice daily, weekdays)"),
				recipeTextareaInput("escalation_notes", "Escalation notes", "metadata.escalation_notes", false, "Optional thresholds/recipients for blocker escalation, if different from defaults (doing >48h, review >24h, sprint budget <20%)."),
				recipeTextareaInput("boot_knowledge_hint", "Boot knowledge hint", "metadata.boot_knowledge_hint", false, "Preview-only non-secret notes about institutional-memory docs (followups, sprint/plan files) this PM should read on first boot."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff instructions for the project manager."),
			},
			Tags: []string{"advisor", "project-manager", "work-coordination", "product"},
		},
		{
			ID:               "proxima-relay",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProjectAdvisor,
			Name:             "Proxima Relay",
			Description:      "An operator-facing relay/concierge durable agent that can front other durable agents through Nanite surfaces.",
			LifecycleClass:   store.DurableAgentClassAdvisor,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "proxima_relay",
				},
			},
			Metadata: map[string]string{
				"product_story":      "proxima_relay",
				"relay_status":       "operator_routed",
				"exposure_surface":   "chat_and_harness_v1",
				"integration_status": "mailbox_and_auto_routing_preview_only",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "relay-brief",
				Kind:        "planned_context",
				Target:      "metadata.agent_network_notes",
				Description: "Operator-provided network and routing notes retained as non-secret metadata for the relay agent.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Proxima Relay"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "proxima-relay"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/proxima"),
				recipeStringInput("operator_scope", "Operator scope", "metadata.operator_scope", true, "project operations and agent routing"),
				recipeTextareaInput("agent_network_notes", "Agent network notes", "metadata.agent_network_notes", false, "Durable-agent roster, escalation paths, or manual routing guidance for Proxima."),
				recipeTextareaInput("relay_channel_strategy", "Relay channel strategy", "metadata.relay_channel_strategy", false, "Preview-only notes about mailbox, web chat, or external channel routing."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff prompt for the relay/concierge agent."),
			},
			Tags: []string{"advisor", "relay", "concierge", "product"},
		},
		{
			// Profile pairing: operator selects the existing `reviewer`
			// AgentProfile (internal/agent/builtin/profiles/reviewer.md) at
			// apply-time — reused, not duplicated, per the ticket's explicit
			// ask. Distinct from `code-auditor`: reviewer is
			// acceptance-criteria-driven (the parent supplies criteria),
			// code-auditor is rubric-driven. Don't conflate the two.
			ID:               "reviewer",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindTemplateWorker,
			Name:             "Reviewer",
			Description:      "A one-shot, freeform (non-workflow) acceptance-criteria review pass: audits a delivered work product against stated criteria and reports a structured verdict.",
			LifecycleClass:   store.DurableAgentClassTemplate,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchTaskTemplateRun,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeLifecycleStart,
				Facts: map[string]string{
					"story": "reviewer",
				},
			},
			Metadata: map[string]string{
				"product_story":      "reviewer",
				"run_shape":          "fresh_template_run",
				"integration_status": "operator_managed",
				"review_mode":        "acceptance_criteria_freeform",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "review-brief",
				Kind:        "planned_native_file",
				Target:      "tasks/review-brief.md",
				Description: "Future non-secret work-product pointer or acceptance-criteria brief planted for standalone reviewer runs.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Reviewer"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "reviewer"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/project"),
				recipeStringInput("torque_task_id", "Torque task ID", "metadata.torque_task_id", true, "CW-20260815-0001"),
				recipeTextareaInput("review_subject", "Review subject and acceptance criteria", "wake_payload.prompt", true, "What to review (the work product — diff, commit, PR, deliverable) and the acceptance criteria to check it against. A review without stated criteria is opinion dressed up as judgment."),
			},
			Tags: []string{"template", "reviewer", "acceptance-criteria", "product"},
		},
		{
			ID:               "system-monitor",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProcessMonitor,
			Name:             "System Monitor",
			Description:      "A product-shaped monitor that prepares a process durable agent for explicit wake passes and due-list scheduling.",
			LifecycleClass:   store.DurableAgentClassProcess,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchProcessTick,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeScheduled,
				Facts: map[string]string{
					"monitor_mode": "explicit_due_wake_pass",
				},
			},
			Metadata: map[string]string{
				"product_story":      "system_monitor",
				"monitor_mode":       "explicit_due_wake_pass",
				"integration_status": "no_background_poller",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "monitor-facts",
				Kind:        "planned_context",
				Target:      "wake_payload.facts",
				Description: "Future probes or repo/system facts summarized into wake facts before each explicit due pass.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "System Monitor"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "system-monitor"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "/tmp/system-monitor"),
				recipeStringInput("system_name", "System / service name", "metadata.system_name", true, "nanite api"),
				recipeStringInput("target_path", "Target URL / path", "metadata.target_path", true, "http://localhost:8090/health"),
				recipeStringInput("schedule_hint", "Schedule / cadence hint", "metadata.schedule_hint", false, "cron: */15 * * * *"),
				recipeTextareaInput("alert_notes", "Alert / escalation notes", "metadata.alert_notes", false, "Non-secret notes about who should investigate or what symptoms matter."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional monitor instructions. Phase 29 still uses explicit wake passes rather than a background poller."),
			},
			Tags: []string{"process", "monitor", "product"},
		},
		{
			ID:               "task-writer",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindTemplateWorker,
			Name:             "Task Writer",
			Description:      "A one-shot task brief and work-plan writer that compiles into a template durable agent.",
			LifecycleClass:   store.DurableAgentClassTemplate,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchTaskTemplateRun,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeLifecycleStart,
				Facts: map[string]string{
					"story": "task_writer",
				},
			},
			Metadata: map[string]string{
				"product_story":      "task_writer",
				"run_shape":          "fresh_template_run",
				"integration_status": "operator_managed",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "task-brief",
				Kind:        "planned_native_file",
				Target:      "tasks/task-brief.md",
				Description: "Future non-secret task brief planted for template-worker style runs.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Task Writer"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "task-writer"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "/tmp/task-writer"),
				recipeStringInput("task_domain", "Task domain / project", "metadata.task_domain", true, "nanite runtime hardening"),
				recipeSelectInput("output_style", "Output style", "metadata.output_style", true, "implementation_plan", []DurableAgentRecipeInputOption{
					{Value: "implementation_plan", Label: "Implementation plan"},
					{Value: "backlog_brief", Label: "Backlog brief"},
					{Value: "operating_procedure", Label: "Operating procedure"},
				}),
				recipeTextareaInput("task_brief_hint", "Task brief hint", "metadata.task_brief_hint", false, "Preview-only non-secret task brief or routine context to preserve with the durable agent."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional one-shot task-writing instructions."),
			},
			Tags: []string{"template", "task", "writer", "product"},
		},
		{
			ID:               "template-worker",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindTemplateWorker,
			Name:             "Template Worker",
			Description:      "A one-shot worker template that creates a fresh run session.",
			LifecycleClass:   store.DurableAgentClassTemplate,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchTaskTemplateRun,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeLifecycleStart,
				Facts:  map[string]string{"run_type": "one_shot"},
			},
			Metadata: map[string]string{
				"product_story":      "template_worker_substrate",
				"run_shape":          "fresh_template_run",
				"integration_status": "substrate_level",
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "task-brief",
				Kind:        "planned_native_file",
				Target:      "tasks/task.md",
				Description: "Future non-secret task brief planted for CLI/template runs.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Template Worker"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "template-worker"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "/tmp/task-worker"),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional task brief or worker instructions."),
			},
			Tags: []string{"template", "worker"},
		},
		{
			ID:               "web-chat-agent",
			SchemaVersion:    DurableAgentRecipeSchemaVersion,
			Kind:             DurableAgentRecipeKindProjectAdvisor,
			Name:             "Web Chat Agent",
			Description:      "A durable advisor prepared to back a web chat experience through Nanite sessions and the harness v1 API.",
			LifecycleClass:   store.DurableAgentClassAdvisor,
			ProfileRule:      "operator_selected",
			Provider:         "anthropic",
			Model:            "",
			RuntimeKind:      string(runtimekind.API),
			LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
			WakeDefaults: DurableAgentWakePayload{
				Reason: DurableAgentWakeManual,
				Facts: map[string]string{
					"story": "web_chat_agent",
				},
			},
			Metadata: map[string]string{
				"product_story":      "web_chat_agent",
				"exposure_surface":   "harness_v1",
				"integration_status": "web_widget_operator_managed",
				"session_reuse":      DurableAgentSessionPolicyReuseLatestOrCreate,
			},
			Injections: []RecipeInjectionPlan{{
				ID:          "persona-brief",
				Kind:        "planned_context",
				Target:      "metadata.persona_scope",
				Description: "Public-facing scope/persona guidance retained as non-secret metadata for the eventual web client integration.",
			}},
			Inputs: []DurableAgentRecipeInput{
				recipeStringInput("name", "Name", "durable_agent.name", true, "Web Chat Agent"),
				recipeStringInput("slug", "Slug", "durable_agent.slug", false, "web-chat-agent"),
				recipeProfileInput(true),
				recipeProviderInput(false, "anthropic"),
				recipeModelInput(false, ""),
				recipeRuntimeKindInput(false, string(runtimekind.API)),
				recipePathInput("work_root", "Work root", "durable_agent.work_root", false, "~/dev/web-chat-agent"),
				recipeStringInput("public_display_name", "Public display name", "metadata.public_display_name", true, "Support Concierge"),
				recipeTextareaInput("persona_scope", "Persona / scope", "metadata.persona_scope", true, "What the public-facing web agent should cover and how it should speak."),
				recipeSelectInput("session_reuse_policy", "Session reuse policy", "metadata.session_reuse", false, DurableAgentSessionPolicyReuseLatestOrCreate, []DurableAgentRecipeInputOption{
					{Value: DurableAgentSessionPolicyReuseLatestOrCreate, Label: "Reuse latest or create"},
					{Value: DurableAgentSessionPolicyReuseManaged, Label: "Reuse managed"},
				}),
				recipeTextareaInput("exposure_notes", "Exposure notes", "metadata.exposure_notes", false, "Notes about how the external web client will call the harness API. No web widget is provisioned by this recipe."),
				recipeTextareaInput("wake_prompt", "Wake prompt", "wake_payload.prompt", false, "Optional kickoff prompt for the web-facing advisor."),
			},
			Tags: []string{"advisor", "web", "chat", "product"},
		},
	}
}

type durableAgentRecipeCatalogFile struct {
	Recipes []DurableAgentRecipe `json:"recipes" yaml:"recipes"`
}

func loadDurableAgentRecipes(catalogPaths ...string) ([]DurableAgentRecipe, error) {
	builtins := builtinDurableAgentRecipes()
	merged := make(map[string]DurableAgentRecipe, len(builtins))
	for _, recipe := range builtins {
		merged[recipe.ID] = recipe
	}
	configured, err := loadConfiguredDurableAgentRecipes(catalogPaths...)
	if err != nil {
		return nil, err
	}
	for _, recipe := range configured {
		merged[recipe.ID] = recipe
	}
	out := make([]DurableAgentRecipe, 0, len(merged))
	for _, recipe := range merged {
		out = append(out, recipe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func loadConfiguredDurableAgentRecipes(catalogPaths ...string) ([]DurableAgentRecipe, error) {
	files, err := resolveDurableAgentRecipeCatalogFiles(catalogPaths)
	if err != nil {
		return nil, err
	}
	var out []DurableAgentRecipe
	seen := make(map[string]string)
	for _, path := range files {
		recipes, err := loadDurableAgentRecipeCatalogFile(path)
		if err != nil {
			return nil, err
		}
		for _, recipe := range recipes {
			if prior, ok := seen[recipe.ID]; ok {
				return nil, fmt.Errorf("%w %q in %s and %s", ErrDurableAgentRecipeDuplicateID, recipe.ID, prior, path)
			}
			seen[recipe.ID] = path
			out = append(out, recipe)
		}
	}
	return out, nil
}

func resolveDurableAgentRecipeCatalogFiles(paths []string) ([]string, error) {
	var out []string
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("durable agent recipe catalog %q: %w", path, err)
		}
		if !info.IsDir() {
			if isDurableAgentRecipeCatalogFile(path) {
				out = append(out, path)
			}
			continue
		}
		var dirFiles []string
		err = filepath.WalkDir(path, func(filePath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if isDurableAgentRecipeCatalogFile(filePath) {
				dirFiles = append(dirFiles, filePath)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("durable agent recipe catalog dir %q: %w", path, err)
		}
		sort.Strings(dirFiles)
		out = append(out, dirFiles...)
	}
	return out, nil
}

func loadDurableAgentRecipeCatalogFile(path string) ([]DurableAgentRecipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read durable agent recipe catalog %q: %w", path, err)
	}
	var recipes []DurableAgentRecipe
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		if err := json.Unmarshal(data, &recipes); err != nil {
			var catalog durableAgentRecipeCatalogFile
			if err2 := json.Unmarshal(data, &catalog); err2 != nil {
				return nil, fmt.Errorf("parse durable agent recipe catalog %q: %w", path, err)
			}
			recipes = catalog.Recipes
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &recipes); err != nil {
			var catalog durableAgentRecipeCatalogFile
			if err2 := yaml.Unmarshal(data, &catalog); err2 != nil {
				return nil, fmt.Errorf("parse durable agent recipe catalog %q: %w", path, err)
			}
			recipes = catalog.Recipes
		}
	default:
		return nil, fmt.Errorf("%w: unsupported catalog extension for %q", ErrDurableAgentRecipeInvalid, path)
	}
	for _, recipe := range recipes {
		if missing := validateDurableAgentRecipe(recipe); len(missing) > 0 {
			return nil, fmt.Errorf("%w %q: %s", ErrDurableAgentRecipeInvalid, recipe.ID, strings.Join(missing, ", "))
		}
	}
	return recipes, nil
}

func isDurableAgentRecipeCatalogFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func missingRecipeInputs(recipe DurableAgentRecipe, req DurableAgentRecipeRequest) []string {
	var missing []string
	for _, input := range recipe.Inputs {
		if !input.Required {
			continue
		}
		if recipeRequestValue(req, input.MapsTo) != "" {
			continue
		}
		if input.Default != nil {
			continue
		}
		name := input.MapsTo
		if name == "" {
			name = input.ID
		}
		missing = append(missing, name)
	}
	return missing
}

func recipeRequestValue(req DurableAgentRecipeRequest, key string) string {
	switch key {
	case "durable_agent.name":
		return req.Name
	case "durable_agent.slug":
		return req.Slug
	case "durable_agent.profile_id":
		return req.ProfileID
	case "durable_agent.provider":
		return req.Provider
	case "durable_agent.model":
		return req.Model
	case "durable_agent.runtime_kind":
		return req.RuntimeKind
	case "durable_agent.work_root":
		return req.WorkRoot
	case "project_id":
		return req.ProjectID
	case "wake_payload.reason":
		return req.WakePayload.Reason
	case "wake_payload.prompt":
		return req.WakePayload.Prompt
	default:
		if strings.HasPrefix(key, "metadata.") {
			return req.Metadata[strings.TrimPrefix(key, "metadata.")]
		}
		if strings.HasPrefix(key, "wake_payload.facts.") {
			return req.WakePayload.Facts[strings.TrimPrefix(key, "wake_payload.facts.")]
		}
		if strings.HasPrefix(key, "wake_payload.metadata.") {
			return req.WakePayload.Metadata[strings.TrimPrefix(key, "wake_payload.metadata.")]
		}
		return ""
	}
}

func isSupportedRecipeInputType(value string) bool {
	switch value {
	case DurableAgentRecipeInputTypeString,
		DurableAgentRecipeInputTypeTextarea,
		DurableAgentRecipeInputTypeBoolean,
		DurableAgentRecipeInputTypeSelect,
		DurableAgentRecipeInputTypePath,
		DurableAgentRecipeInputTypeProfile,
		DurableAgentRecipeInputTypeProvider,
		DurableAgentRecipeInputTypeModel,
		DurableAgentRecipeInputTypeRuntimeKind:
		return true
	default:
		return false
	}
}

func recipeStringInput(id, label, mapsTo string, required bool, placeholder string) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:          id,
		Label:       label,
		Type:        DurableAgentRecipeInputTypeString,
		Required:    required,
		Placeholder: placeholder,
		MapsTo:      mapsTo,
	}
}

func recipeTextareaInput(id, label, mapsTo string, required bool, help string) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:       id,
		Label:    label,
		Type:     DurableAgentRecipeInputTypeTextarea,
		Required: required,
		Help:     help,
		MapsTo:   mapsTo,
	}
}

func recipePathInput(id, label, mapsTo string, required bool, placeholder string) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:          id,
		Label:       label,
		Type:        DurableAgentRecipeInputTypePath,
		Required:    required,
		Placeholder: placeholder,
		MapsTo:      mapsTo,
	}
}

func recipeSelectInput(id, label, mapsTo string, required bool, defaultValue string, options []DurableAgentRecipeInputOption) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:       id,
		Label:    label,
		Type:     DurableAgentRecipeInputTypeSelect,
		Required: required,
		Default:  defaultValue,
		Options:  options,
		MapsTo:   mapsTo,
	}
}

func recipeProfileInput(required bool) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:       "profile_id",
		Label:    "Profile",
		Type:     DurableAgentRecipeInputTypeProfile,
		Required: required,
		Help:     "Select the durable-agent profile/persona to attach.",
		MapsTo:   "durable_agent.profile_id",
	}
}

func recipeProviderInput(required bool, defaultValue string) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:       "provider",
		Label:    "Provider",
		Type:     DurableAgentRecipeInputTypeProvider,
		Required: required,
		Default:  defaultValue,
		MapsTo:   "durable_agent.provider",
	}
}

func recipeModelInput(required bool, defaultValue string) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:       "model",
		Label:    "Model",
		Type:     DurableAgentRecipeInputTypeModel,
		Required: required,
		Default:  defaultValue,
		MapsTo:   "durable_agent.model",
	}
}

func recipeRuntimeKindInput(required bool, defaultValue string) DurableAgentRecipeInput {
	return DurableAgentRecipeInput{
		ID:       "runtime_kind",
		Label:    "Runtime kind",
		Type:     DurableAgentRecipeInputTypeRuntimeKind,
		Required: required,
		Default:  defaultValue,
		MapsTo:   "durable_agent.runtime_kind",
	}
}

func mergeWakePayload(base, override DurableAgentWakePayload) DurableAgentWakePayload {
	out := base
	if override.Reason != "" {
		out.Reason = override.Reason
	}
	if override.Prompt != "" {
		out.Prompt = override.Prompt
	}
	out.Facts = mergeStringMap(base.Facts, override.Facts)
	out.Metadata = mergeStringMap(base.Metadata, override.Metadata)
	return out
}

func mergeStringMap(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func recipeMetadataJSON(recipe DurableAgentRecipe, req DurableAgentRecipeRequest) string {
	metadata := map[string]string{
		"recipe_id":      recipe.ID,
		"recipe_kind":    recipe.Kind,
		"schema_version": fmt.Sprintf("%d", recipe.SchemaVersion),
	}
	for k, v := range recipe.Metadata {
		metadata[k] = v
	}
	for k, v := range req.Metadata {
		metadata[k] = v
	}
	return mustMarshalStringMap(metadata)
}

func mustMarshalStringMap(v map[string]string) string {
	if len(v) == 0 {
		return "{}"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func durableAgentRecipeSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
