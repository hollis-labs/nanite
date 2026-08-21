/**
 * Phase 6 — interactive-table row-actions primitive (see
 * docs/engineering/architecture/08-cards.md and
 * TASKS/phase-6/04-build-interactive-table-row-actions-primitive.md).
 *
 * Pins the frontend half of the emit → render → click → respond round
 * trip: given a schema-shaped table-card `data` payload with row/column
 * `actions`, clicking a rendered action button calls `onRespond` with the
 * exact ResponseV1 partial payload the backend's TableCardActionHandler
 * expects (action_id, row_index, row, and column_key for column-scoped
 * actions).
 */

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TableCard } from "@/components/chat/envelopes/primitives/TableCard";

afterEach(cleanup);

const baseData = {
  title: "Open incidents",
  columns: [
    { key: "id", label: "ID" },
    {
      key: "status",
      label: "Status",
      actions: [{ id: "resolve", label: "Resolve" }],
    },
  ],
  rows: [
    { id: "INC-1", status: "open" },
    { id: "INC-2", status: "open" },
  ],
  actions: [
    { id: "approve", label: "Approve" },
    { id: "reject", label: "Reject", style: "destructive" as const, confirm: true, confirm_message: "Reject?" },
  ],
};

describe("TableCard row/column actions", () => {
  it("renders no action buttons when onRespond is not supplied (passive/no-id envelope)", () => {
    render(<TableCard data={baseData} />);
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("clicking a row action posts action_id + row_index + row via onRespond", async () => {
    const onRespond = vi.fn().mockResolvedValue(undefined);
    render(<TableCard data={baseData} onRespond={onRespond} />);

    const buttons = screen.getAllByRole("button", { name: "Approve" });
    fireEvent.click(buttons[1]); // second row (INC-2)

    expect(onRespond).toHaveBeenCalledTimes(1);
    expect(onRespond).toHaveBeenCalledWith({
      status: "submitted",
      data: {
        action_id: "approve",
        row_index: 1,
        row: { id: "INC-2", status: "open" },
      },
    });
  });

  it("clicking a column action includes column_key in the response payload", async () => {
    const onRespond = vi.fn().mockResolvedValue(undefined);
    render(<TableCard data={baseData} onRespond={onRespond} />);

    const buttons = screen.getAllByRole("button", { name: "Resolve" });
    fireEvent.click(buttons[0]); // first row (INC-1)

    expect(onRespond).toHaveBeenCalledWith({
      status: "submitted",
      data: {
        action_id: "resolve",
        row_index: 0,
        row: { id: "INC-1", status: "open" },
        column_key: "status",
      },
    });
  });

  it("a confirm:true action asks for confirmation and is skipped when the user declines", () => {
    const onRespond = vi.fn().mockResolvedValue(undefined);
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<TableCard data={baseData} onRespond={onRespond} />);

    const buttons = screen.getAllByRole("button", { name: "Reject" });
    fireEvent.click(buttons[0]);

    expect(confirmSpy).toHaveBeenCalledWith("Reject?");
    expect(onRespond).not.toHaveBeenCalled();
    confirmSpy.mockRestore();
  });

  it("row order for action responses is unaffected by column sorting", async () => {
    const onRespond = vi.fn().mockResolvedValue(undefined);
    const data = {
      ...baseData,
      columns: [{ key: "id", label: "ID", sortable: true }, baseData.columns[1]],
      rows: [
        { id: "B", status: "open" },
        { id: "A", status: "open" },
      ],
    };
    render(<TableCard data={data} onRespond={onRespond} />);

    // Sort ascending by ID — visually reorders A above B.
    fireEvent.click(screen.getByRole("button", { name: /ID/ }));

    const approveButtons = screen.getAllByRole("button", { name: "Approve" });
    // After sorting, the first rendered row is "A", whose ORIGINAL index
    // in the unsorted server payload is 1.
    fireEvent.click(approveButtons[0]);

    expect(onRespond).toHaveBeenCalledWith({
      status: "submitted",
      data: {
        action_id: "approve",
        row_index: 1,
        row: { id: "A", status: "open" },
      },
    });
  });

  it("degrades to read-only once prior_response is present", () => {
    const onRespond = vi.fn().mockResolvedValue(undefined);
    render(
      <TableCard
        data={{
          ...baseData,
          prior_response: { v: 1, kind: "table-card", id: "env-1", status: "submitted", data: { action_id: "approve", row_index: 0 } },
        }}
        onRespond={onRespond}
      />,
    );

    const buttons = screen.getAllByRole("button", { name: "Approve" });
    expect((buttons[0] as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(buttons[0]);
    expect(onRespond).not.toHaveBeenCalled();
    expect(screen.getByText(/already recorded/i)).toBeTruthy();
  });
});
