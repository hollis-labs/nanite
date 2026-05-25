import {
  ChevronLeft,
  Code2,
  Copy,
  Eye,
  FileText,
  FolderKanban,
  FolderOpen,
  Loader2,
  Lock,
  Plus,
  Settings,
  Trash2,
  User,
  Wrench,
  X,
  Zap,
} from "lucide-react";
import { useCallback, useState } from "react";
import { SourceBadge } from "@/components/agents/SourceBadge";
import { StatusDot } from "@/components/agents/StatusDot";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { DynamicIcon, IconPicker } from "@/components/ui/icon-picker";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { TagInput } from "@/components/ui/tag-input";
import type {
  AgentModeProfile,
  AgentProfile,
  Project,
  PromptTemplate,
  Skill,
} from "@/lib/types";
import { ConstraintsEditor } from "./editors/ConstraintsEditor";
import { EditableStringList } from "./editors/EditableStringList";
import { SystemPromptEditor } from "./editors/SystemPromptEditor";
import { AgentBootPlanPanel } from "./AgentBootPlanPanel";
import { AgentCapabilitiesPanel } from "./AgentCapabilitiesPanel";
import { AgentReflexesPanel } from "./AgentReflexesPanel";

// ─── Props ──────────────────────────────────────────────────────────

export interface AgentDetailViewProps {
  agent: AgentProfile;
  modes: AgentModeProfile[];
  modelOptions: { id: string; label: string }[];
  allKnownTags: string[];
  // Surfaced 409 / conflict messages from the parent's mutations.
  actionError?: string | null;
  // Mutations
  onUpdateAgent: (data: Partial<AgentProfile>) => void;
  onCopyToManaged?: () => void;
  onDeleteAgent?: () => void;
  onCreateMode: (data: Omit<AgentModeProfile, "id" | "agent_id">) => void;
  isCreatingMode?: boolean;
  // Skills
  agentSkills: Skill[];
  availableSkills: Skill[];
  onAssignSkill: (skillId: string) => void;
  onRemoveSkill: (skillId: string) => void;
  // Templates
  agentTemplates: PromptTemplate[];
  availableTemplates: PromptTemplate[];
  onAssignTemplate: (templateId: string) => void;
  onRemoveTemplate: (templateId: string) => void;
  // Projects
  agentProjects: Project[];
  availableProjects: Project[];
  onAddProject: (projectId: string) => void;
  onRemoveProject: (projectId: string) => void;
  // Navigation
  onBack: () => void;
}

// ─── Component ──────────────────────────────────────────────────────

export function AgentDetailView({
  agent,
  modes,
  modelOptions,
  allKnownTags,
  actionError,
  onUpdateAgent,
  onCopyToManaged,
  onDeleteAgent,
  onCreateMode,
  isCreatingMode,
  agentSkills,
  availableSkills,
  onAssignSkill,
  onRemoveSkill,
  agentTemplates,
  availableTemplates,
  onAssignTemplate,
  onRemoveTemplate,
  agentProjects,
  availableProjects,
  onAddProject,
  onRemoveProject,
  onBack,
}: AgentDetailViewProps) {
  const [editField, setEditField] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [showModeForm, setShowModeForm] = useState(false);
  const [showProjectPicker, setShowProjectPicker] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  // Editability is driven entirely by the backend `editable` flag (true only
  // for manage_class === "managed"). Default to editable when the flag is
  // absent so DB-only agents without the contract still work.
  const isReadOnly = agent.editable === false;
  // Plugin/external agents can be forked into an editable managed copy.
  const canCopyToManaged = agent.copy_to_managed === true;

  const updateField = useCallback(
    (field: string, value: string | boolean) => {
      if (isReadOnly) return;
      onUpdateAgent({ [field]: value } as Partial<AgentProfile>);
    },
    [isReadOnly, onUpdateAgent],
  );

  // Parse agent.settings JSON for debug toggle
  const parseSettings = (): Record<string, unknown> => {
    try { return JSON.parse(agent.settings || "{}"); }
    catch { return {}; }
  };

  const agentSettings = parseSettings();
  const debugMode = agentSettings.debug === true;

  const toggleDebugMode = useCallback(() => {
    if (isReadOnly) return;
    const current = (() => { try { return JSON.parse(agent.settings || "{}"); } catch { return {}; } })();
    const updated = { ...current, debug: !current.debug };
    onUpdateAgent({ settings: JSON.stringify(updated) } as Partial<AgentProfile>);
  }, [isReadOnly, agent.settings, onUpdateAgent]);

  const startEditing = useCallback((field: string, value: string) => {
    if (isReadOnly) return;
    setEditField(field);
    setEditValue(value);
  }, [isReadOnly]);

  const saveField = useCallback(
    (field: string) => {
      updateField(field, editValue);
      setEditField(null);
      setEditValue("");
    },
    [editValue, updateField],
  );

  const cancelEditing = useCallback(() => {
    setEditField(null);
    setEditValue("");
  }, []);

  const inputClass =
    "w-full px-2 py-1 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary";

  // ─── Inline editable field ────────────────────────────────────────

  const editableRow = (
    field: string,
    label: string,
    value: string,
    opts?: {
      type?: "input" | "select";
      selectOptions?: { id: string; label: string }[];
    },
  ) => {
    const type = opts?.type || "input";
    if (editField === field) {
      const handleSave = () => saveField(field);
      const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === "Enter") handleSave();
        if (e.key === "Escape") cancelEditing();
      };
      return (
        <div className="flex items-center gap-3 py-1.5">
          <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
          {type === "select" ? (
            <select
              autoFocus
              value={editValue}
              onChange={(e) => {
                setEditValue(e.target.value);
                updateField(field, e.target.value);
                setEditField(null);
              }}
              onBlur={handleSave}
              onKeyDown={handleKeyDown}
              className={`${inputClass} flex-1`}
            >
              {(opts?.selectOptions || []).map((o) => (
                <option key={o.id} value={o.id}>
                  {o.label}
                </option>
              ))}
            </select>
          ) : (
            <input
              autoFocus
              type="text"
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={handleSave}
              onKeyDown={handleKeyDown}
              className={`${inputClass} flex-1`}
            />
          )}
        </div>
      );
    }
    return (
      <div
        className="flex items-center gap-3 py-1.5 group cursor-pointer rounded px-1 -mx-1 hover:bg-surface/40 transition-colors"
        onClick={() => startEditing(field, value)}
      >
        <span className="text-xs text-fg-muted w-28 shrink-0">{label}</span>
        <span className="text-xs text-fg-secondary flex-1 truncate">{value || "—"}</span>
      </div>
    );
  };

  // ─── Parse JSON helpers ───────────────────────────────────────────

  const parseTags = (): string[] => {
    try { return JSON.parse(agent.tags || "[]"); }
    catch { return []; }
  };

  // ─── Render ───────────────────────────────────────────────────────

  return (
    <div className="space-y-5">
      {/* ── Header ───────────────────────────────────────────────── */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={onBack}>
          <ChevronLeft className="w-4 h-4" />
        </Button>
        <div className="flex items-center gap-3 flex-1 min-w-0">
          <div className="size-12 rounded-sm bg-surface flex items-center justify-center shrink-0">
            {agent.icon ? (
              <DynamicIcon name={agent.icon} className="w-6 h-6 text-fg-secondary" fallback={User} />
            ) : agent.avatar ? (
              <span className="text-xl">{agent.avatar}</span>
            ) : (
              <User className="w-6 h-6 text-fg-secondary" />
            )}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="text-xl font-semibold text-fg truncate">{agent.name}</h2>
              <StatusDot status={agent.status || "active"} />
              <SourceBadge source={agent.source} />
            </div>
            <p className="text-xs text-fg-muted font-mono truncate">{agent.slug}</p>
          </div>
        </div>
        {/* Delete is only available for managed/editable agents. */}
        {!isReadOnly && onDeleteAgent && (
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5 text-danger hover:text-danger shrink-0"
            onClick={() => setShowDeleteConfirm(true)}
          >
            <Trash2 className="w-3.5 h-3.5" />
            Delete
          </Button>
        )}
      </div>

      {/* ── Conflict / action error surfaced from the parent's mutations ── */}
      {actionError && (
        <div className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2.5 text-xs text-danger">
          {actionError}
        </div>
      )}

      {/* ── Read-only banner — editability driven by backend `editable` flag ── */}
      {isReadOnly && canCopyToManaged && (
        <div className="flex items-start gap-3 rounded-md border border-border-subtle bg-surface/60 px-3 py-2.5">
          <Lock className="w-4 h-4 mt-0.5 shrink-0 text-fg-secondary" />
          <div className="text-xs text-fg-secondary leading-relaxed flex-1 min-w-0">
            <div className="font-medium text-fg">This agent is read-only</div>
            <div className="mt-1 text-fg-muted">
              Make an editable copy in your managed config to customize it.
            </div>
          </div>
          {onCopyToManaged && (
            <Button
              type="button"
              size="sm"
              variant="secondary"
              className="gap-1.5 shrink-0"
              onClick={onCopyToManaged}
            >
              <Copy className="w-3.5 h-3.5" />
              Make editable
            </Button>
          )}
        </div>
      )}

      {isReadOnly && !canCopyToManaged && (
        <div className="flex items-start gap-3 rounded-md border border-border-subtle bg-surface/60 px-3 py-2.5">
          <Lock className="w-4 h-4 mt-0.5 shrink-0 text-fg-secondary" />
          <div className="text-xs text-fg-muted leading-relaxed flex-1 min-w-0">
            This agent is managed by Nanite and isn't editable here.
          </div>
        </div>
      )}

      {/* ── Tabs ─────────────────────────────────────────────────── */}
      <Tabs defaultValue="overview">
        <TabsList variant="line" className="w-full justify-start border-b border-border">
          <TabsTrigger value="overview" className="gap-1.5 text-xs">
            <Settings className="w-3.5 h-3.5" /> Overview
          </TabsTrigger>
          <TabsTrigger value="prompt" className="gap-1.5 text-xs">
            <FileText className="w-3.5 h-3.5" /> Prompt
          </TabsTrigger>
          <TabsTrigger value="capabilities" className="gap-1.5 text-xs">
            <Wrench className="w-3.5 h-3.5" /> Capabilities
          </TabsTrigger>
          <TabsTrigger value="boot" className="gap-1.5 text-xs">
            <FolderKanban className="w-3.5 h-3.5" /> Boot
          </TabsTrigger>
          <TabsTrigger value="reflexes" className="gap-1.5 text-xs">
            <Zap className="w-3.5 h-3.5" /> Reflexes
          </TabsTrigger>
          <TabsTrigger value="scope" className="gap-1.5 text-xs">
            <FolderOpen className="w-3.5 h-3.5" /> Scope
          </TabsTrigger>
          <TabsTrigger value="modes" className="gap-1.5 text-xs">
            <Code2 className="w-3.5 h-3.5" /> Modes
            {modes.length > 0 && <span className="text-[10px] text-fg-faint">({modes.length})</span>}
          </TabsTrigger>
        </TabsList>

        {/* ── Overview Tab ───────────────────────────────────────── */}
        <TabsContent value="overview" className="pt-4 space-y-4">
          <Card>
            <CardHeader>
              <Settings className="w-4 h-4" />
              <span>Identity</span>
            </CardHeader>
            <div className="px-4 pb-4 space-y-0.5">
              {editableRow("name", "Name", agent.name)}
              {editableRow("slug", "Slug", agent.slug)}
              {editableRow("avatar", "Avatar", agent.avatar)}
              <div className="flex items-center gap-3 py-1.5">
                <span className="text-xs text-fg-muted w-28 shrink-0">Icon</span>
                <IconPicker
                  value={agent.icon || ""}
                  onChange={(iconName) => updateField("icon", iconName)}
                />
              </div>
              {editableRow("description", "Description", agent.description)}
              {editableRow("default_model", "Default Model", agent.default_model, {
                type: "select",
                selectOptions: modelOptions,
              })}
              <div
                className="flex items-center gap-3 py-1.5 group cursor-pointer rounded px-1 -mx-1 hover:bg-surface/40 transition-colors"
                onClick={() => updateField("can_execute", !agent.can_execute)}
              >
                <span className="text-xs text-fg-muted w-28 shrink-0">Can Execute</span>
                <span className={`text-xs font-medium ${agent.can_execute ? "text-success" : "text-fg-secondary"}`}>
                  {agent.can_execute ? "Yes" : "No"}
                </span>
              </div>
              <div className="flex items-center gap-3 py-1.5 rounded px-1 -mx-1">
                <span className="text-xs text-fg-muted w-28 shrink-0">Debug Mode</span>
                <div className="flex items-center gap-2 flex-1">
                  <button
                    type="button"
                    role="switch"
                    aria-checked={debugMode}
                    onClick={toggleDebugMode}
                    className={`relative shrink-0 inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                      debugMode ? 'bg-toggle-on' : 'bg-surface-hover'
                    }`}
                  >
                    <span className={`inline-block h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform ${
                      debugMode ? 'translate-x-[18px]' : 'translate-x-[3px]'
                    }`} />
                  </button>
                  <span className="text-[11px] text-fg-muted">Capture turn snapshots and execution details</span>
                </div>
              </div>
            </div>
          </Card>

          {/* Read-only metadata */}
          <Card>
            <CardHeader>
              <Eye className="w-4 h-4" />
              <span>Metadata</span>
            </CardHeader>
            <div className="px-4 pb-3 space-y-0.5">
              <MetaRow label="Source" value={agent.source || "unknown"} />
              {agent.source_ref && <MetaRow label="Source Ref" value={agent.source_ref} />}
              <MetaRow label="Version" value={`v${agent.version || 0}`} />
              {agent.agent_hash && (
                <div className="flex items-center gap-3 py-1.5">
                  <span className="text-xs text-fg-muted w-28 shrink-0">Hash</span>
                  <span className="flex items-center gap-1 text-xs text-fg-secondary font-mono">
                    {agent.agent_hash.slice(0, 12)}
                    <button
                      onClick={() => navigator.clipboard.writeText(agent.agent_hash)}
                      className="p-0.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface-hover transition-colors"
                      title="Copy full hash"
                    >
                      <Copy className="w-3 h-3" />
                    </button>
                  </span>
                </div>
              )}
            </div>
          </Card>

          {/* Tags */}
          <Card>
            <CardHeader>
              <span className="text-fg-muted">#</span>
              <span>Tags</span>
            </CardHeader>
            <div className="px-4 pb-4">
              <TagInput
                value={parseTags()}
                onChange={(newTags) => updateField("tags", JSON.stringify(newTags))}
                suggestions={allKnownTags}
                placeholder="Add tag..."
              />
            </div>
          </Card>
        </TabsContent>

        {/* ── Prompt Tab ─────────────────────────────────────────── */}
        <TabsContent value="prompt" className="pt-4 space-y-4">
          <Card>
            <div className="p-4">
              <SystemPromptEditor
                value={agent.system_prompt}
                onChange={(v) => updateField("system_prompt", v)}
              />
            </div>
          </Card>

        </TabsContent>

        <TabsContent value="capabilities" className="pt-4">
          <AgentCapabilitiesPanel
            agent={agent}
            isReadOnly={isReadOnly}
            agentSkills={agentSkills}
            availableSkills={availableSkills}
            onAssignSkill={onAssignSkill}
            onRemoveSkill={onRemoveSkill}
            agentTemplates={agentTemplates}
            availableTemplates={availableTemplates}
            onAssignTemplate={onAssignTemplate}
            onRemoveTemplate={onRemoveTemplate}
            onUpdateAgent={onUpdateAgent}
          />
        </TabsContent>

        <TabsContent value="boot" className="pt-4">
          <AgentBootPlanPanel agent={agent} isReadOnly={isReadOnly} />
        </TabsContent>

        <TabsContent value="reflexes" className="pt-4">
          <AgentReflexesPanel agentId={agent.id} isReadOnly={isReadOnly} />
        </TabsContent>

        {/* ── Scope Tab ──────────────────────────────────────────── */}
        <TabsContent value="scope" className="pt-4 space-y-4">
          <Card>
            <div className="p-4">
              <EditableStringList
                value={agent.directories}
                onChange={(v) => updateField("directories", v)}
                icon={FolderOpen}
                label="Directories"
                placeholder="/path/to/directory/"
                emptyText="No directory restrictions"
                pathStyle
                disabled={isReadOnly}
              />
            </div>
          </Card>

          <Card>
            <div className="p-4">
              <ConstraintsEditor
                value={agent.constraints}
                onChange={(v) => updateField("constraints", v)}
              />
            </div>
          </Card>

          {/* Projects */}
          <Card>
            <CardHeader>
              <FolderKanban className="w-4 h-4" />
              <span>Projects ({agentProjects.length})</span>
              <div className="flex-1" />
              <Button
                onClick={() => setShowProjectPicker((v) => !v)}
                size="sm"
                variant="ghost"
                className="h-6 gap-1 text-[11px] text-fg-muted hover:text-fg"
                disabled={isReadOnly || availableProjects.length === 0}
              >
                <Plus className="w-3 h-3" />
                Add
              </Button>
            </CardHeader>
            <div className="px-4 pb-4 space-y-1.5">
              {agentProjects.length === 0 && !showProjectPicker && (
                <p className="text-xs text-fg-muted py-2">No projects assigned</p>
              )}
              {agentProjects.map((project) => (
                <div key={project.id} className="group flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-surface/40 transition-colors">
                  <FolderKanban className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                  <span className="text-xs font-medium text-fg flex-1 truncate">{project.name}</span>
                  {project.description && (
                    <span className="text-[10px] text-fg-faint truncate max-w-[40%]">{project.description}</span>
                  )}
                  <button
                    onClick={() => onRemoveProject(project.id)}
                    disabled={isReadOnly}
                    className="p-0.5 rounded opacity-0 group-hover:opacity-100 text-fg-faint hover:text-primary transition-all disabled:opacity-0 disabled:cursor-not-allowed"
                  >
                    <X className="w-3 h-3" />
                  </button>
                </div>
              ))}

              {showProjectPicker && availableProjects.length > 0 && (
                <PickerList
                  title="Available Projects"
                  items={availableProjects}
                  renderItem={(p) => (
                    <div className="flex items-center gap-2 flex-1 min-w-0">
                      <FolderKanban className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                      <span className="text-xs text-fg truncate">{p.name}</span>
                    </div>
                  )}
                  onSelect={(p) => {
                    onAddProject(p.id);
                    setShowProjectPicker(false);
                  }}
                  onClose={() => setShowProjectPicker(false)}
                />
              )}
            </div>
          </Card>
        </TabsContent>

        {/* ── Modes Tab ──────────────────────────────────────────── */}
        <TabsContent value="modes" className="pt-4 space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-xs text-fg-muted">
              Modes define alternate behaviors for this agent — each with its own prompt addendum and tool overrides.
            </p>
            <Button
              onClick={() => setShowModeForm(true)}
              size="sm"
              variant="ghost"
              className="h-7 gap-1 text-xs text-fg-muted hover:text-fg shrink-0"
              disabled={isReadOnly}
            >
              <Plus className="w-3.5 h-3.5" />
              Add Mode
            </Button>
          </div>

          {modes.length === 0 && !showModeForm && (
            <div className="text-center py-8">
              <Code2 className="w-8 h-8 text-fg-faint mx-auto mb-2" />
              <p className="text-xs text-fg-muted">No modes configured</p>
            </div>
          )}

          <div className="space-y-2">
            {modes.map((mode) => (
              <Card key={mode.id}>
                <div className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <Code2 className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                    <span className="text-sm font-medium text-fg">{mode.name}</span>
                    <span className="text-[10px] font-mono text-fg-faint">{mode.slug}</span>
                  </div>
                  {mode.prompt_addendum && (
                    <p className="text-xs text-fg-secondary mt-1.5 line-clamp-2 pl-5.5">{mode.prompt_addendum}</p>
                  )}
                </div>
              </Card>
            ))}
          </div>

          {/* Add Mode Form */}
          {showModeForm && (
            <Card>
              <div className="p-4 space-y-3">
                <span className="text-xs font-medium text-fg-secondary">New Mode</span>
                <ModeForm
                  onSubmit={(data) => {
                    onCreateMode(data);
                    setShowModeForm(false);
                  }}
                  onCancel={() => setShowModeForm(false)}
                  isPending={isCreatingMode}
                />
              </div>
            </Card>
          )}
        </TabsContent>

      </Tabs>

      {/* ── Delete confirmation (managed/editable agents only) ─────────── */}
      <AlertDialog open={showDeleteConfirm} onOpenChange={setShowDeleteConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Permanently delete {agent.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              This removes the agent file and all its reflexes, tools, and
              skills. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setShowDeleteConfirm(false);
                onDeleteAgent?.();
              }}
            >
              Delete agent
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// ─── Shared Sub-components ──────────────────────────────────────────

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
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

function PickerList<T extends { id: string }>({
  title,
  items,
  renderItem,
  onSelect,
  onClose,
}: {
  title: string;
  items: T[];
  renderItem: (item: T) => React.ReactNode;
  onSelect: (item: T) => void;
  onClose: () => void;
}) {
  return (
    <div className="mt-2 rounded-lg border border-border-subtle bg-bg/40 overflow-hidden">
      <div className="flex items-center justify-between px-3 py-2 border-b border-border-subtle">
        <span className="text-[11px] font-medium text-fg-secondary">{title}</span>
        <Button variant="ghost" size="icon" className="w-5 h-5" onClick={onClose}>
          <X className="w-3 h-3" />
        </Button>
      </div>
      <div className="max-h-48 overflow-y-auto">
        {items.map((item) => (
          <button
            key={item.id}
            onClick={() => onSelect(item)}
            className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-surface/40 transition-colors"
          >
            {renderItem(item)}
            <span className="text-[10px] text-fg-faint shrink-0">Assign</span>
          </button>
        ))}
      </div>
    </div>
  );
}

function ModeForm({
  onSubmit,
  onCancel,
  isPending,
}: {
  onSubmit: (data: Omit<AgentModeProfile, "id" | "agent_id">) => void;
  onCancel: () => void;
  isPending?: boolean;
}) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [promptAddendum, setPromptAddendum] = useState("");

  const handleSubmit = () => {
    if (!name.trim() || !slug.trim()) return;
    onSubmit({
      name: name.trim(),
      slug: slug.trim(),
      prompt_addendum: promptAddendum.trim(),
      tool_overrides: "{}",
      settings: "{}",
    });
  };

  return (
    <div className="space-y-2.5">
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <label className="text-[11px] text-fg-muted">Name</label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Mode name"
            className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg focus:outline-none focus:border-primary"
          />
        </div>
        <div className="space-y-1">
          <label className="text-[11px] text-fg-muted">Slug</label>
          <input
            type="text"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            placeholder="mode-slug"
            className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg font-mono focus:outline-none focus:border-primary"
          />
        </div>
      </div>
      <div className="space-y-1">
        <label className="text-[11px] text-fg-muted">Prompt Addendum</label>
        <textarea
          value={promptAddendum}
          onChange={(e) => setPromptAddendum(e.target.value)}
          rows={3}
          placeholder="Additional instructions for this mode..."
          className="w-full px-2 py-1.5 bg-bg-elevated border border-border-subtle rounded-md text-xs text-fg font-mono focus:outline-none focus:border-primary resize-y"
        />
      </div>
      <div className="flex items-center gap-2 pt-1">
        <Button size="sm" className="h-6 text-[11px]" onClick={handleSubmit} disabled={isPending || !name.trim() || !slug.trim()}>
          {isPending ? <Loader2 className="w-3 h-3 animate-spin" /> : "Add Mode"}
        </Button>
        <Button variant="ghost" size="sm" className="h-6 text-[11px]" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
