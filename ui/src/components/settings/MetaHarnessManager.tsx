import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Save, SquareTerminal, Trash2 } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import type { MetaHarness, MetaHarnessInput } from "@/lib/types";
import { cn } from "@/lib/utils";

const PROVIDER_OPTIONS = [
  { value: "pty-claude", label: "Claude Code (cli)" },
  { value: "codex", label: "Codex (cli)" },
  { value: "opencode", label: "OpenCode (cli)" },
];

const EMPTY_FORM: MetaHarnessInput = {
  id: "",
  display_name: "",
  ui_label: "",
  provider: "pty-claude",
  workdir: "",
  role: "",
  project: "",
  work_root: "",
  tracking_root: "",
  boot_mode: "",
  args: [],
  env: {},
  mcp_servers: [],
};

export function MetaHarnessManager() {
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<string>("");
  const [form, setForm] = useState<MetaHarnessInput>(EMPTY_FORM);
  const [argsText, setArgsText] = useState("");
  const [envText, setEnvText] = useState("");
  const [mcpText, setMcpText] = useState("");
  const [error, setError] = useState<string | null>(null);

  const { data: harnesses = [], isLoading } = useQuery({
    queryKey: ["meta-harnesses"],
    queryFn: api.listMetaHarnesses,
  });

  const selected = useMemo(
    () => harnesses.find((item) => item.id === selectedId) ?? null,
    [harnesses, selectedId],
  );

  useEffect(() => {
    if (selectedId || harnesses.length === 0) return;
    setSelectedId(harnesses[0]?.id ?? "");
  }, [harnesses, selectedId]);

  useEffect(() => {
    if (!selected) return;
    setForm(recordToInput(selected));
    setArgsText((selected.args ?? []).join("\n"));
    setEnvText(formatEnv(selected.env ?? {}));
    setMcpText((selected.mcp_servers ?? []).join("\n"));
    setError(null);
  }, [selected]);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["meta-harnesses"] });
    void queryClient.invalidateQueries({ queryKey: ["start-surface-capabilities"] });
    void queryClient.invalidateQueries({ queryKey: ["provider-statuses"] });
  };

  const save = useMutation({
    mutationFn: () => {
      const payload = buildPayload(form, argsText, envText, mcpText);
      if (!payload.id) throw new Error("ID is required.");
      return selected ? api.updateMetaHarness(selected.id, payload) : api.createMetaHarness(payload);
    },
    onSuccess: (record) => {
      setSelectedId(record.id);
      invalidate();
      setError(null);
    },
    onError: (err) => setError(errorMessage(err)),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteMetaHarness(id),
    onSuccess: () => {
      setSelectedId("");
      setForm(EMPTY_FORM);
      invalidate();
    },
    onError: (err) => setError(errorMessage(err)),
  });

  const startNew = () => {
    setSelectedId("");
    setForm(EMPTY_FORM);
    setArgsText("");
    setEnvText("");
    setMcpText("");
    setError(null);
  };

  return (
    <div className="grid min-h-0 grid-cols-[260px_minmax(0,1fr)] gap-4">
      <aside className="min-h-0 rounded-[8px] border border-border-subtle bg-bg-elevated">
        <div className="flex h-11 items-center justify-between border-b border-border-subtle px-3">
          <div className="flex items-center gap-2 text-sm font-semibold text-fg">
            <SquareTerminal className="size-4 text-fg-muted" />
            Harnesses
          </div>
          <Button size="sm" variant="ghost" className="h-7 gap-1 px-2 text-xs" onClick={startNew}>
            <Plus className="size-3.5" />
            New
          </Button>
        </div>
        <div className="no-scrollbar max-h-[calc(100vh-190px)] overflow-y-auto p-2">
          {isLoading ? <div className="px-2 py-3 text-xs text-fg-muted">Loading...</div> : null}
          {!isLoading && harnesses.length === 0 ? (
            <div className="px-2 py-3 text-xs text-fg-muted">No harnesses configured</div>
          ) : null}
          {harnesses.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setSelectedId(item.id)}
              className={cn(
                "mb-1 w-full rounded-[6px] px-2 py-2 text-left transition-colors",
                selectedId === item.id ? "bg-surface text-fg" : "text-fg-secondary hover:bg-surface/60",
              )}
            >
              <div className="truncate text-sm font-medium">{item.ui_label || item.display_name || item.id}</div>
              <div className="mt-0.5 truncate font-mono text-[11px] text-fg-muted">
                {providerLabel(item.provider)} / {item.id}
              </div>
            </button>
          ))}
        </div>
      </aside>

      <section className="min-h-0 rounded-[8px] border border-border-subtle bg-bg-elevated">
        <div className="flex h-11 items-center justify-between border-b border-border-subtle px-4">
          <div>
            <h2 className="text-sm font-semibold text-fg">
              {selected ? "Edit harness" : "Create harness"}
            </h2>
          </div>
          <div className="flex items-center gap-2">
            {selected ? (
              <Button
                size="sm"
                variant="ghost"
                className="h-7 gap-1 px-2 text-xs text-danger hover:text-danger"
                onClick={() => remove.mutate(selected.id)}
                disabled={remove.isPending}
              >
                <Trash2 className="size-3.5" />
                Delete
              </Button>
            ) : null}
            <Button size="sm" className="h-7 gap-1 px-2 text-xs" onClick={() => save.mutate()} disabled={save.isPending}>
              <Save className="size-3.5" />
              Save
            </Button>
          </div>
        </div>

        <div className="no-scrollbar max-h-[calc(100vh-190px)] overflow-y-auto p-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="ID">
              <Input
                value={form.id ?? ""}
                disabled={Boolean(selected)}
                onChange={(event) => setForm((current) => ({ ...current, id: event.target.value }))}
                placeholder="claude-code"
              />
            </Field>
            <Field label="Label">
              <Input
                value={form.ui_label ?? ""}
                onChange={(event) => {
                  const value = event.target.value;
                  setForm((current) => ({ ...current, ui_label: value, display_name: value }));
                }}
                placeholder="Claude Code (cli)"
              />
            </Field>
            <Field label="Provider">
              <select
                value={form.provider ?? "pty-claude"}
                onChange={(event) => setForm((current) => ({ ...current, provider: event.target.value }))}
                className="h-9 w-full rounded-[6px] border border-border-subtle bg-bg px-2 text-[13px] text-fg outline-none focus:border-ring"
              >
                {PROVIDER_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Workdir">
              <Input
                value={form.workdir ?? ""}
                onChange={(event) => {
                  const value = event.target.value;
                  setForm((current) => ({ ...current, workdir: value, work_root: current.work_root || value }));
                }}
                placeholder="/path/to/project"
              />
            </Field>
            <Field label="Role">
              <Input
                value={form.role ?? ""}
                onChange={(event) => setForm((current) => ({ ...current, role: event.target.value }))}
                placeholder="backend"
              />
            </Field>
            <Field label="Project">
              <Input
                value={form.project ?? ""}
                onChange={(event) => setForm((current) => ({ ...current, project: event.target.value }))}
                placeholder="nanite"
              />
            </Field>
            <Field label="Work root">
              <Input
                value={form.work_root ?? ""}
                onChange={(event) => setForm((current) => ({ ...current, work_root: event.target.value }))}
                placeholder="/path/to/project"
              />
            </Field>
            <Field label="Tracking root">
              <Input
                value={form.tracking_root ?? ""}
                onChange={(event) => setForm((current) => ({ ...current, tracking_root: event.target.value }))}
                placeholder="/path/to/tracking"
              />
            </Field>
          </div>

          <div className="mt-3 grid grid-cols-3 gap-3">
            <Field label="Args">
              <textarea
                value={argsText}
                onChange={(event) => setArgsText(event.target.value)}
                className="min-h-28 w-full resize-none rounded-[6px] border border-border-subtle bg-bg px-2 py-2 font-mono text-xs text-fg outline-none focus:border-ring"
                placeholder="--skip-git-repo-check"
              />
            </Field>
            <Field label="Env">
              <textarea
                value={envText}
                onChange={(event) => setEnvText(event.target.value)}
                className="min-h-28 w-full resize-none rounded-[6px] border border-border-subtle bg-bg px-2 py-2 font-mono text-xs text-fg outline-none focus:border-ring"
                placeholder="KEY=value"
              />
            </Field>
            <Field label="MCP servers">
              <textarea
                value={mcpText}
                onChange={(event) => setMcpText(event.target.value)}
                className="min-h-28 w-full resize-none rounded-[6px] border border-border-subtle bg-bg px-2 py-2 font-mono text-xs text-fg outline-none focus:border-ring"
                placeholder="server-name"
              />
            </Field>
          </div>

          {selected ? (
            <div className="mt-4 rounded-[6px] border border-border-subtle bg-bg px-3 py-2 font-mono text-[11px] text-fg-muted">
              {selected.profile_path}
              <br />
              {selected.launch_path}
            </div>
          ) : null}
          {error ? (
            <div className="mt-4 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger">
              {error}
            </div>
          ) : null}
        </div>
      </section>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="font-mono text-[10px] font-semibold uppercase tracking-[0.14em] text-fg-muted">
        {label}
      </span>
      {children}
    </label>
  );
}

function recordToInput(record: MetaHarness): MetaHarnessInput {
  return {
    id: record.id,
    display_name: record.display_name,
    ui_label: record.ui_label,
    provider: record.provider,
    workdir: record.workdir,
    boot_mode: record.boot_mode,
    role: record.role,
    project: record.project,
    work_root: record.work_root,
    tracking_root: record.tracking_root,
    args: record.args,
    env: record.env,
    mcp_servers: record.mcp_servers,
  };
}

function buildPayload(
  form: MetaHarnessInput,
  argsText: string,
  envText: string,
  mcpText: string,
): MetaHarnessInput {
  return {
    ...form,
    id: form.id?.trim(),
    display_name: (form.display_name || form.ui_label || form.id || "").trim(),
    ui_label: (form.ui_label || form.display_name || form.id || "").trim(),
    args: splitLines(argsText),
    env: parseEnv(envText),
    mcp_servers: splitLines(mcpText),
  };
}

function splitLines(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function formatEnv(env: Record<string, string>): string {
  return Object.entries(env)
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function parseEnv(value: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of splitLines(value)) {
    const idx = line.indexOf("=");
    if (idx < 0) {
      out[line] = "";
      continue;
    }
    out[line.slice(0, idx).trim()] = line.slice(idx + 1);
  }
  return out;
}

function providerLabel(value: string): string {
  return PROVIDER_OPTIONS.find((option) => option.value === value)?.label ?? value;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : "Request failed.";
}
