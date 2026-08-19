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

import { ChatHeader } from "@/components/chat/ChatHeader";
import { SessionDetailsPanel } from "@/components/chat/SessionDetailsPanel";
import { api } from "@/lib/api";
import type {
  AgentProfile,
  DurableAgentInstance,
  Session,
  SessionDetailsResponse,
} from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";

const queryClients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of queryClients) client.clear();
  queryClients.length = 0;
  vi.restoreAllMocks();
  localStorage.clear();
  useAppStore.setState({
    activeProjectId: null,
    activeSessionId: null,
    configVersion: 0,
  });
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

function session(overrides: Partial<Session> = {}): Session {
  return {
    id: "session-1",
    short_code: "c1",
    title: "Session One",
    custom_name: "",
    project_id: "project-1",
    context_type: null,
    context_id: null,
    provider: "anthropic",
    model: "claude-sonnet-4",
    status: "active",
    is_pinned: false,
    sort_order: 0,
    message_count: 3,
    tags: "[]",
    last_activity: "2026-05-24T00:00:00Z",
    created_at: "2026-05-24T00:00:00Z",
    ...overrides,
  };
}

function agent(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
    id: "agent-1",
    name: "Project Agent",
    slug: "project-agent",
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

function durableAgent(
  overrides: Partial<DurableAgentInstance> = {},
): DurableAgentInstance {
  return {
    id: "durable-1",
    name: "Durable Harness",
    slug: "durable-harness",
    profile_id: "agent-1",
    lifecycle_class: "harness",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_kind: "streaming-stdio",
    launch_source_type: "boot_profile",
    launch_source_id: "claude-smoke",
    work_root: "/tmp/work",
    status: "active",
    current_session_id: "session-1",
    failure_reason: "",
    metadata_json: "{}",
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

function details(
  overrides: Partial<SessionDetailsResponse> = {},
): SessionDetailsResponse {
  return {
    session: session(),
    primary_agent: agent(),
    durable_attachments: [],
    current_durable_agent: null,
    activity_state: "idle",
    last_activity_at: "2026-05-24T00:00:00Z",
    last_useful_activity_at: "2026-05-24T00:00:00Z",
    halt: { is_halted: false },
    usage: null,
    recent_durable_events: [],
    runtime: {
      state: "none",
    },
    boot_source: "api_default",
    immutable_start_fields: ["provider", "model"],
    checkpoint: { status: "not_available" },
    ...overrides,
  };
}

function mockHeaderApi(response: SessionDetailsResponse) {
  vi.spyOn(api, "getSession").mockResolvedValue({
    ...response.session,
    messages: [],
  });
  vi.spyOn(api, "listSessionAgents").mockResolvedValue([]);
  vi.spyOn(api, "fetchTools").mockResolvedValue([]);
  vi.spyOn(api, "getContextBreakdown").mockResolvedValue({
    system_prompt_tokens: 0,
    system_prompt_preview: "",
    messages: [],
    message_tokens_total: 0,
    tools: [],
    tool_tokens_total: 0,
    tools_available: 0,
    total: 0,
    ceiling: 0,
    estimated_cost_usd: 0,
  });
  vi.spyOn(api, "listAgents").mockResolvedValue([]);
  vi.spyOn(api, "listUISlots").mockResolvedValue({});
  return vi.spyOn(api, "getSessionDetails").mockResolvedValue(response);
}

describe("SessionDetailsPanel", () => {
  it("details entry opens and calls api.getSessionDetails", async () => {
    const response = details();
    const getSessionDetails = mockHeaderApi(response);
    useAppStore.getState().setActiveSession("session-1");

    renderWithClient(<ChatHeader />);

    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Session details" }),
    );

    await waitFor(() =>
      expect(getSessionDetails).toHaveBeenCalledWith("session-1"),
    );
    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(screen.getByText("Session One")).toBeTruthy();
  });

  it("renders API chat sessions with runtime none", async () => {
    vi.spyOn(api, "getSessionDetails").mockResolvedValue(details());

    renderWithClient(
      <SessionDetailsPanel
        sessionId="session-1"
        open={true}
        onOpenChange={() => undefined}
      />,
    );

    expect(
      await screen.findByText(
        "Runtime state is none. This session has no resident runtime process.",
      ),
    ).toBeTruthy();
    expect(
      screen.getAllByText("Anthropic / claude-sonnet-4").length,
    ).toBeGreaterThan(0);
    expect(screen.getByText("api default")).toBeTruthy();
    expect(screen.getAllByText("idle").length).toBeGreaterThan(0);
  });

  it("renders CLI/runtime-backed session runtime facts", async () => {
    vi.spyOn(api, "getSessionDetails").mockResolvedValue(
      details({
        session: session({ provider: "pty-claude", model: "claude-cli" }),
        activity_state: "failed",
        usage: {
          input_tokens: 100,
          output_tokens: 221,
          total_tokens: 321,
          tool_input_tokens: 0,
          cache_creation_tokens: 0,
          cache_read_tokens: 10,
          estimated_cost_usd: 0.1234,
          message_count: 2,
        },
        boot_source: "legacy_cli",
        runtime: {
          state: "failed",
          runtime_id: "runtime-1",
          runtime_kind: "streaming-stdio",
          provider: "pty-claude",
          pid: 4242,
          boot_dir: "/tmp/boot",
          workspace_dir: "/tmp/workspace",
          provider_session_id: "provider-session-1",
          failure_reason: "Runtime exited before ready.",
          started_at: "2026-05-24T00:01:00Z",
          updated_at: "2026-05-24T00:02:00Z",
        },
      }),
    );

    renderWithClient(
      <SessionDetailsPanel
        sessionId="session-1"
        open={true}
        onOpenChange={() => undefined}
      />,
    );

    expect(await screen.findByText("streaming-stdio")).toBeTruthy();
    expect(screen.getByText("4242")).toBeTruthy();
    expect(screen.getByText("provider-session-1")).toBeTruthy();
    expect(screen.getByText("/tmp/workspace")).toBeTruthy();
    expect(screen.getByText("/tmp/boot")).toBeTruthy();
    expect(screen.getByText("Runtime exited before ready.")).toBeTruthy();
    expect(screen.getByText("$0.1234")).toBeTruthy();
  });

  it("renders durable-agent attachment and current instance", async () => {
    vi.spyOn(api, "getSessionDetails").mockResolvedValue(
      details({
        session: session({
          context_type: "durable_agent",
          context_id: "durable-1",
        }),
        boot_source: "durable_agent",
        current_durable_agent: durableAgent(),
        durable_attachments: [
          {
            instance_id: "durable-1",
            session_id: "session-1",
            relation: "harness",
            attached_at: "",
            session_status: "active",
            provider: "anthropic",
            model: "claude-sonnet-4",
            runtime_state: "running",
            runtime_failure_reason: "binary missing",
            halted_reason: "detector_B",
          },
        ],
        recent_durable_events: [
          {
            id: "evt-1",
            instance_id: "durable-1",
            event_type: "start_failed",
            status_before: "starting",
            status_after: "failed",
            session_id: "session-1",
            source: "api",
            message: "Runtime exited before ready.",
            metadata_json: "{}",
            created_at: "2026-05-24T00:03:00Z",
          },
        ],
      }),
    );

    renderWithClient(
      <SessionDetailsPanel
        sessionId="session-1"
        open={true}
        onOpenChange={() => undefined}
      />,
    );

    expect(await screen.findByText("Durable Harness")).toBeTruthy();
    expect(screen.getAllByText("harness").length).toBeGreaterThan(0);
    expect(screen.getByText("instance durable-1")).toBeTruthy();
    expect(screen.getByText("runtime running")).toBeTruthy();
    expect(screen.getByText(/failure binary missing/i)).toBeTruthy();
    expect(screen.getByText("Runtime exited before ready.")).toBeTruthy();
  });

  it("renders missing and empty fields gracefully", async () => {
    vi.spyOn(api, "getSessionDetails").mockResolvedValue(
      details({
        session: session({
          title: "",
          short_code: "",
          project_id: "",
          provider: "",
          model: "",
        }),
        primary_agent: null,
        activity_state: "",
        immutable_start_fields: [],
        runtime: { state: "none" },
        checkpoint: { status: "" },
      }),
    );

    renderWithClient(
      <SessionDetailsPanel
        sessionId="session-1"
        open={true}
        onOpenChange={() => undefined}
      />,
    );

    expect(await screen.findByText("Untitled")).toBeTruthy();
    expect(screen.getByText("None attached")).toBeTruthy();
    expect(screen.getAllByText("None").length).toBeGreaterThan(3);
  });

  it("routes restart through fork and navigates to the new session", async () => {
    const response = details();
    mockHeaderApi(response);
    const forkSession = vi
      .spyOn(api, "forkSession")
      .mockResolvedValue(
        session({ id: "session-restarted", message_count: 0 }),
      );
    useAppStore.getState().setActiveSession("session-1");

    renderWithClient(<ChatHeader />);

    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Session details" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Restart as new session" }),
    );

    await waitFor(() => {
      expect(forkSession).toHaveBeenCalledWith("session-1", {
        include_messages: false,
        provider: "anthropic",
        model: "claude-sonnet-4",
      });
      expect(useAppStore.getState().activeSessionId).toBe("session-restarted");
    });
  });

  it("recovers the active session from the details panel", async () => {
    const response = details();
    mockHeaderApi(response);
    const recoverSession = vi
      .spyOn(api, "recoverSession")
      .mockResolvedValue("recovered");
    useAppStore.getState().setActiveSession("session-1");

    renderWithClient(<ChatHeader />);

    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Session details" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Recover session" }),
    );

    await waitFor(() => {
      expect(recoverSession).toHaveBeenCalledWith("session-1");
    });
  });
});
