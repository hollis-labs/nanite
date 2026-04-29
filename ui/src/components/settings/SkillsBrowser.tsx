import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Eye,
  Filter,
  GitFork,
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

// resolveSource reads the top-level `source` column when present (J7
// ingestion metadata), falling back to the legacy settings.source path
// used before J7. "db" is the catch-all for rows that pre-date both paths.
function resolveSource(skill: { source?: string; settings: string }): string {
  if (skill.source && skill.source !== "") return skill.source;
  try {
    const parsed = JSON.parse(skill.settings);
    return parsed?.source ?? "db";
  } catch {
    return "db";
  }
}

// E2 (CW-20260428-0017): parse mode tags. Skills carry mode_ids (resolved
// IDs) on the top-level column AND mode_slugs (raw slugs) inside settings
// for back-compat with file-defs that haven't been ingested yet.
function parseModeIDs(skill: { mode_ids?: string }): string[] {
  if (!skill.mode_ids) return [];
  try {
    const parsed = JSON.parse(skill.mode_ids);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function parseModeSlugs(settings: string): string[] {
  try {
    const parsed = JSON.parse(settings);
    if (parsed?.mode_slugs && Array.isArray(parsed.mode_slugs)) {
      return parsed.mode_slugs;
    }
    return [];
  } catch {
    return [];
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
  // E2 (CW-20260428-0017): filter skills by current session mode. "all"
  // disables the filter; otherwise we keep skills whose mode_ids contains
  // the picked mode ID OR whose mode_ids is empty (back-compat — those
  // skills are available everywhere).
  const [modeFilter, setModeFilter] = useState<string>("all");
  const queryClient = useQueryClient();

  const { data: skills = [], isLoading } = useQuery({
    queryKey: ["skills"],
    queryFn: api.listSkills,
  });

  // Modes feed the per-skill tag rendering (slug from ID) and the mode
  // filter dropdown. The list is small and stable enough that we don't
  // need pagination here.
  const { data: modes = [] } = useQuery({
    queryKey: ["modes"],
    queryFn: api.listModes,
  });

  // Dev-mode flag drives the inline-edit affordance on internal skills.
  const { data: devModeData } = useQuery({
    queryKey: ["dev-mode"],
    queryFn: api.getDevMode,
  });
  const devMode = devModeData?.dev_mode ?? false;

  const forkMutation = useMutation({
    mutationFn: ({ id, prompt }: { id: string; prompt?: string }) =>
      api.forkSkillToUser(id, { prompt }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["skills"] });
    },
  });

  const modeByID = useMemo(() => {
    const m = new Map<string, { id: string; slug: string; name: string }>();
    for (const md of modes) m.set(md.id, md);
    return m;
  }, [modes]);

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
      const src = resolveSource(s);
      counts[src] = (counts[src] || 0) + 1;
    }
    return counts;
  }, [skills]);

  // Filter skills based on category, source, and mode binding.
  const filteredSkills = useMemo(() => {
    return skills.filter((skill) => {
      if (categoryFilter !== "all" && skill.category !== categoryFilter) return false;
      if (sourceFilter !== "all" && resolveSource(skill) !== sourceFilter) return false;
      if (modeFilter !== "all") {
        const ids = parseModeIDs(skill);
        // Empty mode_ids → "available everywhere" (back-compat); also kept.
        // E2 acceptance: filter "active for current mode" works for skills
        // bound to that mode AND for skills with no binding.
        if (ids.length > 0 && !ids.includes(modeFilter)) return false;
      }
      return true;
    });
  }, [skills, categoryFilter, sourceFilter, modeFilter]);

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
            <select
              value={modeFilter}
              onChange={(e) => setModeFilter(e.target.value)}
              className="appearance-none px-3 pr-8 py-1.5 bg-surface/50 border border-border rounded-lg text-fg text-xs focus:outline-none focus:ring-1 focus:ring-primary cursor-pointer"
              title="Show only skills active for the selected mode"
            >
              <option value="all">All Modes</option>
              {modes.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
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
                  ? "bg-brand text-brand-fg"
                  : "bg-surface text-fg hover:bg-surface-hover"
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
                className="rounded-xl border border-border-subtle bg-bg-elevated shadow-sm overflow-hidden"
              >
                <div className="px-3.5 py-3 flex items-center gap-2.5">
                  <Skeleton className="size-9 rounded-lg" />
                  <div className="flex flex-col gap-1.5 flex-1">
                    <Skeleton className="h-3.5 w-1/2" />
                    <Skeleton className="h-2.5 w-1/3" />
                  </div>
                </div>
                <div className="border-t border-border-subtle px-3.5 py-2">
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
              const source = resolveSource(skill);
              const ids = parseModeIDs(skill);
              const tagSlugs =
                ids.length > 0
                  ? ids.map((id) => modeByID.get(id)?.slug).filter(Boolean) as string[]
                  : parseModeSlugs(skill.settings);
              const isInternal = source === "" || source === "builtin" || source === "seed";
              return (
                <ContextMenu key={skill.id}>
                  <ContextMenuTrigger asChild>
                    <div
                      className={`rounded-xl border border-border-subtle bg-bg-elevated overflow-hidden transition-all cursor-pointer hover:border-border hover:shadow-sm group border-l-2 ${skill.is_builtin ? "border-l-status-ok" : "border-l-brand"}`}
                      onClick={() => setSelectedSkill(skill.id)}
                    >
                      {/* Header */}
                      <div className="flex items-center gap-2.5 px-3.5 py-3">
                        <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-surface text-fg-secondary shrink-0 group-hover:text-fg transition-colors">
                          <DynamicIcon name={skill.icon} className="w-4 h-4" fallback={Wrench} />
                        </span>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-semibold text-fg truncate">
                              {skill.name}
                            </span>
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
                      <div className="border-t border-border-subtle px-3.5 py-2 bg-bg/40">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-surface border border-border-subtle text-fg-secondary leading-none uppercase tracking-wide">
                            {skill.category}
                          </span>
                          {toolCount > 0 && (
                            <span className="text-[11px] text-fg-muted">
                              {toolCount} tool{toolCount !== 1 ? "s" : ""}
                            </span>
                          )}
                          {tagSlugs.map((slug) => (
                            <span
                              key={slug}
                              className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-brand/10 border border-brand/20 text-brand leading-none uppercase tracking-wide"
                              title={`mode: ${slug}`}
                            >
                              {slug}
                            </span>
                          ))}
                          {tagSlugs.length === 0 && (
                            <span
                              className="text-[10px] text-fg-faint italic"
                              title="No mode binding — available in every mode"
                            >
                              all modes
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
                    {devMode && isInternal && (
                      <ContextMenuItem
                        className="gap-2 text-xs"
                        onClick={() => {
                          forkMutation.mutate({ id: skill.id, prompt: skill.prompt });
                        }}
                      >
                        <GitFork className="w-3.5 h-3.5" />
                        Fork to user override
                      </ContextMenuItem>
                    )}
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
