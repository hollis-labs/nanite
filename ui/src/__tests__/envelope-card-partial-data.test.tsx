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

import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ReportCard } from "@/components/chat/envelopes/ReportCard";
import { ListCard } from "@/components/chat/envelopes/primitives/ListCard";
import { TimelineCard } from "@/components/chat/envelopes/primitives/TimelineCard";
import { TableCard } from "@/components/chat/envelopes/primitives/TableCard";

afterEach(cleanup);

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
});
