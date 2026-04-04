import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Eye,
  Filter,
  Plus,
  Trash2,
  Wrench,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { SourceBadge, SOURCE_LABELS } from "@/components/agents/SourceBadge";
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
import { api } from "@/lib/api";
import type { Skill } from "@/lib/types";
import { SkillCreateWizard } from "./agents/SkillCreateWizard";
import { SkillDetailView } from "./agents/SkillDetailView";

function parseToolBindings(s: string): { server: string; tool: string }[] {
  try {
    return JSON.parse(s);
  } catch {
    return [];
  }
}

function parseSource(settings: string): string {
  try {
    const parsed = JSON.parse(settings);
    return parsed?.source ?? "db";
  } catch {
    return "db";
  }
}

type SkillsBrowserProps = {};

const SKILL_CATEGORIES = [
  "general",
  "development",
  "communication",
  "analysis",
  "automation",
  "integration",
  "productivity",
  "other",
] as const;

export function SkillsBrowser({}: SkillsBrowserProps) {
  const [selectedSkill, setSelectedSkill] = useState<string | null>(null);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [categoryFilter, setCategoryFilter] = useState<string>("all");
  const [sourceFilter, setSourceFilter] = useState<string>("all");
  const queryClient = useQueryClient();

  const { data: skills = [], isLoading } = useQuery({
    queryKey: ["skills"],
    queryFn: api.listSkills,
  });

  const { data: skillDetail } = useQuery({
    queryKey: ["skill-detail", selectedSkill],
    queryFn: () => api.getSkill(selectedSkill!),
    enabled: !!selectedSkill,
  });

  const createMutation = useMutation({
    mutationFn: api.createSkill,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["skills"] });
      setShowCreateForm(false);
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<Skill> }) => api.updateSkill(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["skills"] });
      void queryClient.invalidateQueries({ queryKey: ["skill-detail", selectedSkill] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: api.deleteSkill,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["skills"] });
      setSelectedSkill(null);
    },
  });

  const handleCreateSkill = useCallback(
    (data: {
      name: string;
      slug: string;
      category: string;
      description: string;
      icon: string;
      tool_bindings: string;
      input_schema: string;
      settings: string;
    }) => {
      createMutation.mutate(data);
    },
    [createMutation],
  );

  const handleUpdateField = useCallback(
    (field: string, value: string) => {
      if (!selectedSkill) return;
      updateMutation.mutate({ id: selectedSkill, data: { [field]: value } as Partial<Skill> });
    },
    [selectedSkill, updateMutation],
  );

  // Source counts for pill badges.
  const sourceCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const s of skills) {
      const src = parseSource(s.settings);
      counts[src] = (counts[src] || 0) + 1;
    }
    return counts;
  }, [skills]);

  // Filter skills based on category and source.
  const filteredSkills = useMemo(() => {
    return skills.filter((skill) => {
      if (categoryFilter !== "all" && skill.category !== categoryFilter) return false;
      if (sourceFilter !== "all" && parseSource(skill.settings) !== sourceFilter) return false;
      return true;
    });
  }, [skills, categoryFilter, sourceFilter]);

  // List View
  if (!selectedSkill && !showCreateForm) {
    return (
      <div className="space-y-4">
        {/* Toolbar */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Filter className="w-3.5 h-3.5 text-fg-muted" />
            <select
              value={categoryFilter}
              onChange={(e) => setCategoryFilter(e.target.value)}
              className="appearance-none px-3 pr-8 py-1.5 bg-surface/50 border border-border rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-primary cursor-pointer"
            >
              <option value="all">All Categories</option>
              {SKILL_CATEGORIES.map((category) => (
                <option key={category} value={category}>
                  {category.charAt(0).toUpperCase() + category.slice(1)}
                </option>
              ))}
            </select>
          </div>
          <Button size="sm" onClick={() => setShowCreateForm(true)} className="gap-1.5">
            <Plus className="w-3.5 h-3.5" />
            Create Skill
          </Button>
        </div>

        {/* Source filter pills */}
        <div className="flex items-center gap-1.5 flex-wrap">
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
        ) : filteredSkills.length === 0 ? (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Wrench />
              </EmptyMedia>
              <EmptyTitle className="text-sm">
                {skills.length === 0
                  ? "No skills found"
                  : "No skills match the current filters"}
              </EmptyTitle>
              <EmptyDescription className="text-xs">
                {skills.length === 0
                  ? "Create your first skill to get started."
                  : "Try adjusting your filters to see more results."}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="grid gap-3 grid-cols-2">
            {filteredSkills.map((skill) => {
              const toolCount = parseToolBindings(skill.tool_bindings).length;
              const source = parseSource(skill.settings);
              return (
                <ContextMenu key={skill.id}>
                  <ContextMenuTrigger asChild>
                    <div
                      className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md"
                      onClick={() => setSelectedSkill(skill.id)}
                    >
                      {/* Header */}
                      <div className="flex items-center gap-2.5 px-3.5 py-3">
                        <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-surface-hover text-fg-secondary shrink-0">
                          <DynamicIcon name={skill.icon} className="w-4 h-4" fallback={Wrench} />
                        </span>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-semibold text-fg truncate">
                              {skill.name}
                            </span>
                            {skill.is_builtin && (
                              <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />
                            )}
                          </div>
                          <div className="flex items-center gap-1.5 mt-0.5">
                            <span className="text-[11px] text-fg-muted font-mono truncate">
                              {skill.slug}
                            </span>
                            <SourceBadge source={source} />
                          </div>
                        </div>
                      </div>

                      {/* Detail footer */}
                      <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
                        <div className="flex items-center gap-2">
                          <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                            {skill.category}
                          </span>
                          {toolCount > 0 && (
                            <span className="text-[11px] text-fg-muted">
                              {toolCount} tool{toolCount !== 1 ? "s" : ""}
                            </span>
                          )}
                        </div>
                        {skill.description && (
                          <p className="text-[11px] text-fg-muted line-clamp-2 mt-1">
                            {skill.description}
                          </p>
                        )}
                      </div>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    <ContextMenuItem
                      className="gap-2 text-xs"
                      onClick={() => setSelectedSkill(skill.id)}
                    >
                      <Eye className="w-3.5 h-3.5" />
                      View Details
                    </ContextMenuItem>
                    {!skill.is_builtin && (
                      <>
                        <ContextMenuSeparator />
                        <ContextMenuItem
                          className="gap-2 text-xs text-primary"
                          onClick={() => {
                            setSelectedSkill(skill.id);
                          }}
                        >
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

  // Create Form (wizard)
  if (showCreateForm) {
    return (
      <SkillCreateWizard
        categories={SKILL_CATEGORIES}
        onSubmit={handleCreateSkill}
        onCancel={() => setShowCreateForm(false)}
        isPending={createMutation.isPending}
      />
    );
  }

  // Detail View
  if (selectedSkill && skillDetail) {
    return (
      <SkillDetailView
        skill={skillDetail}
        onUpdate={handleUpdateField}
        onDelete={(id) => deleteMutation.mutate(id)}
        isDeleting={deleteMutation.isPending}
        onBack={() => setSelectedSkill(null)}
      />
    );
  }

  return null;
}
