import { AlertCircle, CheckCircle2, Loader2, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

// Install state machine mirrored from internal/plugin/install/install.go.
// The SSE stream at /api/plugins/events is shared with other plugin
// lifecycle events; we filter to ones whose `state` is in this set and
// whose `plugin_id` matches.
const INSTALL_STATES = new Set([
  "not_installed",
  "downloading",
  "verifying",
  "extracting",
  "validating",
  "loading",
  "ready",
  "failed",
]);

type InstallState =
  | "not_installed"
  | "downloading"
  | "verifying"
  | "extracting"
  | "validating"
  | "loading"
  | "ready"
  | "failed";

interface InstallEvent {
  plugin_id: string;
  state: InstallState;
  progress?: number;
  message?: string;
  err?: string;
}

const STATE_LABELS: Record<InstallState, string> = {
  not_installed: "Preparing",
  downloading: "Downloading",
  verifying: "Verifying",
  extracting: "Extracting",
  validating: "Validating",
  loading: "Loading",
  ready: "Ready",
  failed: "Failed",
};

interface PluginInstallProgressProps {
  pluginId: string | null;
  /** Is the install mutation still pending? Used for the fallback. */
  pending?: boolean;
  onClose: () => void;
}

export function PluginInstallProgress({ pluginId, pending, onClose }: PluginInstallProgressProps) {
  const [event, setEvent] = useState<InstallEvent | null>(null);
  const [timedOutNoEvents, setTimedOutNoEvents] = useState(false);
  const receivedAnyRef = useRef(false);

  // Reset state whenever pluginId changes.
  // biome-ignore lint/correctness/useExhaustiveDependencies: pluginId identity is the reset trigger
  useEffect(() => {
    setEvent(null);
    setTimedOutNoEvents(false);
    receivedAnyRef.current = false;
  }, [pluginId]);

  // Subscribe to SSE stream while a pluginId is active.
  //
  // The backend ships events as named SSE events in the envelope
  //   { type, source, timestamp, data, session_id }
  // (plugin.Event from plugin-sdk). Install-progress fields live under `data`:
  //   data.plugin_id, data.state, data.progress, data.message, data.err
  // We also accept a top-level shape as a fallback in case the bridge
  // forwards install.Event directly without wrapping.
  useEffect(() => {
    if (!pluginId) return;
    const es = new EventSource("/api/plugins/events");

    const handlePayload = (payload: Record<string, unknown>) => {
      const envelopeData =
        payload.data && typeof payload.data === "object"
          ? (payload.data as Record<string, unknown>)
          : payload;

      const rawState =
        typeof envelopeData.state === "string" ? envelopeData.state : undefined;
      const rawPluginId =
        typeof envelopeData.plugin_id === "string" ? envelopeData.plugin_id : undefined;
      if (!rawState || !rawPluginId) return;
      if (rawPluginId !== pluginId) return;
      if (!INSTALL_STATES.has(rawState)) return;

      receivedAnyRef.current = true;
      setEvent({
        plugin_id: rawPluginId,
        state: rawState as InstallState,
        progress:
          typeof envelopeData.progress === "number" ? envelopeData.progress : undefined,
        message:
          typeof envelopeData.message === "string" ? envelopeData.message : undefined,
        err: typeof envelopeData.err === "string" ? envelopeData.err : undefined,
      });
    };

    const onEvent = (e: MessageEvent) => {
      let payload: Record<string, unknown> | null = null;
      try {
        payload = JSON.parse(e.data);
      } catch {
        return;
      }
      if (!payload) return;
      handlePayload(payload);
    };

    // Existing lifecycle event types. The G.6 follow-up will add a dedicated
    // install-progress event type; listen for both the known terminal names
    // and a forward-looking "plugin.install.progress" name.
    const eventNames = [
      "plugin.installed",
      "plugin.load_failed",
      "plugin.install.progress",
      "install",
    ];
    for (const name of eventNames) {
      es.addEventListener(name, onEvent as EventListener);
    }
    // Also handle default (unnamed) messages if the bridge emits those.
    es.onmessage = onEvent;

    return () => {
      es.close();
    };
  }, [pluginId]);

  // Fallback: after 10s with no install events, mark as "unbridged".
  useEffect(() => {
    if (!pluginId) return;
    const t = setTimeout(() => {
      if (!receivedAnyRef.current) setTimedOutNoEvents(true);
    }, 10_000);
    return () => clearTimeout(t);
  }, [pluginId]);

  // Auto-close 2s after ready.
  useEffect(() => {
    if (event?.state !== "ready") return undefined;
    const t = setTimeout(() => onClose(), 2000);
    return () => clearTimeout(t);
  }, [event?.state, onClose]);

  if (!pluginId) return null;

  const state = event?.state;
  const isReady = state === "ready";
  const isFailed = state === "failed";
  const progressValue = typeof event?.progress === "number" ? event.progress : undefined;
  const hasDeterminate =
    (state === "downloading" || state === "extracting") && (progressValue ?? 0) > 0;

  // Tone classes.
  const toneBorder = isReady
    ? "border-emerald-600/40"
    : isFailed
      ? "border-red-600/50"
      : "border-indigo-500/40";
  const toneBg = isReady ? "bg-emerald-600/5" : isFailed ? "bg-red-600/5" : "bg-zinc-950/80";

  return (
    <div
      className={`rounded-xl border ${toneBorder} ${toneBg} p-3 shadow-sm`}
      role="status"
      aria-live="polite"
    >
      <div className="flex items-center gap-2">
        {isReady ? (
          <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
        ) : isFailed ? (
          <AlertCircle className="w-4 h-4 text-red-500 shrink-0" />
        ) : (
          <Loader2 className="w-4 h-4 text-indigo-400 shrink-0 animate-spin" />
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-fg truncate">{pluginId}</span>
            <span className="text-[11px] text-fg-muted shrink-0">
              {state ? STATE_LABELS[state] : "Installing…"}
            </span>
          </div>
          {event?.message && !isFailed && (
            <p className="text-[11px] text-fg-muted mt-0.5 truncate">{event.message}</p>
          )}
          {isFailed && (
            <p className="text-[11px] text-red-400 mt-0.5">
              {event?.message || "Install failed"}
              {event?.err ? ` — ${event.err}` : ""}
            </p>
          )}
          {!event && timedOutNoEvents && pending && (
            <p className="text-[11px] text-fg-faint mt-0.5">
              Installing… (progress stream not yet wired)
            </p>
          )}
        </div>
        {isFailed && (
          <button
            type="button"
            onClick={onClose}
            className="p-1 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
            aria-label="Close"
          >
            <X className="w-3.5 h-3.5" />
          </button>
        )}
      </div>

      {/* Progress bar */}
      {!isReady && !isFailed && (
        <div className="mt-2 h-1 w-full overflow-hidden rounded bg-zinc-800">
          {hasDeterminate ? (
            <div
              className="h-full bg-indigo-500 transition-all"
              style={{ width: `${Math.min(100, Math.max(0, (progressValue ?? 0) * 100))}%` }}
            />
          ) : (
            <div className="h-full w-full animate-pulse bg-indigo-500/70" />
          )}
        </div>
      )}
    </div>
  );
}
