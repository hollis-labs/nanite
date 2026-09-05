import { useEffect, useState } from "react";
import {
  type HostRuntimeFeedEvent,
  type HostRuntimeFeedGap,
  type HostRuntimeFeedState,
  initialHostRuntimeFeedState,
  reduceHostRuntimeEvent,
  reduceHostRuntimeGap,
} from "@/lib/host-runtime-feed";

export function useHostRuntimeFeed(sessionID: string | null): HostRuntimeFeedState {
  const [state, setState] = useState<HostRuntimeFeedState>(initialHostRuntimeFeedState);

  useEffect(() => {
    setState(initialHostRuntimeFeedState);
    if (!sessionID || typeof EventSource === "undefined") return;

    const source = new EventSource(`/api/sessions/${encodeURIComponent(sessionID)}/runtime-events`);
    const onRuntimeEvent = (rawEvent: Event) => {
      try {
        const event = JSON.parse((rawEvent as MessageEvent<string>).data) as HostRuntimeFeedEvent;
        if (event.session_id !== sessionID) return;
        setState((current) => reduceHostRuntimeEvent(current, event));
      } catch {
        // Malformed or newer incompatible data is ignored; EventSource stays
        // connected so one bad record cannot stop subsequent replay.
      }
    };
    const onGap = (rawEvent: Event) => {
      try {
        const gap = JSON.parse((rawEvent as MessageEvent<string>).data) as HostRuntimeFeedGap;
        if (gap.session_id !== sessionID) return;
        setState((current) => reduceHostRuntimeGap(current, gap));
      } catch {
        // See onRuntimeEvent: tolerate a malformed control record.
      }
    };

    source.addEventListener("host_runtime.v1", onRuntimeEvent);
    source.addEventListener("host_runtime.gap.v1", onGap);
    source.onerror = () => {
      // Native EventSource reconnect preserves Last-Event-ID. Do not close
      // here: the backend's committed cursor makes replay idempotent.
    };
    return () => {
      source.removeEventListener("host_runtime.v1", onRuntimeEvent);
      source.removeEventListener("host_runtime.gap.v1", onGap);
      source.close();
    };
  }, [sessionID]);

  return state;
}
