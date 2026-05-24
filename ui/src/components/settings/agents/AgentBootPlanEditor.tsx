import type { ReactNode } from "react";
import { AlertTriangle, FilePlus2, FlaskConical, Route, ShieldAlert, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type {
  AgentBootCallback,
  AgentBootPlanCallbackOperation,
  AgentBootPlanDocument,
  AgentBootPlanDryRunResponse,
  AgentBootPlanPlantOperation,
  AgentBootPlantItem,
} from "@/lib/types";

const PLANT_TIMINGS = ["create", "start", "resume", "every_boot", "recovery_replant"] as const;
const CALLBACK_TIMINGS = [
  "before_boot",
  "after_boot",
  "before_first_turn",
  "on_resume",
  "on_recovery",
] as const;
const SOURCE_KINDS = ["path_file", "path_dir", "literal_file", "literal_dir", "generated"] as const;
const ENTRY_KINDS = ["file", "directory"] as const;
const OVERWRITE_POLICIES = ["never", "if_missing", "always", "if_hash_differs"] as const;
const PLANT_FAILURE_POLICIES = ["fail_boot", "warn", "skip"] as const;
const CALLBACK_TYPES = ["command", "tool_call", "message_injection", "http_request", "local_api"] as const;
const CALLBACK_FAILURE_POLICIES = ["fail_boot", "warn", "retry_once", "ignore"] as const;

export function createEmptyBootPlan(agentId = ""): AgentBootPlanDocument {
  return {
    agent_id: agentId,
    schema_version: 1,
    plant_items: [],
    callbacks: [],
    created_at: "",
    updated_at: "",
  };
}

export function createEmptyPlantItem(): AgentBootPlantItem {
  return {
    id: "",
    name: "",
    source_kind: "literal_file",
    source_path: "",
    content: "",
    target_rel_path: "",
    entry_kind: "file",
    timing: ["create"],
    secret: false,
    overwrite_policy: "if_missing",
    failure_policy: "fail_boot",
    enabled: true,
  };
}

export function createEmptyCallback(): AgentBootCallback {
  return {
    id: "",
    name: "",
    timing: "after_boot",
    callback_type: "message_injection",
    tool_name: "",
    tool_input: {},
    message: "",
    request: {
      method: "POST",
      url: "",
      path: "",
      headers: {},
      body: "",
    },
    permissions: {},
    timeout_seconds: 30,
    env: {},
    failure_policy: "warn",
    enabled: true,
  };
}

export function hasBootPlanContent(plan: AgentBootPlanDocument | null | undefined) {
  if (!plan) return false;
  return (plan.plant_items?.length ?? 0) > 0 || (plan.callbacks?.length ?? 0) > 0;
}

export function AgentBootPlanEditor({
  plan,
  onChange,
  readOnly = false,
  compact = false,
}: {
  plan: AgentBootPlanDocument;
  onChange: (plan: AgentBootPlanDocument) => void;
  readOnly?: boolean;
  compact?: boolean;
}) {
  return (
    <div className="space-y-5">
      <section className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <div>
            <h4 className="text-[13px] font-semibold text-fg">Plant Items</h4>
            <p className="mt-1 text-[12px] text-fg-muted">
              Files, directories, or generated content planted into the boot workspace.
            </p>
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={readOnly}
            onClick={() =>
              onChange({
                ...plan,
                plant_items: [...plan.plant_items, createEmptyPlantItem()],
              })
            }
          >
            <FilePlus2 className="mr-1.5 h-3.5 w-3.5" />
            Add plant item
          </Button>
        </div>

        {plan.plant_items.length === 0 ? (
          <EmptyHint text="No plant items configured." />
        ) : (
          <div className="space-y-3">
            {plan.plant_items.map((item, index) => (
              <PlantItemEditor
                key={`plant-${index}-${item.id || "new"}`}
                item={item}
                compact={compact}
                readOnly={readOnly}
                onChange={(next) =>
                  onChange({
                    ...plan,
                    plant_items: plan.plant_items.map((current, rowIndex) =>
                      rowIndex === index ? next : current,
                    ),
                  })
                }
                onDelete={() =>
                  onChange({
                    ...plan,
                    plant_items: plan.plant_items.filter((_, rowIndex) => rowIndex !== index),
                  })
                }
              />
            ))}
          </div>
        )}
      </section>

      <section className="space-y-3">
        <div className="flex items-center justify-between gap-2">
          <div>
            <h4 className="text-[13px] font-semibold text-fg">Lifecycle Callbacks</h4>
            <p className="mt-1 text-[12px] text-fg-muted">
              Stored now and validated in dry-run. Execution stays preview-only in this phase.
            </p>
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={readOnly}
            onClick={() =>
              onChange({
                ...plan,
                callbacks: [...plan.callbacks, createEmptyCallback()],
              })
            }
          >
            <Route className="mr-1.5 h-3.5 w-3.5" />
            Add callback
          </Button>
        </div>

        {plan.callbacks.length === 0 ? (
          <EmptyHint text="No callbacks configured." />
        ) : (
          <div className="space-y-3">
            {plan.callbacks.map((callback, index) => (
              <CallbackEditor
                key={`callback-${index}-${callback.id || "new"}`}
                callback={callback}
                compact={compact}
                readOnly={readOnly}
                onChange={(next) =>
                  onChange({
                    ...plan,
                    callbacks: plan.callbacks.map((current, rowIndex) =>
                      rowIndex === index ? next : current,
                    ),
                  })
                }
                onDelete={() =>
                  onChange({
                    ...plan,
                    callbacks: plan.callbacks.filter((_, rowIndex) => rowIndex !== index),
                  })
                }
              />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

export function AgentBootPlanPreview({
  preview,
}: {
  preview: AgentBootPlanDryRunResponse;
}) {
  return (
    <div className="space-y-4">
      <FeedbackLists
        errors={preview.errors}
        warnings={preview.warnings}
        unsupported={preview.unsupported_notes}
      />

      <div className="grid gap-3 lg:grid-cols-2">
        <PreviewCard
          title={`Plant Operations (${preview.plant_operations.length})`}
          emptyText="No plant operations planned."
        >
          {preview.plant_operations.map((op) => (
            <PlantOperationRow key={`${op.item_id}-${op.target_rel_path}`} operation={op} />
          ))}
        </PreviewCard>

        <PreviewCard
          title={`Callback Order (${preview.callback_order.length})`}
          emptyText="No callbacks planned."
        >
          {preview.callback_order.map((op) => (
            <CallbackOperationRow key={`${op.callback_id}-${op.timing}`} operation={op} />
          ))}
        </PreviewCard>
      </div>
    </div>
  );
}

function PlantItemEditor({
  item,
  onChange,
  onDelete,
  readOnly,
  compact,
}: {
  item: AgentBootPlantItem;
  onChange: (item: AgentBootPlantItem) => void;
  onDelete: () => void;
  readOnly: boolean;
  compact: boolean;
}) {
  const showSourcePath = item.source_kind === "path_file" || item.source_kind === "path_dir";
  const showContent =
    item.source_kind === "literal_file" ||
    item.source_kind === "literal_dir" ||
    item.source_kind === "generated";

  return (
    <div className="rounded-[10px] border border-border-subtle bg-surface/30 p-3">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium text-fg">
            {item.name.trim() || item.id.trim() || "New plant item"}
          </div>
          <div className="mt-1 text-[12px] text-fg-muted">
            Schema version 1 plant entry. Relative target paths only.
          </div>
        </div>
        <Button type="button" size="sm" variant="ghost" disabled={readOnly} onClick={onDelete}>
          <Trash2 className="mr-1.5 h-3.5 w-3.5" />
          Remove
        </Button>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <Field label="Enabled">
          <ToggleField
            ariaLabel="Plant item enabled"
            checked={item.enabled}
            disabled={readOnly}
            onCheckedChange={(enabled) => onChange({ ...item, enabled })}
          />
        </Field>
        <Field label="Secret">
          <ToggleField
            ariaLabel="Plant item secret"
            checked={item.secret}
            disabled={readOnly}
            onCheckedChange={(secret) => onChange({ ...item, secret })}
          />
        </Field>
        <Field label="ID">
          <Input
            aria-label="Plant item id"
            value={item.id}
            disabled={readOnly}
            onChange={(event) => onChange({ ...item, id: event.target.value })}
          />
        </Field>
        <Field label="Name">
          <Input
            aria-label="Plant item name"
            value={item.name}
            disabled={readOnly}
            onChange={(event) => onChange({ ...item, name: event.target.value })}
          />
        </Field>
        <Field label="Source kind">
          <SelectField
            ariaLabel="Plant item source kind"
            value={item.source_kind}
            disabled={readOnly}
            options={SOURCE_KINDS}
            onChange={(source_kind) =>
              onChange({
                ...item,
                source_kind,
                entry_kind: source_kind.endsWith("_dir") ? "directory" : "file",
              })
            }
          />
        </Field>
        <Field label="Entry kind">
          <SelectField
            ariaLabel="Plant item entry kind"
            value={item.entry_kind}
            disabled={readOnly}
            options={ENTRY_KINDS}
            onChange={(entry_kind) => onChange({ ...item, entry_kind })}
          />
        </Field>
        <Field label="Target relative path" className="md:col-span-2">
          <Input
            aria-label="Plant item target relative path"
            value={item.target_rel_path}
            disabled={readOnly}
            onChange={(event) => onChange({ ...item, target_rel_path: event.target.value })}
            placeholder="docs/README.md"
          />
        </Field>
        {showSourcePath ? (
          <Field label="Source path" className="md:col-span-2">
            <Input
              aria-label="Plant item source path"
              value={item.source_path ?? ""}
              disabled={readOnly}
              onChange={(event) => onChange({ ...item, source_path: event.target.value })}
              placeholder="/abs/path/or/workspace-relative"
            />
          </Field>
        ) : null}
        {showContent ? (
          <Field
            label={item.source_kind === "generated" ? "Generated content / template" : "Content"}
            className="md:col-span-2"
          >
            <Textarea
              aria-label="Plant item content"
              rows={compact ? 3 : 5}
              value={item.content ?? ""}
              disabled={readOnly}
              onChange={(event) => onChange({ ...item, content: event.target.value })}
              placeholder={item.source_kind === "generated" ? "Template or generated seed body" : "Literal content"}
            />
          </Field>
        ) : null}
        <Field label="Overwrite policy">
          <SelectField
            ariaLabel="Plant item overwrite policy"
            value={item.overwrite_policy}
            disabled={readOnly}
            options={OVERWRITE_POLICIES}
            onChange={(overwrite_policy) => onChange({ ...item, overwrite_policy })}
          />
        </Field>
        <Field label="Failure policy">
          <SelectField
            ariaLabel="Plant item failure policy"
            value={item.failure_policy}
            disabled={readOnly}
            options={PLANT_FAILURE_POLICIES}
            onChange={(failure_policy) => onChange({ ...item, failure_policy })}
          />
        </Field>
        <div className="md:col-span-2">
          <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
            Timing
          </span>
          <div className="mt-2 flex flex-wrap gap-2">
            {PLANT_TIMINGS.map((timing) => (
              <label
                key={timing}
                className="inline-flex items-center gap-2 rounded-[8px] border border-border-subtle bg-bg px-2.5 py-1.5 text-xs text-fg"
              >
                <input
                  type="checkbox"
                  aria-label={`Plant timing ${timing}`}
                  checked={item.timing.includes(timing)}
                  disabled={readOnly}
                  onChange={() =>
                    onChange({
                      ...item,
                      timing: toggleString(item.timing, timing),
                    })
                  }
                />
                {timing}
              </label>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

function CallbackEditor({
  callback,
  onChange,
  onDelete,
  readOnly,
  compact,
}: {
  callback: AgentBootCallback;
  onChange: (callback: AgentBootCallback) => void;
  onDelete: () => void;
  readOnly: boolean;
  compact: boolean;
}) {
  return (
    <div className="rounded-[10px] border border-border-subtle bg-surface/30 p-3">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium text-fg">
            {callback.name.trim() || callback.id.trim() || "New callback"}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-[12px] text-fg-muted">
            <span className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] text-amber-200">
              Configured and dry-run only
            </span>
            <span>{callback.callback_type}</span>
          </div>
        </div>
        <Button type="button" size="sm" variant="ghost" disabled={readOnly} onClick={onDelete}>
          <Trash2 className="mr-1.5 h-3.5 w-3.5" />
          Remove
        </Button>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <Field label="Enabled">
          <ToggleField
            ariaLabel="Callback enabled"
            checked={callback.enabled}
            disabled={readOnly}
            onCheckedChange={(enabled) => onChange({ ...callback, enabled })}
          />
        </Field>
        <Field label="Timeout seconds">
          <Input
            aria-label="Callback timeout seconds"
            type="number"
            value={String(callback.timeout_seconds ?? 30)}
            disabled={readOnly}
            onChange={(event) =>
              onChange({
                ...callback,
                timeout_seconds: Number(event.target.value || "0"),
              })
            }
          />
        </Field>
        <Field label="ID">
          <Input
            aria-label="Callback id"
            value={callback.id}
            disabled={readOnly}
            onChange={(event) => onChange({ ...callback, id: event.target.value })}
          />
        </Field>
        <Field label="Name">
          <Input
            aria-label="Callback name"
            value={callback.name}
            disabled={readOnly}
            onChange={(event) => onChange({ ...callback, name: event.target.value })}
          />
        </Field>
        <Field label="Timing">
          <SelectField
            ariaLabel="Callback timing"
            value={callback.timing}
            disabled={readOnly}
            options={CALLBACK_TIMINGS}
            onChange={(timing) => onChange({ ...callback, timing })}
          />
        </Field>
        <Field label="Callback type">
          <SelectField
            ariaLabel="Callback type"
            value={callback.callback_type}
            disabled={readOnly}
            options={CALLBACK_TYPES}
            onChange={(callback_type) => onChange({ ...callback, callback_type })}
          />
        </Field>
        <Field label="Failure policy">
          <SelectField
            ariaLabel="Callback failure policy"
            value={callback.failure_policy}
            disabled={readOnly}
            options={CALLBACK_FAILURE_POLICIES}
            onChange={(failure_policy) => onChange({ ...callback, failure_policy })}
          />
        </Field>
        <Field label="Environment" className={compact ? "" : "md:col-span-2"}>
          <Textarea
            aria-label="Callback environment"
            rows={compact ? 2 : 3}
            value={mapToKeyValueText(callback.env)}
            disabled={readOnly}
            onChange={(event) => onChange({ ...callback, env: keyValueTextToMap(event.target.value) })}
            placeholder="KEY=value"
          />
        </Field>
        <div className="md:col-span-2">
          <CallbackPayloadEditor
            callback={callback}
            compact={compact}
            readOnly={readOnly}
            onChange={onChange}
          />
        </div>
      </div>
    </div>
  );
}

function CallbackPayloadEditor({
  callback,
  onChange,
  readOnly,
  compact,
}: {
  callback: AgentBootCallback;
  onChange: (callback: AgentBootCallback) => void;
  readOnly: boolean;
  compact: boolean;
}) {
  switch (callback.callback_type) {
    case "command":
      return (
        <Field label="Command argv">
          <Textarea
            aria-label="Callback command argv"
            rows={compact ? 3 : 4}
            value={arrayToLineText(callback.command?.argv ?? [])}
            disabled={readOnly}
            onChange={(event) =>
              onChange({
                ...callback,
                command: {
                  ...(callback.command ?? { workdir: "" }),
                  argv: lineTextToArray(event.target.value),
                },
              })
            }
            placeholder={"command\n--flag\nvalue"}
          />
        </Field>
      );
    case "tool_call":
      return (
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Tool name">
            <Input
              aria-label="Callback tool name"
              value={callback.tool_name ?? ""}
              disabled={readOnly}
              onChange={(event) => onChange({ ...callback, tool_name: event.target.value })}
              placeholder="mcp__docs__search"
            />
          </Field>
          <Field label="Tool payload">
            <Textarea
              aria-label="Callback tool payload"
              rows={compact ? 3 : 4}
              value={jsonText(callback.tool_input)}
              disabled={readOnly}
              onChange={(event) => onChange({ ...callback, tool_input: parseJSONObjectText(event.target.value) })}
              placeholder='{"query":"boot plan"}'
            />
          </Field>
        </div>
      );
    case "message_injection":
      return (
        <Field label="Message body">
          <Textarea
            aria-label="Callback message body"
            rows={compact ? 3 : 4}
            value={callback.message ?? ""}
            disabled={readOnly}
            onChange={(event) => onChange({ ...callback, message: event.target.value })}
            placeholder="Boot complete. Continue with the first task."
          />
        </Field>
      );
    case "http_request":
    case "local_api":
      return (
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Method">
            <Input
              aria-label="Callback request method"
              value={callback.request?.method ?? ""}
              disabled={readOnly}
              onChange={(event) =>
                onChange({
                  ...callback,
                  request: { ...(callback.request ?? {}), method: event.target.value },
                })
              }
              placeholder="POST"
            />
          </Field>
          <Field label={callback.callback_type === "local_api" ? "Path" : "URL"}>
            <Input
              aria-label="Callback request address"
              value={
                callback.callback_type === "local_api"
                  ? callback.request?.path ?? ""
                  : callback.request?.url ?? ""
              }
              disabled={readOnly}
              onChange={(event) =>
                onChange({
                  ...callback,
                  request:
                    callback.callback_type === "local_api"
                      ? { ...(callback.request ?? {}), path: event.target.value }
                      : { ...(callback.request ?? {}), url: event.target.value },
                })
              }
              placeholder={callback.callback_type === "local_api" ? "/v1/boot/ready" : "https://example.com/hook"}
            />
          </Field>
          <Field label="Headers" className="md:col-span-2">
            <Textarea
              aria-label="Callback request headers"
              rows={compact ? 2 : 3}
              value={mapToKeyValueText(callback.request?.headers)}
              disabled={readOnly}
              onChange={(event) =>
                onChange({
                  ...callback,
                  request: { ...(callback.request ?? {}), headers: keyValueTextToMap(event.target.value) },
                })
              }
              placeholder="Authorization=Bearer ..."
            />
          </Field>
          <Field label="Body" className="md:col-span-2">
            <Textarea
              aria-label="Callback request body"
              rows={compact ? 3 : 4}
              value={callback.request?.body ?? ""}
              disabled={readOnly}
              onChange={(event) =>
                onChange({
                  ...callback,
                  request: { ...(callback.request ?? {}), body: event.target.value },
                })
              }
              placeholder="Request body"
            />
          </Field>
        </div>
      );
    default:
      return (
        <EmptyHint text="Select a callback type to configure its payload." />
      );
  }
}

function PreviewCard({
  title,
  emptyText,
  children,
}: {
  title: string;
  emptyText: string;
  children: ReactNode;
}) {
  const rows = Array.isArray(children) ? children.filter(Boolean) : children;
  const empty = Array.isArray(rows) ? rows.length === 0 : !rows;
  return (
    <div className="rounded-[10px] border border-border-subtle bg-surface/30 p-3">
      <div className="text-sm font-medium text-fg">{title}</div>
      <div className="mt-3 space-y-2">
        {empty ? <div className="text-xs text-fg-muted">{emptyText}</div> : rows}
      </div>
    </div>
  );
}

function PlantOperationRow({ operation }: { operation: AgentBootPlanPlantOperation }) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-bg/60 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="text-xs font-medium text-fg">{operation.name || operation.item_id}</div>
        <Badge>{operation.entry_kind}</Badge>
        <Badge>{operation.overwrite_policy}</Badge>
        {operation.secret ? <Badge tone="warn">secret</Badge> : null}
      </div>
      <div className="mt-1 text-[12px] text-fg-muted">{operation.target_rel_path}</div>
      <div className="mt-2 text-[12px] text-fg-muted">
        Timing: {operation.timing.join(", ")}
      </div>
      {operation.source_path_redacted || operation.content_preview_redacted ? (
        <div className="mt-2 text-[12px] text-amber-200">Secret-backed source or content redacted.</div>
      ) : null}
      {operation.notes?.length ? (
        <ul className="mt-2 space-y-1 text-[12px] text-fg-muted">
          {operation.notes.map((note) => (
            <li key={note}>• {note}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function CallbackOperationRow({ operation }: { operation: AgentBootPlanCallbackOperation }) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-bg/60 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="text-xs font-medium text-fg">{operation.name || operation.callback_id}</div>
        <Badge>{operation.timing}</Badge>
        <Badge>{operation.callback_type}</Badge>
      </div>
      <div className="mt-2 text-[12px] text-fg-muted">
        Timeout: {operation.timeout_seconds}s • Failure policy: {operation.failure_policy}
      </div>
      {operation.payload_preview ? (
        <div className="mt-2 rounded-[8px] bg-bg px-2 py-1.5 text-[11px] text-fg-muted">
          {operation.payload_preview}
        </div>
      ) : null}
      {operation.env_redacted ? (
        <div className="mt-2 text-[12px] text-amber-200">Environment values are redacted in dry-run.</div>
      ) : null}
      {operation.notes?.length ? (
        <ul className="mt-2 space-y-1 text-[12px] text-fg-muted">
          {operation.notes.map((note) => (
            <li key={note}>• {note}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function FeedbackLists({
  errors,
  warnings,
  unsupported,
}: {
  errors: string[];
  warnings: string[];
  unsupported: string[];
}) {
  if (errors.length === 0 && warnings.length === 0 && unsupported.length === 0) {
    return null;
  }
  return (
    <div className="space-y-3">
      {errors.length > 0 ? (
        <FeedbackCard
          icon={<AlertTriangle className="h-4 w-4 text-red-300" />}
          title="Validation errors"
          items={errors}
          tone="error"
        />
      ) : null}
      {warnings.length > 0 ? (
        <FeedbackCard
          icon={<FlaskConical className="h-4 w-4 text-amber-300" />}
          title="Warnings"
          items={warnings}
          tone="warn"
        />
      ) : null}
      {unsupported.length > 0 ? (
        <FeedbackCard
          icon={<ShieldAlert className="h-4 w-4 text-sky-300" />}
          title="Preview-only / unsupported"
          items={unsupported}
          tone="info"
        />
      ) : null}
    </div>
  );
}

function FeedbackCard({
  icon,
  title,
  items,
  tone,
}: {
  icon: ReactNode;
  title: string;
  items: string[];
  tone: "error" | "warn" | "info";
}) {
  const toneClass =
    tone === "error"
      ? "border-red-500/30 bg-red-500/10"
      : tone === "warn"
        ? "border-amber-500/30 bg-amber-500/10"
        : "border-sky-500/30 bg-sky-500/10";
  return (
    <div className={`rounded-[10px] border px-3 py-3 ${toneClass}`}>
      <div className="flex items-center gap-2 text-sm font-medium text-fg">
        {icon}
        {title}
      </div>
      <ul className="mt-2 space-y-1 text-[12px] text-fg-muted">
        {items.map((item) => (
          <li key={item}>• {item}</li>
        ))}
      </ul>
    </div>
  );
}

function Field({
  label,
  className = "",
  children,
}: {
  label: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <label className={`grid gap-1.5 ${className}`}>
      <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-fg-muted">
        {label}
      </span>
      {children}
    </label>
  );
}

function SelectField({
  ariaLabel,
  value,
  options,
  disabled,
  onChange,
}: {
  ariaLabel: string;
  value: string;
  options: readonly string[];
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <select
      aria-label={ariaLabel}
      value={value}
      disabled={disabled}
      onChange={(event) => onChange(event.target.value)}
      className="h-9 rounded-[8px] border border-border-subtle bg-bg px-3 text-sm text-fg disabled:opacity-60"
    >
      {options.map((option) => (
        <option key={option} value={option}>
          {option}
        </option>
      ))}
    </select>
  );
}

function ToggleField({
  ariaLabel,
  checked,
  disabled,
  onCheckedChange,
}: {
  ariaLabel: string;
  checked: boolean;
  disabled: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex h-9 items-center rounded-[8px] border border-border-subtle bg-bg px-3">
      <Switch aria-label={ariaLabel} checked={checked} disabled={disabled} onCheckedChange={onCheckedChange} />
    </div>
  );
}

function Badge({
  children,
  tone = "default",
}: {
  children: ReactNode;
  tone?: "default" | "warn";
}) {
  return (
    <span
      className={`rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-[0.08em] ${
        tone === "warn"
          ? "border-amber-500/30 bg-amber-500/10 text-amber-200"
          : "border-border-subtle bg-surface text-fg-muted"
      }`}
    >
      {children}
    </span>
  );
}

function EmptyHint({ text }: { text: string }) {
  return (
    <div className="rounded-[10px] border border-dashed border-border-subtle px-3 py-3 text-xs text-fg-muted">
      {text}
    </div>
  );
}

function toggleString(values: string[], value: string) {
  return values.includes(value) ? values.filter((current) => current !== value) : [...values, value];
}

function arrayToLineText(values: string[]) {
  return values.join("\n");
}

function lineTextToArray(value: string) {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

function mapToKeyValueText(value?: Record<string, string>) {
  if (!value) return "";
  return Object.entries(value)
    .map(([key, entryValue]) => `${key}=${entryValue}`)
    .join("\n");
}

function keyValueTextToMap(value: string) {
  const entries = value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const index = line.indexOf("=");
      if (index === -1) return [line, ""];
      return [line.slice(0, index).trim(), line.slice(index + 1).trim()];
    })
    .filter(([key]) => key !== "");
  return Object.fromEntries(entries);
}

function jsonText(value?: Record<string, unknown>) {
  return value && Object.keys(value).length > 0 ? JSON.stringify(value, null, 2) : "";
}

function parseJSONObjectText(value: string) {
  if (value.trim() === "") return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}
