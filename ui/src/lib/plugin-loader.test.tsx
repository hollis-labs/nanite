import {
  bundleDigest,
  type PluginRegistryResponse,
  type RegistryContribution,
} from "@hollis-labs/plugin-registry";
import { cleanup, render, waitFor } from "@testing-library/react";
import { type ComponentType, Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ENVELOPE_REGISTRY } from "@/generated/plugin-envelopes";
import { createNanitePluginRegistry } from "./plugin-loader";

const bytes = new TextEncoder().encode(
  "export const First = () => null; export const Second = () => null",
);
function contribution(
  kind = "envelope",
  key = "example.card",
  exportName = "First",
  range = "^19.0.0",
): RegistryContribution {
  return {
    owner_id: "example.plugin",
    owner_generation: "1",
    local_key: key,
    kind,
    schema_version: 1,
    required: false,
    status: "accepted",
    representation: "component",
    metadata: { version: 1, react_range: range },
    component: { export: exportName, region: kind },
    public_binding: key,
  };
}
async function response(
  exportName = "First",
  revision = 1,
  range = "^19.0.0",
): Promise<PluginRegistryResponse> {
  const kinds: PluginRegistryResponse["kinds"] = {};
  const regions: PluginRegistryResponse["regions"] = {};
  for (const kind of ["envelope", "widget", "slot", "panel"]) {
    kinds[kind] = {
      schema_version: 1,
      metadata_schema: {},
      representations: ["component"],
      regions: [kind],
      required_capabilities: [],
    };
    regions[kind] = {
      kinds: [kind],
      representations: ["component"],
      context_schema: {},
      ordering: "manifest",
    };
  }
  return {
    registry_version: 2,
    host_instance: "fixture-epoch",
    revision,
    kinds,
    regions,
    refusals: [],
    plugins: {
      "example.plugin": {
        owner_generation: "1",
        bundle_url: "/bundle.js",
        bundle_version: await bundleDigest(bytes),
      },
    },
    contributions: {
      envelope: {
        "example.plugin/example.card": contribution("envelope", "example.card", exportName, range),
      },
    },
  };
}

afterEach(cleanup);

describe("Nanite browser registry adoption", () => {
  it("renders a verified React export and reconciles a new revision without reimporting", async () => {
    const importer = vi.fn(async (bundle) => {
      expect(bundle.bytes).toEqual(bytes);
      return { First: () => <p>First card</p>, Second: () => <p>Second card</p> };
    });
    const registry = createNanitePluginRegistry({
      importModule: importer,
      fetchBundle: async () => bytes,
      stylesheets: false,
    });
    expect((await registry.sync(await response())).accepted).toBe(true);
    const first = registry.get("envelope", "example.plugin/example.card")!;
    const First = first.value as ComponentType;
    const view = render(
      <Suspense fallback={<p>Loading</p>}>
        <First />
      </Suspense>,
    );
    await waitFor(() => expect(view.container.textContent).toBe("First card"));
    await registry.sync(await response("Second", 2));
    const Second = registry.get("envelope", "example.plugin/example.card")!.value as ComponentType;
    view.rerender(
      <Suspense fallback={<p>Loading</p>}>
        <Second />
      </Suspense>,
    );
    await waitFor(() => expect(view.container.textContent).toBe("Second card"));
    expect(first.isActive()).toBe(false);
    expect(importer).toHaveBeenCalledTimes(1);
    const empty = await response("First", 3);
    empty.plugins = {};
    empty.contributions = {};
    await registry.sync(empty);
    expect(registry.get("envelope", "example.plugin/example.card")).toBeUndefined();
  });

  it("refuses reserved public bindings even when the qualified local key disguises the claim", async () => {
    const registry = createNanitePluginRegistry({
      importModule: async () => ({ First: () => null }),
      fetchBundle: async () => bytes,
      stylesheets: false,
    });
    const data = await response();
    const coreEnvelope = Object.keys(ENVELOPE_REGISTRY).find(
      (key) => ENVELOPE_REGISTRY[key]?.source === "core",
    )!;
    data.contributions.envelope!["example.plugin/disguised"] = {
      ...contribution("envelope", "disguised"),
      public_binding: coreEnvelope,
    };
    data.contributions.widget = {
      "example.plugin/session-info": contribution("widget", "session-info"),
    };
    expect((await registry.sync(data)).accepted).toBe(true);
    expect(registry.get("envelope", "example.plugin/disguised")).toBeUndefined();
    expect(registry.refusals()).toContainEqual(
      expect.objectContaining({ kind: "envelope", local_key: "disguised", reason: "reserved" }),
    );
    expect(registry.get("widget", "example.plugin/session-info")).toBeUndefined();
    expect(registry.refusals()).toContainEqual(
      expect.objectContaining({ kind: "widget", local_key: "session-info", reason: "reserved" }),
    );
    expect(registry.get("envelope", "example.plugin/example.card")).toBeDefined();
  });

  it.each([
    "^18.0.0",
    "^20.0.0",
    "not-a-version",
  ])("refuses React range %s before executing any bundle", async (range) => {
    const importer = vi.fn(async () => ({ First: () => null }));
    const fetcher = vi.fn(async () => bytes);
    const registry = createNanitePluginRegistry({
      importModule: importer,
      fetchBundle: fetcher,
      stylesheets: false,
    });
    await registry.sync(await response("First", 1, range));
    expect(importer).not.toHaveBeenCalled();
    expect(fetcher).not.toHaveBeenCalled();
    expect(registry.get("envelope", "example.plugin/example.card")).toBeUndefined();
    expect(registry.refusals()).toContainEqual(
      expect.objectContaining({ reason: "invalid-metadata" }),
    );
  });

  it("preserves the serving registry when replacement bytes or raw wire are refused", async () => {
    let served = bytes;
    const importer = vi.fn(async () => ({ First: () => null }));
    const registry = createNanitePluginRegistry({
      importModule: importer,
      fetchBundle: async () => served,
      stylesheets: false,
    });
    await registry.sync(await response());
    const serving = registry.get("envelope", "example.plugin/example.card")!;
    const replacement = await response("First", 2);
    replacement.plugins["example.plugin"]!.owner_generation = "2";
    replacement.contributions.envelope!["example.plugin/example.card"]!.owner_generation = "2";
    served = new TextEncoder().encode("tampered");
    expect((await registry.sync(replacement)).accepted).toBe(false);
    expect(serving.isActive()).toBe(true);
    expect(importer).toHaveBeenCalledTimes(1);
    expect(
      (await registry.sync(JSON.stringify({ ...replacement, registry_version: 1 }))).accepted,
    ).toBe(false);
    expect(
      (
        await registry.sync(
          JSON.stringify(replacement).replace('"revision":2', '"revision":2,"Revision":3'),
        )
      ).accepted,
    ).toBe(false);
    expect(serving.isActive()).toBe(true);
  });
});

it.skipIf(!import.meta.env["VITE_NANITE_PLUGIN_SMOKE_URL"])(
  "renders verified bytes served by the real Nanite registry fixture",
  async () => {
    const base = String(import.meta.env["VITE_NANITE_PLUGIN_SMOKE_URL"]);
    const dom = window as unknown as { happyDOM: { setURL(url: string): void } };
    const previousURL = window.location.href;
    dom.happyDOM.setURL(base);
    try {
      const httpResponse = await fetch(`${base}/api/plugins/registry`);
      expect(httpResponse.ok).toBe(true);
      const registry = createNanitePluginRegistry({
        stylesheets: false,
        fetchBundle: async (url, signal) => {
          const response = await fetch(new URL(url, base), { signal });
          if (!response.ok) throw new Error("Bundle fetch failed");
          return new Uint8Array(await response.arrayBuffer());
        },
      });
      expect((await registry.sync(await httpResponse.text())).accepted).toBe(true);
      expect(registry.errors()).toEqual([]);
      const entry = registry.get("envelope", "browser-live/browser-live-card")!;
      const LiveCard = entry.value as ComponentType;
      const view = render(
        <Suspense fallback={null}>
          <LiveCard />
        </Suspense>,
      );
      await waitFor(() => expect(view.container.textContent).toBe("Loaded from Nanite"));
      await registry.clear();
    } finally {
      try {
        await fetch(`${base}/smoke/stop`, { method: "POST" });
      } finally {
        dom.happyDOM.setURL(previousURL);
      }
    }
  },
);
