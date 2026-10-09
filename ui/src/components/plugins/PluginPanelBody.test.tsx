import { act, cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { usePanelRegistryStore } from "@/hooks/usePanelRegistry";
import { usePluginPanels } from "@/hooks/usePluginPanels";
import { browserPluginRegistry, clearDynamicRegistry } from "@/lib/plugin-loader";
import { type FixtureContribution, registryFixture } from "@/test/plugin-registry-fixture";
import { PluginPanelBody } from "./PluginPanelBody";
import { PluginRenderBoundary } from "./PluginRenderBoundary";

afterEach(async () => {
  cleanup();
  await clearDynamicRegistry();
  usePanelRegistryStore.setState({ panels: {}, orderedIds: [] });
  vi.restoreAllMocks();
});

function Harness() {
  const panels = usePluginPanels();
  return (
    <>
      {panels.map((panel) => (
        <PluginPanelBody key={panel.definition.id} panel={panel} sessionId="session-current" />
      ))}
    </>
  );
}

async function declaration(exportName = "First", revision = 1, extra: FixtureContribution[] = []) {
  return registryFixture(
    {
      "docs.plugin": {
        source:
          'export function First(props) { return "First:" + props.session_id }; export function Second(props) { return "Second:" + props.session_id }',
        entries: [
          {
            kind: "panel",
            key: "docs",
            exportName,
            metadata: { title: "Documents", icon: "file-text", default_visible: true, order: 130 },
          },
          ...extra,
        ],
      },
    },
    revision,
  );
}

it("renders manifest panels, reconciles export changes and removes owner metadata on unload", async () => {
  await browserPluginRegistry.sync(await declaration());
  const view = render(<Harness />);
  await waitFor(() => expect(view.container.textContent).toBe("First:session-current"));
  expect(usePanelRegistryStore.getState().panels.docs).toMatchObject({
    label: "Documents",
    pluginId: "docs.plugin",
    defaultVisible: true,
    order: 130,
  });
  await act(async () => {
    await browserPluginRegistry.sync(await declaration("Second", 2));
  });
  await waitFor(() => expect(view.container.textContent).toBe("Second:session-current"));
  await act(async () => {
    await browserPluginRegistry.sync(await registryFixture({}, 3));
  });
  expect(view.container.textContent).toBe("");
  expect(usePanelRegistryStore.getState().panels.docs).toBeUndefined();
});

it("refuses core panel and rail slot claims while preserving core registry definitions", async () => {
  const core = { id: "work", label: "Plan", source: "builtin" as const };
  usePanelRegistryStore.getState().register(core);
  const data = await declaration("First", 1, [
    { kind: "panel", key: "work", exportName: "First", metadata: { title: "Claim" } },
    {
      kind: "slot",
      key: "right-rail-tab/work",
      exportName: "Second",
      metadata: { id: "work", label: "Claim" },
    },
  ]);
  await browserPluginRegistry.sync(data);
  render(<Harness />);
  expect(browserPluginRegistry.refusals()).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ kind: "panel", local_key: "work", reason: "reserved" }),
      expect.objectContaining({ kind: "slot", reason: "reserved" }),
    ]),
  );
  usePanelRegistryStore
    .getState()
    .register({ id: "work", label: "Claim", source: "plugin", pluginId: "docs.plugin" });
  expect(usePanelRegistryStore.getState().panels.work).toEqual(core);
});

it("preserves an edited form when the boundary reset identity changes", () => {
  const view = render(
    <PluginRenderBoundary resetKey="first" label="Plugin panel">
      <input aria-label="Draft" defaultValue="saved" />
    </PluginRenderBoundary>,
  );
  fireEvent.change(view.getByRole("textbox"), { target: { value: "unsaved work" } });
  view.rerender(
    <PluginRenderBoundary resetKey="second" label="Plugin panel">
      <input aria-label="Draft" defaultValue="saved" />
    </PluginRenderBoundary>,
  );
  expect((view.getByRole("textbox") as HTMLInputElement).value).toBe("unsaved work");
});
