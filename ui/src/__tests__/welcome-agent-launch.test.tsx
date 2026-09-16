import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WelcomeScreen } from "@/components/chat/ChatMain";
import { api } from "@/lib/api";
import type { AgentProfile, Session } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";

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

function agent(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
    id: "agent-1",
    name: "ESS Agent",
    slug: "ess",
    avatar: "🧑‍💼",
    icon: "",
    system_prompt: "",
    description: "Answers your own Workday questions — time off and training.",
    modes: "",
    default_model: "",
    mcp_servers: "",
    tool_permissions: "",
    can_execute: true,
    settings: "",
    created_at: "2026-09-15T00:00:00Z",
    updated_at: "2026-09-15T00:00:00Z",
    agent_hash: "",
    version: 1,
    tools: "",
    directories: "",
    constraints: "",
    tags: '["workday","hr"]',
    status: "active",
    source: "managed",
    source_ref: "",
    ...overrides,
  } as AgentProfile;
}

function session(id: string): Session {
  return {
    id,
    short_code: "c1",
    title: "New",
    custom_name: "",
    project_id: "",
    context_type: null,
    context_id: null,
    provider: "openai",
    model: "gpt-5.6-terra",
    status: "active",
    is_pinned: false,
    sort_order: 0,
    message_count: 0,
    tags: "[]",
    last_activity: "2026-09-15T00:00:00Z",
    created_at: "2026-09-15T00:00:00Z",
  } as Session;
}

describe("welcome screen agent launch", () => {
  it("offers a card per agent, with its description and tags", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue([
      agent(),
      agent({
        id: "agent-2",
        name: "ESS Agent — cards",
        slug: "ess-cards",
        description: "The same answers, rendered as cards.",
        tags: '["envelopes"]',
      }),
    ]);

    renderWithClient(<WelcomeScreen />);

    expect(await screen.findByText("ESS Agent")).toBeTruthy();
    expect(screen.getByText("ESS Agent — cards")).toBeTruthy();
    expect(
      screen.getByText("Answers your own Workday questions — time off and training."),
    ).toBeTruthy();
    expect(screen.getByText("workday")).toBeTruthy();
  });

  it("starts a session bound to the agent that was clicked", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue([
      agent(),
      agent({ id: "agent-2", name: "Arek ESS", slug: "ess-arek" }),
    ]);
    const create = vi.spyOn(api, "createSession").mockResolvedValue(session("sess-new"));

    renderWithClient(<WelcomeScreen />);

    fireEvent.click(await screen.findByText("Arek ESS"));

    await waitFor(() => {
      // The agent must ride on the create call — a session started without one
      // lands on the default agent, which is the bug this surface exists to avoid.
      expect(create).toHaveBeenCalledWith({ agent_id: "agent-2" });
    });
    await waitFor(() => {
      expect(useAppStore.getState().activeSessionId).toBe("sess-new");
    });
  });

  it("hides disabled agents", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue([
      agent(),
      agent({ id: "agent-2", name: "Retired Agent", slug: "retired", status: "disabled" }),
    ]);

    renderWithClient(<WelcomeScreen />);

    expect(await screen.findByText("ESS Agent")).toBeTruthy();
    expect(screen.queryByText("Retired Agent")).toBeNull();
  });

  it("falls back to the setup cards when there is no agent to offer", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue([]);

    renderWithClient(<WelcomeScreen />);

    // A first run must not land on an empty screen.
    expect(await screen.findByText("Connect a provider")).toBeTruthy();
  });

  it("caps the grid and points the rest at the launcher", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue(
      Array.from({ length: 9 }, (_, i) =>
        agent({ id: `agent-${i}`, name: `Agent ${i}`, slug: `agent-${i}` }),
      ),
    );

    renderWithClient(<WelcomeScreen />);

    expect(await screen.findByText("Agent 0")).toBeTruthy();
    expect(screen.getByText("Agent 5")).toBeTruthy();
    expect(screen.queryByText("Agent 6")).toBeNull();
    expect(screen.getByText(/3 more/)).toBeTruthy();
  });

  it("surfaces a failure instead of appearing to do nothing", async () => {
    vi.spyOn(api, "listAgents").mockResolvedValue([agent()]);
    vi.spyOn(api, "createSession").mockRejectedValue(new Error("Failed to create session: 500"));

    renderWithClient(<WelcomeScreen />);

    fireEvent.click(await screen.findByText("ESS Agent"));

    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByText(/Failed to create session/)).toBeTruthy();
  });
});
