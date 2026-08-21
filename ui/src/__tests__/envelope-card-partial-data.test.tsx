/**
 * Regression: core envelope cards must not crash on partially-loaded data.
 *
 * A `report-card` envelope reached the renderer with `data.metrics`
 * undefined (observed in smoke-test session c209 — a failed pointer
 * resolve delivers a partial `data` object). `ReportCard` did an
 * unguarded `data.metrics.length`, throwing
 * "Cannot read properties of undefined (reading 'length')", which the
 * EnvelopeErrorBoundary surfaced as "Envelope failed: report-card".
 *
 * ListCard / TimelineCard / TableCard shared the same unguarded
 * `.length` / spread pattern on their required arrays. These tests pin
 * that every core card renders (degrading gracefully) when its required
 * array field is missing, instead of throwing.
 */

import type { ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ReportCard } from "@/components/chat/envelopes/ReportCard";
import { ListCard } from "@/components/chat/envelopes/primitives/ListCard";
import { ConfirmationCard } from "@/components/chat/envelopes/primitives/ConfirmationCard";
import { TimelineCard } from "@/components/chat/envelopes/primitives/TimelineCard";
import { TableCard } from "@/components/chat/envelopes/primitives/TableCard";
import { DiffCard } from "@/components/chat/envelopes/primitives/DiffCard";
import { ArtifactMiniCard } from "@/components/chat/envelopes/ArtifactMiniCard";

afterEach(cleanup);

/**
 * Wrap a card needing react-query (the plan-review composition's live
 * `list-card`/`confirmation-card` branches — TASKS/phase-6/02-rebuild-
 * plan-review-as-composition.md) in a fresh QueryClient.
 */
function renderWithQuery(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("envelope cards tolerate partially-loaded data", () => {
  it("ReportCard renders with metrics missing", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(<ReportCard data={{ title: "Smoke Report" } as any} />);
    expect(container.textContent).toContain("Smoke Report");
  });

  it("ListCard renders with items missing", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(<ListCard data={{ title: "Empty List" } as any} />);
    expect(container.textContent).toContain("0 items");
  });

  it("TimelineCard renders with events missing", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(<TimelineCard data={{ title: "Empty Timeline" } as any} />);
    expect(container.textContent).toContain("0 events");
  });

  it("TableCard renders with columns and rows missing", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(<TableCard data={{ title: "Empty Table" } as any} />);
    expect(container.textContent).toContain("0 rows");
  });

  // CW-20260517-0007 Issue 3: DiffCard did unguarded data.before.label /
  // data.after.content — a partial envelope missing before/after threw.
  it("DiffCard renders with before and after missing", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(<DiffCard data={{ title: "Empty Diff" } as any} />);
    expect(container.textContent).toContain("Empty Diff");
  });

  it("DiffCard renders with only before present", () => {
    const { container } = render(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      <DiffCard data={{ before: { label: "old", content: "x" } } as any} />,
    );
    expect(container.textContent).toContain("old");
  });

  // CW-20260517-0007 ArtifactMiniCard audit: data.name was unguarded when
  // `data` is present-but-empty (the `!data` guard only covers wholly-absent).
  it("ArtifactMiniCard renders with name missing on a present data object", () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(<ArtifactMiniCard data={{} as any} />);
    expect(container.textContent).toContain("Untitled artifact");
  });

  // CW-20260517-0007 Issue 4 (originally pinned against the now-retired
  // PlanReviewCard): the plan-review composition's live `list-card` half
  // must not throw when it arrives with no static `items` snapshot to
  // fall back on while the live plan fetch is still in flight.
  it("ListCard (plans data_source) renders with items missing", () => {
    const { container } = renderWithQuery(
      <ListCard
        data={
          {
            title: "Partial Plan",
            data_source: { kind: "plans", plan_id: "p1" },
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
          } as any
        }
      />,
    );
    expect(container.textContent).toContain("Partial Plan");
  });

  // Same regression, `confirmation-card` half: must not throw when it
  // arrives before the live plan fetch resolves.
  it("ConfirmationCard (plan_approval data_source) renders with plan unresolved", () => {
    const { container } = renderWithQuery(
      <ConfirmationCard
        envelope={
          {
            kind: "envelope",
            version: 1,
            type: "confirmation-card",
            data: {
              title: "Partial Plan",
              message: "Approve this plan?",
              data_source: { kind: "plan_approval", plan_id: "p1" },
            },
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
          } as any
        }
      />,
    );
    expect(container.textContent).toContain("Partial Plan");
  });
});
