import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
      clear: () => values.clear(),
    },
  });
});

import { DurableAgentAdminPanel } from "@/components/settings/DurableAgentAdminPanel";
import { api } from "@/lib/api";
import type {
  AgentSchedule,
  AgentProfile,
  DurableAgentEvent,
  DurableAgentInstance,
  DurableAgentLaunchPlan,
  DurableAgentRecipe,
  DurableAgentRecipeApplyResult,
  DurableAgentRecipePlan,
  DurableAgentSessionAttachmentState,
  DurableAgentWakeDueItem,
  DurableAgentWakeRunResult,
  Session,
} from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";

const queryClients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of queryClients) client.clear();
  queryClients.length = 0;
  vi.restoreAllMocks();
  localStorage.clear();
  useAppStore.setState({
    activeWorkspaceId: null,
    activeProjectId: null,
    activeSessionId: null,
    configVersion: 0,
  });
  useLayoutStore.setState({ currentPage: "settings" });
  window.location.hash = "";
});

function renderWithClient(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  queryClients.push(client);
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>,
  );
}

function durableAgent(
  overrides: Partial<DurableAgentInstance> = {},
): DurableAgentInstance {
  return {
    id: "durable-1",
    name: "Project Advisor",
    slug: "project-advisor",
    profile_id: "profile-1",
    lifecycle_class: "advisor",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_kind: "api",
    launch_source_type: "durable_advisor",
    launch_source_id: "project-advisor",
    work_root: "/tmp/project",
    status: "sleeping",
    current_session_id: "session-1",
    failure_reason: "",
    metadata_json: "{}",
    created_at: "2026-05-24T00:00:00Z",
    updated_at: "2026-05-24T00:00:00Z",
    ...overrides,
  };
}

function session(overrides: Partial<Session> = {}): Session {
  return {
    id: "session-2",
    short_code: "s2",
    title: "Started agent",
    custom_name: "",
    workspace_id: "workspace-1",
    project_id: "project-1",
    context_type: "durable_agent",
    context_id: "durable-1",
    provider: "anthropic",
    model: "claude-sonnet-4",
    status: "active",
    is_pinned: false,
    sort_order: 0,
    message_count: 0,
    tags: "[]",
    last_activity: "",
    created_at: "",
    ...overrides,
  };
}

function launchPlan(
  overrides: Partial<DurableAgentLaunchPlan> = {},
): DurableAgentLaunchPlan {
  return {
    instance_id: "durable-1",
    lifecycle_class: "advisor",
    session_policy: "reuse_latest_or_create",
    launch_source_type: "durable_advisor",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_kind: "api",
    work_root: "/tmp/project",
    attachment_relation: "primary",
    wake_payload: { reason: "manual", prompt: "Wake up" },
    ...overrides,
  };
}

function attachment(
  overrides: Partial<DurableAgentSessionAttachmentState> = {},
): DurableAgentSessionAttachmentState {
  return {
    instance_id: "durable-1",
    session_id: "session-1",
    relation: "primary",
    attached_at: "",
    session_status: "active",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_state: "running",
    runtime_failure_reason: "binary not found",
    halted_reason: "detector_B",
    ...overrides,
  };
}

function durableEvent(
  overrides: Partial<DurableAgentEvent> = {},
): DurableAgentEvent {
  return {
    id: "event-1",
    instance_id: "durable-1",
    event_type: "start_failed",
    status_before: "start_requested",
    status_after: "failed",
    session_id: "session-1",
    source: "runtime",
    message: "Runtime exited before ready.",
    metadata_json: JSON.stringify({
      failure_reason: "binary not found",
      relation: "primary",
      session_reused: false,
    }),
    created_at: "2026-05-24T00:01:00Z",
    ...overrides,
  };
}

function schedule(overrides: Partial<AgentSchedule> = {}): AgentSchedule {
  return {
    id: "sched-1",
    agent_id: "profile-1",
    session_id: "",
    name: "Nightly wake",
    schedule_kind: "cron",
    schedule_spec: "0 9 * * *",
    body: "wake",
    priority: 1,
    status: "active",
    expires_at: "",
    fired_count: 2,
    last_fired_at: "2026-05-24T00:00:00Z",
    created_at: "2026-05-23T00:00:00Z",
    created_by: "operator",
    ...overrides,
  };
}

function dueWakeItem(
  overrides: Partial<DurableAgentWakeDueItem> = {},
): DurableAgentWakeDueItem {
  return {
    instance_id: "durable-1",
    instance_name: "Project Advisor",
    lifecycle_class: "advisor",
    current_session_id: "session-1",
    schedule: schedule(),
    wake_reason: "scheduled_wake",
    due: true,
    ...overrides,
  };
}

function recipe(
  overrides: Partial<DurableAgentRecipe> = {},
): DurableAgentRecipe {
  return {
    id: "project-advisor",
    schema_version: 1,
    kind: "project_advisor",
    name: "Project Advisor Recipe",
    description: "Create an advisor.",
    lifecycle_class: "advisor",
    profile_id: "profile-1",
    profile_rule: "operator_selected",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_kind: "api",
    launch_source_type: "durable_advisor",
    launch_source_id: "project-advisor",
    work_root: "/tmp/project",
    wake_defaults: { reason: "manual" },
    ...overrides,
  };
}

function profile(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
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
    ...overrides,
  };
}

function recipePlan(
  overrides: Partial<DurableAgentRecipePlan> = {},
): DurableAgentRecipePlan {
  const instance = durableAgent({
    id: "durable-created",
    name: "Created Advisor",
  });
  return {
    recipe_id: "project-advisor",
    recipe_schema_version: 1,
    instance,
    launch_policy: launchPlan({ instance_id: instance.id }),
    wake_payload: { reason: "manual" },
    session_policy: "reuse_latest_or_create",
    would_create_session: true,
    would_reuse_session: false,
    ready: true,
    ...overrides,
  };
}

function mockBaseApi(agent = durableAgent()) {
  vi.spyOn(api, "listDurableAgents").mockResolvedValue([agent]);
  vi.spyOn(api, "getDurableAgent").mockResolvedValue(agent);
  vi.spyOn(api, "listDurableAgentSessions").mockResolvedValue([attachment()]);
  vi.spyOn(api, "listDurableAgentSchedules").mockResolvedValue([schedule()]);
  vi.spyOn(api, "getDurableAgentLaunchPlan").mockResolvedValue(launchPlan());
  vi.spyOn(api, "listDurableAgentEvents").mockResolvedValue([durableEvent()]);
  vi.spyOn(api, "listDurableAgentDueWake").mockResolvedValue([dueWakeItem()]);
  vi.spyOn(api, "runDurableAgentDueWake").mockResolvedValue({
    now: "2026-05-24T00:00:00Z",
    dry_run: false,
    results: [],
  } satisfies DurableAgentWakeRunResult);
  vi.spyOn(api, "listDurableAgentRecipes").mockResolvedValue([recipe()]);
  vi.spyOn(api, "listAgents").mockResolvedValue([profile()]);
}

describe("DurableAgentAdminPanel", () => {
  it("renders list, detail overview, activity, sessions, and launch plan", async () => {
    mockBaseApi();

    renderWithClient(<DurableAgentAdminPanel />);

    expect(await screen.findAllByText("Project Advisor")).not.toHaveLength(0);
    expect(screen.getByText("advisor / api")).toBeTruthy();
    expect(
      screen.getAllByText("Anthropic / claude-sonnet-4").length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("session-1")).toBeTruthy();
    expect(screen.getByText(/1 wake item/i)).toBeTruthy();
    expect(await screen.findByText("Start Failed")).toBeTruthy();
    expect(screen.getByText("Runtime exited before ready.")).toBeTruthy();
    expect(screen.getByText("Failure: binary not found")).toBeTruthy();
    expect(await screen.findByText("reuse_latest_or_create")).toBeTruthy();
    expect(screen.getAllByText("primary").length).toBeGreaterThan(0);
  });

  it("wires lifecycle actions and opens returned start session", async () => {
    const agent = durableAgent();
    mockBaseApi(agent);
    const eventsSpy = vi.mocked(api.listDurableAgentEvents);
    const startSpy = vi.spyOn(api, "startDurableAgent").mockResolvedValue({
      instance: durableAgent({
        status: "active",
        current_session_id: "session-2",
      }),
      policy: launchPlan(),
      session: session(),
      created_session: true,
      reused_session: false,
    });
    const resumeSpy = vi.spyOn(api, "resumeDurableAgent").mockResolvedValue({
      instance: durableAgent({ status: "active" }),
      policy: launchPlan(),
      session: session({ id: "session-3" }),
      created_session: false,
      reused_session: true,
    });
    const pauseSpy = vi
      .spyOn(api, "requestDurableAgentPause")
      .mockResolvedValue(agent);
    const stopSpy = vi
      .spyOn(api, "requestDurableAgentStop")
      .mockResolvedValue(agent);
    const archiveSpy = vi
      .spyOn(api, "archiveDurableAgent")
      .mockResolvedValue(agent);

    renderWithClient(<DurableAgentAdminPanel />);

    fireEvent.click(
      await screen.findByRole("button", { name: "Start durable agent" }),
    );
    await waitFor(() => expect(startSpy).toHaveBeenCalledWith("durable-1", {}));
    await waitFor(() => expect(eventsSpy.mock.calls.length).toBeGreaterThan(1));
    await waitFor(() =>
      expect(useAppStore.getState().activeSessionId).toBe("session-2"),
    );
    expect(useLayoutStore.getState().currentPage).toBe("chat");

    useLayoutStore.setState({ currentPage: "settings" });
    fireEvent.click(
      screen.getByRole("button", { name: "Resume durable agent" }),
    );
    await waitFor(() =>
      expect(resumeSpy).toHaveBeenCalledWith("durable-1", {}),
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Request pause durable agent" }),
    );
    await waitFor(() => expect(pauseSpy).toHaveBeenCalledWith("durable-1"));

    fireEvent.click(
      screen.getByRole("button", { name: "Request stop durable agent" }),
    );
    await waitFor(() => expect(stopSpy).toHaveBeenCalledWith("durable-1"));

    fireEvent.click(
      screen.getByRole("button", { name: "Archive durable agent" }),
    );
    await waitFor(() => expect(archiveSpy).toHaveBeenCalledWith("durable-1"));
  });

  it("opens attached sessions from the sessions table", async () => {
    mockBaseApi();

    renderWithClient(<DurableAgentAdminPanel />);

    fireEvent.click(await screen.findByRole("button", { name: "Open" }));

    expect(useAppStore.getState().activeSessionId).toBe("session-1");
    expect(useLayoutStore.getState().currentPage).toBe("chat");
  });

  it("renders wake controls and schedules for process/template agents", async () => {
    mockBaseApi(
      durableAgent({
        lifecycle_class: "process",
        launch_source_type: "process_tick",
      }),
    );
    const wakeSpy = vi.spyOn(api, "wakeDurableAgent").mockResolvedValue({
      instance_id: "durable-1",
      wake_reason: "process_tick",
      skipped: false,
    });
    const runDueSpy = vi
      .spyOn(api, "runDurableAgentDueWake")
      .mockResolvedValue({
        now: "2026-05-24T00:00:00Z",
        dry_run: false,
        results: [],
      });

    renderWithClient(<DurableAgentAdminPanel />);

    expect(await screen.findByText("Nightly wake")).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: "Wake durable agent now" }),
    );
    await waitFor(() => expect(wakeSpy).toHaveBeenCalledWith("durable-1", {}));

    fireEvent.click(screen.getByRole("button", { name: "Run due wake pass" }));
    await waitFor(() => expect(runDueSpy).toHaveBeenCalledWith(false));
  });

  it("supports recipe dry-run and apply creation", async () => {
    mockBaseApi();
    const plan = recipePlan();
    const dryRunSpy = vi
      .spyOn(api, "dryRunDurableAgentRecipe")
      .mockResolvedValue(plan);
    const applyResult: DurableAgentRecipeApplyResult = {
      plan,
      instance: plan.instance,
      launch_result: {
        instance: plan.instance,
        policy: launchPlan({ instance_id: plan.instance.id }),
        session: session({ id: "session-created" }),
        created_session: true,
        reused_session: false,
      },
    };
    const applySpy = vi
      .spyOn(api, "applyDurableAgentRecipe")
      .mockResolvedValue(applyResult);

    renderWithClient(<DurableAgentAdminPanel />);

    fireEvent.click(await screen.findByText("Create from recipe"));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Created Advisor" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));

    await waitFor(() =>
      expect(dryRunSpy).toHaveBeenCalledWith(
        "project-advisor",
        expect.objectContaining({ name: "Created Advisor", start: true }),
      ),
    );
    expect(await screen.findByText("Ready to apply")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() =>
      expect(applySpy).toHaveBeenCalledWith(
        "project-advisor",
        expect.objectContaining({ name: "Created Advisor", start: true }),
      ),
    );
    await waitFor(() =>
      expect(useAppStore.getState().activeSessionId).toBe("session-created"),
    );
  });

  it("renders clear empty states", async () => {
    vi.spyOn(api, "listDurableAgents").mockResolvedValue([]);
    vi.spyOn(api, "listDurableAgentDueWake").mockResolvedValue([]);
    vi.spyOn(api, "runDurableAgentDueWake").mockResolvedValue({
      now: "2026-05-24T00:00:00Z",
      dry_run: false,
      results: [],
    } satisfies DurableAgentWakeRunResult);
    vi.spyOn(api, "listDurableAgentRecipes").mockResolvedValue([]);
    vi.spyOn(api, "listAgents").mockResolvedValue([]);

    renderWithClient(<DurableAgentAdminPanel />);

    expect(
      await screen.findByText(
        "No durable agents yet. Use New agent to create one from a recipe.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "New agent" })).toBeTruthy();
    expect(screen.getByText("No recipes available")).toBeTruthy();
    expect(
      screen.getByText("Select a durable agent to inspect it."),
    ).toBeTruthy();
  });
});
