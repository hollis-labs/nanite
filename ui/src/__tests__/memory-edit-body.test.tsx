import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryBrowse } from "@/components/memory/MemoryBrowse";
import { MemoryDetail } from "@/components/memory/MemoryDetail";

const memoryListResponse = {
  memories: [
    {
      key: "encoded-path-key",
      memory_key: "review_summary",
      namespace: "user/default/memory/notes",
      summary: "Review summary",
      body: "PRESERVE-ME",
      origin: "user",
      trigger: "manual",
      confidence: 0.8,
      tags: [],
      scope: "user",
      session_id: "manual:nanite",
      revision_id: "revision-1",
      status: "draft",
      created_at: "2026-09-04T00:00:00Z",
      updated_at: "2026-09-04T00:00:00Z",
    },
  ],
  total: 1,
};

function renderWithClient(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("memory editing", () => {
  it("loads the full body and writes that exact body back", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PUT") {
        return new Response(JSON.stringify({}), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(
        JSON.stringify(memoryListResponse),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    renderWithClient(<MemoryDetail memoryKey="encoded-path-key" onBack={() => {}} />);

    const body = await screen.findByDisplayValue("PRESERVE-ME");
    expect(body).toBeInstanceOf(HTMLTextAreaElement);
    fireEvent.change(screen.getByDisplayValue("Review summary"), {
      target: { value: "Edited summary" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const updateCall = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
      expect(updateCall).toBeDefined();
      expect(String(updateCall?.[0])).toContain("/memories/encoded-path-key");
      expect(JSON.parse(String(updateCall?.[1]?.body))).toMatchObject({
        summary: "Edited summary",
        body: "PRESERVE-ME",
      });
    });
  });

  it("opens an item with the API's path-safe key", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(JSON.stringify(memoryListResponse), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    const onSelect = vi.fn();
    renderWithClient(<MemoryBrowse onSelect={onSelect} onCreate={() => {}} />);

    fireEvent.click(await screen.findByText("Review summary"));
    expect(onSelect).toHaveBeenCalledWith("encoded-path-key");
  });
});
