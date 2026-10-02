import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { PluginInstallReview, PluginInstallReviewResponse } from "@/lib/types";
import { PluginReviewDialog } from "./PluginReviewDialog";

afterEach(cleanup);

it("shows scope widening and content access before approval", () => {
  const before: PluginInstallReview = {
    id: "query-reader", name: "Query reader", version: "1.0.0",
    bundle_digest: "old", entrypoint: "plugin", arguments: [],
    capabilities: [{ name: "readonly.query", metadata: { resources: ["context_slots"], session_ids: ["allowed-session"] } }],
    secrets: [], environment: [], tools: [],
  };
  const after: PluginInstallReview = {
    ...before, version: "2.0.0", bundle_digest: "new",
    capabilities: [{ name: "readonly.query", metadata: { resources: ["context_slots"], all_sessions: true, include_content: true } }],
  };
  const review: PluginInstallReviewResponse = {
    status: "review_required", review: after, review_digest: "reviewed",
    previous: { review: before, review_digest: "old-reviewed" },
  };
  const onApprove = vi.fn();
  render(<PluginReviewDialog review={review} busy={false} onCancel={vi.fn()} onApprove={onApprove} />);
  expect(screen.getByText("Capability: readonly.query · Changed")).toBeTruthy();
  expect(screen.getByText(/Before:/).textContent).toContain("allowed-session");
  const requested = screen.getByText(/After:/).textContent;
  expect(requested).toContain('"all_sessions": true');
  expect(requested).toContain('"include_content": true');
  expect(requested).not.toContain("undefined");
  expect(onApprove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Approve and install" }));
  expect(onApprove).toHaveBeenCalledOnce();
});
