import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Eye,
  Keyboard,
  LayoutGrid,
  MessageSquare,
  Package,
  Puzzle,
  Settings2,
  Wrench,
  Zap,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type {
  PluginInfo,
  PluginKeybinding,
  PluginUIComponent,
  SkippedRegistration,
  SlashCommandDef,
  UISlotEntry,
} from "@/lib/types";
import { PluginConfigPanel } from "./PluginConfigPanel";

// ─── Props ──────────────────────────────────────────────────────────

export interface PluginDetailViewProps {
  plugin: PluginInfo;
  onBack: () => void;
}

// ─── Sub-components ─────────────────────────────────────────────────

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
      {children}
    </div>
  );
}

function CardHeader({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 px-4 py-3 text-sm font-medium text-fg">{children}</div>
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

const COMPONENT_TYPE_ICONS: Record<string, typeof Puzzle> = {
  widget: LayoutGrid,
  envelope: MessageSquare,
  action: Zap,
  workflow: Settings2,
  view: Eye,
};

function ComponentTypeIcon({ type }: { type: string }) {
  const Icon = COMPONENT_TYPE_ICONS[type] || Puzzle;
  return <Icon className="w-3.5 h-3.5 text-fg-muted shrink-0" />;
}

// ─── Main Component ─────────────────────────────────────────────────

export function PluginDetailView({ plugin, onBack }: PluginDetailViewProps) {
  const isActive = plugin.status === "active";

  // Fetch all plugin-registered UI components
  const { data: allComponents = [] } = useQuery({
    queryKey: ["plugin-ui-components"],
    queryFn: api.listUIComponents,
    staleTime: 60_000,
  });

  // Fetch all slash commands
  const { data: allCommands = [] } = useQuery({
    queryKey: ["commands"],
    queryFn: api.listCommands,
    staleTime: 60_000,
  });

  // Fetch all keybindings
  const { data: keybindingsData } = useQuery({
    queryKey: ["plugin-keybindings"],
    queryFn: api.listPluginKeybindings,
    staleTime: 60_000,
  });

  // Fetch all UI slots
  const { data: allSlots = {} } = useQuery({
    queryKey: ["plugin-ui-slots"],
    queryFn: api.listUISlots,
    staleTime: 60_000,
  });

  // Filter to this plugin
  const components = useMemo(
    () => allComponents.filter((c: PluginUIComponent) => c.plugin_id === plugin.name),
    [allComponents, plugin.name],
  );

  const commands = useMemo(
    () => allCommands.filter((c: SlashCommandDef) => c.source === plugin.name),
    [allCommands, plugin.name],
  );

  const keybindings = useMemo(() => {
    const all: PluginKeybinding[] = keybindingsData?.keybindings ?? [];
    // Keybindings from this plugin — the action field typically starts with plugin name
    return all.filter(
      (k) => k.action.startsWith(plugin.name + ":") || k.action.startsWith(plugin.name + "."),
    );
  }, [keybindingsData, plugin.name]);

  const slots = useMemo(() => {
    const result: UISlotEntry[] = [];
    for (const entries of Object.values(allSlots)) {
      for (const entry of entries as UISlotEntry[]) {
        if (entry.plugin_id === plugin.name) result.push(entry);
      }
    }
    return result;
  }, [allSlots, plugin.name]);

  // Group components by type
  const componentsByType = useMemo(() => {
    const map = new Map<string, PluginUIComponent[]>();
    for (const c of components) {
      const list = map.get(c.type) || [];
      list.push(c);
      map.set(c.type, list);
    }
    return map;
  }, [components]);

  const totalRegistrations =
    components.length + commands.length + keybindings.length + slots.length;

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
              isActive ? "bg-surface-hover text-fg-secondary" : "bg-surface text-fg-muted"
            }`}
          >
            <Package className="w-6 h-6" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-xl font-semibold text-fg truncate">{plugin.name}</h2>
              {isActive && <span className="w-1.5 h-1.5 rounded-full bg-status-ok shrink-0" />}
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                {plugin.type}
              </span>
            </div>
            <div className="flex items-center gap-1.5 mt-0.5">
              <span className="text-xs text-fg-muted">v{plugin.version || "0.0.0"}</span>
              {plugin.author && (
                <>
                  <span className="text-fg-faint text-[10px]">&middot;</span>
                  <span className="text-xs text-fg-muted">{plugin.author}</span>
                </>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* ── Tabs ───────────────────────────────────────────────────── */}
      <Tabs defaultValue="overview">
        <TabsList variant="line" className="w-full justify-start border-b border-border">
          <TabsTrigger value="overview" className="gap-1.5 text-xs">
            <Eye className="w-3.5 h-3.5" /> Overview
          </TabsTrigger>
          <TabsTrigger value="components" className="gap-1.5 text-xs">
            <Puzzle className="w-3.5 h-3.5" /> Components
            {components.length > 0 && (
              <span className="text-[10px] text-fg-faint">({components.length})</span>
            )}
          </TabsTrigger>
          <TabsTrigger value="commands" className="gap-1.5 text-xs">
            <Wrench className="w-3.5 h-3.5" /> Commands
            {commands.length > 0 && (
              <span className="text-[10px] text-fg-faint">({commands.length})</span>
            )}
          </TabsTrigger>
          <TabsTrigger value="config" className="gap-1.5 text-xs">
            <Settings2 className="w-3.5 h-3.5" /> Config
          </TabsTrigger>
        </TabsList>

        {/* ── Overview Tab ─────────────────────────────────────────── */}
        <TabsContent value="overview" className="pt-4 space-y-4">
          <Card>
            <CardHeader>
              <Package className="w-4 h-4" />
              <span>Details</span>
            </CardHeader>
            <div className="px-4 pb-4 space-y-0.5">
              <MetaRow label="Name" value={plugin.name} />
              <MetaRow label="Version" value={plugin.version || "0.0.0"} />
              <MetaRow label="Status" value={plugin.status} />
              <MetaRow label="Type" value={plugin.type} />
              {plugin.author && <MetaRow label="Author" value={plugin.author} />}
              {plugin.url && <MetaRow label="URL" value={plugin.url} />}
            </div>
          </Card>

          {plugin.description && (
            <Card>
              <CardHeader>
                <Eye className="w-4 h-4" />
                <span>Description</span>
              </CardHeader>
              <div className="px-4 pb-4">
                <p className="text-xs text-fg-secondary leading-relaxed">{plugin.description}</p>
              </div>
            </Card>
          )}

          {/* Registration summary */}
          <Card>
            <CardHeader>
              <Puzzle className="w-4 h-4" />
              <span>Registrations ({totalRegistrations})</span>
            </CardHeader>
            <div className="px-4 pb-4 space-y-1">
              {components.length > 0 && (
                <SummaryRow icon={LayoutGrid} label="UI Components" count={components.length} />
              )}
              {commands.length > 0 && (
                <SummaryRow icon={Wrench} label="Slash Commands" count={commands.length} />
              )}
              {keybindings.length > 0 && (
                <SummaryRow icon={Keyboard} label="Keybindings" count={keybindings.length} />
              )}
              {slots.length > 0 && <SummaryRow icon={Zap} label="UI Slots" count={slots.length} />}
              {totalRegistrations === 0 && (
                <p className="text-xs text-fg-muted py-2">No registrations</p>
              )}
            </div>
          </Card>

          {/* Skipped registrations — runtime opt-outs the plugin declined. */}
          <SkippedRegistrationsCard skipped={plugin.skipped_registrations ?? []} />
        </TabsContent>

        {/* ── Components Tab ───────────────────────────────────────── */}
        <TabsContent value="components" className="pt-4 space-y-4">
          {components.length === 0 && slots.length === 0 && keybindings.length === 0 ? (
            <div className="text-center py-8">
              <Puzzle className="w-8 h-8 text-fg-faint mx-auto mb-2" />
              <p className="text-xs text-fg-muted">No UI components registered</p>
            </div>
          ) : (
            <>
              {/* Grouped components by type */}
              {Array.from(componentsByType.entries()).map(([type, items]) => (
                <Card key={type}>
                  <CardHeader>
                    <ComponentTypeIcon type={type} />
                    <span className="capitalize">
                      {type}s ({items.length})
                    </span>
                  </CardHeader>
                  <div className="px-4 pb-3 space-y-1">
                    {items.map((item) => (
                      <div
                        key={item.id}
                        className="flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface/40 transition-colors"
                      >
                        <ComponentTypeIcon type={item.type} />
                        <span className="text-xs font-medium text-fg flex-1 truncate">
                          {item.name}
                        </span>
                        <span className="text-[10px] font-mono text-fg-faint truncate max-w-[40%]">
                          {item.id}
                        </span>
                      </div>
                    ))}
                  </div>
                  {/* Show descriptions in a detail footer */}
                  {items.some((i) => i.description) && (
                    <div className="border-t border-border-subtle px-4 py-2.5 bg-bg/40 space-y-1">
                      {items
                        .filter((i) => i.description)
                        .map((item) => (
                          <p key={item.id} className="text-[11px] text-fg-muted">
                            <span className="font-medium text-fg-secondary">{item.name}:</span>{" "}
                            {item.description}
                          </p>
                        ))}
                    </div>
                  )}
                </Card>
              ))}

              {/* UI Slots */}
              {slots.length > 0 && (
                <Card>
                  <CardHeader>
                    <Zap className="w-4 h-4" />
                    <span>UI Slots ({slots.length})</span>
                  </CardHeader>
                  <div className="px-4 pb-3 space-y-1">
                    {slots.map((slot) => (
                      <div
                        key={slot.id}
                        className="flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface/40 transition-colors"
                      >
                        <Zap className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                        <span className="text-xs font-medium text-fg truncate">{slot.label}</span>
                        <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                          {slot.slot}
                        </span>
                        {slot.component && (
                          <span className="text-[10px] font-mono text-fg-faint truncate">
                            {slot.component}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </Card>
              )}

              {/* Keybindings */}
              {keybindings.length > 0 && (
                <Card>
                  <CardHeader>
                    <Keyboard className="w-4 h-4" />
                    <span>Keybindings ({keybindings.length})</span>
                  </CardHeader>
                  <div className="px-4 pb-3 space-y-1">
                    {keybindings.map((kb) => (
                      <div
                        key={kb.id}
                        className="flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface/40 transition-colors"
                      >
                        <Keyboard className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                        <kbd className="text-[11px] font-mono px-1.5 py-0.5 rounded bg-bg-elevated border border-border-subtle text-fg-secondary">
                          {kb.key}
                        </kbd>
                        <span className="text-xs text-fg flex-1 truncate">
                          {kb.label || kb.action}
                        </span>
                      </div>
                    ))}
                  </div>
                </Card>
              )}
            </>
          )}
        </TabsContent>

        {/* ── Commands Tab ─────────────────────────────────────────── */}
        <TabsContent value="commands" className="pt-4 space-y-4">
          {commands.length === 0 ? (
            <div className="text-center py-8">
              <Wrench className="w-8 h-8 text-fg-faint mx-auto mb-2" />
              <p className="text-xs text-fg-muted">No slash commands registered</p>
            </div>
          ) : (
            <Card>
              <CardHeader>
                <Wrench className="w-4 h-4" />
                <span>Slash Commands ({commands.length})</span>
              </CardHeader>
              <div className="px-4 pb-3 space-y-1">
                {commands.map((cmd) => (
                  <div
                    key={cmd.name}
                    className="flex items-start gap-2 px-2 py-2 rounded-md hover:bg-surface/40 transition-colors"
                  >
                    <span className="text-fg-faint mt-0.5 shrink-0">/</span>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-xs font-medium text-fg">{cmd.name}</span>
                        {cmd.category && (
                          <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                            {cmd.category}
                          </span>
                        )}
                      </div>
                      {cmd.description && (
                        <p className="text-[11px] text-fg-muted mt-0.5">{cmd.description}</p>
                      )}
                      {cmd.args && cmd.args.length > 0 && (
                        <div className="flex flex-wrap gap-1 mt-1">
                          {cmd.args.map((arg) => (
                            <span
                              key={arg.name}
                              className={`text-[10px] font-mono px-1 py-0.5 rounded border leading-none ${
                                arg.required
                                  ? "border-primary/30 bg-primary/5 text-primary"
                                  : "border-border-subtle bg-bg-elevated text-fg-faint"
                              }`}
                            >
                              {arg.required ? arg.name : `[${arg.name}]`}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </Card>
          )}
        </TabsContent>

        {/* ── Config Tab ───────────────────────────────────────────── */}
        <TabsContent value="config" className="pt-4">
          <PluginConfigPanel
            pluginId={plugin.name}
            pluginName={plugin.name}
            onBack={onBack}
            embedded
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// ─── Summary Row ────────────────────────────────────────────────────

function SummaryRow({
  icon: Icon,
  label,
  count,
}: {
  icon: typeof Puzzle;
  label: string;
  count: number;
}) {
  return (
    <div className="flex items-center gap-2 py-1">
      <Icon className="w-3.5 h-3.5 text-fg-muted shrink-0" />
      <span className="text-xs text-fg-secondary flex-1">{label}</span>
      <span className="text-xs font-medium text-fg tabular-nums">{count}</span>
    </div>
  );
}

// ─── Skipped Registrations Card ────────────────────────────────────

function SkippedRegistrationsCard({ skipped }: { skipped: SkippedRegistration[] }) {
  const collapsible = skipped.length > 3;
  const [open, setOpen] = useState(!collapsible);

  if (skipped.length === 0) return null;

  return (
    <div className="rounded-xl border border-amber-600/40 bg-amber-600/5 overflow-hidden">
      <button
        type="button"
        onClick={() => collapsible && setOpen((v) => !v)}
        className={`w-full flex items-center gap-2 px-4 py-3 text-sm font-medium text-amber-600 ${
          collapsible ? "cursor-pointer hover:bg-amber-600/10" : "cursor-default"
        }`}
        aria-expanded={open}
      >
        <AlertTriangle className="w-4 h-4 shrink-0" />
        <span className="flex-1 text-left">Skipped registrations ({skipped.length})</span>
        {collapsible &&
          (open ? (
            <ChevronDown className="w-3.5 h-3.5" />
          ) : (
            <ChevronRight className="w-3.5 h-3.5" />
          ))}
      </button>
      {open && (
        <ul className="px-4 pb-3 space-y-1">
          {skipped.map((sr) => (
            <li
              key={`${sr.kind}:${sr.id}:${sr.reason}`}
              className="text-[11px] text-amber-600/90 leading-relaxed"
            >
              <span className="font-mono font-medium">{sr.kind}</span>
              <span className="text-amber-600/70">: </span>
              <span className="font-medium">{sr.id}</span>
              {sr.reason && (
                <>
                  <span className="text-amber-600/60"> — </span>
                  <span className="text-amber-600/80">{sr.reason}</span>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
