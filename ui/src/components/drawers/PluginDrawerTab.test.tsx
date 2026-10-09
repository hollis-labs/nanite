import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import { browserPluginRegistry, clearDynamicRegistry } from "@/lib/plugin-loader";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { registryFixture } from "@/test/plugin-registry-fixture";
import { ChatPrimaryDrawer } from "./ChatPrimaryDrawer";
import { ChatWorkingDrawer } from "./ChatWorkingDrawer";
import { type PluginDrawerTab, PluginDrawerTabBody } from "./PluginDrawerTab";

vi.mock("@/hooks/useSettings", () => ({
  useSettings: () => ({ data: { developer_mode: false } }),
}));

afterEach(async () => {
  cleanup();
  await clearDynamicRegistry();
  vi.restoreAllMocks();
});

it("contains a failed plugin render and recovers when its declaration changes", async () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  const data = await registryFixture({
    broken: {
      source:
        'export function Broken() { throw new Error("fixture") }; export function Fixed() { return "Recovered" }',
      entries: [
        {
          kind: "slot",
          key: "drawer.primary.tabs/broken",
          exportName: "Broken",
          metadata: { id: "broken" },
        },
      ],
    },
  });
  await browserPluginRegistry.sync(data);
  const entry: PluginDrawerTab = {
    tabId: "plugin:broken:broken",
    id: "broken",
    plugin_id: "broken",
    component: "Broken",
    label: "Broken",
    slot: "drawer.primary.tabs",
  };
  const view = render(<PluginDrawerTabBody entry={entry} sessionId="session-a" />);
  await waitFor(() =>
    expect(view.getByRole("alert").textContent).toBe("Plugin tab could not render."),
  );
  Object.values(data.contributions.slot!)[0]!.component!.export = "Fixed";
  data.revision++;
  await browserPluginRegistry.sync(data);
  view.rerender(
    <PluginDrawerTabBody entry={{ ...entry, component: "Fixed" }} sessionId="session-a" />,
  );
  await waitFor(() => expect(view.container.textContent).toBe("Recovered"));
});

for (const [slot, Drawer] of [
  ["drawer.primary.tabs", ChatPrimaryDrawer],
  ["drawer.working.tabs", ChatWorkingDrawer],
] as const) {
  it(`${slot} renders the owning export and session, then removes it on unload`, async () => {
    vi.spyOn(api, "listDrawerCards").mockResolvedValue([]);
    const data = await registryFixture({
      "docs.plugin": {
        source: 'export function Shared(props) { return props.session_id + ":" + props.caption }',
        entries: [
          {
            kind: "slot",
            key: `${slot}/docs`,
            exportName: "Shared",
            metadata: {
              id: "docs",
              label: "Plugin documents",
              icon: "file-text",
              props: { caption: "Owned view", session_id: "stale" },
            },
          },
        ],
      },
      "other.plugin": {
        source: 'export function Shared() { return "Wrong owner" }',
        entries: [
          { kind: "slot", key: "modal/other", exportName: "Shared", metadata: { id: "other" } },
        ],
      },
    });
    await browserPluginRegistry.sync(data);
    expect(browserPluginRegistry.errors()).toEqual([]);
    const store = useLayoutStore.getState();
    store.setChatPrimaryDrawer({ open: true, activeTab: "plugin:docs.plugin:docs" });
    store.setChatWorkingDrawer({ open: true, activeTab: "plugin:docs.plugin:docs" }, "session-a");
    useAppStore.setState({ activeSessionId: null });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(
      <QueryClientProvider client={client}>
        <Drawer />
      </QueryClientProvider>,
    );
    act(() => useAppStore.setState({ activeSessionId: "session-a" }));
    await waitFor(() => expect(view.container.textContent).toContain("session-a:Owned view"));
    expect(view.container.textContent).not.toContain("Wrong owner");
    expect(view.getByRole("button", { name: "Plugin documents" })).toBeDefined();
    // State and hook ordering survive an inactive/active session transition.
    act(() => useAppStore.setState({ activeSessionId: null }));
    act(() => useAppStore.setState({ activeSessionId: "session-a" }));
    await waitFor(() => expect(view.container.textContent).toContain("session-a:Owned view"));
    await act(async () => {
      await browserPluginRegistry.sync(await registryFixture({}, 2));
    });
    expect(view.queryByRole("button", { name: "Plugin documents" })).toBeNull();
    expect(view.container.textContent).toContain("Plugin tab is unavailable.");
    fireEvent.click(
      view.getByRole("button", { name: slot === "drawer.primary.tabs" ? "Tools" : "Scratchpad" }),
    );
    expect(view.container.textContent).not.toContain("Plugin tab is unavailable.");
    client.clear();
  });
}
