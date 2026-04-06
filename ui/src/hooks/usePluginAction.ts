import { useCallback } from "react";
import { useAppStore } from "@/stores/useAppStore";
import { api } from "@/lib/api";
import type { UISlotEntry } from "@/lib/types";

/**
 * Returns a stable callback that dispatches a plugin slot entry's action.
 * Handles: command, navigate, handler, modal action types.
 */
export function usePluginAction() {
  const activeSessionId = useAppStore((s) => s.activeSessionId);

  return useCallback(
    (entry: UISlotEntry) => {
      switch (entry.action) {
        case "command":
          if (activeSessionId && entry.props?.command) {
            void api.executeCommand(
              String(entry.props.command),
              activeSessionId,
              "",
            );
          }
          break;
        case "navigate":
          if (entry.props?.hash) {
            window.location.hash = String(entry.props.hash);
          }
          break;
        case "handler":
          window.dispatchEvent(
            new CustomEvent("plugin-action", { detail: entry }),
          );
          break;
        case "modal":
          window.dispatchEvent(
            new CustomEvent("plugin-modal", {
              detail: {
                id: entry.id,
                component: entry.component,
                props: entry.props,
              },
            }),
          );
          break;
      }
    },
    [activeSessionId],
  );
}
