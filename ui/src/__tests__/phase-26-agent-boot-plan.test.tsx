import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentBootPlanPanel } from "@/components/settings/agents/AgentBootPlanPanel";
import { AgentBuilderWizard } from "@/components/settings/agents/AgentBuilderWizard";
import { api } from "@/lib/api";
import type {
  AgentBootPlanDocument,
  AgentBootPlanDryRunResponse,
  AgentBuilderDraftResponse,
  AgentBuilderDryRunResponse,
  AgentProfile,
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

function agent(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
    id: "agent-1",
    name: "Planner",
    slug: "planner",
    avatar: "",
    icon: "",
    system_prompt: "Plan carefully.",
    description: "Planner profile",
    modes: "[]",
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
    source: "user",
    source_ref: "",
    role_tools: "[]",
    role_skills: "[]",
    context_policy: "{}",
    activation_mode: "singleton",
    class: "advisor",
    default_state: "sleeping",
    ...overrides,
  };
}

function emptyBootPlan(agentId = "agent-1"): AgentBootPlanDocument {
  return {
    agent_id: agentId,
    schema_version: 1,
    plant_items: [],
    callbacks: [],
    created_at: "",
    updated_at: "",
  };
}

function bootPreview(overrides: Partial<AgentBootPlanDryRunResponse> = {}): AgentBootPlanDryRunResponse {
  return {
    valid: true,
    errors: [],
    warnings: ["Generated items are declarative only in this phase."],
    normalized_plan: {
      agent_id: "agent-1",
      schema_version: 1,
      plant_items: [
        {
          id: "brief",
          name: "Task Brief",
          source_kind: "literal_file",
          content: "hello",
          target_rel_path: "docs/brief.md",
          entry_kind: "file",
          timing: ["create"],
          secret: true,
          overwrite_policy: "if_missing",
          failure_policy: "warn",
          enabled: true,
        },
      ],
      callbacks: [
        {
          id: "ready",
          name: "Ready",
          timing: "after_boot",
          callback_type: "message_injection",
          message: "ready",
          timeout_seconds: 30,
          failure_policy: "warn",
          enabled: true,
        },
      ],
      created_at: "",
      updated_at: "",
    },
    plant_operations: [
      {
        item_id: "brief",
        name: "Task Brief",
        timing: ["create"],
        target_rel_path: "docs/brief.md",
        entry_kind: "file",
        source_kind: "literal_file",
        overwrite_policy: "if_missing",
        failure_policy: "warn",
        enabled: true,
        secret: true,
        content_preview_redacted: true,
        notes: ["secret item details are redacted in dry-run"],
      },
    ],
    callback_order: [
      {
        callback_id: "ready",
        name: "Ready",
        timing: "after_boot",
        callback_type: "message_injection",
        timeout_seconds: 30,
        failure_policy: "warn",
        enabled: true,
        payload_preview: "ready",
        env_redacted: true,
        notes: ["configured but not executed by this phase"],
      },
    ],
    unsupported_notes: ['callback "Ready" is stored but not executed yet (message_injection)'],
    ...overrides,
  };
}

function startSurface(): StartSurfaceCapabilitiesResponse {
  return {
    schema_version: 1,
    lifecycle_classes: [{ value: "advisor", label: "Advisor" }],
    durable_statuses: [{ value: "sleeping", label: "Sleeping" }],
    launch_sources: [{ value: "durable_advisor", label: "Durable advisor" }],
    attachment_relations: [{ value: "primary", label: "Primary" }],
    runtime_kinds: [{ value: "api", label: "API", managed_automation: true, product_supported: true }],
    recipe_kinds: [{ value: "project_advisor", label: "Project advisor" }],
    wake_reasons: [{ value: "manual", label: "Manual" }],
    session_policies: [{ value: "reuse_latest_or_create", label: "Reuse latest or create" }],
    recipes: [],
    durable_agents: [],
    profiles: [],
    providers: [
      {
        id: "anthropic",
        name: "Anthropic",
        provider_type: "api",
        base_url: "",
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
        role_tools: "[]",
        role_skills: "[]",
        context_policy: "{}",
        activation_mode: "singleton",
        class: "advisor",
        default_state: "sleeping",
        default_model: "claude-sonnet-4",
        source: "api",
      },
      capabilities: {
        assigned_skill_ids: [],
        prompt_template_ids: [],
        known_tools: [],
        known_skills: [],
        procedures: [],
        knowledge_seeds: [],
        reflex_suggestions: [],
      },
      boot_plan: {
        agent_id: "",
        schema_version: 1,
        plant_items: [
          {
            id: "brief",
            name: "Task Brief",
            source_kind: "literal_file",
            content: "hello",
            target_rel_path: "docs/brief.md",
            entry_kind: "file",
            timing: ["create"],
            secret: false,
            overwrite_policy: "if_missing",
            failure_policy: "warn",
            enabled: true,
          },
        ],
        callbacks: [],
        created_at: "",
        updated_at: "",
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
    questions: [],
    warnings: [],
    unsupported_requests: [],
    confidence: 0.8,
    ...overrides,
  };
}

function dryRunResponse(overrides: Partial<AgentBuilderDryRunResponse> = {}): AgentBuilderDryRunResponse {
  return {
    schema_version: 1,
    valid: true,
    errors: [],
    warnings: [],
    unsupported_fields: [],
    normalized_profile_payload: {
      name: "Planner",
      slug: "planner",
      system_prompt: "Plan carefully.",
      description: "Planner profile",
      role_tools: "[]",
      role_skills: "[]",
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
    capability_operations: [],
    boot_plan_preview: bootPreview(),
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

describe("Phase 26 boot plan UI", () => {
  it("loads the default boot plan, edits entries, dry-runs, saves normalized data, and clears the plan", async () => {
    vi.spyOn(api, "getAgentBootPlan").mockResolvedValue(emptyBootPlan());
    const dryRunSpy = vi.spyOn(api, "dryRunAgentBootPlan").mockResolvedValue(bootPreview());
    const updateSpy = vi.spyOn(api, "updateAgentBootPlan").mockResolvedValue(bootPreview().normalized_plan);
    const deleteSpy = vi.spyOn(api, "deleteAgentBootPlan").mockResolvedValue({ status: "deleted" });

    renderWithClient(<AgentBootPlanPanel agent={agent()} isReadOnly={false} />);

    expect(await screen.findByText("No plant items configured.")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Add plant item" }));
    fireEvent.change(screen.getByLabelText("Plant item id"), { target: { value: "brief" } });
    fireEvent.change(screen.getByLabelText("Plant item name"), { target: { value: "Task Brief" } });
    fireEvent.change(screen.getByLabelText("Plant item target relative path"), {
      target: { value: "docs/brief.md" },
    });
    fireEvent.change(screen.getByLabelText("Plant item content"), { target: { value: "hello" } });

    fireEvent.click(screen.getByRole("button", { name: "Add callback" }));
    fireEvent.change(screen.getAllByLabelText("Callback id")[0], { target: { value: "ready" } });
    fireEvent.change(screen.getAllByLabelText("Callback name")[0], { target: { value: "Ready" } });
    fireEvent.change(screen.getByLabelText("Callback message body"), { target: { value: "ready" } });

    fireEvent.click(screen.getByRole("button", { name: "Dry Run" }));
    await waitFor(() => expect(dryRunSpy).toHaveBeenCalledTimes(1));
    expect(screen.getByText("Dry Run Preview")).toBeTruthy();
    expect(screen.getByText("Secret-backed source or content redacted.")).toBeTruthy();
    expect(screen.getByText(/stored but not executed yet/i)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Save Boot Plan" }));
    await waitFor(() => expect(updateSpy).toHaveBeenCalledTimes(1));
    expect(updateSpy.mock.calls[0]?.[1]).toMatchObject({
      agent_id: "agent-1",
      plant_items: [{ id: "brief", target_rel_path: "docs/brief.md" }],
      callbacks: [{ id: "ready", name: "Ready" }],
    });

    fireEvent.click(screen.getByRole("button", { name: "Clear Plan" }));
    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("agent-1"));
  });

  it("renders saved plans and disables mutation controls for read-only agents", async () => {
    vi.spyOn(api, "getAgentBootPlan").mockResolvedValue(bootPreview().normalized_plan);

    renderWithClient(<AgentBootPlanPanel agent={agent({ source: "internal" })} isReadOnly />);

    expect(await screen.findByText("Task Brief")).toBeTruthy();
    expect(screen.getByText(/file-backed and read-only/i)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save Boot Plan" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Add plant item" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("includes boot plan in builder dry-run and persists it during deterministic submit", async () => {
    vi.spyOn(api, "getStartSurfaceCapabilities").mockResolvedValue(startSurface());
    vi.spyOn(api, "listSkills").mockResolvedValue([skill()]);
    vi.spyOn(api, "listPromptTemplates").mockResolvedValue([template()]);
    vi.spyOn(api, "agentBuilderDraft").mockResolvedValue(draftResponse());
    const dryRunSpy = vi.spyOn(api, "agentBuilderDryRun").mockResolvedValue(dryRunResponse());
    vi.spyOn(api, "createAgentProfile").mockResolvedValue(agent());
    const updateBootSpy = vi.spyOn(api, "updateAgentBootPlan").mockResolvedValue(bootPreview().normalized_plan);

    renderWithClient(
      <AgentBuilderWizard
        modelOptions={[{ id: "claude-sonnet-4", label: "Claude Sonnet 4" }]}
        defaultModel="claude-sonnet-4"
        onCancel={vi.fn()}
        onCreated={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText("Builder brief"), {
      target: { value: "Create a planner agent with a boot-planted task brief." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Generate Draft" }));

    expect(await screen.findByText("Boot Plan")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Run Dry Run" }));
    await waitFor(() => expect(dryRunSpy).toHaveBeenCalledTimes(1));
    expect(dryRunSpy.mock.calls[0]?.[0]).toMatchObject({
      boot_plan: {
        plant_items: [{ id: "brief", target_rel_path: "docs/brief.md" }],
      },
    });

    fireEvent.click(screen.getByRole("button", { name: "Create Agent Deterministically" }));
    await waitFor(() => expect(updateBootSpy).toHaveBeenCalledTimes(1));
    expect(updateBootSpy).toHaveBeenCalledWith(
      "agent-1",
      expect.objectContaining({
        agent_id: "agent-1",
        plant_items: [expect.objectContaining({ id: "brief" })],
      }),
    );
    expect(await screen.findByText(/ready notice remains preview-only/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy preview JSON" })).toBeTruthy();
  });
});
