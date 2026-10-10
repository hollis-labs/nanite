import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { useSettings } from "@/hooks/useSettings";
import { api } from "@/lib/api";
import type {
  AgentHostSettings,
  NativeAgentHostSettings,
  PinnedAgentDefinition,
} from "@/lib/types";

// Intrinsic content is installed at immutable pins. Host configuration has its
// own revision and never edits content, enrolls an actor or grants capabilities.
export function AgentProfileManager() {
  const cache = useQueryClient();
  const { data: definitions = [], error: catalogError } = useQuery({
    queryKey: ["agent-definitions"],
    queryFn: api.listAgentDefinitions,
  });
  const { data: hosts = [], error: hostsError } = useQuery({
    queryKey: ["agent-host-settings"],
    queryFn: api.listAgentHostSettings,
  });
  const { data: defaults } = useSettings();
  const [selected, setSelected] = useState<AgentHostSettings | null>(null);
  const [title, setTitle] = useState("");
  const [slug, setSlug] = useState("");
  const [pinKey, setPinKey] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [artifact, setArtifact] = useState("");
  const [role, setRole] = useState("");
  const [resources, setResources] = useState("[]");
  const [execution, setExecution] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const key = (d: PinnedAgentDefinition) => `${d.definition_ref.id}@${d.definition_ref.revision}`;
  const refresh = async () => {
    await cache.invalidateQueries({ queryKey: ["agent-host-settings"] });
    await cache.invalidateQueries({ queryKey: ["agent-profiles"] });
  };
  const act = async (f: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await f();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Request failed");
    } finally {
      setBusy(false);
    }
  };
  const edit = (host: AgentHostSettings | null) => {
    setSelected(host);
    setTitle(host?.title ?? "");
    setSlug(host?.slug ?? "");
    setEnabled(host?.enabled ?? true);
    setExecution(host ? JSON.stringify(host.settings, null, 2) : "");
    setPinKey(host ? `${host.definition_ref.id}@${host.definition_ref.revision}` : "");
  };
  const save = async () => {
    const def = definitions.find((d) => key(d) === pinKey);
    if (!def) throw new Error("Select an installed definition pin.");
    const host = await api.saveAgentHostSettings(
      {
        slug,
        title,
        definition_ref: def.definition_ref,
        enabled,
        revision: selected?.revision ?? "",
        settings: execution
          ? (JSON.parse(execution) as NativeAgentHostSettings)
          : {
              version: "1",
              runtime: "api",
              provider: defaults?.default_provider ?? "",
              model: defaults?.default_model ?? "",
              native_loop: {},
            },
      },
      selected?.id,
    );
    edit(host);
    await refresh();
  };
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold">Agent definitions</h2>
        <p className="text-sm text-fg-muted">
          Install immutable content, then configure its host. Chat uses the selected pin; actor
          enrollment and capability grants are separate.
        </p>
      </div>
      {(error || catalogError || hostsError) && (
        <p role="alert" className="text-sm text-status-error">
          {error || catalogError?.message || hostsError?.message}
        </p>
      )}
      <section className="space-y-3">
        <h3 className="font-medium">Installed pins</h3>
        <select
          aria-label="Definition pin"
          value={pinKey}
          onChange={(e) => setPinKey(e.target.value)}
          className="w-full rounded border border-border bg-bg p-2"
        >
          <option value="">Select a definition</option>
          {definitions.map((d) => (
            <option key={key(d)} value={key(d)}>
              {key(d)}
            </option>
          ))}
        </select>
        {definitions.find((d) => key(d) === pinKey) && (
          <details>
            <summary className="cursor-pointer text-sm">View immutable definition</summary>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap text-xs">
              {definitions.find((d) => key(d) === pinKey)?.artifact}
            </pre>
          </details>
        )}
      </section>
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="font-medium">Host settings</h3>
          <Button variant="outline" onClick={() => edit(null)}>
            New host
          </Button>
        </div>
        {hosts.map((h) => (
          <Button key={h.id} variant="outline" onClick={() => edit(h)}>
            {h.title}
            {h.enabled ? "" : " (disabled)"}
          </Button>
        ))}
        <label className="block text-sm">
          Title
          <input
            className="mt-1 block w-full rounded border border-border bg-bg p-2"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </label>
        <label className="block text-sm">
          Slug
          <input
            className="mt-1 block w-full rounded border border-border bg-bg p-2"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
          />
        </label>
        <label className="flex gap-2 text-sm">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enabled
        </label>
        <label className="block text-sm">
          Execution settings (optional JSON override)
          <textarea
            className="mt-1 block h-36 w-full rounded border border-border bg-bg p-2 font-mono text-xs"
            value={execution}
            onChange={(e) => setExecution(e.target.value)}
            placeholder={JSON.stringify(
              {
                version: "1",
                runtime: "api",
                provider: defaults?.default_provider ?? "",
                model: defaults?.default_model ?? "",
                native_loop: {},
              },
              null,
              2,
            )}
          />
        </label>
        <p className="text-xs text-fg-muted">
          {selected
            ? `Revision ${selected.revision}. Execution settings are validated by the host.`
            : "New hosts use your configured native provider and model."}
        </p>
        <div className="flex gap-2">
          <Button disabled={busy} onClick={() => void act(save)}>
            Save host settings
          </Button>
          {selected && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await api.deleteAgentHostSettings(selected.id, selected.revision);
                  edit(null);
                  await refresh();
                })
              }
            >
              Delete host settings
            </Button>
          )}
        </div>
      </section>
      <section className="space-y-3">
        <h3 className="font-medium">Author a definition</h3>
        <p className="text-sm text-fg-muted">
          Edit a new revision or ID. Installation cannot replace a pin with different bytes.
        </p>
        <label className="block text-sm">
          Concrete v2 definition
          <textarea
            className="mt-1 block h-48 w-full rounded border border-border bg-bg p-2 font-mono text-xs"
            value={artifact}
            onChange={(e) => setArtifact(e.target.value)}
          />
        </label>
        <label className="block text-sm">
          Optional v2 role template
          <textarea
            className="mt-1 block h-24 w-full rounded border border-border bg-bg p-2 font-mono text-xs"
            value={role}
            onChange={(e) => setRole(e.target.value)}
          />
        </label>
        <Button
          variant="outline"
          disabled={busy || !artifact}
          onClick={() =>
            void act(async () => {
              const result = await api.authorAgentDefinition(artifact, role || undefined);
              setArtifact(result.artifact);
            })
          }
        >
          Flatten role behavior
        </Button>
        <label className="block text-sm">
          Pinned resources (JSON array of uri and content)
          <textarea
            className="mt-1 block h-24 w-full rounded border border-border bg-bg p-2 font-mono text-xs"
            value={resources}
            onChange={(e) => setResources(e.target.value)}
          />
        </label>
        <Button
          disabled={busy || !artifact}
          onClick={() =>
            void act(async () => {
              const pin = await api.installAgentDefinition(
                artifact,
                JSON.parse(resources) as Array<{ uri: string; content: string }>,
              );
              await cache.invalidateQueries({ queryKey: ["agent-definitions"] });
              setPinKey(`${pin.id}@${pin.revision}`);
            })
          }
        >
          Install pinned definition
        </Button>
      </section>
    </div>
  );
}
