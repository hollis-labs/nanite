import {
  ChevronLeft,
  Eye,
  EyeOff,
  LayoutGrid,
  Package,
  Settings2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { WIDGET_REGISTRY } from "@/generated/plugin-widgets";
import type { PluginUIComponent } from "@/lib/types";

// ─── Props ──────────────────────────────────────────────────────────

export interface WidgetDetailViewProps {
  widget: PluginUIComponent;
  widgetId: string;
  visible: boolean;
  onToggleVisibility: () => void;
  onConfigurePlugin: (pluginId: string) => void;
  onBack: () => void;
}

// ─── Sub-components ─────────────────────────────────────────────────

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      {children}
    </div>
  );
}

function CardHeader({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 px-4 py-3 text-sm font-medium text-fg">
      {children}
    </div>
  );
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-3 py-1.5">
      <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
      <span className="text-xs text-fg-secondary">{value}</span>
    </div>
  );
}

// ─── Main Component ─────────────────────────────────────────────────

export function WidgetDetailView({
  widget,
  widgetId,
  visible,
  onToggleVisibility,
  onConfigurePlugin,
  onBack,
}: WidgetDetailViewProps) {
  const registryEntry = WIDGET_REGISTRY[widgetId];
  const source = registryEntry?.source ?? "unknown";
  const isCore = source === "core";

  return (
    <div className="space-y-5">
      {/* ── Header ─────────────────────────────────────────────────── */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={onBack}>
          <ChevronLeft className="w-4 h-4" />
        </Button>
        <div className="flex items-center gap-3 flex-1 min-w-0">
          <span
            className={`inline-flex items-center justify-center w-12 h-12 rounded-lg shrink-0 ${
              visible ? "bg-zinc-700 text-zinc-300" : "bg-zinc-300 text-zinc-500"
            }`}
          >
            <LayoutGrid className="w-6 h-6" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-xl font-semibold text-fg truncate">
                {widget.name}
              </h2>
              {visible && (
                <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />
              )}
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                {isCore ? "core" : "plugin"}
              </span>
            </div>
            <p className="text-xs text-fg-muted font-mono truncate">{widgetId}</p>
          </div>
          {/* Actions */}
          <div className="flex items-center gap-1 shrink-0">
            {widget.plugin_id && (
              <Button
                variant="ghost"
                size="icon"
                className="w-8 h-8 text-fg-faint hover:text-fg-secondary"
                onClick={() => onConfigurePlugin(widget.plugin_id!)}
                title="Configure source plugin"
              >
                <Settings2 className="w-4 h-4" />
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon"
              className="w-8 h-8 text-fg-faint hover:text-fg-secondary"
              onClick={onToggleVisibility}
              title={visible ? "Hide widget" : "Show widget"}
            >
              {visible ? (
                <Eye className="w-4 h-4" />
              ) : (
                <EyeOff className="w-4 h-4" />
              )}
            </Button>
          </div>
        </div>
      </div>

      {/* ── Details Card ───────────────────────────────────────────── */}
      <Card>
        <CardHeader>
          <LayoutGrid className="w-4 h-4" />
          <span>Details</span>
        </CardHeader>
        <div className="px-4 pb-4 space-y-0.5">
          <MetaRow label="Widget ID" value={widgetId} />
          <MetaRow label="Name" value={widget.name} />
          <MetaRow label="Type" value={widget.type} />
          <MetaRow label="Source" value={isCore ? "Core (built-in)" : source} />
          {widget.plugin_id && <MetaRow label="Plugin" value={widget.plugin_id} />}
          <MetaRow label="Visible" value={visible ? "Yes" : "No"} />
        </div>
      </Card>

      {/* ── Description ────────────────────────────────────────────── */}
      {widget.description && (
        <Card>
          <CardHeader>
            <Eye className="w-4 h-4" />
            <span>Description</span>
          </CardHeader>
          <div className="px-4 pb-4">
            <p className="text-xs text-fg-secondary leading-relaxed">
              {widget.description}
            </p>
          </div>
        </Card>
      )}

      {/* ── Props Schema ───────────────────────────────────────────── */}
      {widget.props && Object.keys(widget.props).length > 0 && (
        <Card>
          <CardHeader>
            <Settings2 className="w-4 h-4" />
            <span>Props</span>
          </CardHeader>
          <div className="px-4 pb-4">
            <pre className="text-xs text-fg-secondary overflow-x-auto font-mono bg-bg-elevated/40 rounded-lg p-3 border border-border-subtle">
              {JSON.stringify(widget.props, null, 2)}
            </pre>
          </div>
        </Card>
      )}

      {/* ── Source Plugin Link ──────────────────────────────────────── */}
      {widget.plugin_id && (
        <Card>
          <CardHeader>
            <Package className="w-4 h-4" />
            <span>Source Plugin</span>
          </CardHeader>
          <div className="px-4 pb-4">
            <button
              onClick={() => onConfigurePlugin(widget.plugin_id!)}
              className="flex items-center gap-2 px-3 py-2 rounded-md hover:bg-surface/40 transition-colors w-full text-left"
            >
              <span className="inline-flex items-center justify-center w-8 h-8 rounded-lg bg-zinc-700 text-zinc-300 shrink-0">
                <Package className="w-4 h-4" />
              </span>
              <div className="flex-1 min-w-0">
                <span className="text-xs font-medium text-fg truncate block">
                  {widget.plugin_id}
                </span>
                <span className="text-[11px] text-fg-muted">
                  Open plugin settings
                </span>
              </div>
              <Settings2 className="w-3.5 h-3.5 text-fg-faint shrink-0" />
            </button>
          </div>
        </Card>
      )}
    </div>
  );
}
