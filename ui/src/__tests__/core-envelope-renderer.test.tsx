import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EnvelopeRenderer } from "@/components/chat/envelopes/EnvelopeRenderer";
import type { Envelope } from "@/lib/types";

// Keep the generated registry and its lazy-loaded React cards real. Stub only
// app state/services so this render proof needs no server or installed plugins.
vi.mock("@/hooks/useSettings", () => ({ useSettings: () => ({ data: { recover_mode: false } }) }));
vi.mock("@/lib/plugin-loader", () => ({
  getDynamicEnvelope: () => undefined,
  getEnvelopePluginId: () => undefined,
  getPluginLoadError: () => undefined,
  getRegistryVersion: () => 0,
  subscribeRegistry: () => () => {},
}));

beforeEach(() =>
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(new Response("{}", { headers: { "Content-Type": "application/json" } })),
  ),
);
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderEnvelope(envelope: Envelope, respond = vi.fn().mockResolvedValue(undefined)) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return {
    ...render(
      <QueryClientProvider client={client}>
        <EnvelopeRenderer envelope={envelope} onEnvelopeResponse={respond} />
      </QueryClientProvider>,
    ),
    respond,
  };
}

function envelope(type: string, data: Record<string, unknown>): Envelope {
  return { kind: "agent_envelope", version: 1, id: `env-${type}`, type, data };
}

describe("host-owned core bindings through the real EnvelopeRenderer", () => {
  it("lazy-loads a passive card with data props", async () => {
    const view = renderEnvelope(
      envelope("info-card", { title: "Catalog adoption", body: "Real core renderer" }),
    );
    await view.findByText("Catalog adoption");
    expect(view.container.textContent).toContain("Real core renderer");
    expect(view.container.textContent).not.toContain("Unsupported envelope");
  });

  it.each([
    "approval-card",
    "subagent-spawn-approval",
  ])("hydrates %s through full-envelope props", async (type) => {
    const env = envelope(
      type,
      type === "approval-card"
        ? { description: "Fixture approval", risk_level: "low" }
        : { run_id: "run-fixture", role: "reviewer", prompt: "Fixture review", mode: "sync" },
    );
    env.prior_response = {
      v: 1,
      kind: type,
      id: `env-${type}`,
      status: "submitted",
      data: { approved: true },
    };
    const view = renderEnvelope(env);
    await waitFor(() => expect(view.container.textContent?.toLowerCase()).toContain("approved"));
    expect(view.container.textContent).not.toContain("Approval required");
  });

  it("loads an interactive card and submits its typed response", async () => {
    const env = envelope("confirmation-card", {
      title: "Fixture confirmation",
      message: "Confirm fixture?",
      confirm_label: "Accept fixture",
    });
    const view = renderEnvelope(env);
    fireEvent.click(await view.findByRole("button", { name: "Accept fixture" }));
    await waitFor(() =>
      expect(view.respond).toHaveBeenCalledWith({
        v: 1,
        kind: "confirmation-card",
        id: env.id,
        status: "submitted",
        data: { confirmed: true },
      }),
    );
  });
});
