import { useEffect, useRef } from "react";
import { useLayoutStore } from "@/stores/useLayoutStore";

/**
 * Syncs app navigation state with the URL hash.
 *
 * Hash format:
 *   #chat              → chat page
 *   #settings           → settings page, default tab
 *   #settings/providers → settings page, providers tab
 *
 * On mount, reads the hash and updates the store.
 * On store change, updates the hash.
 * On browser back/forward, reads the hash and updates the store.
 */

type SettingsSection =
  | "preferences"
  | "providers"
  | "shortcuts"
  | "workspaces"
  | "agents"
  | "skills"
  | "prompts"
  | "tools"
  | "plugins"
  | "widgets"
  | "observability";

const VALID_SETTINGS_SECTIONS = new Set<string>([
  "preferences",
  "providers",
  "shortcuts",
  "workspaces",
  "agents",
  "skills",
  "prompts",
  "tools",
  "plugins",
  "widgets",
  "observability",
]);

function parseHash(hash: string): {
  page: string;
  settingsSection?: SettingsSection;
} {
  const clean = hash.replace(/^#\/?/, "");
  if (!clean || clean === "chat") {
    return { page: "chat" };
  }
  if (clean === "settings") {
    return { page: "settings" };
  }
  if (clean.startsWith("settings/")) {
    const section = clean.slice("settings/".length);
    if (VALID_SETTINGS_SECTIONS.has(section)) {
      return { page: "settings", settingsSection: section as SettingsSection };
    }
    return { page: "settings" };
  }
  // Plugin page: #plugin-id → page = "plugin-id"
  if (/^[a-z0-9][a-z0-9-]*$/.test(clean)) {
    return { page: clean };
  }
  return { page: "chat" };
}

// Callback for settings section changes — set by SettingsPage
let onSettingsSectionChange: ((section: SettingsSection) => void) | null = null;

export function setSettingsSectionCallback(cb: ((section: SettingsSection) => void) | null) {
  onSettingsSectionChange = cb;
}

// Read initial hash and return the settings section if applicable
export function getInitialSettingsSection(): SettingsSection | undefined {
  const { settingsSection } = parseHash(window.location.hash);
  return settingsSection;
}

export function useHashRoute() {
  const currentPage = useLayoutStore((s) => s.currentPage);
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage);
  const suppressHashUpdate = useRef(false);

  // On mount: apply hash to store
  useEffect(() => {
    const { page, settingsSection } = parseHash(window.location.hash);
    if (page !== currentPage) {
      suppressHashUpdate.current = true;
      setCurrentPage(page);
    }
    if (settingsSection && onSettingsSectionChange) {
      onSettingsSectionChange(settingsSection);
    }
    // Only run on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Listen for popstate (back/forward)
  useEffect(() => {
    function handleHashChange() {
      const { page, settingsSection } = parseHash(window.location.hash);
      suppressHashUpdate.current = true;
      setCurrentPage(page);
      if (settingsSection && onSettingsSectionChange) {
        onSettingsSectionChange(settingsSection);
      }
    }
    window.addEventListener("hashchange", handleHashChange);
    return () => window.removeEventListener("hashchange", handleHashChange);
  }, [setCurrentPage]);

  // Sync store → hash (preserve existing section when on settings)
  useEffect(() => {
    if (suppressHashUpdate.current) {
      suppressHashUpdate.current = false;
      return;
    }
    if (currentPage === "chat") {
      if (window.location.hash !== "#chat") {
        window.location.hash = "#chat";
      }
    } else if (currentPage === "settings") {
      // Only update if we're not already on a settings/* hash
      const existing = window.location.hash.replace(/^#\/?/, "");
      if (!existing.startsWith("settings")) {
        window.location.hash = "#settings";
      }
    } else {
      // Plugin page: sync hash to page ID
      const target = `#${currentPage}`;
      if (window.location.hash !== target) {
        window.location.hash = target;
      }
    }
  }, [currentPage]);
}

/** Call from SettingsPage when tab changes to update the hash */
export function updateSettingsHash(section: SettingsSection) {
  const target = section === "preferences" ? "#settings" : `#settings/${section}`;
  if (window.location.hash !== target) {
    window.location.hash = target;
  }
}
