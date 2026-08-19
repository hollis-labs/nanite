import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { LeftRailSessionRow } from "@/components/chat/LeftRail";
import {
  buildSidebarSessionTree,
  buildSidebarSessionTreeSections,
  deriveSidebarActivityState,
  deriveSidebarSessionKind,
  deriveSidebarSessionSummary,
  type SidebarPresenceMaps,
} from "@/lib/sidebar-session";
import type { Session } from "@/lib/types";

const emptyPresence: SidebarPresenceMaps = {
  activeStreams: new Map(),
  pendingTools: new Map(),
  cliActiveSessions: new Map(),
};

function session(overrides: Partial<Session> = {}): Session {
  return {
    id: "session-1",
    short_code: "c1",
    title: "Chat",
    custom_name: "",
    project_id: "project-1",
    context_type: null,
    context_id: null,
    provider: "anthropic",
    model: "claude-sonnet-4",
    status: "active",
    is_pinned: false,
    sort_order: 0,
    message_count: 0,
    tags: "[]",
    last_activity: "2026-05-23T00:00:00Z",
    created_at: "2026-05-23T00:00:00Z",
    ...overrides,
  };
}

describe("sidebar session kind derivation", () => {
  it("distinguishes API, CLI/boot-profile, and durable-agent sessions", () => {
    expect(deriveSidebarSessionKind(session())).toBe("api");
    expect(deriveSidebarSessionKind(session({ provider: "pty-claude" }))).toBe(
      "cli",
    );
    expect(
      deriveSidebarSessionKind(
        session({ provider: "bootprofile:claude-smoke" }),
      ),
    ).toBe("cli");
    expect(
      deriveSidebarSessionKind(
        session({
          context_type: "durable_agent",
          context_id: "agent-1",
          provider: "pty-claude",
        }),
      ),
    ).toBe("durable");
  });

  it("derives compact metadata labels", () => {
    expect(deriveSidebarSessionSummary(session()).metadataLabel).toBe(
      "Anthropic / claude-sonnet-4",
    );
    expect(
      deriveSidebarSessionSummary(
        session({ provider: "bootprofile:claude-smoke" }),
      ).metadataLabel,
    ).toBe("boot / claude-smoke");
    expect(
      deriveSidebarSessionSummary(
        session({ context_type: "durable_agent", context_id: "agent-1" }),
      ).metadataLabel,
    ).toBe("durable / agent-1");
  });
});

describe("sidebar activity derivation", () => {
  it("prioritizes archived and failure states over live presence", () => {
    const presence: SidebarPresenceMaps = {
      activeStreams: new Map([["session-1", {}]]),
      pendingTools: new Map([["session-1", {}]]),
      cliActiveSessions: new Map([["session-1", {}]]),
    };

    expect(
      deriveSidebarActivityState(session({ status: "archived" }), presence)
        .state,
    ).toBe("archived");
    expect(
      deriveSidebarActivityState(session({ status: "failed" }), presence).state,
    ).toBe("failed");
    expect(
      deriveSidebarActivityState(
        session({ halted_at: "2026-05-24T00:00:00Z" }),
        presence,
      ).state,
    ).toBe("halted");
  });

  it("prioritizes pending, working, online, then idle", () => {
    expect(
      deriveSidebarActivityState(session(), {
        activeStreams: new Map([["session-1", {}]]),
        pendingTools: new Map([["session-1", {}]]),
        cliActiveSessions: new Map(),
      }).state,
    ).toBe("pending_action");

    expect(
      deriveSidebarActivityState(session(), {
        activeStreams: new Map([["session-1", {}]]),
        pendingTools: new Map(),
        cliActiveSessions: new Map([["session-1", {}]]),
      }).state,
    ).toBe("working");

    expect(
      deriveSidebarActivityState(session(), {
        activeStreams: new Map(),
        pendingTools: new Map(),
        cliActiveSessions: new Map([["session-1", {}]]),
      }).state,
    ).toBe("online");

    expect(deriveSidebarActivityState(session(), emptyPresence).state).toBe(
      "idle",
    );
    expect(
      deriveSidebarActivityState(session({ status: "stopped" }), emptyPresence)
        .state,
    ).toBe("stopped");
  });
});

describe("sidebar session tree derivation", () => {
  it("leaves flat sessions unchanged", () => {
    const rows = buildSidebarSessionTree([
      session({ id: "parent-a", title: "A" }),
      session({ id: "parent-b", title: "B" }),
    ]);

    expect(rows.map((row) => row.session.id)).toEqual(["parent-a", "parent-b"]);
    expect(rows.map((row) => row.depth)).toEqual([0, 0]);
  });

  it("nests child sessions under their parent", () => {
    const rows = buildSidebarSessionTree([
      session({ id: "parent", title: "Parent" }),
      session({
        id: "child",
        title: "Child",
        parent_session_id: "parent",
        root_session_id: "parent",
        relation: "subagent",
        depth: 1,
      }),
      session({ id: "sibling", title: "Sibling" }),
    ]);

    expect(rows.map((row) => [row.session.id, row.depth])).toEqual([
      ["parent", 0],
      ["child", 1],
      ["sibling", 0],
    ]);
  });

  it("falls back safely when a child parent is missing", () => {
    const rows = buildSidebarSessionTree([
      session({
        id: "orphan-child",
        parent_session_id: "missing-parent",
        root_session_id: "missing-parent",
        relation: "subagent",
        depth: 1,
      }),
    ]);

    expect(rows).toHaveLength(1);
    expect(rows[0]?.session.id).toBe("orphan-child");
    expect(rows[0]?.depth).toBe(0);
    expect(rows[0]?.missingParent).toBe(true);
  });

  it("derives activity state for child rows by child session id", () => {
    const child = session({ id: "child", parent_session_id: "parent" });

    expect(
      deriveSidebarActivityState(child, {
        activeStreams: new Map(),
        pendingTools: new Map([["child", {}]]),
        cliActiveSessions: new Map(),
      }).state,
    ).toBe("pending_action");
  });

  it("keeps unpinned children under pinned parents in the pinned tree", () => {
    const { pinnedRows, unpinnedRows } = buildSidebarSessionTreeSections(
      [session({ id: "parent", title: "Parent", is_pinned: true })],
      [
        session({
          id: "child",
          title: "Child",
          parent_session_id: "parent",
          root_session_id: "parent",
          relation: "subagent",
          depth: 1,
        }),
        session({ id: "recent", title: "Recent" }),
      ],
    );

    expect(pinnedRows.map((row) => [row.session.id, row.depth])).toEqual([
      ["parent", 0],
      ["child", 1],
    ]);
    expect(unpinnedRows.map((row) => [row.session.id, row.depth])).toEqual([
      ["recent", 0],
    ]);
  });
});

describe("LeftRailSessionRow sidebar metadata", () => {
  it("renders kind labels, metadata, and activity labels accessibly", () => {
    render(
      <LeftRailSessionRow
        title="Durable advisor"
        timeLabel="now"
        kindIcon={<span aria-hidden="true">kind</span>}
        kindLabel="Durable agent session"
        metadataLabel="durable / agent-1"
        statusIndicator={
          <span role="img" aria-label="Working now" title="Working now" />
        }
        onClick={() => undefined}
        onTogglePin={() => undefined}
        onToggleArchive={() => undefined}
        pinned={false}
        onStartEdit={() => undefined}
        onCommitEdit={() => undefined}
        onCancelEdit={() => undefined}
      />,
    );

    expect(screen.getByText("Durable advisor")).toBeTruthy();
    expect(screen.getByText("durable / agent-1")).toBeTruthy();
    expect(screen.getByLabelText("Durable agent session")).toBeTruthy();
    expect(screen.getByLabelText("Working now")).toBeTruthy();
  });

  it("renders an elbow connector for nested rows", () => {
    render(
      <LeftRailSessionRow
        title="Child subagent"
        timeLabel="now"
        kindIcon={<span aria-hidden="true">kind</span>}
        kindLabel="CLI harness session"
        metadataLabel="stdio / claude"
        statusIndicator={<span role="img" aria-label="Idle" title="Idle" />}
        nestingDepth={1}
        onClick={() => undefined}
        onTogglePin={() => undefined}
        onToggleArchive={() => undefined}
        pinned={false}
        onStartEdit={() => undefined}
        onCommitEdit={() => undefined}
        onCancelEdit={() => undefined}
      />,
    );

    expect(
      screen.getByText("Child subagent").closest("[role='button']")?.className,
    ).toContain("pl-16");
  });

  it("fires row actions from nested child rows", () => {
    const onToggleArchive = vi.fn();
    const onTogglePin = vi.fn();

    render(
      <LeftRailSessionRow
        title="Child subagent"
        timeLabel="now"
        kindIcon={<span aria-hidden="true">kind</span>}
        kindLabel="CLI harness session"
        metadataLabel="stdio / claude"
        statusIndicator={<span role="img" aria-label="Idle" title="Idle" />}
        nestingDepth={1}
        onClick={() => undefined}
        onTogglePin={onTogglePin}
        onToggleArchive={onToggleArchive}
        pinned={false}
        onStartEdit={() => undefined}
        onCommitEdit={() => undefined}
        onCancelEdit={() => undefined}
      />,
    );

    fireEvent.click(screen.getByLabelText("Archive"));
    fireEvent.click(screen.getByLabelText("Pin"));

    expect(onToggleArchive).toHaveBeenCalledTimes(1);
    expect(onTogglePin).toHaveBeenCalledTimes(1);
  });
});
