import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Edit,
  Eye,
  FileText,
  Plus,
  Trash2,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
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
import type { PromptTemplate, TemplateVariable } from "@/lib/types";
import { PromptCreateWizard } from "./agents/PromptCreateWizard";
import { PromptDetailView } from "./agents/PromptDetailView";

function parseVariables(s: string): TemplateVariable[] {
  try {
    return JSON.parse(s);
  } catch {
    return [];
  }
}

type PromptTemplateEditorProps = {};

export function PromptTemplateEditor({}: PromptTemplateEditorProps) {
  const [selectedTemplate, setSelectedTemplate] = useState<string | null>(null);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [previewVariables, setPreviewVariables] = useState<Record<string, any>>({});
  const queryClient = useQueryClient();

  const { data: templates = [], isLoading } = useQuery({
    queryKey: ["prompt-templates"],
    queryFn: api.listPromptTemplates,
  });

  const { data: templateDetail } = useQuery({
    queryKey: ["prompt-template", selectedTemplate],
    queryFn: () => api.getPromptTemplate(selectedTemplate!),
    enabled: !!selectedTemplate,
  });

  const createMutation = useMutation({
    mutationFn: api.createPromptTemplate,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["prompt-templates"] });
      setShowCreateForm(false);
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<PromptTemplate> }) =>
      api.updatePromptTemplate(id, data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["prompt-templates"] });
      void queryClient.invalidateQueries({ queryKey: ["prompt-template", selectedTemplate] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: api.deletePromptTemplate,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["prompt-templates"] });
      setSelectedTemplate(null);
    },
  });

  const handleCreateTemplate = useCallback(
    (data: {
      name: string;
      slug: string;
      scope: "system" | "mode" | "skill" | "context";
      template: string;
      variables: string;
      priority: number;
      icon: string;
    }) => {
      createMutation.mutate(data);
    },
    [createMutation],
  );

  const handleUpdateTemplate = useCallback(
    (id: string, data: Partial<PromptTemplate>) => {
      updateMutation.mutate({ id, data });
    },
    [updateMutation],
  );

  const renderTemplatePreview = useMemo(() => {
    if (!templateDetail) return "";

    let preview = templateDetail.template;

    // Replace variables with sample values
    parseVariables(templateDetail.variables).forEach((variable) => {
      const placeholder = `{{${variable.name}}}`;
      let value = previewVariables[variable.name] || variable.default || `[${variable.name}]`;

      if (variable.type === "boolean") {
        value = value === true || value === "true" ? "true" : "false";
      }

      preview = preview.replace(
        new RegExp(placeholder.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "g"),
        String(value),
      );
    });

    return preview;
  }, [templateDetail, previewVariables]);

  const getScopeBadgeColor = (scope: string) => {
    switch (scope) {
      case "system":
        return "bg-blue-500";
      case "mode":
        return "bg-green-500";
      case "skill":
        return "bg-yellow-500";
      case "context":
        return "bg-primary";
      default:
        return "bg-gray-500";
    }
  };

  // Sorted templates by priority then scope
  const sortedTemplates = useMemo(() => {
    return [...templates].sort((a, b) => {
      if (a.priority !== b.priority) return b.priority - a.priority;
      return a.scope.localeCompare(b.scope);
    });
  }, [templates]);

  // List View
  if (!selectedTemplate && !showCreateForm) {
    return (
      <div className="space-y-4">
        {/* Toolbar */}
        <div className="flex items-center gap-3">
          <div className="flex-1" />
          <Button size="sm" onClick={() => setShowCreateForm(true)} className="gap-1.5">
            <Plus className="w-3.5 h-3.5" />
            Create Prompt
          </Button>
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
        ) : sortedTemplates.length === 0 ? (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <FileText />
              </EmptyMedia>
              <EmptyTitle className="text-sm">No prompts found</EmptyTitle>
              <EmptyDescription className="text-xs">
                Create your first prompt to get started.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="grid gap-3 grid-cols-2">
            {sortedTemplates.map((template) => {
              const varCount = parseVariables(template.variables).length;
              return (
                <ContextMenu key={template.id}>
                  <ContextMenuTrigger asChild>
                    <div
                      className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden transition-all cursor-pointer hover:shadow-md"
                      onClick={() => setSelectedTemplate(template.id)}
                    >
                      {/* Header */}
                      <div className="flex items-center gap-2.5 px-3.5 py-3">
                        <span className="inline-flex items-center justify-center w-9 h-9 rounded-lg bg-surface-hover text-fg-secondary shrink-0">
                          <DynamicIcon
                            name={template.icon}
                            className="w-4 h-4"
                            fallback={FileText}
                          />
                        </span>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-semibold text-fg truncate">
                              {template.name}
                            </span>
                            {template.is_builtin && (
                              <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />
                            )}
                          </div>
                          <span className="text-[11px] text-fg-muted font-mono truncate block">
                            {template.slug}
                          </span>
                        </div>
                      </div>

                      {/* Detail footer */}
                      <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40 flex items-center gap-2">
                        <span
                          className={`text-[10px] px-1.5 py-0.5 rounded-md leading-none text-white ${getScopeBadgeColor(template.scope)}`}
                        >
                          {template.scope}
                        </span>
                        <span className="text-[11px] text-fg-muted">P{template.priority}</span>
                        {varCount > 0 && (
                          <>
                            <div className="w-px h-3.5 bg-border shrink-0" />
                            <span className="text-[11px] text-fg-muted">
                              {varCount} var{varCount !== 1 ? "s" : ""}
                            </span>
                          </>
                        )}
                      </div>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    <ContextMenuItem
                      className="gap-2 text-xs"
                      onSelect={() => setSelectedTemplate(template.id)}
                    >
                      <Eye className="w-3.5 h-3.5" />
                      View Details
                    </ContextMenuItem>
                    {!template.is_builtin && (
                      <>
                        <ContextMenuItem
                          className="gap-2 text-xs"
                          onSelect={() => setSelectedTemplate(template.id)}
                        >
                          <Edit className="w-3.5 h-3.5" />
                          Edit
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem
                          className="gap-2 text-xs text-primary focus:text-primary"
                          onSelect={() => setSelectedTemplate(template.id)}
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
      <PromptCreateWizard
        onSubmit={handleCreateTemplate}
        onCancel={() => setShowCreateForm(false)}
        isPending={createMutation.isPending}
      />
    );
  }

  // Detail View (inline-editable)
  if (selectedTemplate && templateDetail) {
    return (
      <PromptDetailView
        template={templateDetail}
        onUpdate={handleUpdateTemplate}
        onDelete={(id) => deleteMutation.mutate(id)}
        isDeleting={deleteMutation.isPending}
        onBack={() => setSelectedTemplate(null)}
        previewVariables={previewVariables}
        onPreviewVariablesChange={setPreviewVariables}
        renderedPreview={renderTemplatePreview}
      />
    );
  }

  return null;
}
