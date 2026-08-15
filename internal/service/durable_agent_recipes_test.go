package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func newRecipeServiceForTest(t *testing.T, agents DurableAgentService, paths ...string) DurableAgentRecipeService {
	t.Helper()
	svc, err := NewDurableAgentRecipeService(agents, paths...)
	if err != nil {
		t.Fatalf("NewDurableAgentRecipeService: %v", err)
	}
	return svc
}

func TestDurableAgentRecipeCatalogListGetDeterministic(t *testing.T) {
	svc := newRecipeServiceForTest(t, nil)
	recipes, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantIDs := []string{
		"architect-advisor",
		"external-company-agent",
		"managed-cli-harness",
		"planner",
		"process-monitor",
		"project-advisor",
		"project-manager",
		"proxima-relay",
		"system-monitor",
		"task-writer",
		"template-worker",
		"web-chat-agent",
	}
	if len(recipes) != len(wantIDs) {
		t.Fatalf("recipe count = %d, want %d", len(recipes), len(wantIDs))
	}
	for i, want := range wantIDs {
		if recipes[i].ID != want || recipes[i].SchemaVersion != DurableAgentRecipeSchemaVersion {
			t.Fatalf("recipes[%d] = %+v, want id %q schema %d", i, recipes[i], want, DurableAgentRecipeSchemaVersion)
		}
		if len(recipes[i].Inputs) == 0 {
			t.Fatalf("recipes[%d] missing operator inputs: %+v", i, recipes[i])
		}
	}
	got, err := svc.Get(context.Background(), "project-advisor")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LifecycleClass != store.DurableAgentClassAdvisor {
		t.Fatalf("project-advisor = %+v", got)
	}
	if got.Inputs[0].ID != "name" {
		t.Fatalf("project-advisor inputs = %+v", got.Inputs)
	}
	if _, err := svc.Get(context.Background(), "web-chat-agent"); err != nil {
		t.Fatalf("Get web-chat-agent: %v", err)
	}
}

func TestDurableAgentRecipeCatalogLoadJSONOverrideBuiltIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipes.json")
	if err := os.WriteFile(path, []byte(`[
		{
			"id": "project-advisor",
			"schema_version": 1,
			"kind": "project_advisor",
			"name": "Catalog Advisor",
			"description": "Configured override",
			"lifecycle_class": "advisor",
			"profile_rule": "operator_selected",
			"provider": "openai",
			"model": "gpt-4.1",
			"runtime_kind": "api",
			"launch_source_type": "durable_advisor",
			"wake_defaults": {"reason": "manual"},
			"inputs": [
				{"id": "name", "label": "Name", "type": "string", "required": true, "maps_to": "durable_agent.name"}
			]
		}
	]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newRecipeServiceForTest(t, nil, path)
	got, err := svc.Get(context.Background(), "project-advisor")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Catalog Advisor" || got.Provider != "openai" {
		t.Fatalf("override recipe = %+v", got)
	}
}

func TestDurableAgentRecipeCatalogLoadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipes.yaml")
	if err := os.WriteFile(path, []byte(`
recipes:
  - id: architect-proxima
    schema_version: 1
    kind: project_advisor
    name: Architect Proxima
    description: Durable architect agent
    lifecycle_class: advisor
    profile_rule: operator_selected
    provider: anthropic
    model: claude-sonnet-4
    runtime_kind: api
    launch_source_type: durable_advisor
    wake_defaults:
      reason: manual
    inputs:
      - id: name
        label: Name
        type: string
        required: true
        maps_to: durable_agent.name
      - id: provider
        label: Provider
        type: provider
        default: anthropic
        maps_to: durable_agent.provider
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := newRecipeServiceForTest(t, nil, dir)
	got, err := svc.Get(context.Background(), "architect-proxima")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Architect Proxima" || len(got.Inputs) != 2 {
		t.Fatalf("yaml recipe = %+v", got)
	}
}

func TestDurableAgentRecipeCatalogRejectsInvalidConfiguredCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(path, []byte(`
recipes:
  - id: broken
    schema_version: 1
    kind: project_advisor
    name: Broken
    lifecycle_class: advisor
    provider: anthropic
    model: claude-sonnet-4
    runtime_kind: api
    launch_source_type: durable_advisor
    wake_defaults:
      reason: manual
    inputs:
      - id: mode
        label: Mode
        type: select
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := NewDurableAgentRecipeService(nil, path)
	if err == nil || !errors.Is(err, ErrDurableAgentRecipeInvalid) {
		t.Fatalf("err = %v, want ErrDurableAgentRecipeInvalid", err)
	}
}

func TestDurableAgentRecipeCatalogRejectsDuplicateConfiguredIDs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`[
		{"id":"web-chat-agent","schema_version":1,"kind":"project_advisor","name":"A","description":"A","lifecycle_class":"advisor","provider":"anthropic","model":"claude-sonnet-4","runtime_kind":"api","launch_source_type":"durable_advisor","wake_defaults":{"reason":"manual"},"inputs":[{"id":"name","label":"Name","type":"string","required":true,"maps_to":"durable_agent.name"}]}
	]`), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(`[
		{"id":"web-chat-agent","schema_version":1,"kind":"project_advisor","name":"B","description":"B","lifecycle_class":"advisor","provider":"anthropic","model":"claude-sonnet-4","runtime_kind":"api","launch_source_type":"durable_advisor","wake_defaults":{"reason":"manual"},"inputs":[{"id":"name","label":"Name","type":"string","required":true,"maps_to":"durable_agent.name"}]}
	]`), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}
	_, err := NewDurableAgentRecipeService(nil, dir)
	if err == nil || !errors.Is(err, ErrDurableAgentRecipeDuplicateID) {
		t.Fatalf("err = %v, want ErrDurableAgentRecipeDuplicateID", err)
	}
}

func TestDurableAgentRecipeDryRunCompilesWithoutMutation(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Recipe Agent", Slug: "recipe-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	recipeSvc := newRecipeServiceForTest(t, NewDurableAgentService(st))

	plan, err := recipeSvc.DryRun(context.Background(), "project-advisor", DurableAgentRecipeRequest{
		Name:      "Advisor Instance",
		Slug:      "advisor-instance",
		ProfileID: profile.ID,
		WorkRoot:  "/tmp/advisor",
		Metadata: map[string]string{
			"scope_topic": "agridd",
		},
		WakePayload: DurableAgentWakePayload{
			Facts: map[string]string{"project": "agridd"},
		},
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !plan.Ready || plan.Instance.ProfileID != profile.ID || plan.Instance.WorkRoot != "/tmp/advisor" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.LaunchPolicy.SessionPolicy != DurableAgentSessionPolicyReuseLatestOrCreate {
		t.Fatalf("launch policy = %+v", plan.LaunchPolicy)
	}
	if plan.WakePayload.Facts["project"] != "agridd" {
		t.Fatalf("wake payload = %+v", plan.WakePayload)
	}
	instances, err := st.ListDurableAgentInstances(true)
	if err != nil {
		t.Fatalf("ListDurableAgentInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("dry-run mutated store: %+v", instances)
	}
}

func TestDurableAgentRecipeDryRun_ProductShapes(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Recipe Product Agent", Slug: "recipe-product-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	recipeSvc := newRecipeServiceForTest(t, NewDurableAgentService(st))

	systemPlan, err := recipeSvc.DryRun(context.Background(), "system-monitor", DurableAgentRecipeRequest{
		Name:      "API Monitor",
		Slug:      "api-monitor",
		ProfileID: profile.ID,
		Metadata: map[string]string{
			"system_name":   "nanite api",
			"target_path":   "http://localhost:8090/health",
			"schedule_hint": "cron: */15 * * * *",
		},
	})
	if err != nil {
		t.Fatalf("DryRun system-monitor: %v", err)
	}
	if !systemPlan.Ready || systemPlan.Instance.LifecycleClass != store.DurableAgentClassProcess {
		t.Fatalf("systemPlan = %+v", systemPlan)
	}
	if systemPlan.LaunchPolicy.SessionPolicy != DurableAgentSessionPolicyFreshPerWake ||
		systemPlan.WakePayload.Reason != DurableAgentWakeScheduled {
		t.Fatalf("system monitor launch/wake = %+v %+v", systemPlan.LaunchPolicy, systemPlan.WakePayload)
	}

	taskPlan, err := recipeSvc.DryRun(context.Background(), "task-writer", DurableAgentRecipeRequest{
		Name:      "Task Writer",
		Slug:      "task-writer-one",
		ProfileID: profile.ID,
		Metadata: map[string]string{
			"task_domain":  "runtime migration",
			"output_style": "implementation_plan",
		},
	})
	if err != nil {
		t.Fatalf("DryRun task-writer: %v", err)
	}
	if !taskPlan.Ready || taskPlan.LaunchPolicy.SessionPolicy != DurableAgentSessionPolicyFreshOneShot {
		t.Fatalf("taskPlan = %+v", taskPlan)
	}

	webPlan, err := recipeSvc.DryRun(context.Background(), "web-chat-agent", DurableAgentRecipeRequest{
		Name:      "Web Support",
		Slug:      "web-support",
		ProfileID: profile.ID,
		Metadata: map[string]string{
			"public_display_name": "Support Concierge",
			"persona_scope":       "help users navigate the product",
		},
	})
	if err != nil {
		t.Fatalf("DryRun web-chat-agent: %v", err)
	}
	if !strings.Contains(webPlan.Instance.MetadataJSON, `"exposure_surface":"harness_v1"`) ||
		!strings.Contains(webPlan.Instance.MetadataJSON, `"integration_status":"web_widget_operator_managed"`) {
		t.Fatalf("web metadata = %s", webPlan.Instance.MetadataJSON)
	}

	externalPlan, err := recipeSvc.DryRun(context.Background(), "external-company-agent", DurableAgentRecipeRequest{
		Name:      "Partner Endpoint",
		Slug:      "partner-endpoint",
		ProfileID: profile.ID,
		Metadata: map[string]string{
			"company_name":     "Partner",
			"endpoint_purpose": "accept support escalations",
		},
	})
	if err != nil {
		t.Fatalf("DryRun external-company-agent: %v", err)
	}
	if !strings.Contains(externalPlan.Instance.MetadataJSON, `"adapter_status":"recipe_prepares_agent_only"`) ||
		!strings.Contains(externalPlan.Instance.MetadataJSON, `"permissions_posture":"review_required"`) {
		t.Fatalf("external metadata = %s", externalPlan.Instance.MetadataJSON)
	}
}

func TestDurableAgentRecipeApplyCreatesInstance(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Apply Agent", Slug: "apply-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	recipeSvc := newRecipeServiceForTest(t, NewDurableAgentService(st))

	result, err := recipeSvc.Apply(context.Background(), "template-worker", DurableAgentRecipeRequest{
		Name:      "Review Worker",
		Slug:      "review-worker",
		ProfileID: profile.ID,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Instance == nil || result.Instance.LifecycleClass != store.DurableAgentClassTemplate {
		t.Fatalf("result = %+v", result)
	}
	if result.LaunchResult != nil {
		t.Fatalf("apply without start launched: %+v", result.LaunchResult)
	}
	got, err := st.GetDurableAgentInstance(result.Instance.ID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstance: %v", err)
	}
	if got.Slug != "review-worker" || got.Provider != "anthropic" {
		t.Fatalf("stored instance = %+v", got)
	}
}

func TestDurableAgentRecipeApplyWithStartAttachesSession(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Started Recipe Agent", Slug: "started-recipe-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-a", Name: "Workspace A"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	durableSvc := NewDurableAgentService(st)
	recipeSvc := newRecipeServiceForTest(t, durableSvc)

	result, err := recipeSvc.Apply(context.Background(), "process-monitor", DurableAgentRecipeRequest{
		Name:        "Monitor",
		Slug:        "monitor",
		ProfileID:   profile.ID,
		WorkspaceID: "workspace-a",
		Start:       true,
	})
	if err != nil {
		t.Fatalf("Apply start: %v", err)
	}
	if result.LaunchResult == nil || result.LaunchResult.Session == nil || !result.LaunchResult.CreatedSession {
		t.Fatalf("launch result = %+v", result.LaunchResult)
	}
	rels, err := durableSvc.ListSessions(context.Background(), result.Instance.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rels) != 1 || rels[0].Relation != store.DurableAgentSessionRelationWake {
		t.Fatalf("relations = %+v", rels)
	}
}

func TestDurableAgentRecipeDryRunMissingRequirementsDrivenByInputSchema(t *testing.T) {
	recipeSvc := newRecipeServiceForTest(t, nil)
	plan, err := recipeSvc.DryRun(context.Background(), "template-worker", DurableAgentRecipeRequest{})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if plan.Ready || len(plan.MissingRequirements) == 0 {
		t.Fatalf("plan should not be ready without profile: %+v", plan)
	}
	if len(plan.Injections) != 1 || plan.Injections[0].Secret {
		t.Fatalf("injection plan = %+v", plan.Injections)
	}
	if !strings.Contains(strings.Join(plan.MissingRequirements, ","), "durable_agent.name") {
		t.Fatalf("missing requirements = %+v", plan.MissingRequirements)
	}
}

func TestDurableAgentRecipeRequiredInputsSupportMetadataAndWakeMaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(path, []byte(`[
		{
			"id": "metadata-driven",
			"schema_version": 1,
			"kind": "project_advisor",
			"name": "Metadata Driven",
			"description": "Requires mapped metadata inputs.",
			"lifecycle_class": "advisor",
			"profile_id": "profile-fixed",
			"provider": "anthropic",
			"model": "claude-sonnet-4",
			"runtime_kind": "api",
			"launch_source_type": "durable_advisor",
			"wake_defaults": {"reason": "manual"},
			"inputs": [
				{"id": "external_id", "label": "External ID", "type": "string", "required": true, "maps_to": "metadata.external_id"},
				{"id": "project", "label": "Project", "type": "string", "required": true, "maps_to": "wake_payload.facts.project"}
			]
		}
	]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	recipeSvc := newRecipeServiceForTest(t, nil, path)

	plan, err := recipeSvc.DryRun(context.Background(), "metadata-driven", DurableAgentRecipeRequest{})
	if err != nil {
		t.Fatalf("DryRun missing: %v", err)
	}
	missing := strings.Join(plan.MissingRequirements, ",")
	if !strings.Contains(missing, "metadata.external_id") || !strings.Contains(missing, "wake_payload.facts.project") {
		t.Fatalf("missing requirements = %+v", plan.MissingRequirements)
	}

	plan, err = recipeSvc.DryRun(context.Background(), "metadata-driven", DurableAgentRecipeRequest{
		Metadata: map[string]string{"external_id": "ext-1"},
		WakePayload: DurableAgentWakePayload{
			Facts: map[string]string{"project": "agridd"},
		},
	})
	if err != nil {
		t.Fatalf("DryRun satisfied: %v", err)
	}
	if !plan.Ready || len(plan.MissingRequirements) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestDurableAgentRecipeApplyBackwardCompatibleExistingFields(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Compat Agent", Slug: "compat-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	recipeSvc := newRecipeServiceForTest(t, NewDurableAgentService(st))
	result, err := recipeSvc.Apply(context.Background(), "project-advisor", DurableAgentRecipeRequest{
		Name:      "Compat Advisor",
		Slug:      "compat-advisor",
		ProfileID: profile.ID,
		Provider:  "openai",
		Model:     "gpt-4.1",
		WorkRoot:  "/tmp/compat",
		Metadata: map[string]string{
			"scope_topic": "compatibility regression coverage",
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Instance.Provider != "openai" || result.Instance.Model != "gpt-4.1" || result.Instance.WorkRoot != "/tmp/compat" {
		t.Fatalf("instance = %+v", result.Instance)
	}
}
