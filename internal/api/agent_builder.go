package api

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
