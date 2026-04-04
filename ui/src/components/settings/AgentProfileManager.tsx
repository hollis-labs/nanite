import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Eye,
  Plus,
  Settings,
  Trash2,
  User,
  Users,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { SourceBadge, SOURCE_LABELS } from "@/components/agents/SourceBadge";
import { StatusDot } from "@/components/agents/StatusDot";
import { TagPills } from "@/components/agents/TagPills";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { DynamicIcon } from "@/components/ui/icon-picker";
import { Skeleton } from "@/components/ui/skeleton";
import { useModels, useSettings } from "@/hooks/useSettings";
import { api } from "@/lib/api";
import type { AgentModeProfile, AgentProfile } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { AgentCreateWizard } from "./agents/AgentCreateWizard";
import { AgentDetailView } from "./agents/AgentDetailView";

type AgentProfileManagerProps = {};

export function AgentProfileManager({}: AgentProfileManagerProps) {
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [showDisabled, setShowDisabled] = useState(false);
  const [sourceFilter, setSourceFilter] = useState<string>("all");
  const queryClient = useQueryClient();
  const { data: modelRecords } = useModels();
  const { data: userSettings } = useSettings();
  const defaultModel = userSettings?.default_model || "";
  const modelOptions = (modelRecords ?? []).map((m) => ({ id: m.model_id, label: m.display_name }));

  const { data: agents = [], isLoading } = useQuery({
    queryKey: ["agent-profiles"],
    queryFn: api.listAgents,
  });

  // Collect all unique tags across agents for autocomplete
  const allKnownTags = useMemo(() => {
    const tags = new Set<string>();
    for (const a of agents) {
      try {
        const parsed: string[] = JSON.parse(a.tags || "[]");
        for (const t of parsed) tags.add(t);
      } catch {
        /* skip */
      }
    }
    return [...tags].sort();
  }, [agents]);

  const { data: agentDetail } = useQuery({
    queryKey: ["agent-detail", selectedAgent],
    queryFn: () => api.getAgentProfile(selectedAgent!),
    enabled: !!selectedAgent,
  });

  const { data: allSkills = [] } = useQuery({
    queryKey: ["skills"],
    queryFn: api.listSkills,
    enabled: !!selectedAgent,
  });

  const { data: agentSkills = [] } = useQuery({
    queryKey: ["agent-skills", selectedAgent],
    queryFn: () => api.listAgentSkills(selectedAgent!),
    enabled: !!selectedAgent,
  });

  const { data: allTemplates = [] } = useQuery({
    queryKey: ["prompt-templates"],
    queryFn: api.listPromptTemplates,
    enabled: !!selectedAgent,
  });

  const { data: agentTemplates = [] } = useQuery({
    queryKey: ["agent-templates", selectedAgent],
    queryFn: () => api.listAgentTemplates(selectedAgent!),
    enabled: !!selectedAgent,
  });

  // Agent-Project many-to-many
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const { data: allProjects = [] } = useQuery({
    queryKey: ["projects", activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: !!selectedAgent && !!activeWorkspaceId,
  });
  const { data: agentProjects = [] } = useQuery({
    queryKey: ["agent-projects", selectedAgent],
    queryFn: () => api.listAgentProjects(selectedAgent!),
    enabled: !!selectedAgent,
  });

  const createMutation = useMutation({
    mutationFn: api.createAgentProfile,
    onSuccess: (newAgent) => {
      void queryClient.invalidateQueries({ queryKey: ["agent-profiles"] });
      setShowCreateForm(false);
      setSelectedAgent(newAgent.id);
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<AgentProfile> }) =>
      api.updateAgentProfile(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-profiles"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-detail", selectedAgent] });
    },
  });

  // const deleteMutation = useMutation({  // DELETE not implemented in backend
  //   mutationFn: api.deleteAgentProfile,
  //   onSuccess: () => {
  //     void queryClient.invalidateQueries({ queryKey: ['agent-profiles'] })
  //     setSelectedAgent(null)
  //     setShowDeleteConfirm(null)
  //   },
  // })

  const createModeMutation = useMutation({
    mutationFn: ({
      agentId,
      data,
    }: {
      agentId: string;
      data: Omit<AgentModeProfile, "id" | "agent_id">;
    }) => api.createAgentMode(agentId, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-detail", selectedAgent] });
    },
  });

  const assignSkillMutation = useMutation({
    mutationFn: ({ agentId, skillId }: { agentId: string; skillId: string }) =>
      api.assignSkillToAgent(agentId, { skill_id: skillId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-skills", selectedAgent] });
    },
  });

  const removeSkillMutation = useMutation({
    mutationFn: ({ agentId, skillId }: { agentId: string; skillId: string }) =>
      api.removeSkillFromAgent(agentId, skillId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-skills", selectedAgent] });
    },
  });

  const assignTemplateMutation = useMutation({
    mutationFn: ({ agentId, templateId }: { agentId: string; templateId: string }) =>
      api.assignTemplateToAgent(agentId, { template_id: templateId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-templates", selectedAgent] });
    },
  });

  const removeTemplateMutation = useMutation({
    mutationFn: ({ agentId, templateId }: { agentId: string; templateId: string }) =>
      api.removeTemplateFromAgent(agentId, templateId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-templates", selectedAgent] });
    },
  });

  const addProjectMutation = useMutation({
    mutationFn: ({ agentId, projectId }: { agentId: string; projectId: string }) =>
      api.addAgentProject(agentId, projectId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-projects", selectedAgent] });
    },
  });

  const removeProjectMutation = useMutation({
    mutationFn: ({ agentId, projectId }: { agentId: string; projectId: string }) =>
      api.removeAgentProject(agentId, projectId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-projects", selectedAgent] });
    },
  });

  // const deleteModeMutation = useMutation({  // DELETE not implemented in backend
  //   mutationFn: ({ agentId, modeId }: { agentId: string; modeId: string }) =>
  //     api.deleteAgentMode(agentId, modeId),
  //   onSuccess: () => {
  //     void queryClient.invalidateQueries({ queryKey: ['agent-detail', selectedAgent] })
  //   },
  // })

  const handleAssignSkill = useCallback(
    (skillId: string) => {
      if (!selectedAgent) return;
      assignSkillMutation.mutate({ agentId: selectedAgent, skillId });
    },
    [selectedAgent, assignSkillMutation],
  );

  const handleRemoveSkill = useCallback(
    (skillId: string) => {
      if (!selectedAgent) return;
      removeSkillMutation.mutate({ agentId: selectedAgent, skillId });
    },
    [selectedAgent, removeSkillMutation],
  );

  const handleAssignTemplate = useCallback(
    (templateId: string) => {
      if (!selectedAgent) return;
      assignTemplateMutation.mutate({ agentId: selectedAgent, templateId });
    },
    [selectedAgent, assignTemplateMutation],
  );

  const handleRemoveTemplate = useCallback(
    (templateId: string) => {
      if (!selectedAgent) return;
      removeTemplateMutation.mutate({ agentId: selectedAgent, templateId });
    },
    [selectedAgent, removeTemplateMutation],
  );

  // Get available skills (not yet assigned to this agent)
  const availableSkills = allSkills.filter((skill) => !agentSkills.some((s) => s.id === skill.id));

  // Get available templates (not yet assigned to this agent)
  const availableTemplates = allTemplates.filter(
    (template) => !agentTemplates.some((t) => t.id === template.id),
  );

  // Get available projects (not yet assigned to this agent)
  const availableProjects = allProjects.filter(
    (project) => !agentProjects.some((p) => p.id === project.id),
  );

  const handleAddProject = useCallback(
    (projectId: string) => {
      if (!selectedAgent) return;
      addProjectMutation.mutate({ agentId: selectedAgent, projectId });
    },
    [selectedAgent, addProjectMutation],
  );

  const handleRemoveProject = useCallback(
    (projectId: string) => {
      if (!selectedAgent) return;
      removeProjectMutation.mutate({ agentId: selectedAgent, projectId });
    },
    [selectedAgent, removeProjectMutation],
  );

  const filteredAgents = useMemo(() => {
    return agents.filter((a) => {
      if (!showDisabled && a.status === "disabled") return false;
      if (sourceFilter !== "all" && a.source !== sourceFilter) return false;
      return true;
    });
  }, [agents, showDisabled, sourceFilter]);

  const sourceCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const a of agents) {
      const s = a.source || "unknown";
      counts[s] = (counts[s] || 0) + 1;
    }
    return counts;
  }, [agents]);

  // List View
  if (!selectedAgent && !showCreateForm) {
    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold text-fg">Agent Profiles</h2>
          <Button onClick={() => setShowCreateForm(true)} className="gap-2">
            <Plus className="w-4 h-4" />
            Create Agent
          </Button>
        </div>

        {/* Filters */}
        <div className="flex items-center gap-3 flex-wrap">
          <div className="flex items-center gap-1.5">
            {["all", ...Object.keys(sourceCounts)].map((s) => (
              <button
                key={s}
                onClick={() => setSourceFilter(s)}
                className={`px-2.5 py-1 rounded-md text-xs font-medium transition-colors ${
                  sourceFilter === s
                    ? "bg-primary text-white"
                    : "bg-surface text-fg-secondary hover:text-fg hover:bg-surface-hover"
                }`}
              >
                {s === "all" ? "All" : (SOURCE_LABELS[s] ?? s)}
                {s !== "all" && (
                  <span className="ml-1 text-[10px] opacity-70">{sourceCounts[s]}</span>
                )}
              </button>
            ))}
          </div>
          <label className="flex items-center gap-1.5 text-xs text-fg-secondary ml-auto cursor-pointer select-none">
            <input
              type="checkbox"
              checked={showDisabled}
              onChange={(e) => setShowDisabled(e.target.checked)}
              className="rounded border-border-subtle bg-surface text-primary focus:ring-primary focus:ring-offset-bg-elevated"
            />
            Show disabled
          </label>
        </div>

        {isLoading ? (
          <div className="grid grid-cols-2 gap-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <div
                key={i}
                className="rounded-xl border border-border-subtle bg-bg-elevated/60 shadow-sm overflow-hidden"
              >
                <div className="px-3.5 py-3 flex items-center gap-2.5">
                  <Skeleton className="size-9 rounded-lg" />
                  <div className="flex flex-col gap-1.5 flex-1">
                    <Skeleton className="h-3.5 w-1/2" />
                    <Skeleton className="h-2.5 w-1/3" />
                  </div>
                </div>
                <div className="border-t border-border/50 px-3.5 py-2">
                  <Skeleton className="h-2.5 w-3/4" />
                </div>
              </div>
            ))}
          </div>
        ) : filteredAgents.length === 0 ? (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Users />
              </EmptyMedia>
              <EmptyTitle className="text-sm">
                {agents.length === 0
                  ? "No agent profiles found"
                  : "No agents match the current filters"}
              </EmptyTitle>
              <EmptyDescription className="text-xs">
                {agents.length === 0
                  ? "Create your first agent to get started."
                  : "Try adjusting your filters to see more results."}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="grid gap-3 grid-cols-2">
            {filteredAgents.map((agent) => {
              const isActive = agent.status !== "disabled";
              return (
                <ContextMenu key={agent.id}>
                  <ContextMenuTrigger asChild>
                    <div
                      className={`rounded-xl border shadow-sm overflow-hidden transition-all cursor-pointer ${
                        isActive
                          ? "border-border-subtle bg-white dark:bg-bg-elevated/60 hover:shadow-md"
                          : "border-border bg-white dark:bg-bg/30 opacity-45"
                      }`}
                      onClick={() => setSelectedAgent(agent.id)}
                    >
                      {/* Header: Icon · Name · Status dot */}
                      <div className="flex items-center gap-2.5 px-3.5 py-3">
                        <span
                          className={`inline-flex items-center justify-center w-9 h-9 rounded-lg text-sm shrink-0 ${
                            isActive ? "bg-surface-hover text-fg-secondary" : "bg-surface text-fg-muted"
                          }`}
                        >
                          {agent.icon ? (
                            <DynamicIcon name={agent.icon} className="w-4 h-4" fallback={User} />
                          ) : (
                            agent.avatar || <User className="w-4 h-4" />
                          )}
                        </span>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span
                              className={`text-sm font-semibold truncate ${isActive ? "text-fg" : "text-fg-muted"}`}
                            >
                              {agent.name}
                            </span>
                            {isActive && <StatusDot status={agent.status || "active"} />}
                          </div>
                          <div className="flex items-center gap-1.5 mt-0.5">
                            <span className="text-[11px] text-fg-muted font-mono truncate">
                              {agent.slug}
                            </span>
                            <SourceBadge source={agent.source} />
                          </div>
                        </div>
                      </div>

                      {/* Detail footer */}
                      <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40 flex flex-col gap-1.5">
                        {agent.description && (
                          <p className="text-[11px] text-fg-muted line-clamp-2">
                            {agent.description}
                          </p>
                        )}
                        <TagPills tags={agent.tags} max={4} />
                      </div>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    <ContextMenuItem
                      className="gap-2 text-xs"
                      onClick={() => setSelectedAgent(agent.id)}
                    >
                      <Eye className="w-3.5 h-3.5" />
                      View Details
                    </ContextMenuItem>
                    {agent.source === "api" && (
                      <>
                        <ContextMenuItem
                          className="gap-2 text-xs"
                          onClick={() => setSelectedAgent(agent.id)}
                        >
                          <Settings className="w-3.5 h-3.5" />
                          Edit
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem className="gap-2 text-xs text-primary" disabled>
                          <Trash2 className="w-3.5 h-3.5" />
                          Delete
                        </ContextMenuItem>
                      </>
                    )}
                  </ContextMenuContent>
                </ContextMenu>
              );
            })}
          </div>
        )}
      </div>
    );
  }

  // Create Wizard
  if (showCreateForm) {
    return (
      <AgentCreateWizard
        modelOptions={modelOptions}
        defaultModel={defaultModel}
        onSubmit={(data) => createMutation.mutate(data)}
        onCancel={() => setShowCreateForm(false)}
        isPending={createMutation.isPending}
      />
    );
  }

  // Detail View
  if (selectedAgent && agentDetail) {
    const { agent, modes } = agentDetail;

    return (
      <AgentDetailView
        agent={agent}
        modes={modes}
        modelOptions={modelOptions}
        allKnownTags={allKnownTags}
        onUpdateAgent={(data) => updateMutation.mutate({ id: agent.id, data })}
        onCreateMode={(data) => createModeMutation.mutate({ agentId: agent.id, data })}
        isCreatingMode={createModeMutation.isPending}
        agentSkills={agentSkills}
        availableSkills={availableSkills}
        onAssignSkill={handleAssignSkill}
        onRemoveSkill={handleRemoveSkill}
        agentTemplates={agentTemplates}
        availableTemplates={availableTemplates}
        onAssignTemplate={handleAssignTemplate}
        onRemoveTemplate={handleRemoveTemplate}
        agentProjects={agentProjects}
        availableProjects={availableProjects}
        onAddProject={handleAddProject}
        onRemoveProject={handleRemoveProject}
        onBack={() => setSelectedAgent(null)}
      />
    );
  }

  return null;
}
