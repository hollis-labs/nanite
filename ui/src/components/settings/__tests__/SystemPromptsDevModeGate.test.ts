/**
 * Regression test — CW-20260421-0005
 *
 * Verifies the dev-mode gate for the "System Prompts" settings section.
 *
 * Tests the nav group composition logic extracted from SettingsPage:
 *   - developer_mode=false → "System Prompts" absent from AI group
 *   - developer_mode=true  → "System Prompts" present in AI group, before "Memory"
 *   - Existing AI group items unaffected by the gate
 *   - Fallback: devMode undefined (falsy) treated as false
 */

import { describe, expect, it } from "vitest";

// ─── Nav group composition logic (mirrors SettingsPage internals) ──────────
// Extracted to a pure function so we can unit-test the gate without mounting
// the full React tree (which requires QueryClient, stores, etc.).

interface NavItem {
  id: string;
  label: string;
}

interface NavGroup {
  label: string;
  items: NavItem[];
}

const SYSTEM_PROMPTS_NAV_ITEM: NavItem = {
  id: "system-prompts",
  label: "System Prompts",
};

const BASE_AI_ITEMS: NavItem[] = [
  { id: "providers", label: "Providers" },
  { id: "agents", label: "Agents" },
  { id: "skills", label: "Skills" },
  { id: "memory", label: "Memory" },
];

const BASE_NAV_GROUPS: NavGroup[] = [
  {
    label: "You",
    items: [
      { id: "profile", label: "Profile" },
      { id: "preferences", label: "Preferences" },
    ],
  },
  {
    label: "AI",
    items: BASE_AI_ITEMS,
  },
  {
    label: "System",
    items: [{ id: "observability", label: "Observability" }],
  },
];

/**
 * Pure function equivalent to the SettingsPage navGroups computation.
 * developerMode: boolean | undefined — undefined treated as false (falsy).
 */
function buildNavGroups(developerMode: boolean | undefined): NavGroup[] {
  return BASE_NAV_GROUPS.map((group) => {
    if (group.label !== "AI") return group;
    if (!developerMode) return group;
    const memoryIdx = group.items.findIndex((i) => i.id === "memory");
    const items =
      memoryIdx >= 0
        ? [
            ...group.items.slice(0, memoryIdx),
            SYSTEM_PROMPTS_NAV_ITEM,
            ...group.items.slice(memoryIdx),
          ]
        : [...group.items, SYSTEM_PROMPTS_NAV_ITEM];
    return { ...group, items };
  });
}

function getAiGroup(groups: NavGroup[]): NavGroup {
  const g = groups.find((g) => g.label === "AI");
  if (!g) throw new Error("AI group not found");
  return g;
}

// ─── Tests ────────────────────────────────────────────────────────────────

describe("System Prompts dev-mode gate (CW-20260421-0005)", () => {
  it("hides System Prompts when developer_mode is false", () => {
    const groups = buildNavGroups(false);
    const ai = getAiGroup(groups);
    const ids = ai.items.map((i) => i.id);
    expect(ids).not.toContain("system-prompts");
  });

  it("hides System Prompts when developer_mode is undefined (falsy)", () => {
    const groups = buildNavGroups(undefined);
    const ai = getAiGroup(groups);
    const ids = ai.items.map((i) => i.id);
    expect(ids).not.toContain("system-prompts");
  });

  it("shows System Prompts when developer_mode is true", () => {
    const groups = buildNavGroups(true);
    const ai = getAiGroup(groups);
    const ids = ai.items.map((i) => i.id);
    expect(ids).toContain("system-prompts");
  });

  it("System Prompts label is 'System Prompts' (not old 'Prompts')", () => {
    const groups = buildNavGroups(true);
    const ai = getAiGroup(groups);
    const entry = ai.items.find((i) => i.id === "system-prompts");
    expect(entry?.label).toBe("System Prompts");
    // Confirm the old 'prompts' id is gone
    const ids = ai.items.map((i) => i.id);
    expect(ids).not.toContain("prompts");
  });

  it("System Prompts appears before Memory in the AI group", () => {
    const groups = buildNavGroups(true);
    const ai = getAiGroup(groups);
    const ids = ai.items.map((i) => i.id);
    const spIdx = ids.indexOf("system-prompts");
    const memIdx = ids.indexOf("memory");
    expect(spIdx).toBeGreaterThanOrEqual(0);
    expect(memIdx).toBeGreaterThanOrEqual(0);
    expect(spIdx).toBeLessThan(memIdx);
  });

  it("existing AI group items (providers, agents, skills, memory) are always present", () => {
    for (const devMode of [false, true] as const) {
      const groups = buildNavGroups(devMode);
      const ai = getAiGroup(groups);
      const ids = ai.items.map((i) => i.id);
      expect(ids).toContain("providers");
      expect(ids).toContain("agents");
      expect(ids).toContain("skills");
      expect(ids).toContain("memory");
    }
  });

  it("non-AI groups are unaffected by the dev-mode gate", () => {
    const withDev = buildNavGroups(true);
    const withoutDev = buildNavGroups(false);
    const getNonAi = (groups: NavGroup[]) =>
      groups
        .filter((g) => g.label !== "AI")
        .map((g) => ({
          label: g.label,
          ids: g.items.map((i) => i.id),
        }));
    expect(getNonAi(withDev)).toEqual(getNonAi(withoutDev));
  });
});
