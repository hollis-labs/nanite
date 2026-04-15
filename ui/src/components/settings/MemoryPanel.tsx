import { useQuery } from "@tanstack/react-query";
import { useSettings, useSettingsMutation } from "@/hooks/useSettings";
import { api } from "@/lib/api";
import type { EmbeddingProviderInfo, UserSettings } from "@/lib/types";

const STATUS_BADGE: Record<
  NonNullable<UserSettings["embedding_status"]>,
  { label: string; cls: string }
> = {
  active: {
    label: "Active",
    cls: "bg-emerald-100 text-emerald-900 dark:bg-emerald-900/40 dark:text-emerald-200",
  },
  disabled: {
    label: "Disabled",
    cls: "bg-zinc-200 text-zinc-700 dark:bg-zinc-700 dark:text-zinc-200",
  },
  missing_credentials: {
    label: "Missing credentials",
    cls: "bg-red-100 text-red-900 dark:bg-red-900/40 dark:text-red-200",
  },
  unreachable: {
    label: "Unreachable",
    cls: "bg-red-100 text-red-900 dark:bg-red-900/40 dark:text-red-200",
  },
};

function StatusBadge({ status }: { status?: UserSettings["embedding_status"] }) {
  const entry = status ? STATUS_BADGE[status] : STATUS_BADGE.disabled;
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium ${entry.cls}`}
    >
      {entry.label}
    </span>
  );
}

export function MemoryPanel() {
  const { data: settings } = useSettings();
  const mutation = useSettingsMutation();
  const { data: providers = [] } = useQuery<EmbeddingProviderInfo[]>({
    queryKey: ["embedding-providers"],
    queryFn: api.listEmbeddingProviders,
    staleTime: 60 * 60 * 1000,
  });

  if (!settings) return null;

  const currentProvider = providers.find((p) => p.id === settings.embedding_provider);
  const modelOptions = currentProvider?.default_models ?? [];

  const handleProviderChange = (value: string) => {
    const next: Partial<UserSettings> = { embedding_provider: value };
    // Reset model when switching providers; default to the first known model.
    const nextProvider = providers.find((p) => p.id === value);
    next.embedding_model = nextProvider?.default_models[0] ?? "";
    mutation.mutate(next);
  };

  const handleModeChange = (value: "disabled" | "explicit") => {
    mutation.mutate({ embedding_mode: value });
  };

  const handleModelChange = (value: string) => {
    mutation.mutate({ embedding_model: value });
  };

  return (
    <div className="space-y-4 p-4">
      <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
        <div className="px-4 py-3 border-b border-border/50 flex items-center justify-between gap-4">
          <div>
            <h3 className="text-sm font-semibold text-fg">Memory Embeddings</h3>
            <p className="text-[11px] text-fg-muted mt-0.5">
              Controls similarity-based recall. When off, memory is stored but not searchable by
              semantic similarity.
            </p>
          </div>
          <StatusBadge status={settings.embedding_status} />
        </div>
        <div className="px-4 py-3 space-y-4">
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm text-fg">Mode</div>
              <div className="text-[11px] text-fg-muted mt-0.5">
                Choose <strong>Disabled</strong> for privacy, or <strong>Explicit</strong> to pick a
                provider.
              </div>
            </div>
            <select
              className="rounded border border-border bg-bg-elevated px-2 py-1 text-sm"
              value={settings.embedding_mode}
              onChange={(e) => handleModeChange(e.target.value as "disabled" | "explicit")}
            >
              <option value="disabled">Disabled</option>
              <option value="explicit">Explicit</option>
            </select>
          </div>

          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm text-fg">Provider</div>
              <div className="text-[11px] text-fg-muted mt-0.5">
                Embeddings send memory text to the selected provider. Ollama is local.
              </div>
            </div>
            <select
              className="rounded border border-border bg-bg-elevated px-2 py-1 text-sm"
              value={settings.embedding_provider}
              onChange={(e) => handleProviderChange(e.target.value)}
              disabled={settings.embedding_mode === "disabled"}
            >
              <option value="">— Select a provider —</option>
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>

          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm text-fg">Model</div>
              <div className="text-[11px] text-fg-muted mt-0.5">
                Provider-specific embedding model ID.
              </div>
            </div>
            <select
              className="rounded border border-border bg-bg-elevated px-2 py-1 text-sm"
              value={settings.embedding_model}
              onChange={(e) => handleModelChange(e.target.value)}
              disabled={!settings.embedding_provider || settings.embedding_mode === "disabled"}
            >
              <option value="">— Select a model —</option>
              {modelOptions.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </div>

          {settings.embedding_status === "missing_credentials" && (
            <div className="rounded-md border border-red-300 bg-red-50 dark:bg-red-900/20 px-3 py-2 text-[12px] text-red-900 dark:text-red-200">
              <strong>Missing credentials.</strong> Add an API key for{" "}
              {currentProvider?.name ?? settings.embedding_provider} in{" "}
              <em>Settings → Providers</em>.
            </div>
          )}
          {settings.embedding_status === "unreachable" && (
            <div className="rounded-md border border-red-300 bg-red-50 dark:bg-red-900/20 px-3 py-2 text-[12px] text-red-900 dark:text-red-200">
              <strong>Provider unreachable.</strong>{" "}
              {currentProvider?.name ?? settings.embedding_provider} did not respond. Start it or
              switch providers.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
