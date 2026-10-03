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

it("shows tool activation changes before approval", () => {
  const before: PluginInstallReview = {
    id: "tool-reader", name: "Tool reader", version: "1.0.0",
    bundle_digest: "old", entrypoint: "plugin", arguments: [],
    capabilities: [], secrets: [], environment: [],
    tools: [{ name: "bookmarks_list", effect: "read" }], tool_load_type: "opt-in",
  };
  const after: PluginInstallReview = { ...before, version: "2.0.0", tool_load_type: "auto" };
  render(<PluginReviewDialog review={{ status: "review_required", review: after, review_digest: "reviewed", previous: { review: before, review_digest: "old" } }} busy={false} onCancel={vi.fn()} onApprove={vi.fn()} />);
  expect(screen.getByText("Tool loading · Changed")).toBeTruthy();
  expect(screen.getByText("Before: opt-in")).toBeTruthy();
  expect(screen.getByText("After: auto")).toBeTruthy();
});

it("shows reflex target, reminder and predicate changes before approval", () => {
  const before: PluginInstallReview = {
    id: "nanite.loom", name: "Loom", version: "1.0.0",
    bundle_digest: "old", entrypoint: "plugin", arguments: [],
    capabilities: [], secrets: [], environment: [], tools: [],
    reflex_seeds: [{ id: "search-first", agent_slug: "loom-weaver", reminder: "Search first", trigger: { kind: "tool_calls_window", window: 2, op: "=", value: 0 } }],
  };
  const after: PluginInstallReview = { ...before, reflex_seeds: [{ ...before.reflex_seeds![0], agent_slug: "loom-curator", reminder: "Search again" }] };
  const onApprove = vi.fn();
  render(<PluginReviewDialog review={{ status: "review_required", review: after, review_digest: "reviewed", previous: { review: before, review_digest: "old" } }} busy={false} onCancel={vi.fn()} onApprove={onApprove} />);
  expect(screen.getByText("Reflex reminder: search-first · Changed")).toBeTruthy();
  expect(screen.getByText(/Before:/).textContent).toContain("loom-weaver");
  const changed = screen.getByText(/After:/).textContent;
  expect(changed).toContain("loom-curator");
  expect(changed).toContain("Search again");
  expect(changed).toContain("tool_calls_window");
  expect(onApprove).not.toHaveBeenCalled();
});

it("shows the host-authored persistent context warning before approval", () => {
  const before: PluginInstallReview = {
    id: "nanite.pins", name: "Pins", version: "1.0.0", bundle_digest: "old",
    entrypoint: "pins", arguments: [], capabilities: [], secrets: [], environment: [], tools: [],
  };
  const after: PluginInstallReview = { ...before, capabilities: [{ name: "context.always_ship" }], host_notice: "Pins adds text to the system prompt; up to 1500 uncached tokens per turn; use pins_list." };
  const onApprove = vi.fn();
  render(<PluginReviewDialog review={{ status: "review_required", review: after, review_digest: "new", previous: { review: before, review_digest: "old" } }} busy={false} onCancel={vi.fn()} onApprove={onApprove} />);
  const warning = screen.getByText("Persistent context warning · Added");
  expect(warning).toBeTruthy();
  const capability = screen.getByText("Capability: context.always_ship · Added");
  expect(warning.compareDocumentPosition(capability) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(warning.parentElement?.className).toContain("border-2");
  expect(warning.parentElement?.className).toContain("amber");
  expect(screen.getByText(/After: Pins adds text/).textContent).toContain("system prompt");
  expect(screen.getByText(/After: Pins adds text/).textContent).toContain("1500 uncached tokens");
  expect(screen.getByText(/After: Pins adds text/).textContent).toContain("pins_list");
  expect(onApprove).not.toHaveBeenCalled();
});
