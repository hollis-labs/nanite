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
import { TimelineCard } from "@/components/chat/envelopes/primitives/TimelineCard";
import { TableCard } from "@/components/chat/envelopes/primitives/TableCard";
import { DiffCard } from "@/components/chat/envelopes/primitives/DiffCard";
import { ArtifactMiniCard } from "@/components/chat/envelopes/ArtifactMiniCard";
import { PlanReviewCard } from "@/components/chat/envelopes/PlanReviewCard";

afterEach(cleanup);

/** Wrap a card needing react-query (PlanReviewCard) in a fresh QueryClient. */
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

  // CW-20260517-0007 Issue 4: PlanReviewCard did data.steps.map() unguarded —
  // a partial envelope missing `steps` threw.
  it("PlanReviewCard renders with steps missing", () => {
    const { container } = renderWithQuery(
      <PlanReviewCard
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        data={{ plan_id: "p1", title: "Partial Plan", status: "proposed" } as any}
      />,
    );
    expect(container.textContent).toContain("Partial Plan");
  });
});
