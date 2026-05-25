import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LeftRail } from "@/components/chat/LeftRail";
import { StartSurfaceDialog } from "@/components/sidebar/StartSurfaceDialog";
import { api } from "@/lib/api";
import type {
  DurableAgentLaunchResult,
  DurableAgentRecipeApplyResult,
  DurableAgentRecipePlan,
  Session,
  StartSurfaceCapabilitiesResponse,
} from "@/lib/types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function renderWithClient(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

function session(id: string, overrides: Partial<Session> = {}): Session {
  return {
    id,
    short_code: "c1",
    title: "Started",
    custom_name: "",
    workspace_id: "workspace-1",
    project_id: "project-1",
    context_type: null,
    context_id: null,
    provider: "anthropic",
    model: "claude-sonnet-4",
    status: "active",
    is_pinned: false,
    sort_order: 0,
    message_count: 0,
    tags: "[]",
    last_activity: "2026-05-23T00:00:00Z",
    created_at: "2026-05-23T00:00:00Z",
    ...overrides,
  };
}

function capabilities(
  overrides: Partial<StartSurfaceCapabilitiesResponse> = {},
): StartSurfaceCapabilitiesResponse {
  return {
    schema_version: 1,
    lifecycle_classes: [],
    durable_statuses: [],
    launch_sources: [],
    attachment_relations: [],
    runtime_kinds: [
      {
        value: "api",
        label: "API",
        managed_automation: false,
        product_supported: true,
      },
      {
        value: "subprocess",
        label: "Subprocess",
        managed_automation: true,
        product_supported: true,
      },
    ],
    recipe_kinds: [],
    wake_reasons: [],
    session_policies: [],
    recipes: [
      {
        id: "project-advisor",
        schema_version: 1,
        kind: "project_advisor",
        name: "Project Advisor",
        description: "Advisor",
        lifecycle_class: "advisor",
        profile_rule: "operator_selected",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        launch_source_type: "durable_advisor",
        wake_defaults: { reason: "manual" },
      },
    ],
    durable_agents: [
      {
        id: "durable-1",
        name: "Durable One",
        slug: "durable-one",
        profile_id: "profile-1",
        lifecycle_class: "advisor",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        launch_source_type: "durable_advisor",
        launch_source_id: "project-advisor",
        work_root: "",
        status: "sleeping",
        current_session_id: "",
        failure_reason: "",
        metadata_json: "{}",
        created_at: "2026-05-23T00:00:00Z",
        updated_at: "2026-05-23T00:00:00Z",
      },
    ],
    profiles: [
      {
        id: "profile-1",
        name: "Default Agent",
        slug: "default",
        avatar: "",
        icon: "",
        system_prompt: "",
        description: "",
        modes: "",
        default_model: "",
        mcp_servers: "",
        tool_permissions: "",
        can_execute: true,
        settings: "",
        created_at: "",
        updated_at: "",
        agent_hash: "",
        version: 1,
        tools: "",
        directories: "",
        constraints: "",
        tags: "",
        status: "active",
        source: "",
        source_ref: "",
      },
    ],
    providers: [
      {
        id: "anthropic",
        name: "Anthropic",
        provider_type: "anthropic",
        base_url: "",
        is_enabled: true,
        settings: "",
        created_at: "",
        updated_at: "",
      },
      {
        id: "bootprofile:claude-smoke",
        name: "Claude Smoke",
        provider_type: "bootprofile:claude-smoke",
        base_url: "",
        is_enabled: true,
        settings: "",
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
        context_window: 0,
        max_output: 0,
        supports_tools: true,
        supports_vision: false,
        is_enabled: true,
        pricing: "",
        sort_order: 0,
        provider_type: "anthropic",
      },
    ],
    boot_profiles: [
      {
        id: "bootprofile:claude-smoke",
        label: "Claude Smoke",
        provider: "pty-claude",
        work_root: "/tmp/work",
      },
    ],
    work_root_hints: [
      {
        id: "project-root",
        label: "Project root",
        path: "/tmp/work",
        description: "Current project root",
      },
    ],
    ...overrides,
  };
}

function mockCapabilities(caps = capabilities()) {
  vi.spyOn(api, "getStartSurfaceCapabilities").mockResolvedValue(caps);
}

function durableFixture() {
  const durable = capabilities().durable_agents[0];
  if (!durable) throw new Error("missing durable fixture");
  return durable;
}

describe("Phase 11 Start surface", () => {
  it("exposes the rail Start entry point", () => {
    const onStart = vi.fn();
    render(
      <LeftRail
        open
        workspaceHeader={<div />}
        onNewChat={onStart}
        onSearch={() => undefined}
        newChatLabel="Start"
        footer={<div />}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /start/i }));
    expect(onStart).toHaveBeenCalledTimes(1);
  });

  it("creates a normal chat session", async () => {
    mockCapabilities();
    const createSession = vi.spyOn(api, "createSession").mockResolvedValue(session("session-chat"));
    const onSessionStarted = vi.fn();

    renderWithClient(
      <StartSurfaceDialog
        open
        onOpenChange={() => undefined}
        workspaceId="workspace-1"
        projectId="project-1"
        defaultProvider="anthropic"
        defaultModel="claude-sonnet-4"
        defaultAgent="profile-1"
        onSessionStarted={onSessionStarted}
      />,
    );

    await screen.findByText("Chat with a model");
    fireEvent.click(screen.getByRole("button", { name: /start chat/i }));

    await waitFor(() => {
      expect(createSession).toHaveBeenCalledWith({
        workspace_id: "workspace-1",
        project_id: "project-1",
        provider: "anthropic",
        model: "claude-sonnet-4",
        agent_id: "profile-1",
      });
      expect(onSessionStarted).toHaveBeenCalledWith("session-chat");
    });
  });

  it("handles nullable capability arrays from empty backend lists", async () => {
    mockCapabilities({
      ...capabilities(),
      recipes: null,
      durable_agents: null,
      profiles: null,
      providers: null,
      models: null,
      boot_profiles: null,
    } as unknown as StartSurfaceCapabilitiesResponse);

    renderWithClient(
      <StartSurfaceDialog
        open
        onOpenChange={() => undefined}
        workspaceId="workspace-1"
        projectId={null}
        onSessionStarted={() => undefined}
      />,
    );

    await screen.findByText("Chat with a model");
    fireEvent.click(screen.getByRole("button", { name: "Recipe" }));
    expect(await screen.findByText("No recipes available")).toBeTruthy();
  });

  it("starts a boot-profile harness session", async () => {
    mockCapabilities();
    const createSession = vi
      .spyOn(api, "createSession")
      .mockResolvedValue(session("session-harness"));
    const onSessionStarted = vi.fn();

    renderWithClient(
      <StartSurfaceDialog
        open
        onOpenChange={() => undefined}
        workspaceId="workspace-1"
        projectId={null}
        onSessionStarted={onSessionStarted}
      />,
    );

    await screen.findByText("Chat with a model");
    fireEvent.click(screen.getByRole("button", { name: "Harness" }));
    fireEvent.click(screen.getByRole("button", { name: /start harness/i }));

    await waitFor(() => {
      expect(createSession).toHaveBeenCalledWith({
        workspace_id: "workspace-1",
        project_id: undefined,
        provider: "bootprofile:claude-smoke",
        model: "bootprofile:claude-smoke",
        agent_id: "profile-1",
      });
      expect(onSessionStarted).toHaveBeenCalledWith("session-harness");
    });
  });

  it("starts and resumes durable agents", async () => {
    mockCapabilities();
    const durable = durableFixture();
    const result: DurableAgentLaunchResult = {
      instance: { ...durable, current_session_id: "session-durable" },
      policy: {
        instance_id: "durable-1",
        lifecycle_class: "advisor",
        session_policy: "reuse_latest_or_create",
        launch_source_type: "durable_advisor",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        work_root: "",
        attachment_relation: "primary",
        wake_payload: { reason: "manual" },
      },
      session: session("session-durable"),
      created_session: true,
      reused_session: false,
    };
    const startDurableAgent = vi.spyOn(api, "startDurableAgent").mockResolvedValue(result);
    const resumeDurableAgent = vi.spyOn(api, "resumeDurableAgent").mockResolvedValue(result);
    const onSessionStarted = vi.fn();

    renderWithClient(
      <StartSurfaceDialog
        open
        onOpenChange={() => undefined}
        workspaceId="workspace-1"
        projectId={null}
        onSessionStarted={onSessionStarted}
      />,
    );

    await screen.findByText("Chat with a model");
    fireEvent.click(screen.getByRole("button", { name: "Durable" }));
    fireEvent.click(screen.getByRole("button", { name: /start agent/i }));

    await waitFor(() => expect(startDurableAgent).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: "resume" }));
    fireEvent.click(screen.getByRole("button", { name: /resume agent/i }));

    await waitFor(() => {
      expect(resumeDurableAgent).toHaveBeenCalled();
      expect(onSessionStarted).toHaveBeenCalledWith("session-durable");
    });
  });

  it("dry-runs and applies a recipe, showing missing requirements", async () => {
    mockCapabilities(capabilities({ profiles: [] }));
    const durable = durableFixture();
    const plan: DurableAgentRecipePlan = {
      recipe_id: "project-advisor",
      recipe_schema_version: 1,
      instance: durable,
      launch_policy: {
        instance_id: "durable-1",
        lifecycle_class: "advisor",
        session_policy: "reuse_latest_or_create",
        launch_source_type: "durable_advisor",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        work_root: "",
        attachment_relation: "primary",
        wake_payload: { reason: "manual" },
      },
      wake_payload: { reason: "manual" },
      session_policy: "reuse_latest_or_create",
      would_create_session: true,
      would_reuse_session: false,
      missing_requirements: ["profile_id"],
      ready: false,
    };
    const readyPlan = { ...plan, missing_requirements: [], ready: true };
    const dryRun = vi
      .spyOn(api, "dryRunDurableAgentRecipe")
      .mockResolvedValueOnce(plan)
      .mockResolvedValueOnce(readyPlan);
    const applyResult: DurableAgentRecipeApplyResult = {
      plan: readyPlan,
      instance: { ...durable, current_session_id: "session-recipe" },
      launch_result: {
        instance: { ...durable, current_session_id: "session-recipe" },
        policy: plan.launch_policy,
        session: session("session-recipe"),
        created_session: true,
        reused_session: false,
      },
    };
    const apply = vi.spyOn(api, "applyDurableAgentRecipe").mockResolvedValue(applyResult);
    const onSessionStarted = vi.fn();

    renderWithClient(
      <StartSurfaceDialog
        open
        onOpenChange={() => undefined}
        workspaceId="workspace-1"
        projectId={null}
        onSessionStarted={onSessionStarted}
      />,
    );

    await screen.findByText("Chat with a model");
    fireEvent.click(screen.getByRole("button", { name: "Recipe" }));
    fireEvent.click(screen.getByRole("button", { name: /dry run/i }));

    await screen.findByText(/missing: profile_id/i);
    expect(dryRun).toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /dry run/i }));
    await screen.findByText("Ready to apply");
    fireEvent.click(screen.getByRole("button", { name: /apply and start/i }));
    await waitFor(() => {
      expect(apply).toHaveBeenCalled();
      expect(onSessionStarted).toHaveBeenCalledWith("session-recipe");
    });
  });

  it("uses dropdowns and selectable path hints on the recipe form", async () => {
    mockCapabilities();
    const dryRun = vi.spyOn(api, "dryRunDurableAgentRecipe").mockResolvedValue({
      recipe_id: "project-advisor",
      recipe_schema_version: 1,
      instance: durableFixture(),
      launch_policy: {
        instance_id: "durable-1",
        lifecycle_class: "advisor",
        session_policy: "reuse_latest_or_create",
        launch_source_type: "durable_advisor",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "subprocess",
        work_root: "/tmp/work",
        attachment_relation: "primary",
        wake_payload: { reason: "manual" },
      },
      wake_payload: { reason: "manual" },
      session_policy: "reuse_latest_or_create",
      would_create_session: true,
      would_reuse_session: false,
      ready: true,
    } as DurableAgentRecipePlan);

    renderWithClient(
      <StartSurfaceDialog
        open
        onOpenChange={() => undefined}
        workspaceId="workspace-1"
        projectId={null}
        onSessionStarted={() => undefined}
      />,
    );

    await screen.findByText("Chat with a model");
    fireEvent.click(screen.getByRole("button", { name: "Recipe" }));
    fireEvent.change(screen.getByLabelText("Recipe runtime kind"), {
      target: { value: "subprocess" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Project root" }));
    fireEvent.click(screen.getByRole("button", { name: /dry run/i }));

    await waitFor(() => {
      expect(dryRun).toHaveBeenCalledWith(
        "project-advisor",
        expect.objectContaining({
          provider: "anthropic",
          model: "claude-sonnet-4",
          runtime_kind: "subprocess",
          work_root: "/tmp/work",
        }),
      );
    });
  });
});
