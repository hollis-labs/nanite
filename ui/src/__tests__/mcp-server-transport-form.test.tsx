import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ToolDashboard } from "@/components/settings/ToolDashboard";
import { api } from "@/lib/api";
import type { MCPServerConfig } from "@/lib/types";

const legacy: MCPServerConfig = {
  id: "retained",
  name: "legacy",
  transport_type: "sse",
  command: "",
  url: "https://example.invalid/sse",
  args: "[]",
  env: "[]",
  enabled: true,
  created_at: "",
  updated_at: "",
};

beforeEach(() => {
  vi.spyOn(api, "fetchToolServers").mockResolvedValue([]);
  vi.spyOn(api, "listMCPServers").mockResolvedValue([]);
  vi.spyOn(api, "fetchTools").mockResolvedValue([]);
  vi.spyOn(api, "fetchAllToolsWithLoadType").mockResolvedValue([]);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function dashboard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  render(
    <QueryClientProvider client={client}>
      <ToolDashboard />
    </QueryClientProvider>,
  );
}

describe("MCP transport form", () => {
  it("creates Streamable HTTP with the operator's endpoint and offers no legacy transport", async () => {
    const add = vi
      .spyOn(api, "addMCPServer")
      .mockResolvedValue({ ...legacy, transport_type: "streamable" });
    dashboard();
    fireEvent.click(screen.getByRole("button", { name: "Add Server" }));
    expect(screen.queryByRole("option", { name: /Legacy SSE/ })).toBeNull();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "remote" } });
    fireEvent.change(screen.getByLabelText("Transport Type"), { target: { value: "streamable" } });
    fireEvent.change(screen.getByLabelText("URL"), {
      target: { value: "https://example.invalid/verified" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add Server" }));
    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({
          name: "remote",
          transport_type: "streamable",
          url: "https://example.invalid/verified",
        }),
      ),
    );
  });

  it("exposes an unregistered legacy configuration and disables it without rewriting its URL or type", async () => {
    vi.mocked(api.listMCPServers).mockResolvedValue([legacy]);
    const update = vi
      .spyOn(api, "updateMCPServer")
      .mockResolvedValue({ ...legacy, enabled: false });
    dashboard();
    fireEvent.click(await screen.findByRole("button", { name: "Edit legacy" }));
    expect((screen.getByLabelText("Transport Type") as HTMLSelectElement).value).toBe("sse");
    expect((screen.getByLabelText("URL") as HTMLInputElement).value).toBe(legacy.url);
    fireEvent.click(screen.getByLabelText("Enabled"));
    fireEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() =>
      expect(update).toHaveBeenCalledWith(
        "legacy",
        expect.objectContaining({
          transport_type: "sse",
          url: legacy.url,
          enabled: false,
        }),
      ),
    );
  });

  it("changes legacy transport only on explicit selection and preserves the URL until edited", async () => {
    vi.mocked(api.listMCPServers).mockResolvedValue([legacy]);
    const update = vi
      .spyOn(api, "updateMCPServer")
      .mockResolvedValue({ ...legacy, transport_type: "streamable" });
    dashboard();
    fireEvent.click(await screen.findByRole("button", { name: "Edit legacy" }));
    fireEvent.change(screen.getByLabelText("Transport Type"), { target: { value: "streamable" } });
    expect((screen.getByLabelText("URL") as HTMLInputElement).value).toBe(legacy.url);
    fireEvent.change(screen.getByLabelText("URL"), {
      target: { value: "https://example.invalid/verified" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save Changes" }));
    await waitFor(() =>
      expect(update).toHaveBeenCalledWith(
        "legacy",
        expect.objectContaining({
          transport_type: "streamable",
          url: "https://example.invalid/verified",
        }),
      ),
    );
  });
});
