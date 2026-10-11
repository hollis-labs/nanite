import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Key, Loader2, Terminal } from "lucide-react";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { ProviderStatus } from "@/lib/types";

const CLI_LABELS: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  opencode: "opencode",
};

// The CLI providers the wizard offers. Nanite has no default provider or
// model: choosing a CLI here only dismisses first-run setup, and the provider
// and model are chosen per agent or per launch.
const CLI_PROVIDER_TYPES = new Set(["pty", "pty-codex", "pty-opencode"]);

/** The "connect with an API key" half of the wizard: pick a provider and save
 * the key to the OS keychain. No default model is chosen; that is per agent. */
function ApiKeyConnect({ providers, onDone }: { providers: ProviderStatus[]; onDone: () => void }) {
  const apiProviders = useMemo(
    () => providers.filter((p) => !p.provider_type.startsWith("pty")),
    [providers],
  );
  const [providerId, setProviderId] = useState(apiProviders[0]?.id ?? "");
  const [key, setKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const selectedProvider = apiProviders.find((p) => p.id === providerId);

  if (apiProviders.length === 0) return null;

  const handleSaveKey = async () => {
    if (!selectedProvider || !key.trim()) return;
    setError(null);
    setSaving(true);
    try {
      await api.setProviderAPIKey(selectedProvider.id, key.trim());
      await queryClient.invalidateQueries({ queryKey: ["provider-statuses"] });
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save that key.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="rounded-[10px] border border-border-subtle bg-bg-elevated p-4">
      <div className="flex items-center gap-2 mb-3">
        <Key className="w-4 h-4 text-primary" />
        <h3 className="text-sm font-medium text-fg">Connect with an API key</h3>
      </div>

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
    </div>
  );
}

/**
 * First-run setup: auto-detect a Claude/Codex CLI already on PATH, or fall
 * back to an API key. Neither path writes a default provider or model:
 * Nanite has none, and each agent or launch names its own (CW-20261011-0031).
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
  const [justConfigured, setJustConfigured] = useState(false);

  const detected = useMemo(
    () => (detections ?? []).filter((d) => d.detected && CLI_PROVIDER_TYPES.has(d.provider_type)),
    [detections],
  );

  const handleUseCLI = () => setJustConfigured(true);

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
                onClick={handleUseCLI}
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
        <ApiKeyConnect providers={providers ?? []} onDone={() => setJustConfigured(true)} />
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
