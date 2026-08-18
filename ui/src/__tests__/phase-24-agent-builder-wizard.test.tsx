import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentProfileManager } from "@/components/settings/AgentProfileManager";
import { AgentBuilderWizard } from "@/components/settings/agents/AgentBuilderWizard";
import { api } from "@/lib/api";
import type {
  AgentBuilderDraftResponse,
  AgentBuilderDryRunResponse,
  AgentBuilderReviewResponse,
  AgentProfile,
  DurableAgentInstance,
  PromptTemplate,
  Skill,
  StartSurfaceCapabilitiesResponse,
} from "@/lib/types";

vi.mock("@/hooks/useSettings", () => ({
  useModels: () => ({
    data: [{ model_id: "claude-sonnet-4", display_name: "Claude Sonnet 4" }],
  }),
  useSettings: () => ({
    data: { default_model: "claude-sonnet-4" },
  }),
}));

vi.mock("@/stores/useAppStore", () => ({
  useAppStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      activeWorkspaceId: "workspace-1",
      activeProjectId: "project-1",
      setActiveSession: vi.fn(),
    }),
}));

vi.mock("@/stores/useLayoutStore", () => ({
  useLayoutStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      setCurrentPage: vi.fn(),
    }),
}));

const queryClients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of queryClients) client.clear();
  queryClients.length = 0;
  vi.restoreAllMocks();
});

function renderWithClient(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  queryClients.push(client);
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

function startSurface(): StartSurfaceCapabilitiesResponse {
  return {
    schema_version: 1,
    lifecycle_classes: [{ value: "advisor", label: "Advisor" }],
    durable_statuses: [{ value: "sleeping", label: "Sleeping" }],
    launch_sources: [{ value: "durable_advisor", label: "Durable advisor" }],
    attachment_relations: [{ value: "primary", label: "Primary" }],
    runtime_kinds: [
      {
        value: "api",
        label: "API",
        managed_automation: true,
        product_supported: true,
      },
    ],
    recipe_kinds: [{ value: "project_advisor", label: "Project advisor" }],
    wake_reasons: [{ value: "manual", label: "Manual" }],
    session_policies: [{ value: "reuse_latest_or_create", label: "Reuse latest or create" }],
    recipes: [
      {
        id: "project-advisor",
        schema_version: 1,
        name: "Project Advisor",
        kind: "project_advisor",
        description: "Project advisor",
        lifecycle_class: "advisor",
        profile_id: "",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        work_root: "",
        launch_source_type: "durable_advisor",
        launch_source_id: "project-advisor",
        wake_defaults: { reason: "manual" },
      },
    ],
    durable_agents: [],
    profiles: [],
    providers: [
      {
        id: "anthropic",
        name: "Anthropic",
        provider_type: "api",
        is_enabled: true,
        settings: "{}",
        created_at: "",
        updated_at: "",
      },
    ],
    models: [
      {
        id: "model-1",
        provider_id: "anthropic",
        model_id: "claude-sonnet-4",
        display_name: "Claude Sonnet 4",
        context_window: 200000,
        max_output: 8192,
        supports_tools: true,
        supports_vision: true,
        is_enabled: true,
        pricing: "{}",
        sort_order: 0,
        provider_type: "api",
      },
    ],
    boot_profiles: [],
    work_root_hints: [],
  };
}

function skill(overrides: Partial<Skill> = {}): Skill {
  return {
    id: "skill-1",
    name: "Planner",
    slug: "planner",
    category: "planning",
    description: "Plan the work.",
    icon: "",
    tool_bindings: "[]",
    input_schema: "{}",
    is_builtin: false,
    settings: "{}",
    created_at: "",
    updated_at: "",
    source: "user",
    ...overrides,
  };
}

function template(overrides: Partial<PromptTemplate> = {}): PromptTemplate {
  return {
    id: "template-1",
    name: "Escalation Guard",
    slug: "escalation-guard",
    scope: "system",
    template: "Guard the escalation path.",
    variables: "[]",
    priority: 10,
    icon: "",
    is_builtin: false,
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

function profile(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
    id: "agent-1",
    name: "Planner",
    slug: "planner",
    avatar: "",
    icon: "",
    system_prompt: "Plan carefully.",
    description: "Planner profile",
    modes: "",
    default_model: "claude-sonnet-4",
    mcp_servers: "[]",
    tool_permissions: "{}",
    can_execute: true,
    settings: "{}",
    created_at: "",
    updated_at: "",
    agent_hash: "",
    version: 1,
    tools: "[]",
    directories: "[]",
    constraints: "{}",
    tags: "[]",
    status: "active",
    source: "api",
    source_ref: "",
    role_tools: '["task_execute"]',
    role_skills: '["planner"]',
    context_policy: "{}",
    activation_mode: "singleton",
    class: "advisor",
    default_state: "sleeping",
    ...overrides,
  };
}

function dryRunResponse(overrides: Partial<AgentBuilderDryRunResponse> = {}): AgentBuilderDryRunResponse {
  return {
    schema_version: 1,
    valid: true,
    errors: [],
    warnings: ["Needs a tighter context policy."],
    unsupported_fields: ["capabilities.reflex_suggestions"],
    normalized_profile_payload: {
      name: "Planner",
      slug: "planner",
      system_prompt: "Plan carefully.",
      description: "Planner profile",
      role_tools: '["task_execute"]',
      role_skills: '["planner"]',
      context_policy: "{}",
      activation_mode: "singleton",
      class: "advisor",
      default_state: "sleeping",
      default_model: "claude-sonnet-4",
      source: "api",
      tool_permissions: "{}",
      settings: "{}",
      tools: "[]",
      directories: "[]",
      constraints: "{}",
      tags: "[]",
      status: "active",
    },
    capability_operations: [{ area: "skills", action: "assign", target: "draft", count: 1 }],
    launch_plan_preview: {
      lifecycle_class: "advisor",
      session_policy: "reuse_latest_or_create",
      attachment_relation: "primary",
      provider: "anthropic",
      model: "claude-sonnet-4",
      runtime_kind: "api",
      work_root: "",
      would_create_session: false,
      wake_payload: { reason: "manual" },
    },
    notification_preview: {
      target_kind: "operator",
      target_id: "current-user",
      profile: { id: "preview-profile", name: "Planner", slug: "planner" },
      durable_instance: { id: "", name: "", slug: "" },
      session: { id: "", name: "", slug: "" },
      links: [{ kind: "agent_profile", path: "/settings/ai/agents/planner", label: "Open agent profile" }],
      warnings: [],
      followups: ["Review the dry-run output before deterministic submit."],
    },
    ...overrides,
  };
}

function draftResponse(overrides: Partial<AgentBuilderDraftResponse> = {}): AgentBuilderDraftResponse {
  return {
    schema_version: 1,
    draft: {
      mode: "create_profile",
      profile: {
        name: "Planner",
        slug: "planner",
        system_prompt: "Plan carefully.",
        description: "Planner profile",
        role_tools: '["task_execute"]',
        role_skills: '["planner"]',
        context_policy: "{}",
        activation_mode: "singleton",
        class: "advisor",
        default_state: "sleeping",
        default_model: "claude-sonnet-4",
        source: "api",
      },
      capabilities: {
        assigned_skill_ids: ["skill-1"],
        prompt_template_ids: ["template-1"],
        known_tools: [],
        known_skills: [],
        procedures: [],
        knowledge_seeds: [],
        reflex_suggestions: ["check memory before guessing"],
      },
      durable_instance: {
        create: false,
        lifecycle_class: "advisor",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        start: true,
        metadata: {},
      },
      operator_notification: {
        target_kind: "operator",
        target_id: "current-user",
        include_links: true,
      },
    },
    questions: ["Should this agent stay project-scoped?"],
    warnings: ["Builder draft used default context policy."],
    unsupported_requests: ["capabilities.reflex_suggestions"],
    confidence: 0.72,
    ...overrides,
  };
}

function reviewResponse(overrides: Partial<AgentBuilderReviewResponse> = {}): AgentBuilderReviewResponse {
  return {
    schema_version: 1,
    accepted: false,
    questions: ["Add a clearer operator-facing description."],
    warnings: ["Durable work root is still blank."],
    suggested_patch_operations: [
      {
        op: "replace",
        path: "/profile/description",
        note: "Clarify scope for operators.",
      },
    ],
    max_rounds_recommended: 3,
    ...overrides,
  };
}

describe("Phase 24 agent builder wizard", () => {
  it("opens from agent settings and preserves a manual path", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue([]);
    vi.spyOn(api, "getStartSurfaceCapabilities").mockResolvedValue(startSurface());
    vi.spyOn(api, "listSkills").mockResolvedValue([]);
    vi.spyOn(api, "listPromptTemplates").mockResolvedValue([]);

    renderWithClient(<AgentProfileManager />);

    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    expect(await screen.findByText("Agent Builder")).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Manual" })).toBeTruthy();
  });

  it("runs draft, review, dry-run, and deterministic submit without writing early", async () => {
    vi.spyOn(api, "getStartSurfaceCapabilities").mockResolvedValue(startSurface());
    vi.spyOn(api, "listSkills").mockResolvedValue([skill()]);
    vi.spyOn(api, "listPromptTemplates").mockResolvedValue([template()]);
    const draftSpy = vi.spyOn(api, "agentBuilderDraft").mockResolvedValue(draftResponse());
    const reviewSpy = vi.spyOn(api, "agentBuilderReview").mockResolvedValue(reviewResponse());
    const dryRunSpy = vi.spyOn(api, "agentBuilderDryRun").mockResolvedValue(dryRunResponse());
    const createSpy = vi.spyOn(api, "createAgentProfile").mockResolvedValue(profile());
    const assignSkillSpy = vi.spyOn(api, "assignSkillToAgent").mockResolvedValue(skill());
    const assignTemplateSpy = vi
      .spyOn(api, "assignTemplateToAgent")
      .mockResolvedValue(template());

    const onCreated = vi.fn();
    renderWithClient(
      <AgentBuilderWizard
        modelOptions={[{ id: "claude-sonnet-4", label: "Claude Sonnet 4" }]}
        defaultModel="claude-sonnet-4"
        onCancel={vi.fn()}
        onCreated={onCreated}
      />,
    );

    fireEvent.change(screen.getByLabelText("Builder brief"), {
      target: { value: "Create a planner agent for the active project." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Generate Draft" }));

    expect(await screen.findByDisplayValue("Planner")).toBeTruthy();
    expect(screen.getByText("Should this agent stay project-scoped?")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Request Review" }));
    fireEvent.click(screen.getByRole("button", { name: "Request Review" }));
    fireEvent.click(screen.getByRole("button", { name: "Request Review" }));

    await waitFor(() => expect(reviewSpy).toHaveBeenCalledTimes(3));
    expect(screen.getByText(/Review rounds used: 3\/3/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Request Review" }) as HTMLButtonElement).disabled).toBe(
      true,
    );

    fireEvent.click(screen.getByRole("button", { name: "Run Dry Run" }));
    expect(await screen.findByText("Dry Run Preview")).toBeTruthy();
    expect(screen.getByText("Notification Preview")).toBeTruthy();
    expect(createSpy).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Create Agent Deterministically" }));

    await waitFor(() => expect(createSpy).toHaveBeenCalledTimes(1));
    expect(assignSkillSpy).toHaveBeenCalledWith("agent-1", { skill_id: "skill-1" });
    expect(assignTemplateSpy).toHaveBeenCalledWith("agent-1", { template_id: "template-1" });
    expect(draftSpy).toHaveBeenCalledTimes(1);
    expect(dryRunSpy).toHaveBeenCalledTimes(1);
    expect(onCreated).toHaveBeenCalledWith("agent-1");
    expect(await screen.findByText("Agent Ready")).toBeTruthy();
  });

  it("defaults launch on and lets the operator disable launch before submit", async () => {
    vi.spyOn(api, "getStartSurfaceCapabilities").mockResolvedValue(startSurface());
    vi.spyOn(api, "listSkills").mockResolvedValue([]);
    vi.spyOn(api, "listPromptTemplates").mockResolvedValue([]);
    vi.spyOn(api, "agentBuilderDraft").mockResolvedValue(
      draftResponse({
        draft: {
          ...draftResponse().draft,
          mode: "create_profile_and_instance",
          capabilities: {
            assigned_skill_ids: [],
            prompt_template_ids: [],
            known_tools: [],
            known_skills: [],
            procedures: [],
            knowledge_seeds: [],
            reflex_suggestions: [],
          },
          durable_instance: {
            create: true,
            lifecycle_class: "advisor",
            provider: "anthropic",
            model: "claude-sonnet-4",
            runtime_kind: "api",
            work_root: "/tmp/planner",
            start: true,
            metadata: {},
          },
        },
      }),
    );
    vi.spyOn(api, "agentBuilderDryRun").mockResolvedValue(
      dryRunResponse({
        launch_plan_preview: {
          lifecycle_class: "advisor",
          session_policy: "reuse_latest_or_create",
          attachment_relation: "primary",
          provider: "anthropic",
          model: "claude-sonnet-4",
          runtime_kind: "api",
          work_root: "/tmp/planner",
          would_create_session: false,
          wake_payload: { reason: "manual" },
        },
      }),
    );
    vi.spyOn(api, "createAgentProfile").mockResolvedValue(profile({ id: "agent-2" }));
    const createDurableSpy = vi.spyOn(api, "createDurableAgent").mockResolvedValue({
      id: "durable-1",
      name: "Planner",
      slug: "planner",
      profile_id: "agent-2",
      lifecycle_class: "advisor",
      provider: "anthropic",
      model: "claude-sonnet-4",
      runtime_kind: "api",
      launch_source_type: "durable_advisor",
      launch_source_id: "planner",
      work_root: "/tmp/planner",
      status: "sleeping",
      current_session_id: "",
      failure_reason: "",
      metadata_json: "{}",
      created_at: "",
      updated_at: "",
    } as DurableAgentInstance);
    const startDurableSpy = vi.spyOn(api, "startDurableAgent").mockResolvedValue({
      instance: {} as DurableAgentInstance,
      policy: {} as never,
      session: { id: "session-1", title: "", custom_name: "", workspace_id: "", project_id: "", context_type: null, context_id: null, provider: "anthropic", model: "claude-sonnet-4", status: "active", is_pinned: false, sort_order: 0, message_count: 0, tags: "", last_activity: "", created_at: "" },
    } as never);

    renderWithClient(
      <AgentBuilderWizard
        modelOptions={[{ id: "claude-sonnet-4", label: "Claude Sonnet 4" }]}
        defaultModel="claude-sonnet-4"
        onCancel={vi.fn()}
        onCreated={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText("Builder brief"), {
      target: { value: "Create a durable planner agent." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Generate Draft" }));

    await screen.findByText("Editable Draft");
    const launchSwitches = screen.getAllByRole("switch", { name: "Launch after submit" });
    expect(launchSwitches.at(-1)?.getAttribute("data-state")).toBe("checked");
    fireEvent.click(launchSwitches.at(-1) as HTMLElement);

    fireEvent.click(screen.getByRole("button", { name: "Run Dry Run" }));
    expect(await screen.findByText("Dry Run Preview")).toBeTruthy();
    expect(createDurableSpy).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Create Agent Deterministically" }));

    await waitFor(() => expect(createDurableSpy).toHaveBeenCalledTimes(1));
    expect(startDurableSpy).not.toHaveBeenCalled();
  });
});
