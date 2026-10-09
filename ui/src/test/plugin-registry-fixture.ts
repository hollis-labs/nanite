import {
  bundleDigest,
  type PluginRegistryResponse,
  type RegistryContribution,
} from "@hollis-labs/plugin-registry";

export interface FixtureContribution {
  kind: string;
  key: string;
  exportName: string;
  metadata: Record<string, unknown>;
}

/** A test host declares real JavaScript bytes and their SDK-computed digest. */
export async function registryFixture(
  plugins: Record<string, { source: string; entries: FixtureContribution[] }>,
  revision = 1,
): Promise<PluginRegistryResponse> {
  const response: PluginRegistryResponse = {
    registry_version: 2,
    host_instance: "browser-fixture",
    revision,
    plugins: {},
    contributions: {},
    kinds: {},
    regions: {},
    refusals: [],
  };
  for (const [owner, plugin] of Object.entries(plugins)) {
    response.plugins[owner] = {
      owner_generation: "1",
      bundle_url: `data:text/javascript;base64,${btoa(plugin.source)}`,
      bundle_version: await bundleDigest(new TextEncoder().encode(plugin.source)),
    };
    for (const entry of plugin.entries) {
      const kind = entry.kind;
      response.kinds[kind] = {
        schema_version: 1,
        metadata_schema: {},
        representations: ["component"],
        regions: [kind],
        required_capabilities: [],
      };
      response.regions[kind] = {
        kinds: [kind],
        representations: ["component"],
        context_schema: {},
        ordering: "manifest",
      };
      const localKey =
        kind === "slot"
          ? `slot-${Array.from(new TextEncoder().encode(entry.key), (byte) => byte.toString(16).padStart(2, "0")).join("")}`
          : entry.key;
      const contribution: RegistryContribution = {
        owner_id: owner,
        owner_generation: "1",
        local_key: localKey,
        public_binding: entry.key,
        kind,
        schema_version: 1,
        required: false,
        status: "accepted",
        representation: "component",
        metadata: entry.metadata,
        component: { export: entry.exportName, region: kind },
      };
      response.contributions[kind] ??= {};
      response.contributions[kind][`${owner}/${localKey}`] = contribution;
    }
  }
  return response;
}
