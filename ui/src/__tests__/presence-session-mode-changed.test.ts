/**
 * F1 (CW-20260429-0001) — verify the presence-event dispatcher routes a
 * `session_mode_changed` SSE event to the right query invalidations so a
 * second tab open on the same session refreshes its mode chip without
 * manual refetch. Pure unit test against the extracted dispatcher; no
 * EventSource / React tree required.
 */

import { describe, expect, it } from "vitest";

import { dispatchPresenceEvent, type PresenceHandlers } from "@/hooks/usePresence";
import type { PresenceEvent } from "@/lib/types";

interface InvalidationCall {
  queryKey: unknown;
}

function makeHandlers(): { handlers: PresenceHandlers; invalidations: InvalidationCall[] } {
  const invalidations: InvalidationCall[] = [];
  const handlers: PresenceHandlers = {
    setActiveStream: () => {},
    removeActiveStream: () => {},
    setPendingTool: () => {},
    removePendingTool: () => {},
    setCLIActive: () => {},
    removeCLIActive: () => {},
    queryClient: {
      invalidateQueries: ((filters: { queryKey: unknown }) => {
        invalidations.push({ queryKey: filters.queryKey });
        return Promise.resolve();
      }) as PresenceHandlers["queryClient"]["invalidateQueries"],
    },
  };
  return { handlers, invalidations };
}

describe("dispatchPresenceEvent — session_mode_changed", () => {
  it("invalidates session-mode and session queries on mode change", () => {
    const { handlers, invalidations } = makeHandlers();
    const evt: PresenceEvent = {
      type: "session_mode_changed",
      session_id: "sess-42",
      mode_id: "mode-plan",
      mode_slug: "plan",
      timestamp: "2026-04-29T01:00:00Z",
    };

    dispatchPresenceEvent(evt, handlers);

    expect(invalidations).toHaveLength(2);
    expect(invalidations[0].queryKey).toEqual(["session-mode", "sess-42"]);
    expect(invalidations[1].queryKey).toEqual(["session", "sess-42"]);
  });

  it("invalidates the same queries on the clear path (empty mode_id/slug)", () => {
    const { handlers, invalidations } = makeHandlers();
    const evt: PresenceEvent = {
      type: "session_mode_changed",
      session_id: "sess-99",
      // No mode_id / mode_slug — the BE clear path emits an empty event.
      timestamp: "2026-04-29T01:01:00Z",
    };

    dispatchPresenceEvent(evt, handlers);

    expect(invalidations).toHaveLength(2);
    expect(invalidations[0].queryKey).toEqual(["session-mode", "sess-99"]);
    expect(invalidations[1].queryKey).toEqual(["session", "sess-99"]);
  });

  it("does not invalidate session-mode for unrelated event types", () => {
    const { handlers, invalidations } = makeHandlers();
    // sanity: existing session_archived event should NOT touch session-mode.
    const evt: PresenceEvent = {
      type: "session_archived",
      session_id: "sess-other",
      timestamp: "2026-04-29T01:02:00Z",
    };

    dispatchPresenceEvent(evt, handlers);

    // Only the existing ['sessions'] invalidation should fire.
    expect(invalidations).toHaveLength(1);
    expect(invalidations[0].queryKey).toEqual(["sessions"]);
  });
});
