import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  BookOpen,
  Check,
  FileText,
  Loader2,
  Plus,
  Shield,
  Wrench,
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  AgentKnowledgeSeed,
  AgentKnowledgeSeedUpsertRequest,
  AgentKnownSkill,
  AgentKnownSkillUpsertRequest,
  AgentKnownTool,
  AgentKnownToolUpsertRequest,
  AgentProcedure,
  AgentProcedureUpsertRequest,
  AgentProfile,
  PromptTemplate,
  Skill,
  ToolLoadItem,
} from "@/lib/types";
import { EditableStringList } from "./editors/EditableStringList";
import { ToolPermissionsEditor } from "./editors/ToolPermissionsEditor";

type CapabilitiesProps = {
  agent: AgentProfile;
  isReadOnly: boolean;
  agentSkills: Skill[];
  availableSkills: Skill[];
  onAssignSkill: (skillId: string) => void;
  onRemoveSkill: (skillId: string) => void;
  agentTemplates: PromptTemplate[];
  availableTemplates: PromptTemplate[];
  onAssignTemplate: (templateId: string) => void;
  onRemoveTemplate: (templateId: string) => void;
  onUpdateAgent: (data: Partial<AgentProfile>) => void;
};

type KnownToolEditorState = {
  tool_name: string;
  pinned: boolean;
  sort_order: string;
  ttl_seconds: string;
  reason: string;
};

type KnownSkillEditorState = {
  skill_name: string;
  pinned: boolean;
  ttl_seconds: string;
  reason: string;
};

type ProcedureEditorState = {
  name: string;
  body: string;
  scope: string;
};

type SeedEditorState = {
  seed_key: string;
  namespace: string;
  body: string;
  tagsText: string;
};

const EMPTY_KNOWN_TOOL: KnownToolEditorState = {
  tool_name: "",
  pinned: true,
  sort_order: "0",
  ttl_seconds: "0",
  reason: "",
};

const EMPTY_KNOWN_SKILL: KnownSkillEditorState = {
  skill_name: "",
  pinned: true,
  ttl_seconds: "0",
  reason: "",
};

const EMPTY_PROCEDURE: ProcedureEditorState = {
  name: "",
  body: "",
  scope: "agent",
};

const EMPTY_SEED: SeedEditorState = {
  seed_key: "",
  namespace: "",
  body: "",
  tagsText: "",
};

export function AgentCapabilitiesPanel({
  agent,
  isReadOnly,
  agentSkills,
  availableSkills,
  onAssignSkill,
  onRemoveSkill,
  agentTemplates,
  availableTemplates,
  onAssignTemplate,
  onRemoveTemplate,
  onUpdateAgent,
}: CapabilitiesProps) {
  const queryClient = useQueryClient();
  const [showSkillPicker, setShowSkillPicker] = useState(false);
  const [showTemplatePicker, setShowTemplatePicker] = useState(false);
  const [knownToolDraft, setKnownToolDraft] = useState<KnownToolEditorState | null>(null);
  const [editingKnownTool, setEditingKnownTool] = useState<string | null>(null);
  const [knownSkillDraft, setKnownSkillDraft] = useState<KnownSkillEditorState | null>(null);
  const [editingKnownSkill, setEditingKnownSkill] = useState<string | null>(null);
  const [procedureDraft, setProcedureDraft] = useState<ProcedureEditorState | null>(null);
  const [editingProcedure, setEditingProcedure] = useState<string | null>(null);
  const [seedDraft, setSeedDraft] = useState<SeedEditorState | null>(null);
  const [editingSeed, setEditingSeed] = useState<string | null>(null);

  const knownToolsQuery = useQuery({
    queryKey: ["agent-known-tools", agent.id],
    queryFn: () => api.listAgentKnownTools(agent.id),
  });
  const knownSkillsQuery = useQuery({
    queryKey: ["agent-known-skills", agent.id],
    queryFn: () => api.listAgentKnownSkills(agent.id),
  });
  const proceduresQuery = useQuery({
    queryKey: ["agent-procedures", agent.id],
    queryFn: () => api.listAgentProcedures(agent.id),
  });
  const knowledgeSeedsQuery = useQuery({
    queryKey: ["agent-knowledge-seeds", agent.id],
    queryFn: () => api.listAgentKnowledgeSeeds(agent.id),
  });
  const allToolsQuery = useQuery({
    queryKey: ["tools-with-load-type"],
    queryFn: api.fetchAllToolsWithLoadType,
  });

  const refreshKnownTools = () =>
    queryClient.invalidateQueries({ queryKey: ["agent-known-tools", agent.id] });
  const refreshKnownSkills = () =>
    queryClient.invalidateQueries({ queryKey: ["agent-known-skills", agent.id] });
  const refreshProcedures = () =>
    queryClient.invalidateQueries({ queryKey: ["agent-procedures", agent.id] });
  const refreshKnowledgeSeeds = () =>
    queryClient.invalidateQueries({ queryKey: ["agent-knowledge-seeds", agent.id] });

  const createKnownToolMutation = useMutation({
    mutationFn: (payload: AgentKnownToolUpsertRequest) => api.createAgentKnownTool(agent.id, payload),
    onSuccess: () => {
      setKnownToolDraft(null);
      setEditingKnownTool(null);
      void refreshKnownTools();
    },
  });
  const updateKnownToolMutation = useMutation({
    mutationFn: ({ toolName, payload }: { toolName: string; payload: AgentKnownToolUpsertRequest }) =>
      api.updateAgentKnownTool(agent.id, toolName, payload),
    onSuccess: () => {
      setKnownToolDraft(null);
      setEditingKnownTool(null);
      void refreshKnownTools();
    },
  });
  const deleteKnownToolMutation = useMutation({
    mutationFn: (toolName: string) => api.deleteAgentKnownTool(agent.id, toolName),
    onSuccess: () => void refreshKnownTools(),
  });

  const createKnownSkillMutation = useMutation({
    mutationFn: (payload: AgentKnownSkillUpsertRequest) =>
      api.createAgentKnownSkill(agent.id, payload),
    onSuccess: () => {
      setKnownSkillDraft(null);
      setEditingKnownSkill(null);
      void refreshKnownSkills();
    },
  });
  const updateKnownSkillMutation = useMutation({
    mutationFn: ({
      skillName,
      payload,
    }: {
      skillName: string;
      payload: AgentKnownSkillUpsertRequest;
    }) => api.updateAgentKnownSkill(agent.id, skillName, payload),
    onSuccess: () => {
      setKnownSkillDraft(null);
      setEditingKnownSkill(null);
      void refreshKnownSkills();
    },
  });
  const deleteKnownSkillMutation = useMutation({
    mutationFn: (skillName: string) => api.deleteAgentKnownSkill(agent.id, skillName),
    onSuccess: () => void refreshKnownSkills(),
  });

  const createProcedureMutation = useMutation({
    mutationFn: (payload: AgentProcedureUpsertRequest) => api.createAgentProcedure(agent.id, payload),
    onSuccess: () => {
      setProcedureDraft(null);
      setEditingProcedure(null);
      void refreshProcedures();
    },
  });
  const updateProcedureMutation = useMutation({
    mutationFn: ({ name, payload }: { name: string; payload: AgentProcedureUpsertRequest }) =>
      api.updateAgentProcedure(agent.id, name, payload),
    onSuccess: () => {
      setProcedureDraft(null);
      setEditingProcedure(null);
      void refreshProcedures();
    },
  });
  const deleteProcedureMutation = useMutation({
    mutationFn: (name: string) => api.deleteAgentProcedure(agent.id, name),
    onSuccess: () => void refreshProcedures(),
  });

  const createSeedMutation = useMutation({
    mutationFn: (payload: AgentKnowledgeSeedUpsertRequest) =>
      api.createAgentKnowledgeSeed(agent.id, payload),
    onSuccess: () => {
      setSeedDraft(null);
      setEditingSeed(null);
      void refreshKnowledgeSeeds();
    },
  });
  const updateSeedMutation = useMutation({
    mutationFn: ({
      seedKey,
      payload,
    }: {
      seedKey: string;
      payload: AgentKnowledgeSeedUpsertRequest;
    }) => api.updateAgentKnowledgeSeed(agent.id, seedKey, payload),
    onSuccess: () => {
      setSeedDraft(null);
      setEditingSeed(null);
      void refreshKnowledgeSeeds();
    },
  });
  const deleteSeedMutation = useMutation({
    mutationFn: (seedKey: string) => api.deleteAgentKnowledgeSeed(agent.id, seedKey),
    onSuccess: () => void refreshKnowledgeSeeds(),
  });
  const markSeedAppliedMutation = useMutation({
    mutationFn: (seedKey: string) => api.markAgentKnowledgeSeedApplied(agent.id, seedKey),
    onSuccess: () => void refreshKnowledgeSeeds(),
  });

  const knownToolNames = useMemo(
    () => (allToolsQuery.data ?? []).map((tool) => tool.name).sort(),
    [allToolsQuery.data],
  );
  const availableLoadCount = useMemo(
    () => (allToolsQuery.data ?? []).filter((tool) => tool.enabled).length,
    [allToolsQuery.data],
  );
  const autoLoadCount = useMemo(
    () => (allToolsQuery.data ?? []).filter((tool) => tool.load_type === "auto" && tool.enabled).length,
    [allToolsQuery.data],
  );
  const toolPreview = useMemo(
    () => (allToolsQuery.data ?? []).filter((tool) => tool.enabled).slice(0, 6),
    [allToolsQuery.data],
  );

  const allowlistCount = parseStringArray(agent.tools).length;
  const permissionSummary = parseToolPermissions(agent.tool_permissions);
  const knownSkillsCatalog = useMemo(
    () => [...agentSkills, ...availableSkills].reduce<Skill[]>((acc, skill) => {
      if (!acc.some((entry) => entry.id === skill.id)) acc.push(skill);
      return acc;
    }, []),
    [agentSkills, availableSkills],
  );

  return (
    <div className="space-y-4">
      <SectionCard
        title={`Skills (${agentSkills.length})`}
        description="Assigned reusable skills for this agent. Global skill catalog management stays under Settings → AI → Skills."
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={() => setShowSkillPicker((value) => !value)}
            disabled={isReadOnly || availableSkills.length === 0}
          >
            <Plus className="w-3.5 h-3.5" />
            Assign
          </Button>
        }
      >
        {agentSkills.length === 0 && !showSkillPicker ? (
          <PanelMessage>No skills assigned yet.</PanelMessage>
        ) : null}
        <div className="space-y-1.5">
          {agentSkills.map((skill) => (
            <InlineRow
              key={skill.id}
              label={skill.name}
              badges={[
                skill.category || "uncategorized",
                skill.source || (skill.is_builtin ? "builtin" : "user"),
              ]}
              detail={skill.description}
              actionLabel={`Remove skill ${skill.name}`}
              disabled={isReadOnly}
              onRemove={() => onRemoveSkill(skill.id)}
            />
          ))}
        </div>
        {showSkillPicker ? (
          <PickerList
            title="Available Skills"
            items={availableSkills}
            getKey={(skill) => skill.id}
            emptyText="No unassigned skills available."
            onClose={() => setShowSkillPicker(false)}
            renderItem={(skill) => (
              <>
                <div className="min-w-0 flex-1">
                  <div className="text-xs font-medium text-fg">{skill.name}</div>
                  <div className="text-[11px] text-fg-muted truncate">
                    {[skill.category, skill.source || (skill.is_builtin ? "builtin" : "user")]
                      .filter(Boolean)
                      .join(" · ")}
                  </div>
                </div>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-6 text-[11px]"
                  onClick={() => {
                    onAssignSkill(skill.id);
                    setShowSkillPicker(false);
                  }}
                  disabled={isReadOnly}
                >
                  Assign
                </Button>
              </>
            )}
          />
        ) : null}
      </SectionCard>

      <SectionCard
        title={`Prompt Templates (${agentTemplates.length})`}
        description="Template assignment only. This does not edit the agent’s system prompt body."
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={() => setShowTemplatePicker((value) => !value)}
            disabled={isReadOnly || availableTemplates.length === 0}
          >
            <Plus className="w-3.5 h-3.5" />
            Assign
          </Button>
        }
      >
        {agentTemplates.length === 0 && !showTemplatePicker ? (
          <PanelMessage>No prompt templates assigned yet.</PanelMessage>
        ) : null}
        <div className="space-y-1.5">
          {[...agentTemplates]
            .sort((a, b) => b.priority - a.priority)
            .map((template) => (
              <InlineRow
                key={template.id}
                label={template.name}
                badges={[template.scope, `P${template.priority}`]}
                detail={template.slug}
                actionLabel={`Remove template ${template.name}`}
                disabled={isReadOnly}
                onRemove={() => onRemoveTemplate(template.id)}
              />
            ))}
        </div>
        {showTemplatePicker ? (
          <PickerList
            title="Available Prompt Templates"
            items={[...availableTemplates].sort((a, b) => b.priority - a.priority)}
            getKey={(template) => template.id}
            emptyText="No unassigned templates available."
            onClose={() => setShowTemplatePicker(false)}
            renderItem={(template) => (
              <>
                <div className="min-w-0 flex-1">
                  <div className="text-xs font-medium text-fg">{template.name}</div>
                  <div className="text-[11px] text-fg-muted truncate">
                    {template.scope} · priority {template.priority}
                  </div>
                </div>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-6 text-[11px]"
                  onClick={() => {
                    onAssignTemplate(template.id);
                    setShowTemplatePicker(false);
                  }}
                  disabled={isReadOnly}
                >
                  Assign
                </Button>
              </>
            )}
          />
        ) : null}
      </SectionCard>

      <SectionCard
        title="Tools And Permissions"
        description="Allowlist controls broad tool availability. Permissions define explicit allow and deny patterns. Known tools below bias context toward specific tools."
      >
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1.1fr)_minmax(0,0.9fr)]">
          <div className="space-y-4">
            <div className="rounded-lg border border-border-subtle bg-surface/30 p-3">
              <EditableStringList
                value={agent.tools}
                onChange={(value) => onUpdateAgent({ tools: value })}
                icon={Wrench}
                label="Tools Allowlist"
                placeholder="mcp__server__tool_name or glob pattern"
                emptyText="No allowlist — all discovered tools are eligible"
                pathStyle={false}
                disabled={isReadOnly}
              />
            </div>
            <div className="rounded-lg border border-border-subtle bg-surface/30 p-3">
              <ToolPermissionsEditor
                value={agent.tool_permissions}
                onChange={(value) => onUpdateAgent({ tool_permissions: value })}
                disabled={isReadOnly}
              />
            </div>
          </div>

          <div className="rounded-lg border border-border-subtle bg-surface/30 p-3 space-y-3">
            <div className="grid grid-cols-3 gap-2">
              <MetricCard label="Allowlist" value={String(allowlistCount)} />
              <MetricCard
                label="Discovered"
                value={allToolsQuery.isLoading ? "…" : String(availableLoadCount)}
              />
              <MetricCard
                label="Auto Load"
                value={allToolsQuery.isLoading ? "…" : String(autoLoadCount)}
              />
            </div>
            <div className="space-y-1">
              <div className="text-[11px] font-medium text-fg-secondary">Permission summary</div>
              <div className="text-xs text-fg-muted">
                Allow {permissionSummary.allow} · Deny {permissionSummary.deny}
              </div>
            </div>
            <div className="space-y-1">
              <div className="text-[11px] font-medium text-fg-secondary">Available tools</div>
              {allToolsQuery.isLoading ? <PanelMessage>Loading tool catalog...</PanelMessage> : null}
              {allToolsQuery.error ? (
                <PanelError message={`Failed to load tools: ${errorMessage(allToolsQuery.error)}`} />
              ) : null}
              {!allToolsQuery.isLoading && !allToolsQuery.error && toolPreview.length === 0 ? (
                <PanelMessage>No discovered tools available.</PanelMessage>
              ) : null}
              {!allToolsQuery.isLoading && !allToolsQuery.error ? (
                <div className="flex flex-wrap gap-1">
                  {toolPreview.map((tool) => (
                    <ToolChip key={tool.name} tool={tool} />
                  ))}
                </div>
              ) : null}
            </div>
          </div>
        </div>
      </SectionCard>

      <SectionCard
        title={`Known Tools (${knownToolsQuery.data?.length ?? 0})`}
        description="Pinned or learned tools that should stay context-known for this agent."
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={() => {
              setEditingKnownTool(null);
              setKnownToolDraft({ ...EMPTY_KNOWN_TOOL });
            }}
            disabled={isReadOnly}
          >
            <Plus className="w-3.5 h-3.5" />
            Add Known Tool
          </Button>
        }
      >
        <MutationMessage
          errors={[
            createKnownToolMutation.error,
            updateKnownToolMutation.error,
            deleteKnownToolMutation.error,
          ]}
        />
        <QueryStateBlock
          query={knownToolsQuery}
          empty="No known tools recorded for this agent."
          render={(rows) => (
            <div className="space-y-2">
              {rows
                .slice()
                .sort((a, b) => a.sort_order - b.sort_order || a.tool_name.localeCompare(b.tool_name))
                .map((row) =>
                  editingKnownTool === row.tool_name ? (
                    <KnownToolEditor
                      key={row.tool_name}
                      title={`Edit ${row.tool_name}`}
                      draft={knownToolDraft ?? toKnownToolDraft(row)}
                      toolNames={knownToolNames}
                      disabled={isReadOnly}
                      pending={updateKnownToolMutation.isPending}
                      onChange={setKnownToolDraft}
                      onCancel={() => {
                        setEditingKnownTool(null);
                        setKnownToolDraft(null);
                      }}
                      onSubmit={() => {
                        if (!knownToolDraft) return;
                        updateKnownToolMutation.mutate({
                          toolName: row.tool_name,
                          payload: buildKnownToolPayload(knownToolDraft),
                        });
                      }}
                    />
                  ) : (
                    <KnownToolRow
                      key={row.tool_name}
                      row={row}
                      disabled={isReadOnly}
                      onEdit={() => {
                        setEditingKnownTool(row.tool_name);
                        setKnownToolDraft(toKnownToolDraft(row));
                      }}
                      onDelete={() => deleteKnownToolMutation.mutate(row.tool_name)}
                    />
                  ),
                )}
              {knownToolDraft && editingKnownTool === null ? (
                <KnownToolEditor
                  title="Add Known Tool"
                  draft={knownToolDraft}
                  toolNames={knownToolNames}
                  disabled={isReadOnly}
                  pending={createKnownToolMutation.isPending}
                  onChange={setKnownToolDraft}
                  onCancel={() => setKnownToolDraft(null)}
                  onSubmit={() => createKnownToolMutation.mutate(buildKnownToolPayload(knownToolDraft))}
                />
              ) : null}
            </div>
          )}
        />
      </SectionCard>

      <SectionCard
        title={`Known Skills (${knownSkillsQuery.data?.length ?? 0})`}
        description="Pinned or learned skills that should stay top-of-mind during routing and prompt assembly."
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={() => {
              setEditingKnownSkill(null);
              setKnownSkillDraft({ ...EMPTY_KNOWN_SKILL });
            }}
            disabled={isReadOnly}
          >
            <Plus className="w-3.5 h-3.5" />
            Add Known Skill
          </Button>
        }
      >
        <MutationMessage
          errors={[
            createKnownSkillMutation.error,
            updateKnownSkillMutation.error,
            deleteKnownSkillMutation.error,
          ]}
        />
        <QueryStateBlock
          query={knownSkillsQuery}
          empty="No known skills recorded for this agent."
          render={(rows) => (
            <div className="space-y-2">
              {rows
                .slice()
                .sort((a, b) => a.skill_name.localeCompare(b.skill_name))
                .map((row) =>
                  editingKnownSkill === row.skill_name ? (
                    <KnownSkillEditor
                      key={row.skill_name}
                      title={`Edit ${row.skill_name}`}
                      draft={knownSkillDraft ?? toKnownSkillDraft(row)}
                      skills={knownSkillsCatalog}
                      disabled={isReadOnly}
                      pending={updateKnownSkillMutation.isPending}
                      onChange={setKnownSkillDraft}
                      onCancel={() => {
                        setEditingKnownSkill(null);
                        setKnownSkillDraft(null);
                      }}
                      onSubmit={() => {
                        if (!knownSkillDraft) return;
                        updateKnownSkillMutation.mutate({
                          skillName: row.skill_name,
                          payload: buildKnownSkillPayload(knownSkillDraft),
                        });
                      }}
                    />
                  ) : (
                    <KnownSkillRow
                      key={row.skill_name}
                      row={row}
                      skill={knownSkillsCatalog.find((skill) => skill.name === row.skill_name)}
                      disabled={isReadOnly}
                      onEdit={() => {
                        setEditingKnownSkill(row.skill_name);
                        setKnownSkillDraft(toKnownSkillDraft(row));
                      }}
                      onDelete={() => deleteKnownSkillMutation.mutate(row.skill_name)}
                    />
                  ),
                )}
              {knownSkillDraft && editingKnownSkill === null ? (
                <KnownSkillEditor
                  title="Add Known Skill"
                  draft={knownSkillDraft}
                  skills={knownSkillsCatalog}
                  disabled={isReadOnly}
                  pending={createKnownSkillMutation.isPending}
                  onChange={setKnownSkillDraft}
                  onCancel={() => setKnownSkillDraft(null)}
                  onSubmit={() =>
                    createKnownSkillMutation.mutate(buildKnownSkillPayload(knownSkillDraft))
                  }
                />
              ) : null}
            </div>
          )}
        />
      </SectionCard>

      <SectionCard
        title={`Procedures (${proceduresQuery.data?.length ?? 0})`}
        description="Named routines stored against this agent. Keep bodies short and operational."
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={() => {
              setEditingProcedure(null);
              setProcedureDraft({ ...EMPTY_PROCEDURE });
            }}
            disabled={isReadOnly}
          >
            <Plus className="w-3.5 h-3.5" />
            New Procedure
          </Button>
        }
      >
        <MutationMessage
          errors={[
            createProcedureMutation.error,
            updateProcedureMutation.error,
            deleteProcedureMutation.error,
          ]}
        />
        <QueryStateBlock
          query={proceduresQuery}
          empty="No procedures configured for this agent."
          render={(rows) => (
            <div className="space-y-2">
              {rows
                .slice()
                .sort((a, b) => a.name.localeCompare(b.name))
                .map((row) =>
                  editingProcedure === row.name ? (
                    <ProcedureEditor
                      key={row.name}
                      title={`Edit ${row.name}`}
                      draft={procedureDraft ?? toProcedureDraft(row)}
                      disabled={isReadOnly}
                      pending={updateProcedureMutation.isPending}
                      onChange={setProcedureDraft}
                      onCancel={() => {
                        setEditingProcedure(null);
                        setProcedureDraft(null);
                      }}
                      onSubmit={() => {
                        if (!procedureDraft) return;
                        updateProcedureMutation.mutate({
                          name: row.name,
                          payload: buildProcedurePayload(procedureDraft),
                        });
                      }}
                    />
                  ) : (
                    <ProcedureRow
                      key={row.name}
                      row={row}
                      disabled={isReadOnly}
                      onEdit={() => {
                        setEditingProcedure(row.name);
                        setProcedureDraft(toProcedureDraft(row));
                      }}
                      onDelete={() => deleteProcedureMutation.mutate(row.name)}
                    />
                  ),
                )}
              {procedureDraft && editingProcedure === null ? (
                <ProcedureEditor
                  title="Add Procedure"
                  draft={procedureDraft}
                  disabled={isReadOnly}
                  pending={createProcedureMutation.isPending}
                  onChange={setProcedureDraft}
                  onCancel={() => setProcedureDraft(null)}
                  onSubmit={() => createProcedureMutation.mutate(buildProcedurePayload(procedureDraft))}
                />
              ) : null}
            </div>
          )}
        />
      </SectionCard>

      <SectionCard
        title={`Knowledge Seeds (${knowledgeSeedsQuery.data?.length ?? 0})`}
        description="Preloaded memory seed entries. Tags are sent as a structured list even when edited as plain text here."
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1 text-xs"
            onClick={() => {
              setEditingSeed(null);
              setSeedDraft({ ...EMPTY_SEED });
            }}
            disabled={isReadOnly}
          >
            <Plus className="w-3.5 h-3.5" />
            New Seed
          </Button>
        }
      >
        <MutationMessage
          errors={[
            createSeedMutation.error,
            updateSeedMutation.error,
            deleteSeedMutation.error,
            markSeedAppliedMutation.error,
          ]}
        />
        <QueryStateBlock
          query={knowledgeSeedsQuery}
          empty="No knowledge seeds configured for this agent."
          render={(rows) => (
            <div className="space-y-2">
              {rows
                .slice()
                .sort((a, b) => a.seed_key.localeCompare(b.seed_key))
                .map((row) =>
                  editingSeed === row.seed_key ? (
                    <KnowledgeSeedEditor
                      key={row.seed_key}
                      title={`Edit ${row.seed_key}`}
                      draft={seedDraft ?? toSeedDraft(row)}
                      disabled={isReadOnly}
                      pending={updateSeedMutation.isPending}
                      onChange={setSeedDraft}
                      onCancel={() => {
                        setEditingSeed(null);
                        setSeedDraft(null);
                      }}
                      onSubmit={() => {
                        if (!seedDraft) return;
                        updateSeedMutation.mutate({
                          seedKey: row.seed_key,
                          payload: buildSeedPayload(seedDraft),
                        });
                      }}
                    />
                  ) : (
                    <KnowledgeSeedRow
                      key={row.seed_key}
                      row={row}
                      disabled={isReadOnly}
                      pendingMark={markSeedAppliedMutation.isPending}
                      onEdit={() => {
                        setEditingSeed(row.seed_key);
                        setSeedDraft(toSeedDraft(row));
                      }}
                      onDelete={() => deleteSeedMutation.mutate(row.seed_key)}
                      onMarkApplied={() => markSeedAppliedMutation.mutate(row.seed_key)}
                    />
                  ),
                )}
              {seedDraft && editingSeed === null ? (
                <KnowledgeSeedEditor
                  title="Add Knowledge Seed"
                  draft={seedDraft}
                  disabled={isReadOnly}
                  pending={createSeedMutation.isPending}
                  onChange={setSeedDraft}
                  onCancel={() => setSeedDraft(null)}
                  onSubmit={() => createSeedMutation.mutate(buildSeedPayload(seedDraft))}
                />
              ) : null}
            </div>
          )}
        />
      </SectionCard>
    </div>
  );
}

function SectionCard({
  title,
  description,
  action,
  children,
}: {
  title: string;
  description?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden">
      <div className="flex items-start gap-3 px-4 py-3 border-b border-border-subtle">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-fg">{title}</div>
          {description ? <div className="mt-1 text-xs text-fg-muted">{description}</div> : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
      <div className="p-4 space-y-3">{children}</div>
    </div>
  );
}

function MetricCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-border-subtle bg-bg/40 px-3 py-2">
      <div className="text-[10px] uppercase tracking-wide text-fg-faint">{label}</div>
      <div className="mt-1 text-sm font-medium text-fg">{value}</div>
    </div>
  );
}

function InlineRow({
  label,
  badges,
  detail,
  disabled,
  actionLabel,
  onRemove,
}: {
  label: string;
  badges: string[];
  detail?: string;
  disabled: boolean;
  actionLabel: string;
  onRemove: () => void;
}) {
  return (
    <div className="group flex items-start gap-3 rounded-md border border-border-subtle bg-surface/20 px-3 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs font-medium text-fg">{label}</span>
          {badges.filter(Boolean).map((badge) => (
            <span
              key={badge}
              className="rounded bg-surface px-1.5 py-0.5 text-[10px] text-fg-muted"
            >
              {badge}
            </span>
          ))}
        </div>
        {detail ? <div className="mt-1 text-[11px] text-fg-muted">{detail}</div> : null}
      </div>
      <Button
        size="icon"
        variant="ghost"
        className="h-6 w-6 opacity-0 transition-opacity group-hover:opacity-100 disabled:opacity-40"
        aria-label={actionLabel}
        onClick={onRemove}
        disabled={disabled}
      >
        <X className="w-3.5 h-3.5" />
      </Button>
    </div>
  );
}

function PickerList<T>({
  title,
  items,
  emptyText,
  getKey,
  onClose,
  renderItem,
}: {
  title: string;
  items: T[];
  emptyText: string;
  getKey: (item: T) => string;
  onClose: () => void;
  renderItem: (item: T) => React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-border-subtle bg-surface/20 overflow-hidden">
      <div className="flex items-center justify-between border-b border-border-subtle px-3 py-2">
        <span className="text-[11px] font-medium text-fg-secondary">{title}</span>
        <Button size="icon" variant="ghost" className="h-5 w-5" onClick={onClose}>
          <X className="w-3 h-3" />
        </Button>
      </div>
      {items.length === 0 ? (
        <div className="px-3 py-3 text-xs text-fg-muted">{emptyText}</div>
      ) : (
        <div className="max-h-56 overflow-y-auto">
          {items.map((item) => (
            <div
              key={getKey(item)}
              className="flex items-center gap-3 border-b border-border-subtle/60 px-3 py-2 last:border-b-0"
            >
              {renderItem(item)}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function QueryStateBlock<T>({
  query,
  empty,
  render,
}: {
  query: {
    isLoading: boolean;
    error: unknown;
    data: T[] | undefined;
  };
  empty: string;
  render: (rows: T[]) => React.ReactNode;
}) {
  if (query.isLoading) return <PanelMessage>Loading...</PanelMessage>;
  if (query.error) return <PanelError message={errorMessage(query.error)} />;
  if (!query.data || query.data.length === 0) return <PanelMessage>{empty}</PanelMessage>;
  return <>{render(query.data)}</>;
}

function PanelMessage({ children }: { children: React.ReactNode }) {
  return <div className="text-xs text-fg-muted">{children}</div>;
}

function PanelError({ message }: { message: string }) {
  return <div className="text-xs text-primary">{message}</div>;
}

function MutationMessage({ errors }: { errors: Array<unknown> }) {
  const first = errors.find(Boolean);
  if (!first) return null;
  return <PanelError message={errorMessage(first)} />;
}

function ToolChip({ tool }: { tool: ToolLoadItem }) {
  return (
    <span className="inline-flex items-center gap-1 rounded bg-surface px-2 py-1 text-[10px] text-fg-secondary">
      <Wrench className="w-3 h-3" />
      <span className="font-mono">{tool.name}</span>
      <span className="text-fg-faint">· {tool.load_type}</span>
    </span>
  );
}

function KnownToolRow({
  row,
  disabled,
  onEdit,
  onDelete,
}: {
  row: AgentKnownTool;
  disabled: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <DataRow
      icon={<Wrench className="w-4 h-4 text-fg-muted" />}
      title={row.tool_name}
      subtitle={describeKnownTool(row)}
      meta={timestampLabel("Used", row.last_used_at) ?? timestampLabel("Added", row.added_at)}
      disabled={disabled}
      onEdit={onEdit}
      onDelete={onDelete}
    />
  );
}

function KnownSkillRow({
  row,
  skill,
  disabled,
  onEdit,
  onDelete,
}: {
  row: AgentKnownSkill;
  skill?: Skill;
  disabled: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <DataRow
      icon={<BookOpen className="w-4 h-4 text-fg-muted" />}
      title={row.skill_name}
      subtitle={[skill?.category, row.pinned ? "pinned" : "", ttlLabel(row.ttl_seconds), reasonLabel(row.reason)]
        .filter(Boolean)
        .join(" · ")}
      meta={timestampLabel("Used", row.last_used_at) ?? timestampLabel("Added", row.added_at)}
      disabled={disabled}
      onEdit={onEdit}
      onDelete={onDelete}
    />
  );
}

function ProcedureRow({
  row,
  disabled,
  onEdit,
  onDelete,
}: {
  row: AgentProcedure;
  disabled: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <DataRow
      icon={<FileText className="w-4 h-4 text-fg-muted" />}
      title={row.name}
      subtitle={`${row.scope || "agent"} · ${truncate(row.body, 140)}`}
      meta={timestampLabel("Updated", row.updated_at) ?? timestampLabel("Created", row.created_at)}
      disabled={disabled}
      onEdit={onEdit}
      onDelete={onDelete}
    />
  );
}

function KnowledgeSeedRow({
  row,
  disabled,
  pendingMark,
  onEdit,
  onDelete,
  onMarkApplied,
}: {
  row: AgentKnowledgeSeed;
  disabled: boolean;
  pendingMark: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onMarkApplied: () => void;
}) {
  const tags = parseStringArray(row.tags_json);
  return (
    <div className="rounded-md border border-border-subtle bg-surface/20 px-3 py-2">
      <div className="flex items-start gap-3">
        <Shield className="mt-0.5 w-4 h-4 text-fg-muted shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-xs font-medium text-fg">{row.seed_key}</span>
            <span className="rounded bg-surface px-1.5 py-0.5 text-[10px] text-fg-muted">
              {row.namespace || "default"}
            </span>
            {row.applied_at ? (
              <span className="rounded bg-surface px-1.5 py-0.5 text-[10px] text-success">
                applied
              </span>
            ) : null}
          </div>
          <div className="mt-1 text-[11px] text-fg-muted">{truncate(row.body, 180)}</div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5 text-[10px] text-fg-faint">
            {tags.map((tag) => (
              <span key={tag} className="rounded bg-surface px-1.5 py-0.5">
                {tag}
              </span>
            ))}
            {timestampLabel("Created", row.created_at) ? (
              <span>{timestampLabel("Created", row.created_at)}</span>
            ) : null}
          </div>
        </div>
        <div className="flex items-center gap-1">
          <Button
            size="sm"
            variant="ghost"
            className="h-7 text-[11px]"
            aria-label={`Mark seed ${row.seed_key} applied`}
            onClick={onMarkApplied}
            disabled={disabled || !!row.applied_at || pendingMark}
          >
            {pendingMark ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Check className="w-3.5 h-3.5" />}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 text-[11px]"
            aria-label={`Edit seed ${row.seed_key}`}
            onClick={onEdit}
            disabled={disabled}
          >
            Edit
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 text-[11px]"
            aria-label={`Delete seed ${row.seed_key}`}
            onClick={onDelete}
            disabled={disabled}
          >
            Delete
          </Button>
        </div>
      </div>
    </div>
  );
}

function DataRow({
  icon,
  title,
  subtitle,
  meta,
  disabled,
  onEdit,
  onDelete,
}: {
  icon: React.ReactNode;
  title: string;
  subtitle: string;
  meta?: string | null;
  disabled: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div className="rounded-md border border-border-subtle bg-surface/20 px-3 py-2">
      <div className="flex items-start gap-3">
        <div className="shrink-0">{icon}</div>
        <div className="min-w-0 flex-1">
          <div className="text-xs font-medium text-fg">{title}</div>
          <div className="mt-1 text-[11px] text-fg-muted">{subtitle}</div>
          {meta ? <div className="mt-1 text-[10px] text-fg-faint">{meta}</div> : null}
        </div>
        <div className="flex items-center gap-1">
          <Button
            size="sm"
            variant="ghost"
            className="h-7 text-[11px]"
            aria-label={`Edit ${title}`}
            onClick={onEdit}
            disabled={disabled}
          >
            Edit
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 text-[11px]"
            aria-label={`Delete ${title}`}
            onClick={onDelete}
            disabled={disabled}
          >
            Delete
          </Button>
        </div>
      </div>
    </div>
  );
}

function KnownToolEditor({
  title,
  draft,
  toolNames,
  disabled,
  pending,
  onChange,
  onCancel,
  onSubmit,
}: {
  title: string;
  draft: KnownToolEditorState;
  toolNames: string[];
  disabled: boolean;
  pending: boolean;
  onChange: (draft: KnownToolEditorState) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const valid = draft.tool_name.trim().length > 0;
  return (
    <EditorShell title={title} disabled={disabled} pending={pending} valid={valid} onCancel={onCancel} onSubmit={onSubmit}>
      <Input
        aria-label="Known tool name"
        list="known-tool-name-options"
        value={draft.tool_name}
        onChange={(event) => onChange({ ...draft, tool_name: event.target.value })}
        disabled={disabled}
        placeholder="mcp__server__tool_name"
      />
      <datalist id="known-tool-name-options">
        {toolNames.map((name) => (
          <option key={name} value={name} />
        ))}
      </datalist>
      <div className="grid gap-3 sm:grid-cols-3">
        <LabeledField label="Pinned">
          <select
            aria-label="Known tool pinned"
            value={draft.pinned ? "true" : "false"}
            onChange={(event) => onChange({ ...draft, pinned: event.target.value === "true" })}
            disabled={disabled}
            className="w-full rounded-md border border-border-subtle bg-bg px-2 py-2 text-xs text-fg"
          >
            <option value="true">Pinned</option>
            <option value="false">Not pinned</option>
          </select>
        </LabeledField>
        <LabeledField label="Sort order">
          <Input
            aria-label="Known tool sort order"
            value={draft.sort_order}
            onChange={(event) => onChange({ ...draft, sort_order: event.target.value })}
            disabled={disabled}
            inputMode="numeric"
          />
        </LabeledField>
        <LabeledField label="TTL seconds">
          <Input
            aria-label="Known tool ttl seconds"
            value={draft.ttl_seconds}
            onChange={(event) => onChange({ ...draft, ttl_seconds: event.target.value })}
            disabled={disabled}
            inputMode="numeric"
          />
        </LabeledField>
      </div>
      <LabeledField label="Reason">
        <Input
          aria-label="Known tool reason"
          value={draft.reason}
          onChange={(event) => onChange({ ...draft, reason: event.target.value })}
          disabled={disabled}
          placeholder="Why keep this tool context-known?"
        />
      </LabeledField>
    </EditorShell>
  );
}

function KnownSkillEditor({
  title,
  draft,
  skills,
  disabled,
  pending,
  onChange,
  onCancel,
  onSubmit,
}: {
  title: string;
  draft: KnownSkillEditorState;
  skills: Skill[];
  disabled: boolean;
  pending: boolean;
  onChange: (draft: KnownSkillEditorState) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const valid = draft.skill_name.trim().length > 0;
  return (
    <EditorShell title={title} disabled={disabled} pending={pending} valid={valid} onCancel={onCancel} onSubmit={onSubmit}>
      <Input
        aria-label="Known skill name"
        list="known-skill-name-options"
        value={draft.skill_name}
        onChange={(event) => onChange({ ...draft, skill_name: event.target.value })}
        disabled={disabled}
        placeholder="Skill name"
      />
      <datalist id="known-skill-name-options">
        {skills.map((skill) => (
          <option key={skill.id} value={skill.name} />
        ))}
      </datalist>
      <div className="grid gap-3 sm:grid-cols-2">
        <LabeledField label="Pinned">
          <select
            aria-label="Known skill pinned"
            value={draft.pinned ? "true" : "false"}
            onChange={(event) => onChange({ ...draft, pinned: event.target.value === "true" })}
            disabled={disabled}
            className="w-full rounded-md border border-border-subtle bg-bg px-2 py-2 text-xs text-fg"
          >
            <option value="true">Pinned</option>
            <option value="false">Not pinned</option>
          </select>
        </LabeledField>
        <LabeledField label="TTL seconds">
          <Input
            aria-label="Known skill ttl seconds"
            value={draft.ttl_seconds}
            onChange={(event) => onChange({ ...draft, ttl_seconds: event.target.value })}
            disabled={disabled}
            inputMode="numeric"
          />
        </LabeledField>
      </div>
      <LabeledField label="Reason">
        <Input
          aria-label="Known skill reason"
          value={draft.reason}
          onChange={(event) => onChange({ ...draft, reason: event.target.value })}
          disabled={disabled}
          placeholder="Why keep this skill context-known?"
        />
      </LabeledField>
    </EditorShell>
  );
}

function ProcedureEditor({
  title,
  draft,
  disabled,
  pending,
  onChange,
  onCancel,
  onSubmit,
}: {
  title: string;
  draft: ProcedureEditorState;
  disabled: boolean;
  pending: boolean;
  onChange: (draft: ProcedureEditorState) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const valid = draft.name.trim().length > 0 && draft.body.trim().length > 0;
  return (
    <EditorShell title={title} disabled={disabled} pending={pending} valid={valid} onCancel={onCancel} onSubmit={onSubmit}>
      <div className="grid gap-3 sm:grid-cols-2">
        <LabeledField label="Name">
          <Input
            aria-label="Procedure name"
            value={draft.name}
            onChange={(event) => onChange({ ...draft, name: event.target.value })}
            disabled={disabled}
          />
        </LabeledField>
        <LabeledField label="Scope">
          <select
            aria-label="Procedure scope"
            value={draft.scope}
            onChange={(event) => onChange({ ...draft, scope: event.target.value })}
            disabled={disabled}
            className="w-full rounded-md border border-border-subtle bg-bg px-2 py-2 text-xs text-fg"
          >
            <option value="agent">agent</option>
            <option value="session">session</option>
            <option value="mode">mode</option>
          </select>
        </LabeledField>
      </div>
      <LabeledField label="Body">
        <Textarea
          aria-label="Procedure body"
          value={draft.body}
          onChange={(event) => onChange({ ...draft, body: event.target.value })}
          disabled={disabled}
          rows={6}
        />
      </LabeledField>
    </EditorShell>
  );
}

function KnowledgeSeedEditor({
  title,
  draft,
  disabled,
  pending,
  onChange,
  onCancel,
  onSubmit,
}: {
  title: string;
  draft: SeedEditorState;
  disabled: boolean;
  pending: boolean;
  onChange: (draft: SeedEditorState) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const valid =
    draft.seed_key.trim().length > 0 &&
    draft.namespace.trim().length > 0 &&
    draft.body.trim().length > 0;
  return (
    <EditorShell title={title} disabled={disabled} pending={pending} valid={valid} onCancel={onCancel} onSubmit={onSubmit}>
      <div className="grid gap-3 sm:grid-cols-2">
        <LabeledField label="Seed key">
          <Input
            aria-label="Knowledge seed key"
            value={draft.seed_key}
            onChange={(event) => onChange({ ...draft, seed_key: event.target.value })}
            disabled={disabled}
          />
        </LabeledField>
        <LabeledField label="Namespace">
          <Input
            aria-label="Knowledge seed namespace"
            value={draft.namespace}
            onChange={(event) => onChange({ ...draft, namespace: event.target.value })}
            disabled={disabled}
          />
        </LabeledField>
      </div>
      <LabeledField label="Tags">
        <Input
          aria-label="Knowledge seed tags"
          value={draft.tagsText}
          onChange={(event) => onChange({ ...draft, tagsText: event.target.value })}
          disabled={disabled}
          placeholder="comma or newline separated"
        />
      </LabeledField>
      <LabeledField label="Body">
        <Textarea
          aria-label="Knowledge seed body"
          value={draft.body}
          onChange={(event) => onChange({ ...draft, body: event.target.value })}
          disabled={disabled}
          rows={6}
        />
      </LabeledField>
    </EditorShell>
  );
}

function EditorShell({
  title,
  disabled,
  pending,
  valid,
  onCancel,
  onSubmit,
  children,
}: {
  title: string;
  disabled: boolean;
  pending: boolean;
  valid: boolean;
  onCancel: () => void;
  onSubmit: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-border-subtle bg-bg/40 p-3 space-y-3">
      <div className="text-xs font-medium text-fg">{title}</div>
      {children}
      <div className="flex items-center gap-2">
        <Button size="sm" className="h-7 text-[11px]" onClick={onSubmit} disabled={disabled || pending || !valid}>
          {pending ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : "Save"}
        </Button>
        <Button size="sm" variant="ghost" className="h-7 text-[11px]" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

function LabeledField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <span className="text-[11px] text-fg-muted">{label}</span>
      {children}
    </label>
  );
}

function parseStringArray(value: string): string[] {
  try {
    const parsed = JSON.parse(value || "[]");
    return Array.isArray(parsed) ? parsed.filter((entry) => typeof entry === "string") : [];
  } catch {
    return [];
  }
}

function parseToolPermissions(value: string): { allow: number; deny: number } {
  try {
    const parsed = JSON.parse(value || "{}") as { allow?: unknown[]; deny?: unknown[] };
    return {
      allow: Array.isArray(parsed.allow) ? parsed.allow.length : 0,
      deny: Array.isArray(parsed.deny) ? parsed.deny.length : 0,
    };
  } catch {
    return { allow: 0, deny: 0 };
  }
}

function buildKnownToolPayload(draft: KnownToolEditorState): AgentKnownToolUpsertRequest {
  return {
    tool_name: draft.tool_name.trim(),
    pinned: draft.pinned,
    sort_order: safeNumber(draft.sort_order),
    ttl_seconds: safeNumber(draft.ttl_seconds),
    reason: draft.reason.trim(),
  };
}

function buildKnownSkillPayload(draft: KnownSkillEditorState): AgentKnownSkillUpsertRequest {
  return {
    skill_name: draft.skill_name.trim(),
    pinned: draft.pinned,
    ttl_seconds: safeNumber(draft.ttl_seconds),
    reason: draft.reason.trim(),
  };
}

function buildProcedurePayload(draft: ProcedureEditorState): AgentProcedureUpsertRequest {
  return {
    name: draft.name.trim(),
    body: draft.body.trim(),
    scope: draft.scope.trim() || "agent",
  };
}

function buildSeedPayload(draft: SeedEditorState): AgentKnowledgeSeedUpsertRequest {
  return {
    seed_key: draft.seed_key.trim(),
    namespace: draft.namespace.trim(),
    body: draft.body.trim(),
    tags: parseTagText(draft.tagsText),
  };
}

function toKnownToolDraft(row: AgentKnownTool): KnownToolEditorState {
  return {
    tool_name: row.tool_name,
    pinned: row.pinned,
    sort_order: String(row.sort_order),
    ttl_seconds: String(row.ttl_seconds),
    reason: row.reason || "",
  };
}

function toKnownSkillDraft(row: AgentKnownSkill): KnownSkillEditorState {
  return {
    skill_name: row.skill_name,
    pinned: row.pinned,
    ttl_seconds: String(row.ttl_seconds),
    reason: row.reason || "",
  };
}

function toProcedureDraft(row: AgentProcedure): ProcedureEditorState {
  return {
    name: row.name,
    body: row.body,
    scope: row.scope || "agent",
  };
}

function toSeedDraft(row: AgentKnowledgeSeed): SeedEditorState {
  return {
    seed_key: row.seed_key,
    namespace: row.namespace,
    body: row.body,
    tagsText: parseStringArray(row.tags_json).join(", "),
  };
}

function parseTagText(value: string): string[] {
  return Array.from(
    new Set(
      value
        .split(/[\n,]/g)
        .map((entry) => entry.trim())
        .filter(Boolean),
    ),
  );
}

function safeNumber(value: string): number {
  const parsed = Number.parseInt(value, 10);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return "Request failed.";
}

function timestampLabel(label: string, value?: string | null): string | null {
  if (!value) return null;
  return `${label} ${formatTimestamp(value)}`;
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

function ttlLabel(value: number): string {
  return value > 0 ? `ttl ${value}s` : "no ttl";
}

function reasonLabel(value: string): string {
  return value ? `reason: ${truncate(value, 48)}` : "";
}

function truncate(value: string, length: number): string {
  if (value.length <= length) return value;
  return `${value.slice(0, Math.max(0, length - 1))}…`;
}

function describeKnownTool(row: AgentKnownTool): string {
  return [
    row.pinned ? "pinned" : "",
    `order ${row.sort_order}`,
    ttlLabel(row.ttl_seconds),
    row.activation_count > 0 ? `activations ${row.activation_count}` : "",
    reasonLabel(row.reason),
  ]
    .filter(Boolean)
    .join(" · ");
}
