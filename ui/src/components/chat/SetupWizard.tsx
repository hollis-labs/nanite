import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Key, Loader2, Terminal } from "lucide-react";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { useSettingsMutation } from "@/hooks/useSettings";
import { api } from "@/lib/api";
import type { ModelRecord, ProviderStatus } from "@/lib/types";

const CLI_LABELS: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  opencode: "opencode",
};

// pty-* providers seed a single placeholder model row (see
// internal/store/seed.go), but /api/models and /api/providers/status
// deliberately hide pty-* rows (internal/api/providers.go's
// isHiddenPTYProviderType — they aren't meant to appear in the normal
// provider/model pickers). The placeholder model ids are stable sentinels
// used throughout the backend (tests reference "claude-cli"/"codex-cli"
// literally), so we mirror them here rather than trying to fetch them.
const CLI_DEFAULT_MODEL_ID: Record<string, string> = {
  pty: "claude-cli",
  "pty-codex": "codex-cli",
  "pty-opencode": "opencode-cli",
};

/** The "connect with an API key" half of the wizard: pick a provider, save
 * the key to the OS keychain, then pick which of that provider's models
 * becomes the default. */
function ApiKeyConnect({
  providers,
  models,
  onDone,
}: {
  providers: ProviderStatus[];
  models: ModelRecord[] | undefined;
  onDone: () => void;
}) {
  const apiProviders = useMemo(
    () => providers.filter((p) => !p.provider_type.startsWith("pty")),
    [providers],
  );
  const [providerId, setProviderId] = useState(apiProviders[0]?.id ?? "");
  const [key, setKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [modelId, setModelId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const settingsMutation = useSettingsMutation();
  const queryClient = useQueryClient();

  const selectedProvider = apiProviders.find((p) => p.id === providerId);
  const providerModels = useMemo(
    () =>
      selectedProvider
        ? (models ?? []).filter((m) => m.provider_type === selectedProvider.provider_type)
        : [],
    [models, selectedProvider],
  );

  if (apiProviders.length === 0) return null;

  const handleSaveKey = async () => {
    if (!selectedProvider || !key.trim()) return;
    setError(null);
    setSaving(true);
    try {
      await api.setProviderAPIKey(selectedProvider.id, key.trim());
      await queryClient.invalidateQueries({ queryKey: ["provider-statuses"] });
      setModelId(providerModels[0]?.model_id ?? "");
      setSaved(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save that key.");
    } finally {
      setSaving(false);
    }
  };

  const handleConfirm = () => {
    if (!selectedProvider || !modelId) return;
    settingsMutation.mutate(
      { default_provider: selectedProvider.provider_type, default_model: modelId },
      { onSuccess: onDone },
    );
  };

  return (
    <div className="rounded-[10px] border border-border-subtle bg-bg-elevated p-4">
      <div className="flex items-center gap-2 mb-3">
        <Key className="w-4 h-4 text-primary" />
        <h3 className="text-sm font-medium text-fg">Connect with an API key</h3>
      </div>

      {!saved ? (
        <div className="space-y-2.5">
          <select
            value={providerId}
            onChange={(e) => setProviderId(e.target.value)}
            className="w-full bg-surface border border-border-subtle rounded-md px-3 py-2 text-sm text-fg focus:outline-none focus:ring-1 focus:ring-primary"
          >
            {apiProviders.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <input
            type="password"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void handleSaveKey();
            }}
            placeholder="sk-..."
            className="w-full bg-surface border border-border-subtle rounded-md px-3 py-2 text-sm text-fg font-mono focus:outline-none focus:ring-1 focus:ring-primary"
          />
          {error && <p className="text-xs text-danger">{error}</p>}
          <Button
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white w-full"
            disabled={!key.trim() || saving}
            onClick={() => void handleSaveKey()}
          >
            {saving ? "Saving…" : "Save key"}
          </Button>
        </div>
      ) : (
        <div className="space-y-2.5">
          <p className="text-xs text-fg-muted">
            Key saved. Pick the model you want as your default.
          </p>
          <select
            value={modelId}
            onChange={(e) => setModelId(e.target.value)}
            className="w-full bg-surface border border-border-subtle rounded-md px-3 py-2 text-sm text-fg focus:outline-none focus:ring-1 focus:ring-primary"
          >
            {providerModels.map((m) => (
              <option key={m.model_id} value={m.model_id}>
                {m.display_name}
              </option>
            ))}
          </select>
          <Button
            size="sm"
            className="bg-primary hover:bg-primary-hover text-white w-full"
            disabled={!modelId || settingsMutation.isPending}
            onClick={handleConfirm}
          >
            {settingsMutation.isPending ? "Setting default…" : "Set as my default"}
          </Button>
        </div>
      )}
    </div>
  );
}

/**
 * First-run setup: auto-detect a Claude/Codex CLI already on PATH, or fall
 * back to an API key. Either path ends by writing user_settings.default_provider
 * / default_model, which is all the rest of the app needs — agent profiles
 * already inherit these as their own default (internal/store/defaults.go).
 */
export function SetupWizard({ onDismiss }: { onDismiss: () => void }) {
  const { data: providers, isLoading: providersLoading } = useQuery({
    queryKey: ["provider-statuses"],
    queryFn: api.listProviderStatuses,
    staleTime: 30_000,
  });
  const { data: detections, isLoading: detectLoading } = useQuery({
    queryKey: ["cli-detection"],
    queryFn: api.detectCLI,
    staleTime: 30_000,
  });
  const { data: models } = useQuery({
    queryKey: ["models"],
    queryFn: api.listModels,
    staleTime: 5 * 60 * 1000,
  });
  const settingsMutation = useSettingsMutation();
  const [justConfigured, setJustConfigured] = useState(false);

  const detected = useMemo(
    () => (detections ?? []).filter((d) => d.detected && d.provider_type in CLI_DEFAULT_MODEL_ID),
    [detections],
  );

  const handleUseCLI = (providerType: string) => {
    settingsMutation.mutate(
      { default_provider: providerType, default_model: CLI_DEFAULT_MODEL_ID[providerType] ?? "" },
      { onSuccess: () => setJustConfigured(true) },
    );
  };

  if (justConfigured) {
    return (
      <div className="w-full max-w-lg text-center py-10">
        <CheckCircle2 className="w-8 h-8 text-status-ok mx-auto mb-3" />
        <p className="text-sm text-fg">You're set up. Starting a conversation…</p>
      </div>
    );
  }

  const loading = providersLoading || detectLoading;

  return (
    <div className="w-full max-w-lg space-y-3">
      {loading && (
        <div className="flex items-center justify-center gap-2 text-sm text-fg-muted py-6">
          <Loader2 className="w-4 h-4 animate-spin" />
          Looking for Claude and Codex on your machine…
        </div>
      )}

      {!loading && detected.length > 0 && (
        <div className="rounded-[10px] border border-border-subtle bg-bg-elevated p-4">
          <div className="flex items-center gap-2 mb-3">
            <Terminal className="w-4 h-4 text-primary" />
            <h3 className="text-sm font-medium text-fg">Found on your machine</h3>
          </div>
          <div className="space-y-2">
            {detected.map((d) => (
              <button
                key={d.provider_type}
                type="button"
                onClick={() => handleUseCLI(d.provider_type)}
                disabled={settingsMutation.isPending}
                className="w-full flex items-center justify-between rounded-md border border-border-subtle bg-surface px-3 py-2.5 text-left hover:border-border hover:bg-surface-hover transition-colors disabled:opacity-60"
              >
                <div className="min-w-0">
                  <div className="text-sm font-medium text-fg">{CLI_LABELS[d.name] ?? d.name}</div>
                  <div className="text-[11px] text-fg-faint font-mono truncate">{d.path}</div>
                </div>
                <span className="text-[11px] font-semibold uppercase tracking-wide text-primary shrink-0 ml-3">
                  Use this →
                </span>
              </button>
            ))}
          </div>
        </div>
      )}

      {!loading && (
        <ApiKeyConnect
          providers={providers ?? []}
          models={models}
          onDone={() => setJustConfigured(true)}
        />
      )}

      {!loading && (
        <button
          type="button"
          onClick={onDismiss}
          className="w-full text-center text-xs text-fg-faint hover:text-fg-muted transition-colors py-1"
        >
          Skip for now
        </button>
      )}
    </div>
  );
}
