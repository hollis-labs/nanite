package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentvalidation"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const agentBuilderSchemaVersion = 1

const (
	agentBuilderModeCreateProfile            = "create_profile"
	agentBuilderModeCreateProfileAndInstance = "create_profile_and_instance"
	agentBuilderModeUpdateProfile            = "update_profile"
)

type agentBuilderAdvisor interface {
	Draft(context.Context, AgentBuilderDraftRequest) (*AgentBuilderDraftResponse, error)
	Review(context.Context, AgentBuilderReviewRequest) (*AgentBuilderReviewResponse, error)
}

type deterministicAgentBuilderAdvisor struct{}

func (deterministicAgentBuilderAdvisor) Draft(_ context.Context, req AgentBuilderDraftRequest) (*AgentBuilderDraftResponse, error) {
	name := firstNonEmpty(req.Name, deriveBuilderName(req.IntakeText), "New Agent")
	slug := firstNonEmpty(req.Slug, slugify(name))
	lifecycle := firstNonEmpty(req.RequestedLifecycleClass, store.DurableAgentClassAdvisor)
	runtimeKind := firstNonEmpty(req.PreferredRuntimeKind, "api")
	provider := req.PreferredProvider
	model := req.PreferredModel
	workRoot := req.WorkRoot
	mode := agentBuilderModeCreateProfile
	createInstance := false
	if lifecycle == store.DurableAgentClassProcess || lifecycle == store.DurableAgentClassHarness || workRoot != "" {
		mode = agentBuilderModeCreateProfileAndInstance
		createInstance = true
	}
	draft := AgentBuilderDraftEnvelope{
		Mode: mode,
		Profile: AgentBuilderProfileInput{
			Name:            name,
			Slug:            slug,
			SystemPrompt:    fmt.Sprintf("You are %s. %s", name, strings.TrimSpace(req.IntakeText)),
			Description:     firstNonEmpty(req.Description, summarizeBuilderIntake(req.IntakeText)),
			Tags:            "[]",
			Tools:           "[]",
			MCPServers:      "[]",
			ToolPermissions: "{}",
			Settings:        "{}",
			Directories:     "[]",
			Constraints:     "{}",
			RoleTools:       "[]",
			RoleSkills:      "[]",
			ContextPolicy:   "{}",
			// store.DefaultActivationModeForClass, not a hardcoded
			// "singleton" -- a lifecycle=process/template draft that
			// always pre-filled "singleton" here would let an operator
			// unknowingly submit a new durable agent straight into the
			// CW-20260817 "wakeable exactly once" bug this task's
			// migration 117 exists to close (see agents.go's
			// DefaultActivationModeForClass doc comment). The free-text
			// field in AgentBuilderWizard.tsx still lets the operator
			// override this suggestion before submit.
			ActivationMode: store.DefaultActivationModeForClass(lifecycle),
			Class:          lifecycle,
			DefaultState:   "sleeping",
			Source:         "user",
		},
		Capabilities: AgentBuilderCapabilitiesInput{},
		DurableInstance: AgentBuilderDurableInstanceInput{
			Create:         createInstance,
			LifecycleClass: lifecycle,
			Provider:       provider,
			Model:          model,
			RuntimeKind:    runtimeKind,
			WorkRoot:       workRoot,
		},
		OperatorNotification: AgentBuilderOperatorNotificationInput{
			TargetKind:   "operator",
			TargetID:     "current-user",
			IncludeLinks: true,
		},
	}
	var questions []string
	if strings.TrimSpace(req.IntakeText) == "" {
		questions = append(questions, "What job should this agent own?")
	}
	if createInstance && strings.TrimSpace(workRoot) == "" {
		questions = append(questions, "Which work root should the durable instance use?")
	}
	if provider == "" || model == "" {
		questions = append(questions, "Which provider and model should this agent default to?")
	}
	return &AgentBuilderDraftResponse{
		SchemaVersion:       agentBuilderSchemaVersion,
		Draft:               draft,
		Questions:           questions,
		Warnings:            draftWarningsForProfile(draft.Profile),
		UnsupportedRequests: []string{},
		Confidence:          confidenceScore(len(questions)),
	}, nil
}

func (deterministicAgentBuilderAdvisor) Review(_ context.Context, req AgentBuilderReviewRequest) (*AgentBuilderReviewResponse, error) {
	questions := make([]string, 0, 4)
	warnings := make([]string, 0, 4)
	patches := make([]AgentBuilderPatchOperation, 0, 6)
	if strings.TrimSpace(req.CurrentDraft.Profile.Name) == "" {
		questions = append(questions, "Add a profile name before submit.")
		patches = append(patches, AgentBuilderPatchOperation{
			Op:   "replace",
			Path: "/profile/name",
			Note: "A stable operator-facing name is required.",
		})
	}
	if strings.TrimSpace(req.CurrentDraft.Profile.Slug) == "" {
		questions = append(questions, "Add a slug before submit.")
		patches = append(patches, AgentBuilderPatchOperation{
			Op:   "replace",
			Path: "/profile/slug",
			Note: "Use a lowercase slug for deterministic profile routes.",
		})
	}
	if strings.TrimSpace(req.CurrentDraft.Profile.SystemPrompt) == "" {
		questions = append(questions, "Add a system prompt before submit.")
		patches = append(patches, AgentBuilderPatchOperation{
			Op:   "replace",
			Path: "/profile/system_prompt",
			Note: "The wizard only drafts config; deterministic submit still needs a prompt body.",
		})
	}
	if req.CurrentDraft.DurableInstance.Create && strings.TrimSpace(req.CurrentDraft.DurableInstance.WorkRoot) == "" {
		warnings = append(warnings, "Durable instance creation without a work root will not pass dry-run.")
		patches = append(patches, AgentBuilderPatchOperation{
			Op:   "replace",
			Path: "/durable_instance/work_root",
			Note: "Provide a concrete workspace or working directory.",
		})
	}
	return &AgentBuilderReviewResponse{
		SchemaVersion:            agentBuilderSchemaVersion,
		Accepted:                 len(questions) == 0,
		Questions:                questions,
		Warnings:                 warnings,
		SuggestedPatchOperations: patches,
		MaxRoundsRecommended:     3,
	}, nil
}

func (a *API) SetAgentBuilderAdvisor(builder agentBuilderAdvisor) {
	a.agentBuilder = builder
}

func (a *API) handleAgentBuilderDryRun(w http.ResponseWriter, r *http.Request) {
	var req AgentBuilderDryRunRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	resp, err := a.agentBuilderDryRun(r.Context(), req)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, resp)
}

func (a *API) handleAgentBuilderDraft(w http.ResponseWriter, r *http.Request) {
	var req AgentBuilderDraftRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SchemaVersion == 0 {
		req.SchemaVersion = agentBuilderSchemaVersion
	}
	if req.SchemaVersion != agentBuilderSchemaVersion {
		a.errorResp(w, http.StatusBadRequest, fmt.Sprintf("unsupported schema_version %d", req.SchemaVersion))
		return
	}
	resp, err := a.agentBuilder.Draft(r.Context(), req)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, resp)
}

func (a *API) handleAgentBuilderReview(w http.ResponseWriter, r *http.Request) {
	var req AgentBuilderReviewRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SchemaVersion == 0 {
		req.SchemaVersion = agentBuilderSchemaVersion
	}
	if req.SchemaVersion != agentBuilderSchemaVersion {
		a.errorResp(w, http.StatusBadRequest, fmt.Sprintf("unsupported schema_version %d", req.SchemaVersion))
		return
	}
	resp, err := a.agentBuilder.Review(r.Context(), req)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, resp)
}

func (a *API) agentBuilderDryRun(ctx context.Context, req AgentBuilderDryRunRequest) (*AgentBuilderDryRunResponse, error) {
	if req.SchemaVersion == 0 {
		req.SchemaVersion = agentBuilderSchemaVersion
	}
	if req.SchemaVersion != agentBuilderSchemaVersion {
		return nil, fmt.Errorf("unsupported schema_version %d", req.SchemaVersion)
	}
	if !validAgentBuilderMode(req.Mode) {
		return nil, fmt.Errorf("unsupported mode %q", req.Mode)
	}

	normalized, baseAgent, errors := a.normalizeAgentBuilderProfile(req)
	warnings := draftWarningsForProfile(normalized)
	unsupported := unsupportedAgentBuilderFields(req)

	validation := agentvalidation.ValidateAgentConfig(baseAgent)
	errors = append(errors, validation.Errors...)
	warnings = append(warnings, validation.Warnings...)

	ops := capabilityOperations(req.Capabilities, req.Mode)
	var recipePlan *service.DurableAgentRecipePlan
	var launchPreview *AgentBuilderLaunchPlanPreview

	if req.Mode == agentBuilderModeCreateProfileAndInstance || req.DurableInstance.Create {
		if req.DurableInstance.RecipeID != "" {
			plan, err := a.Services.DurableAgentRecipes.DryRun(ctx, req.DurableInstance.RecipeID, service.DurableAgentRecipeRequest{
				Name:        firstNonEmpty(req.Profile.Name, req.DurableInstance.Metadata["name"]),
				Slug:        req.Profile.Slug,
				ProfileID:   normalized.ID,
				Provider:    req.DurableInstance.Provider,
				Model:       req.DurableInstance.Model,
				RuntimeKind: req.DurableInstance.RuntimeKind,
				WorkRoot:    req.DurableInstance.WorkRoot,
				WorkspaceID: req.DurableInstance.WorkspaceID,
				ProjectID:   req.DurableInstance.ProjectID,
				WakePayload: durableWakePayloadForDryRun(req),
				Metadata:    req.DurableInstance.Metadata,
				Start:       req.DurableInstance.Start,
			})
			if err != nil {
				errors = append(errors, err.Error())
			} else {
				recipePlan = plan
				warnings = append(warnings, plan.MissingRequirements...)
				warnings = append(warnings, plan.Unsupported...)
			}
		} else {
			launchPreview = buildAgentBuilderLaunchPlanPreview(req)
			if req.DurableInstance.Start && strings.TrimSpace(req.DurableInstance.WorkspaceID) == "" {
				errors = append(errors, "workspace_id is required when dry-run includes a launch/start preview")
			}
		}
	}

	notification := buildReadyNotificationPreview(req, normalized)
	return &AgentBuilderDryRunResponse{
		SchemaVersion:            agentBuilderSchemaVersion,
		Valid:                    len(errors) == 0,
		Errors:                   dedupeStrings(errors),
		Warnings:                 dedupeStrings(warnings),
		UnsupportedFields:        dedupeStrings(unsupported),
		NormalizedProfilePayload: normalized,
		CapabilityOperations:     ops,
		DurableRecipePlan:        recipePlan,
		LaunchPlanPreview:        launchPreview,
		NotificationPreview:      notification,
	}, nil
}

func (a *API) normalizeAgentBuilderProfile(req AgentBuilderDryRunRequest) (AgentBuilderProfileInput, *store.AgentProfile, []string) {
	errors := make([]string, 0, 6)
	input := req.Profile
	var existing *store.AgentProfile
	var err error

	if req.Mode == agentBuilderModeUpdateProfile {
		if strings.TrimSpace(input.ID) == "" {
			errors = append(errors, "profile.id is required for update_profile dry-run")
		} else {
			existing, err = a.Services.Store.GetAgent(input.ID)
			if err != nil {
				errors = append(errors, fmt.Sprintf("profile %q not found", input.ID))
			} else if existing.Source == "internal" {
				errors = append(errors, "internal/file-SOT profiles remain read-only through Agent Builder dry-run")
			}
		}
	}

	normalized := input
	if existing != nil {
		normalized = mergeBuilderProfileInput(existing, input)
	}
	if strings.TrimSpace(normalized.Source) == "" {
		normalized.Source = "user"
	}
	baseAgent := builderProfileToStoreAgent(normalized, existing)
	baseAgent.ID = normalized.ID
	if baseAgent.ID == "" && existing != nil {
		baseAgent.ID = existing.ID
	}

	if req.Mode != agentBuilderModeUpdateProfile || existing == nil {
		if strings.TrimSpace(baseAgent.Name) == "" {
			errors = append(errors, "profile.name is required")
		}
		if strings.TrimSpace(baseAgent.Slug) == "" {
			errors = append(errors, "profile.slug is required")
		}
		if strings.TrimSpace(baseAgent.SystemPrompt) == "" {
			errors = append(errors, "profile.system_prompt is required")
		}
	}
	normalized.ID = baseAgent.ID
	normalized.ActivationMode = baseAgent.ActivationMode
	normalized.Class = baseAgent.Class
	normalized.DefaultState = baseAgent.DefaultState
	normalized.ContextPolicy = baseAgent.ContextPolicy
	normalized.RoleTools = baseAgent.RoleTools
	normalized.RoleSkills = baseAgent.RoleSkills
	normalized.Source = baseAgent.Source
	if existing != nil {
		normalized.CanExecute = boolPtr(baseAgent.CanExecute)
		normalized.Durable = boolPtr(baseAgent.Durable)
	}
	return normalized, baseAgent, errors
}

func mergeBuilderProfileInput(existing *store.AgentProfile, input AgentBuilderProfileInput) AgentBuilderProfileInput {
	merged := AgentBuilderProfileInput{
		ID:                      existing.ID,
		Name:                    existing.Name,
		Slug:                    existing.Slug,
		Avatar:                  existing.Avatar,
		Icon:                    existing.Icon,
		SystemPrompt:            existing.SystemPrompt,
		Description:             existing.Description,
		DefaultModel:            existing.DefaultModel,
		MCPServers:              existing.MCPServers,
		ToolPermissions:         existing.ToolPermissions,
		Settings:                existing.Settings,
		Tools:                   existing.Tools,
		Directories:             existing.Directories,
		Constraints:             existing.Constraints,
		Tags:                    existing.Tags,
		Status:                  existing.Status,
		Source:                  existing.Source,
		SourceRef:               existing.SourceRef,
		ParentDispatchAllowlist: existing.ParentDispatchAllowlist,
		RoleTools:               existing.RoleTools,
		RoleSkills:              existing.RoleSkills,
		ContextPolicy:           existing.ContextPolicy,
		ActivationMode:          existing.ActivationMode,
		Class:                   existing.Class,
		DefaultState:            existing.DefaultState,
		CanExecute:              boolPtr(existing.CanExecute),
		Durable:                 boolPtr(existing.Durable),
	}
	if input.Name != "" {
		merged.Name = input.Name
	}
	if input.Slug != "" {
		merged.Slug = input.Slug
	}
	if input.Avatar != "" {
		merged.Avatar = input.Avatar
	}
	if input.Icon != "" {
		merged.Icon = input.Icon
	}
	if input.SystemPrompt != "" {
		merged.SystemPrompt = input.SystemPrompt
	}
	if input.Description != "" {
		merged.Description = input.Description
	}
	if input.DefaultModel != "" {
		merged.DefaultModel = input.DefaultModel
	}
	if input.MCPServers != "" {
		merged.MCPServers = input.MCPServers
	}
	if input.ToolPermissions != "" {
		merged.ToolPermissions = input.ToolPermissions
	}
	if input.Settings != "" {
		merged.Settings = input.Settings
	}
	if input.Tools != "" {
		merged.Tools = input.Tools
	}
	if input.Directories != "" {
		merged.Directories = input.Directories
	}
	if input.Constraints != "" {
		merged.Constraints = input.Constraints
	}
	if input.Tags != "" {
		merged.Tags = input.Tags
	}
	if input.Status != "" {
		merged.Status = input.Status
	}
	if input.Source != "" {
		merged.Source = input.Source
	}
	if input.SourceRef != "" {
		merged.SourceRef = input.SourceRef
	}
	if input.ParentDispatchAllowlist != "" {
		merged.ParentDispatchAllowlist = input.ParentDispatchAllowlist
	}
	if input.RoleTools != "" {
		merged.RoleTools = input.RoleTools
	}
	if input.RoleSkills != "" {
		merged.RoleSkills = input.RoleSkills
	}
	if input.ContextPolicy != "" {
		merged.ContextPolicy = input.ContextPolicy
	}
	if input.ActivationMode != "" {
		merged.ActivationMode = input.ActivationMode
	}
	if input.Class != "" {
		merged.Class = input.Class
	}
	if input.DefaultState != "" {
		merged.DefaultState = input.DefaultState
	}
	if input.CanExecute != nil {
		merged.CanExecute = input.CanExecute
	}
	if input.Durable != nil {
		merged.Durable = input.Durable
	}
	return merged
}

func builderProfileToStoreAgent(input AgentBuilderProfileInput, existing *store.AgentProfile) *store.AgentProfile {
	agent := &store.AgentProfile{
		ID:                      input.ID,
		Name:                    input.Name,
		Slug:                    input.Slug,
		Avatar:                  input.Avatar,
		Icon:                    input.Icon,
		SystemPrompt:            input.SystemPrompt,
		Description:             input.Description,
		DefaultModel:            input.DefaultModel,
		MCPServers:              firstNonEmpty(input.MCPServers, "[]"),
		ToolPermissions:         firstNonEmpty(input.ToolPermissions, "{}"),
		Settings:                firstNonEmpty(input.Settings, "{}"),
		Tools:                   firstNonEmpty(input.Tools, "[]"),
		Directories:             firstNonEmpty(input.Directories, "[]"),
		Constraints:             firstNonEmpty(input.Constraints, "{}"),
		Tags:                    firstNonEmpty(input.Tags, "[]"),
		Status:                  firstNonEmpty(input.Status, "active"),
		Source:                  firstNonEmpty(input.Source, "user"),
		SourceRef:               input.SourceRef,
		ParentDispatchAllowlist: firstNonEmpty(input.ParentDispatchAllowlist, "[]"),
		RoleTools:               firstNonEmpty(input.RoleTools, "[]"),
		RoleSkills:              firstNonEmpty(input.RoleSkills, "[]"),
		ContextPolicy:           firstNonEmpty(input.ContextPolicy, "{}"),
		ActivationMode:          input.ActivationMode,
		Class:                   input.Class,
		DefaultState:            input.DefaultState,
	}
	if input.CanExecute != nil {
		agent.CanExecute = *input.CanExecute
	} else if existing != nil {
		agent.CanExecute = existing.CanExecute
	}
	if input.Durable != nil {
		agent.Durable = *input.Durable
	} else if existing != nil {
		agent.Durable = existing.Durable
	}
	if existing != nil {
		agent.AgentHash = existing.AgentHash
		agent.Version = existing.Version
		agent.Kind = existing.Kind
		agent.CapabilitiesJSON = existing.CapabilitiesJSON
		agent.LimitsJSON = existing.LimitsJSON
		agent.ModelStrategy = existing.ModelStrategy
		agent.ImportedAt = existing.ImportedAt
		agent.OriginSystem = existing.OriginSystem
		agent.Format = existing.Format
		agent.URN = existing.URN
		agent.URNAliases = existing.URNAliases
	}
	return agent
}

func capabilityOperations(c AgentBuilderCapabilitiesInput, mode string) []AgentBuilderCapabilityOperation {
	ops := make([]AgentBuilderCapabilityOperation, 0, 8)
	if n := len(c.AssignedSkillIDs) + len(c.AssignedSkillSlugs); n > 0 {
		ops = append(ops, AgentBuilderCapabilityOperation{
			Area:   "skills",
			Action: "assign",
			Target: mode,
			Count:  n,
		})
	}
	if n := len(c.PromptTemplateIDs); n > 0 {
		ops = append(ops, AgentBuilderCapabilityOperation{
			Area:   "prompt_templates",
			Action: "assign",
			Target: mode,
			Count:  n,
		})
	}
	if n := len(c.KnownTools); n > 0 {
		ops = append(ops, AgentBuilderCapabilityOperation{
			Area:   "known_tools",
			Action: "upsert",
			Target: mode,
			Count:  n,
		})
	}
	if n := len(c.KnownSkills); n > 0 {
		ops = append(ops, AgentBuilderCapabilityOperation{
			Area:   "known_skills",
			Action: "upsert",
			Target: mode,
			Count:  n,
		})
	}
	if n := len(c.Procedures); n > 0 {
		ops = append(ops, AgentBuilderCapabilityOperation{
			Area:   "procedures",
			Action: "upsert",
			Target: mode,
			Count:  n,
		})
	}
	if n := len(c.KnowledgeSeeds); n > 0 {
		ops = append(ops, AgentBuilderCapabilityOperation{
			Area:   "knowledge_seeds",
			Action: "upsert",
			Target: mode,
			Count:  n,
		})
	}
	return ops
}

func buildAgentBuilderLaunchPlanPreview(req AgentBuilderDryRunRequest) *AgentBuilderLaunchPlanPreview {
	lifecycle := firstNonEmpty(req.DurableInstance.LifecycleClass, req.Profile.Class, store.DurableAgentClassAdvisor)
	sessionPolicy := service.DurableAgentSessionPolicyReuseLatestOrCreate
	relation := store.DurableAgentSessionRelationPrimary
	switch lifecycle {
	case store.DurableAgentClassProcess:
		sessionPolicy = service.DurableAgentSessionPolicyFreshPerWake
		relation = store.DurableAgentSessionRelationWake
	case store.DurableAgentClassTemplate:
		sessionPolicy = service.DurableAgentSessionPolicyFreshOneShot
		relation = store.DurableAgentSessionRelationRun
	case store.DurableAgentClassHarness:
		sessionPolicy = service.DurableAgentSessionPolicyReuseManaged
		relation = store.DurableAgentSessionRelationHarness
	}
	return &AgentBuilderLaunchPlanPreview{
		LifecycleClass:     lifecycle,
		SessionPolicy:      sessionPolicy,
		AttachmentRelation: relation,
		Provider:           req.DurableInstance.Provider,
		Model:              req.DurableInstance.Model,
		RuntimeKind:        req.DurableInstance.RuntimeKind,
		WorkRoot:           req.DurableInstance.WorkRoot,
		WouldCreateSession: req.DurableInstance.Start,
		WakePayload: DurableAgentWakePayloadRequest{
			Reason:   firstNonEmpty(req.DurableInstance.Metadata["wake_reason"], service.DurableAgentWakeManual),
			Prompt:   req.DurableInstance.Metadata["wake_prompt"],
			Facts:    map[string]string{},
			Metadata: map[string]string{},
		},
	}
}

func buildReadyNotificationPreview(req AgentBuilderDryRunRequest, normalized AgentBuilderProfileInput) AgentBuilderReadyNotificationPreview {
	profileID := normalized.ID
	durableID := ""
	sessionID := ""
	if req.Mode != agentBuilderModeUpdateProfile && profileID == "" {
		profileID = "preview-profile"
	}
	if req.DurableInstance.Create || req.Mode == agentBuilderModeCreateProfileAndInstance {
		durableID = "preview-durable-instance"
		if req.DurableInstance.Start {
			sessionID = "preview-session"
		}
	}
	links := []AgentBuilderDeepLink{}
	if req.OperatorNotification.IncludeLinks {
		links = append(links, AgentBuilderDeepLink{
			Kind:  "agent_profile",
			Path:  "/settings/ai/agents/" + firstNonEmpty(profileID, normalized.Slug),
			Label: "Open agent profile",
		})
		if durableID != "" {
			links = append(links, AgentBuilderDeepLink{
				Kind:  "durable_agent",
				Path:  "/settings/ai/durable-agents/" + durableID,
				Label: "Open durable instance",
			})
		}
		if sessionID != "" {
			links = append(links, AgentBuilderDeepLink{
				Kind:  "chat_session",
				Path:  "/chat/" + sessionID,
				Label: "Open launched session",
			})
		}
	}
	return AgentBuilderReadyNotificationPreview{
		TargetKind: firstNonEmpty(req.OperatorNotification.TargetKind, "operator"),
		TargetID:   firstNonEmpty(req.OperatorNotification.TargetID, "current-user"),
		Profile: AgentBuilderNotificationResource{
			ID:   profileID,
			Name: normalized.Name,
			Slug: normalized.Slug,
		},
		DurableInstance: AgentBuilderNotificationResource{
			ID:   durableID,
			Name: normalized.Name,
			Slug: slugify(normalized.Name),
		},
		Session: AgentBuilderNotificationResource{
			ID:   sessionID,
			Name: firstNonEmpty(normalized.Name, "Agent session"),
			Slug: sessionID,
		},
		Links:     links,
		Warnings:  []string{},
		FollowUps: []string{"Review the dry-run output before deterministic submit."},
	}
}

func draftWarningsForProfile(profile AgentBuilderProfileInput) []string {
	warnings := make([]string, 0, 3)
	if strings.TrimSpace(profile.ContextPolicy) == "{}" {
		warnings = append(warnings, "context_policy is still defaulted; refine if the agent needs aggressive pruning or resumability rules.")
	}
	if strings.TrimSpace(profile.RoleTools) == "[]" && strings.TrimSpace(profile.RoleSkills) == "[]" {
		warnings = append(warnings, "role_tools and role_skills are empty; the Builder draft has not proposed seeded capability defaults.")
	}
	return warnings
}

func unsupportedAgentBuilderFields(req AgentBuilderDryRunRequest) []string {
	unsupported := []string{}
	if len(req.Capabilities.ReflexSuggestions) > 0 {
		unsupported = append(unsupported, "capabilities.reflex_suggestions")
	}
	return unsupported
}

func durableWakePayloadForDryRun(req AgentBuilderDryRunRequest) service.DurableAgentWakePayload {
	return service.DurableAgentWakePayload{
		Reason: firstNonEmpty(req.DurableInstance.Metadata["wake_reason"], service.DurableAgentWakeManual),
	}
}

func validAgentBuilderMode(mode string) bool {
	switch mode {
	case agentBuilderModeCreateProfile, agentBuilderModeCreateProfileAndInstance, agentBuilderModeUpdateProfile:
		return true
	default:
		return false
	}
}

func deriveBuilderName(intake string) string {
	for _, sentence := range strings.FieldsFunc(intake, func(r rune) bool { return r == '.' || r == '\n' }) {
		candidate := strings.TrimSpace(sentence)
		if candidate != "" {
			return truncateWords(candidate, 4)
		}
	}
	return ""
}

func summarizeBuilderIntake(intake string) string {
	return truncateWords(strings.TrimSpace(intake), 18)
}

func truncateWords(value string, limit int) string {
	words := strings.Fields(value)
	if len(words) <= limit {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:limit], " ")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "agent-builder-draft"
	}
	return s
}

func confidenceScore(questionCount int) float64 {
	switch questionCount {
	case 0:
		return 0.9
	case 1:
		return 0.7
	case 2:
		return 0.55
	default:
		return 0.4
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
