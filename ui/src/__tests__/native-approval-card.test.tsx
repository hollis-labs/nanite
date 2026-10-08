import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApprovalCard } from "@/components/chat/ApprovalCard";
import { api } from "@/lib/api";
import type { PendingApproval } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  useAppStore.setState({ activeSessionId: null });
  useChatStore.getState().clearPendingApprovals("view");
});

function mountApproval(native: boolean) {
  const approval: PendingApproval = {
    request_id: "approval",
    tool: "dev_write",
    input: { path: "example.txt" },
    reason: "Permission required",
    receivedAt: Date.now(),
    ...(native ? { run_id: "run", call_id: "call", supported_scopes: ["once" as const] } : {}),
  };
  useAppStore.setState({ activeSessionId: "view" });
  useChatStore.getState().addPendingApproval("view", approval);
  render(<ApprovalCard approval={approval} />);
}

describe("native permission approval card", () => {
  it("offers once scope and answers the native resource", async () => {
    const native = vi.spyOn(api, "respondToAgentApproval").mockResolvedValue({
      approval_id: "approval", session_view_id: "view", run_id: "run", call_id: "call", decision: "allow", scope: "once",
    });
    const retained = vi.spyOn(api, "respondToApproval");
    mountApproval(true);
    expect(screen.queryByRole("button", { name: "Allow for Session" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Allow Once" }));
    await waitFor(() => expect(native).toHaveBeenCalledWith("view", "approval", "allow"));
    await waitFor(() => expect(useChatStore.getState().sessions.get("view")?.pendingApprovals[0].resolved).toEqual({ decision: "allow", scope: "once" }));
    expect(retained).not.toHaveBeenCalled();
  });

  it("preserves the separate retained admin session decision", async () => {
    const retained = vi.spyOn(api, "respondToApproval").mockResolvedValue({ status: "accepted" });
    const native = vi.spyOn(api, "respondToAgentApproval");
    mountApproval(false);
    fireEvent.click(screen.getByRole("button", { name: "Allow for Session" }));
    await waitFor(() => expect(retained).toHaveBeenCalledWith("view", "approval", "allow", "session"));
    expect(native).not.toHaveBeenCalled();
  });
});
