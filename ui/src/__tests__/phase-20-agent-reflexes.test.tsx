import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentReflexesPanel } from "@/components/settings/agents/AgentReflexesPanel";
import { api } from "@/lib/api";
import type { AgentReflexRow, PendingReflexRow, ValidateReflexResponse } from "@/lib/types";

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

function reflex(overrides: Partial<AgentReflexRow> = {}): AgentReflexRow {
  return {
    id: "reflex-1",
    agent_id: "",
    class_tag: "advisor",
    name: "base-reflex",
    trigger_kind: "predicate",
    trigger_spec: '{"kind":"tool_calls_window","window":2,"op":"=","value":0}',
    action_kind: "inject_reminder",
    action_spec: '{"body":"Ground yourself."}',
    status: "active",
    priority: 75,
    fired_count: 2,
    last_fired_at: "",
    created_at: "2026-05-24T00:00:00Z",
    created_by: "seed",
    ...overrides,
  };
}

function pendingReflex(overrides: Partial<PendingReflexRow> = {}): PendingReflexRow {
  return {
    id: "pending-1",
    proposed_by: "agent",
    proposed_at: "2026-05-24T00:00:00Z",
    target_agent_id: "agent-1",
    name: "mail-reminder",
    trigger_kind: "predicate",
    trigger_spec: '{"kind":"mail_unread_count","op":">=","value":1}',
    action_kind: "inject_reminder",
    action_spec: '{"body":"Check mail."}',
    rationale: "Mail keeps getting missed.",
    status: "pending",
    reviewed_at: "",
    reviewed_by: "",
    ...overrides,
  };
}

function validationResult(overrides: Partial<ValidateReflexResponse> = {}): ValidateReflexResponse {
  return {
    valid: true,
    errors: [],
    fired: true,
    state_source: "request",
    state_summary: {
      messages: 2,
      user_messages: 1,
      events: 0,
      mail_unread_count: 0,
      tick_n: 0,
      prefix_tokens: 0,
    },
    ...overrides,
  };
}

describe("Phase 20 agent reflexes panel", () => {
  it("renders inherited rows as read-only and scoped rows with edit/delete actions", async () => {
    vi.spyOn(api, "listAgentReflexes").mockResolvedValue([
      reflex(),
      reflex({
        id: "reflex-2",
        agent_id: "agent-1",
        class_tag: "",
        name: "agent-reflex",
        created_by: "operator",
      }),
    ]);
    vi.spyOn(api, "listPendingReflexes").mockResolvedValue([]);

    renderWithClient(<AgentReflexesPanel agentId="agent-1" isReadOnly={false} />);

    expect(await screen.findByText("base-reflex")).toBeTruthy();
    expect(screen.getByText("agent-reflex")).toBeTruthy();
    expect(screen.getByText("Inherited")).toBeTruthy();
    expect(screen.getByText("Scoped")).toBeTruthy();
    expect(screen.queryByLabelText("Edit reflex base-reflex")).toBeNull();
    expect(screen.queryByLabelText("Delete reflex base-reflex")).toBeNull();
    expect(screen.getByLabelText("Edit reflex agent-reflex")).toBeTruthy();
    expect(screen.getByLabelText("Delete reflex agent-reflex")).toBeTruthy();
  });

  it("renders validation results for reflex dry-runs", async () => {
    vi.spyOn(api, "listAgentReflexes").mockResolvedValue([]);
    vi.spyOn(api, "listPendingReflexes").mockResolvedValue([]);
    const validateSpy = vi
      .spyOn(api, "validateReflex")
      .mockResolvedValue(validationResult({ fired: true, state_source: "store" }));

    renderWithClient(<AgentReflexesPanel agentId="agent-1" isReadOnly={false} />);

    expect(await screen.findByText("No reflexes configured for this agent yet.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "New Reflex" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "check-mail" } });
    fireEvent.click(screen.getByRole("button", { name: "Validate" }));

    await waitFor(() => expect(validateSpy).toHaveBeenCalled());
    expect(await screen.findByText("Validation Result")).toBeTruthy();
    expect(screen.getByText("Would fire")).toBeTruthy();
    expect(screen.getByText("State source: store")).toBeTruthy();
  });

  it("approves pending reflexes and refreshes the pending queue", async () => {
    const pendingRows = [pendingReflex()];
    vi.spyOn(api, "listAgentReflexes").mockResolvedValue([]);
    const pendingSpy = vi
      .spyOn(api, "listPendingReflexes")
      .mockResolvedValueOnce(pendingRows)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([]);
    const approveSpy = vi.spyOn(api, "approvePendingReflex").mockResolvedValue(
      reflex({
        id: "reflex-approved",
        agent_id: "agent-1",
        class_tag: "",
        name: "mail-reminder",
      }),
    );

    renderWithClient(<AgentReflexesPanel agentId="agent-1" isReadOnly={false} />);

    expect(await screen.findByText("mail-reminder")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));

    await waitFor(() => expect(approveSpy).toHaveBeenCalledWith("pending-1", { reviewed_by: "operator-ui" }));
    await waitFor(() =>
      expect(screen.getByText("No pending reflex proposals for this agent.")).toBeTruthy(),
    );
    expect(pendingSpy).toHaveBeenCalled();
  });

  it("rejects pending reflexes", async () => {
    vi.spyOn(api, "listAgentReflexes").mockResolvedValue([]);
    vi.spyOn(api, "listPendingReflexes").mockResolvedValue([pendingReflex()]);
    const rejectSpy = vi
      .spyOn(api, "rejectPendingReflex")
      .mockResolvedValue({ id: "pending-1", status: "rejected" });

    renderWithClient(<AgentReflexesPanel agentId="agent-1" isReadOnly={false} />);

    expect(await screen.findByText("mail-reminder")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));

    await waitFor(() => expect(rejectSpy).toHaveBeenCalledWith("pending-1", { reviewed_by: "operator-ui" }));
  });

  it("disables pending review actions for read-only agent profiles", async () => {
    vi.spyOn(api, "listAgentReflexes").mockResolvedValue([]);
    vi.spyOn(api, "listPendingReflexes").mockResolvedValue([pendingReflex()]);

    renderWithClient(<AgentReflexesPanel agentId="agent-1" isReadOnly />);

    expect(await screen.findByText("mail-reminder")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect((screen.getByRole("button", { name: "Reject" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });

  it("shows empty and error states", async () => {
    vi.spyOn(api, "listAgentReflexes").mockRejectedValue(new Error("boom"));
    vi.spyOn(api, "listPendingReflexes").mockResolvedValue([]);

    renderWithClient(<AgentReflexesPanel agentId="agent-1" isReadOnly={false} />);

    expect(await screen.findByText("Failed to load reflexes: boom")).toBeTruthy();
    expect(screen.getByText("No pending reflex proposals for this agent.")).toBeTruthy();
  });
});
