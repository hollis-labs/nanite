import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AgentPicker } from "@/components/chat/AgentPicker";
import { api } from "@/lib/api";
import type { AgentProfile } from "@/lib/types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("offers active readonly profiles without disabled or already assigned profiles", async () => {
  const rows = [
    {
      id: "active",
      name: "Readonly active",
      slug: "active",
      source: "plugin",
      status: "active",
      tags: "[]",
    },
    {
      id: "disabled",
      name: "Disabled retained",
      slug: "disabled",
      source: "managed",
      status: "disabled",
      tags: "[]",
    },
    {
      id: "assigned",
      name: "Already assigned",
      slug: "assigned",
      source: "internal",
      status: "active",
      tags: "[]",
    },
  ] as AgentProfile[];
  vi.spyOn(api, "listAgents").mockResolvedValue(rows);
  const add = vi.spyOn(api, "addSessionAgent").mockResolvedValue({
    session_id: "session",
    agent_id: "active",
    mode: "participant",
    joined_at: "2026-10-09T00:00:00Z",
    is_primary: false,
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <AgentPicker sessionId="session" existingAgentIds={["assigned"]} onClose={() => {}} />
    </QueryClientProvider>,
  );
  await screen.findByText("Readonly active");
  expect(screen.queryByText("Disabled retained")).toBeNull();
  expect(screen.queryByText("Already assigned")).toBeNull();
  fireEvent.click(screen.getByText("Readonly active"));
  await waitFor(() => expect(add).toHaveBeenCalledWith("session", "active", "participant"));
  // The presentation helper does not mutate source rows or confer edit rights.
  expect(rows[1]?.status).toBe("disabled");
  expect(rows[0]?.source).toBe("plugin");
});
