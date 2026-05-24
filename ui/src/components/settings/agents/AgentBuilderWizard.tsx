import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Bot,
  ChevronLeft,
  CircleHelp,
  ExternalLink,
  FileWarning,
  Loader2,
  Play,
  Sparkles,
  Wrench,
} from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  AgentBuilderCapabilitiesInput,
  AgentBuilderDraft,
  AgentBuilderDraftRequest,
  AgentBuilderDryRunRequest,
  AgentBuilderDryRunResponse,
  AgentBuilderPatchOperation,
  AgentKnowledgeSeedUpsertRequest,
  AgentKnownSkillUpsertRequest,
  AgentKnownToolUpsertRequest,
  AgentBootPlanDocument,
  AgentProcedureUpsertRequest,
  AgentProfile,
  CreateAgentProfileRequest,
  DurableAgentInstance,
  DurableAgentLaunchResult,
  DurableAgentRecipe,
  ModelRecord,
  PromptTemplate,
  RuntimeKind,
  Skill,
  StartSurfaceCapabilitiesResponse,
} from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { AgentCreateWizard } from "./AgentCreateWizard";
import {
  AgentBootPlanEditor,
  AgentBootPlanPreview,
  createEmptyBootPlan,
  hasBootPlanContent,
} from "./AgentBootPlanEditor";
import { EditableStringList } from "./editors/EditableStringList";

type AgentBuilderWizardProps = {
  modelOptions: { id: string; label: string }[];
  defaultModel: string;
  onCancel: () => void;
  onCreated: (agentId: string) => void;
};

type WizardMode = "builder" | "manual";

type BuilderIntake = {
  name: string;
  slug: string;
  description: string;
  intakeText: string;
  workRoot: string;
  recipeId: string;
  preferredProvider: string;
  preferredModel: string;
  preferredRuntimeKind: RuntimeKind | string;
  requestedLifecycleClass: string;
  createDurable: boolean;
  startNow: boolean;
};

type SubmitResult = {
  profile: AgentProfile;
  durableInstance?: DurableAgentInstance | null;
  launchResult?: DurableAgentLaunchResult | null;
  notificationPreview?: AgentBuilderDryRunResponse["notification_preview"];
};

type SuggestionStatus = "accepted" | "dismissed";

const REVIEW_ROUND_LIMIT = 3;

const EMPTY_KNOWN_TOOL: AgentKnownToolUpsertRequest = {
  tool_name: "",
  pinned: true,
  sort_order: 0,
  ttl_seconds: 0,
  reason: "",
};

const EMPTY_KNOWN_SKILL: AgentKnownSkillUpsertRequest = {
  skill_name: "",
  pinned: true,
  ttl_seconds: 0,
  reason: "",
};

const EMPTY_PROCEDURE: AgentProcedureUpsertRequest = {
  name: "",
  body: "",
  scope: "agent",
};

const EMPTY_SEED: AgentKnowledgeSeedUpsertRequest = {
  seed_key: "",
  namespace: "",
  body: "",
  tags: [],
};

export function AgentBuilderWizard({
  modelOptions,
  defaultModel,
  onCancel,
  onCreated,
}: AgentBuilderWizardProps) {
  const queryClient = useQueryClient();
  const activeWorkspaceId = useAppStore((state) => state.activeWorkspaceId);
  const activeProjectId = useAppStore((state) => state.activeProjectId);
  const setActiveSession = useAppStore((state) => state.setActiveSession);
  const setCurrentPage = useLayoutStore((state) => state.setCurrentPage);
  const [mode, setMode] = useState<WizardMode>("builder");
  const [intake, setIntake] = useState<BuilderIntake>({
    name: "",
    slug: "",
    description: "",
    intakeText: "",
    workRoot: "",
    recipeId: "",
    preferredProvider: "",
    preferredModel: defaultModel,
    preferredRuntimeKind: "api",
    requestedLifecycleClass: "advisor",
    createDurable: false,
    startNow: true,
  });
  const [draft, setDraft] = useState<AgentBuilderDraft | null>(null);
  const [draftMeta, setDraftMeta] = useState<{
    questions: string[];
    warnings: string[];
    unsupportedRequests: string[];
    confidence: number;
  } | null>(null);
  const [review, setReview] = useState<{
    questions: string[];
    warnings: string[];
    suggestions: AgentBuilderPatchOperation[];
  } | null>(null);
  const [reviewRounds, setReviewRounds] = useState(0);
  const [suggestionStatus, setSuggestionStatus] = useState<Record<string, SuggestionStatus>>({});
  const [dryRun, setDryRun] = useState<AgentBuilderDryRunResponse | null>(null);
  const [lastValidatedSignature, setLastValidatedSignature] = useState<string | null>(null);
  const [submitResult, setSubmitResult] = useState<SubmitResult | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const startSurfaceQuery = useQuery({
    queryKey: ["start-surface-capabilities"],
    queryFn: api.getStartSurfaceCapabilities,
  });
  const skillsQuery = useQuery({
    queryKey: ["skills"],
    queryFn: api.listSkills,
  });
  const templatesQuery = useQuery({
    queryKey: ["prompt-templates"],
    queryFn: api.listPromptTemplates,
  });

  const capabilities = startSurfaceQuery.data;
  const recipes = capabilities?.recipes ?? [];
  const providers = capabilities?.providers ?? [];
  const models = capabilities?.models ?? [];
  const runtimeKinds = capabilities?.runtime_kinds ?? [];
  const lifecycleClasses = capabilities?.lifecycle_classes ?? [];

  const createManualMutation = useMutation({
    mutationFn: api.createAgentProfile,
    onSuccess: async (profile) => {
      await queryClient.invalidateQueries({ queryKey: ["agent-profiles"] });
      onCreated(profile.id);
    },
  });

  const draftMutation = useMutation({
    mutationFn: (request: AgentBuilderDraftRequest) => api.agentBuilderDraft(request),
    onSuccess: (response) => {
      const merged = mergeDraftWithIntake(response.draft, intake);
      setDraft(merged);
      setDraftMeta({
        questions: response.questions,
        warnings: response.warnings,
        unsupportedRequests: response.unsupported_requests,
        confidence: response.confidence,
      });
      setReview(null);
      setReviewRounds(0);
      setSuggestionStatus({});
      setDryRun(null);
      setSubmitResult(null);
      setSubmitError(null);
      setLastValidatedSignature(null);
    },
  });

  const reviewMutation = useMutation({
    mutationFn: () =>
      api.agentBuilderReview({
        schema_version: 1,
        current_draft: draft as AgentBuilderDraft,
      }),
    onSuccess: (response) => {
      setReview({
        questions: response.questions,
        warnings: response.warnings,
        suggestions: response.suggested_patch_operations,
      });
      setReviewRounds((current) => current + 1);
    },
  });

  const currentDryRunRequest = useMemo(() => buildDryRunRequest(draft), [draft]);
  const currentSignature = useMemo(
    () => JSON.stringify(currentDryRunRequest ?? {}),
    [currentDryRunRequest],
  );

  const dryRunMutation = useMutation({
    mutationFn: () => api.agentBuilderDryRun(currentDryRunRequest as AgentBuilderDryRunRequest),
    onSuccess: (response) => {
      setDryRun(response);
      setLastValidatedSignature(currentSignature);
      setSubmitError(null);
    },
  });

  const submitMutation = useMutation({
    mutationFn: async () => {
      if (!draft || !dryRun) throw new Error("Run a successful dry-run before submit.");
      return submitDraft({
        draft,
        dryRun,
        defaultModel,
        activeWorkspaceId,
        activeProjectId,
        skills: skillsQuery.data ?? [],
      });
    },
    onSuccess: async (result) => {
      setSubmitResult({
        profile: result.profile,
        durableInstance: result.durableInstance,
        launchResult: result.launchResult,
        notificationPreview: dryRun?.notification_preview,
      });
      setSubmitError(null);
      await queryClient.invalidateQueries({ queryKey: ["agent-profiles"] });
      await queryClient.invalidateQueries({ queryKey: ["durable-agents"] });
      onCreated(result.profile.id);
    },
    onError: (error) => {
      setSubmitError(error instanceof Error ? error.message : "Submit failed.");
    },
  });

  const readyToSubmit =
    !!dryRun &&
    dryRun.valid &&
    lastValidatedSignature === currentSignature &&
    !submitMutation.isPending;

  const openChatSession = (sessionId?: string) => {
    if (!sessionId) return;
    setActiveSession(sessionId);
    setCurrentPage("chat");
    window.location.hash = "#chat";
  };

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={onCancel}>
          <ChevronLeft className="h-4 w-4" />
        </Button>
        <div className="min-w-0 flex-1">
          <h2 className="text-xl font-semibold text-fg">Agent Builder</h2>
          <p className="mt-0.5 text-xs text-fg-muted">
            Builder draft, review, and dry-run are advisory. Final submit is the only write step.
          </p>
        </div>
      </div>

      <Tabs value={mode} onValueChange={(value) => setMode(value as WizardMode)}>
        <TabsList>
          <TabsTrigger value="builder">Builder</TabsTrigger>
          <TabsTrigger value="manual">Manual</TabsTrigger>
        </TabsList>

        <TabsContent value="builder" className="space-y-5 pt-4">
          <Panel
            title="Intake"
            description="Capture the agent brief and any operator preferences before asking the Builder for a structured draft."
            action={
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    setDraft(buildBlankDraftFromIntake(intake));
                    setDraftMeta(null);
                    setReview(null);
                    setReviewRounds(0);
                    setSuggestionStatus({});
                    setDryRun(null);
                    setLastValidatedSignature(null);
                    setSubmitResult(null);
                    setSubmitError(null);
                  }}
                >
                  Start Blank Draft
                </Button>
                <Button
                  type="button"
                  onClick={() =>
                    draftMutation.mutate({
                      schema_version: 1,
                      intake_text: intake.intakeText,
                      name: emptyToUndefined(intake.name),
                      slug: emptyToUndefined(intake.slug),
                      description: emptyToUndefined(intake.description),
                      work_root: intake.createDurable ? emptyToUndefined(intake.workRoot) : undefined,
                      preferred_provider: emptyToUndefined(intake.preferredProvider),
                      preferred_model: emptyToUndefined(intake.preferredModel),
                      preferred_runtime_kind: emptyToUndefined(intake.preferredRuntimeKind),
                      requested_lifecycle_class: emptyToUndefined(intake.requestedLifecycleClass),
                    })
                  }
                  disabled={draftMutation.isPending || intake.intakeText.trim() === ""}
                >
                  {draftMutation.isPending ? (
                    <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Sparkles className="mr-1.5 h-3.5 w-3.5" />
                  )}
                  Generate Draft
                </Button>
              </div>
            }
          >
            <div className="grid gap-3 md:grid-cols-2">
              <Field label="Name">
                <Input
                  aria-label="Builder intake name"
                  value={intake.name}
                  onChange={(event) => setIntake({ ...intake, name: event.target.value })}
                />
              </Field>
              <Field label="Slug">
                <Input
                  aria-label="Builder intake slug"
                  value={intake.slug}
                  onChange={(event) => setIntake({ ...intake, slug: event.target.value })}
                />
              </Field>
              <Field label="Description" className="md:col-span-2">
                <Input
                  aria-label="Builder intake description"
                  value={intake.description}
                  onChange={(event) => setIntake({ ...intake, description: event.target.value })}
                />
              </Field>
              <Field label="What should this agent do?" className="md:col-span-2">
                <Textarea
                  aria-label="Builder brief"
                  rows={4}
                  value={intake.intakeText}
                  onChange={(event) => setIntake({ ...intake, intakeText: event.target.value })}
                />
              </Field>
              <Field label="Preferred lifecycle">
                <SelectField
                  ariaLabel="Preferred lifecycle"
                  value={intake.requestedLifecycleClass}
                  onChange={(value) => setIntake({ ...intake, requestedLifecycleClass: value })}
                  options={lifecycleClasses.map((option) => ({
                    value: option.value,
                    label: option.label,
                  }))}
                  fallbackOption={{ value: "advisor", label: "advisor" }}
                />
              </Field>
              <Field label="Preferred runtime">
                <SelectField
                  ariaLabel="Preferred runtime"
                  value={String(intake.preferredRuntimeKind)}
                  onChange={(value) =>
                    setIntake({ ...intake, preferredRuntimeKind: value as RuntimeKind })
                  }
                  options={runtimeKinds.map((option) => ({
                    value: option.value,
                    label: option.label,
                  }))}
                  fallbackOption={{ value: "api", label: "api" }}
                />
              </Field>
              <Field label="Preferred provider">
                <SelectField
                  ariaLabel="Preferred provider"
                  value={intake.preferredProvider}
                  onChange={(value) => setIntake({ ...intake, preferredProvider: value })}
                  options={providers.map((provider) => ({
                    value: provider.id,
                    label: provider.name,
                  }))}
                  allowEmpty
                />
              </Field>
              <Field label="Preferred model">
                <SelectField
                  ariaLabel="Preferred model"
                  value={intake.preferredModel}
                  onChange={(value) => setIntake({ ...intake, preferredModel: value })}
                  options={buildModelOptions(models, modelOptions)}
                  allowEmpty
                />
              </Field>
              <Field label="Recipe">
                <SelectField
                  ariaLabel="Recipe"
                  value={intake.recipeId}
                  onChange={(value) => setIntake({ ...intake, recipeId: value })}
                  options={recipes.map((recipe) => ({ value: recipe.id, label: recipe.name }))}
                  allowEmpty
                />
              </Field>
              <Field label="Work root">
                <Input
                  aria-label="Builder intake work root"
                  value={intake.workRoot}
                  onChange={(event) => setIntake({ ...intake, workRoot: event.target.value })}
                />
              </Field>
            </div>

            <div className="mt-4 grid gap-3 rounded-[10px] border border-border-subtle bg-surface/30 p-3">
              <ToggleRow
                label="Create durable instance"
                description="Keep this off when you only want a reusable profile template."
                checked={intake.createDurable}
                onCheckedChange={(checked) => setIntake({ ...intake, createDurable: checked })}
              />
              <ToggleRow
                label="Launch after submit"
                description="Default on. If submit succeeds and a durable instance exists, start it immediately."
                checked={intake.startNow}
                onCheckedChange={(checked) => setIntake({ ...intake, startNow: checked })}
              />
            </div>
          </Panel>

          {draftMeta ? (
            <Panel title="Builder Draft" description="Advisory output from the Agent Builder draft endpoint.">
              <div className="grid gap-3 md:grid-cols-3">
                <Metric label="Confidence" value={`${Math.round(draftMeta.confidence * 100)}%`} />
                <Metric label="Questions" value={String(draftMeta.questions.length)} />
                <Metric label="Warnings" value={String(draftMeta.warnings.length)} />
              </div>
              <FeedbackLists
                questions={draftMeta.questions}
                warnings={draftMeta.warnings}
                unsupported={draftMeta.unsupportedRequests}
              />
            </Panel>
          ) : null}

          {draft ? (
            <>
              <Panel
                title="Editable Draft"
                description="Review and edit the structured config before asking for review or running a dry-run."
                action={
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => reviewMutation.mutate()}
                      disabled={reviewMutation.isPending || reviewRounds >= REVIEW_ROUND_LIMIT}
                    >
                      {reviewMutation.isPending ? (
                        <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <CircleHelp className="mr-1.5 h-3.5 w-3.5" />
                      )}
                      Request Review
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => dryRunMutation.mutate()}
                      disabled={dryRunMutation.isPending}
                    >
                      {dryRunMutation.isPending ? (
                        <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Wrench className="mr-1.5 h-3.5 w-3.5" />
                      )}
                      Run Dry Run
                    </Button>
                    <Button
                      type="button"
                      onClick={() => submitMutation.mutate()}
                      disabled={!readyToSubmit}
                    >
                      {submitMutation.isPending ? (
                        <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Bot className="mr-1.5 h-3.5 w-3.5" />
                      )}
                      Create Agent Deterministically
                    </Button>
                  </div>
                }
              >
                <DraftEditor
                  draft={draft}
                  setDraft={(next) => {
                    setDraft(next);
                    setDryRun(null);
                    setReview(null);
                    setLastValidatedSignature(null);
                    setSubmitResult(null);
                    setSubmitError(null);
                  }}
                  skills={skillsQuery.data ?? []}
                  templates={templatesQuery.data ?? []}
                  recipes={recipes}
                  providers={providers}
                  models={models}
                  runtimeKinds={runtimeKinds.map((option) => option.value)}
                  lifecycleClasses={lifecycleClasses.map((option) => option.value)}
                />
                <p className="mt-3 text-[11px] text-fg-muted">
                  Submit stays disabled until the current draft has a successful dry-run.
                </p>
              </Panel>

              {review ? (
                <Panel
                  title="Builder Review"
                  description="Review suggestions are advisory. Accept or dismiss them explicitly."
                >
                  <div className="mb-3 text-xs text-fg-muted">
                    Review rounds used: {reviewRounds}/{REVIEW_ROUND_LIMIT}
                    {reviewRounds >= REVIEW_ROUND_LIMIT ? " • Review limit reached" : ""}
                  </div>
                  <FeedbackLists questions={review.questions} warnings={review.warnings} />
                  <div className="mt-4 space-y-2">
                    {review.suggestions.length === 0 ? (
                      <p className="text-xs text-fg-muted">No patch-style suggestions were returned.</p>
                    ) : (
                      review.suggestions.map((suggestion, index) => {
                        const key = `${suggestion.op}:${suggestion.path}:${index}`;
                        const status = suggestionStatus[key];
                        return (
                          <div
                            key={key}
                            className="rounded-[8px] border border-border-subtle bg-surface/30 p-3"
                          >
                            <div className="flex flex-wrap items-start justify-between gap-2">
                              <div>
                                <div className="text-xs font-medium text-fg">
                                  {suggestion.op} <code className="font-mono">{suggestion.path}</code>
                                </div>
                                <div className="mt-1 text-xs text-fg-muted">
                                  {suggestion.note || suggestion.value || "No direct replacement value supplied."}
                                </div>
                              </div>
                              <div className="flex items-center gap-2">
                                <Button
                                  type="button"
                                  size="sm"
                                  variant={status === "accepted" ? "default" : "outline"}
                                  onClick={() =>
                                    setSuggestionStatus((current) => ({
                                      ...current,
                                      [key]: "accepted",
                                    }))
                                  }
                                >
                                  Accept
                                </Button>
                                <Button
                                  type="button"
                                  size="sm"
                                  variant={status === "dismissed" ? "default" : "ghost"}
                                  onClick={() =>
                                    setSuggestionStatus((current) => ({
                                      ...current,
                                      [key]: "dismissed",
                                    }))
                                  }
                                >
                                  Dismiss
                                </Button>
                              </div>
                            </div>
                          </div>
                        );
                      })
                    )}
                  </div>
                </Panel>
              ) : null}

              {dryRun ? (
                <Panel title="Dry Run Preview" description="No writes occurred. Review the normalized payload and previews before submit.">
                  <FeedbackLists
                    errors={dryRun.errors}
                    warnings={dryRun.warnings}
                    unsupported={dryRun.unsupported_fields}
                  />
                  <div className="mt-4 grid gap-4 lg:grid-cols-2">
                    <CodeCard
                      title="Normalized Profile Payload"
                      body={JSON.stringify(dryRun.normalized_profile_payload, null, 2)}
                    />
                    <CodeCard
                      title="Capability Operations"
                      body={JSON.stringify(dryRun.capability_operations, null, 2)}
                    />
                    <CodeCard
                      title={dryRun.durable_recipe_plan ? "Durable Recipe Plan" : "Launch Plan Preview"}
                      body={JSON.stringify(
                        dryRun.durable_recipe_plan ?? dryRun.launch_plan_preview ?? {},
                        null,
                        2,
                      )}
                    />
                    <CodeCard
                      title="Notification Preview"
                      body={JSON.stringify(dryRun.notification_preview, null, 2)}
                    />
                  </div>
                  {dryRun.boot_plan_preview ? (
                    <div className="mt-4">
                      <div className="mb-3 text-sm font-medium text-fg">Boot Plan Preview</div>
                      <AgentBootPlanPreview preview={dryRun.boot_plan_preview} />
                    </div>
                  ) : null}
                  <div className="mt-3 text-xs text-fg-muted">
                    Validation state: {dryRun.valid ? "ready to submit" : "fix errors before submit"}.
                  </div>
                </Panel>
              ) : null}

              {submitError ? (
                <div className="rounded-[10px] border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-100">
                  Submit failed: {submitError}
                </div>
              ) : null}

              {submitResult ? (
                <Panel
                  title="Agent Ready"
                  description="Deterministic submit succeeded. Ready notice remains preview-only because there is no UI-safe endpoint that resolves the current operator mailbox target."
                >
                  <div className="grid gap-3 md:grid-cols-3">
                    <Metric label="Profile" value={submitResult.profile.name || submitResult.profile.slug} />
                    <Metric
                      label="Durable"
                      value={submitResult.durableInstance?.name || submitResult.durableInstance?.id || "Not created"}
                    />
                    <Metric
                      label="Session"
                      value={submitResult.launchResult?.session?.id || "Not launched"}
                    />
                  </div>
                  <div className="mt-4 flex flex-wrap gap-2">
                    <Button type="button" onClick={() => onCreated(submitResult.profile.id)}>
                      Open Profile
                    </Button>
                    {submitResult.launchResult?.session?.id ? (
                      <Button
                        type="button"
                        variant="outline"
                        onClick={() => openChatSession(submitResult.launchResult?.session?.id)}
                      >
                        <Play className="mr-1.5 h-3.5 w-3.5" />
                        Open Chat Session
                      </Button>
                    ) : null}
                    {submitResult.durableInstance ? (
                      <Button type="button" variant="ghost" onClick={() => window.location.hash = "#settings"}>
                        <ExternalLink className="mr-1.5 h-3.5 w-3.5" />
                        Open Durable Admin
                      </Button>
                    ) : null}
                  </div>
                  {submitResult.notificationPreview ? (
                    <div className="mt-4 rounded-[8px] border border-border-subtle bg-surface/30 p-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="text-xs font-medium text-fg">Ready notice preview</div>
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            navigator.clipboard?.writeText(
                              JSON.stringify(submitResult.notificationPreview, null, 2),
                            )
                          }
                        >
                          Copy preview JSON
                        </Button>
                      </div>
                      {submitResult.notificationPreview.links.length > 0 ? (
                        <div className="mt-3 flex flex-wrap gap-2">
                          {submitResult.notificationPreview.links.map((link) => (
                            <Button
                              key={`${link.kind}-${link.path}`}
                              type="button"
                              size="sm"
                              variant="ghost"
                              onClick={() => {
                                window.location.hash = link.path.startsWith("/") ? `#${link.path}` : link.path;
                              }}
                            >
                              <ExternalLink className="mr-1.5 h-3.5 w-3.5" />
                              {link.label}
                            </Button>
                          ))}
                        </div>
                      ) : null}
                      <pre className="mt-2 overflow-x-auto text-[11px] text-fg-muted">
                        {JSON.stringify(submitResult.notificationPreview, null, 2)}
                      </pre>
                    </div>
                  ) : null}
                </Panel>
              ) : null}
            </>
          ) : null}
        </TabsContent>

        <TabsContent value="manual" className="pt-4">
          <AgentCreateWizard
            modelOptions={modelOptions}
            defaultModel={defaultModel}
            onSubmit={(payload) => createManualMutation.mutate(payload)}
            onCancel={onCancel}
            isPending={createManualMutation.isPending}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function DraftEditor({
  draft,
  setDraft,
  skills,
  templates,
  recipes,
  providers,
  models,
  runtimeKinds,
  lifecycleClasses,
}: {
  draft: AgentBuilderDraft;
  setDraft: (draft: AgentBuilderDraft) => void;
  skills: Skill[];
  templates: PromptTemplate[];
  recipes: DurableAgentRecipe[];
  providers: StartSurfaceCapabilitiesResponse["providers"];
  models: ModelRecord[];
  runtimeKinds: string[];
  lifecycleClasses: string[];
}) {
  const roleTools = draft.profile.role_tools ?? "[]";
  const roleSkills = draft.profile.role_skills ?? "[]";

  return (
    <div className="space-y-5">
      <BuilderSection title="Identity">
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Name">
            <Input
              aria-label="Draft name"
              value={draft.profile.name}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, name: event.target.value },
                })
              }
            />
          </Field>
          <Field label="Slug">
            <Input
              aria-label="Draft slug"
              value={draft.profile.slug}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, slug: event.target.value },
                })
              }
            />
          </Field>
          <Field label="Description" className="md:col-span-2">
            <Textarea
              aria-label="Draft description"
              rows={2}
              value={draft.profile.description ?? ""}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, description: event.target.value },
                })
              }
            />
          </Field>
        </div>
      </BuilderSection>

      <BuilderSection title="Profile">
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Class">
            <SelectField
              ariaLabel="Draft class"
              value={draft.profile.class ?? "advisor"}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, class: value },
                  durable_instance: { ...draft.durable_instance, lifecycle_class: value },
                })
              }
              options={lifecycleClasses.map((value) => ({ value, label: value }))}
              fallbackOption={{ value: "advisor", label: "advisor" }}
            />
          </Field>
          <Field label="Activation mode">
            <Input
              aria-label="Draft activation mode"
              value={draft.profile.activation_mode ?? ""}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, activation_mode: event.target.value },
                })
              }
            />
          </Field>
          <Field label="Default state">
            <Input
              aria-label="Draft default state"
              value={draft.profile.default_state ?? ""}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, default_state: event.target.value },
                })
              }
            />
          </Field>
          <Field label="Default model">
            <SelectField
              ariaLabel="Draft default model"
              value={draft.profile.default_model ?? ""}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, default_model: value },
                })
              }
              options={models.map((model) => ({
                value: model.model_id,
                label: model.display_name,
              }))}
              allowEmpty
            />
          </Field>
          <Field label="System prompt" className="md:col-span-2">
            <Textarea
              aria-label="Draft system prompt"
              rows={6}
              value={draft.profile.system_prompt}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, system_prompt: event.target.value },
                })
              }
            />
          </Field>
          <Field label="Context policy" className="md:col-span-2">
            <Textarea
              aria-label="Draft context policy"
              rows={4}
              value={draft.profile.context_policy ?? "{}"}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  profile: { ...draft.profile, context_policy: event.target.value },
                })
              }
            />
          </Field>
        </div>
        <div className="mt-4 grid gap-4 md:grid-cols-2">
          <EditableStringList
            label="Role Tools"
            icon={Wrench}
            value={roleTools}
            onChange={(json) =>
              setDraft({
                ...draft,
                profile: { ...draft.profile, role_tools: json },
              })
            }
            emptyText="No role tools seeded."
          />
          <EditableStringList
            label="Role Skills"
            icon={Sparkles}
            value={roleSkills}
            onChange={(json) =>
              setDraft({
                ...draft,
                profile: { ...draft.profile, role_skills: json },
              })
            }
            emptyText="No role skills seeded."
          />
        </div>
      </BuilderSection>

      <BuilderSection title="Capabilities">
        <div className="grid gap-4 lg:grid-cols-2">
          <SelectableList
            title="Assigned Skills"
            items={skills.map((skill) => ({ id: skill.id, label: skill.name, meta: skill.slug }))}
            selected={draft.capabilities.assigned_skill_ids ?? []}
            onToggle={(values) =>
              setDraft({
                ...draft,
                capabilities: { ...draft.capabilities, assigned_skill_ids: values },
              })
            }
          />
          <SelectableList
            title="Prompt Templates"
            items={templates.map((template) => ({
              id: template.id,
              label: template.name,
              meta: template.slug,
            }))}
            selected={draft.capabilities.prompt_template_ids ?? []}
            onToggle={(values) =>
              setDraft({
                ...draft,
                capabilities: { ...draft.capabilities, prompt_template_ids: values },
              })
            }
          />
        </div>

        <div className="mt-4 grid gap-4 lg:grid-cols-2">
          <UpsertArrayEditor
            title="Known Tools"
            addLabel="Add known tool"
            rows={draft.capabilities.known_tools ?? []}
            createRow={() => ({ ...EMPTY_KNOWN_TOOL })}
            onChange={(rows) =>
              setDraft({
                ...draft,
                capabilities: { ...draft.capabilities, known_tools: rows },
              })
            }
            renderRow={(row, onRowChange) => (
              <div className="grid gap-2 md:grid-cols-2">
                <Input
                  aria-label="Known tool name"
                  value={row.tool_name ?? ""}
                  onChange={(event) => onRowChange({ ...row, tool_name: event.target.value })}
                  placeholder="mcp__docs__search"
                />
                <Input
                  aria-label="Known tool reason"
                  value={row.reason}
                  onChange={(event) => onRowChange({ ...row, reason: event.target.value })}
                  placeholder="Reason"
                />
              </div>
            )}
          />

          <UpsertArrayEditor
            title="Known Skills"
            addLabel="Add known skill"
            rows={draft.capabilities.known_skills ?? []}
            createRow={() => ({ ...EMPTY_KNOWN_SKILL })}
            onChange={(rows) =>
              setDraft({
                ...draft,
                capabilities: { ...draft.capabilities, known_skills: rows },
              })
            }
            renderRow={(row, onRowChange) => (
              <div className="grid gap-2 md:grid-cols-2">
                <Input
                  aria-label="Known skill name"
                  value={row.skill_name ?? ""}
                  onChange={(event) => onRowChange({ ...row, skill_name: event.target.value })}
                  placeholder="Planner"
                />
                <Input
                  aria-label="Known skill reason"
                  value={row.reason}
                  onChange={(event) => onRowChange({ ...row, reason: event.target.value })}
                  placeholder="Reason"
                />
              </div>
            )}
          />

          <UpsertArrayEditor
            title="Procedures"
            addLabel="Add procedure"
            rows={draft.capabilities.procedures ?? []}
            createRow={() => ({ ...EMPTY_PROCEDURE })}
            onChange={(rows) =>
              setDraft({
                ...draft,
                capabilities: { ...draft.capabilities, procedures: rows },
              })
            }
            renderRow={(row, onRowChange) => (
              <div className="grid gap-2">
                <Input
                  aria-label="Procedure name"
                  value={row.name ?? ""}
                  onChange={(event) => onRowChange({ ...row, name: event.target.value })}
                  placeholder="handoff"
                />
                <Textarea
                  aria-label="Procedure body"
                  rows={3}
                  value={row.body}
                  onChange={(event) => onRowChange({ ...row, body: event.target.value })}
                  placeholder="Procedure body"
                />
              </div>
            )}
          />

          <UpsertArrayEditor
            title="Knowledge Seeds"
            addLabel="Add knowledge seed"
            rows={draft.capabilities.knowledge_seeds ?? []}
            createRow={() => ({ ...EMPTY_SEED })}
            onChange={(rows) =>
              setDraft({
                ...draft,
                capabilities: { ...draft.capabilities, knowledge_seeds: rows },
              })
            }
            renderRow={(row, onRowChange) => (
              <div className="grid gap-2">
                <div className="grid gap-2 md:grid-cols-2">
                  <Input
                    aria-label="Knowledge seed key"
                    value={row.seed_key ?? ""}
                    onChange={(event) => onRowChange({ ...row, seed_key: event.target.value })}
                    placeholder="repo-layout"
                  />
                  <Input
                    aria-label="Knowledge seed namespace"
                    value={row.namespace}
                    onChange={(event) => onRowChange({ ...row, namespace: event.target.value })}
                    placeholder="project"
                  />
                </div>
                <Textarea
                  aria-label="Knowledge seed body"
                  rows={3}
                  value={row.body}
                  onChange={(event) => onRowChange({ ...row, body: event.target.value })}
                  placeholder="Seed body"
                />
              </div>
            )}
          />
        </div>
      </BuilderSection>

      <BuilderSection title="Boot Plan">
        <AgentBootPlanEditor
          plan={draft.boot_plan ?? createEmptyBootPlan()}
          compact
          onChange={(boot_plan) =>
            setDraft({
              ...draft,
              boot_plan,
            })
          }
        />
      </BuilderSection>

      <BuilderSection title="Durable / Launch">
        <div className="grid gap-3 rounded-[10px] border border-border-subtle bg-surface/30 p-3">
          <ToggleRow
            label="Create durable instance"
            description="Create a durable layer above the profile."
            checked={!!draft.durable_instance.create}
            onCheckedChange={(checked) =>
              setDraft({
                ...draft,
                durable_instance: { ...draft.durable_instance, create: checked },
              })
            }
          />
          <ToggleRow
            label="Launch after submit"
            description="Start the durable instance immediately after creation."
            checked={!!draft.durable_instance.start}
            onCheckedChange={(checked) =>
              setDraft({
                ...draft,
                durable_instance: { ...draft.durable_instance, start: checked },
              })
            }
          />
        </div>
        <div className="mt-4 grid gap-3 md:grid-cols-2">
          <Field label="Recipe">
            <SelectField
              ariaLabel="Draft recipe"
              value={draft.durable_instance.recipe_id ?? ""}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  durable_instance: { ...draft.durable_instance, recipe_id: value },
                })
              }
              options={recipes.map((recipe) => ({ value: recipe.id, label: recipe.name }))}
              allowEmpty
            />
          </Field>
          <Field label="Lifecycle class">
            <SelectField
              ariaLabel="Draft lifecycle class"
              value={draft.durable_instance.lifecycle_class ?? draft.profile.class ?? "advisor"}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  durable_instance: { ...draft.durable_instance, lifecycle_class: value },
                })
              }
              options={lifecycleClasses.map((value) => ({ value, label: value }))}
              fallbackOption={{ value: "advisor", label: "advisor" }}
            />
          </Field>
          <Field label="Provider">
            <SelectField
              ariaLabel="Draft provider"
              value={draft.durable_instance.provider ?? ""}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  durable_instance: { ...draft.durable_instance, provider: value },
                })
              }
              options={providers.map((provider) => ({ value: provider.id, label: provider.name }))}
              allowEmpty
            />
          </Field>
          <Field label="Model">
            <SelectField
              ariaLabel="Draft model"
              value={draft.durable_instance.model ?? ""}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  durable_instance: { ...draft.durable_instance, model: value },
                })
              }
              options={models.map((model) => ({
                value: model.model_id,
                label: model.display_name,
              }))}
              allowEmpty
            />
          </Field>
          <Field label="Runtime kind">
            <SelectField
              ariaLabel="Draft runtime kind"
              value={String(draft.durable_instance.runtime_kind ?? "api")}
              onChange={(value) =>
                setDraft({
                  ...draft,
                  durable_instance: { ...draft.durable_instance, runtime_kind: value },
                })
              }
              options={runtimeKinds.map((value) => ({ value, label: value }))}
              fallbackOption={{ value: "api", label: "api" }}
            />
          </Field>
          <Field label="Work root">
            <Input
              aria-label="Draft work root"
              value={draft.durable_instance.work_root ?? ""}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  durable_instance: { ...draft.durable_instance, work_root: event.target.value },
                })
              }
            />
          </Field>
        </div>
      </BuilderSection>

      <BuilderSection title="Notification">
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Target kind">
            <Input
              aria-label="Notification target kind"
              value={draft.operator_notification.target_kind ?? "operator"}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  operator_notification: {
                    ...draft.operator_notification,
                    target_kind: event.target.value,
                  },
                })
              }
            />
          </Field>
          <Field label="Target ID">
            <Input
              aria-label="Notification target id"
              value={draft.operator_notification.target_id ?? "current-user"}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  operator_notification: {
                    ...draft.operator_notification,
                    target_id: event.target.value,
                  },
                })
              }
            />
          </Field>
        </div>
      </BuilderSection>
    </div>
  );
}

function Panel({
  title,
  description,
  action,
  children,
}: {
  title: string;
  description: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="rounded-[12px] border border-border-subtle bg-bg-elevated">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-divider px-4 py-3">
        <div>
          <h3 className="text-[14px] font-semibold text-fg">{title}</h3>
          <p className="mt-1 text-[12px] text-fg-muted">{description}</p>
        </div>
        {action}
      </div>
      <div className="p-4">{children}</div>
    </section>
  );
}

function BuilderSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="space-y-3">
      <h4 className="text-[13px] font-semibold text-fg">{title}</h4>
      {children}
    </div>
  );
}

function Field({
  label,
  children,
  className = "",
}: {
  label: string;
  children: ReactNode;
  className?: string;
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

function ToggleRow({
  label,
  description,
  checked,
  onCheckedChange,
}: {
  label: string;
  description: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="min-w-0">
        <div className="text-sm font-medium text-fg">{label}</div>
        <div className="mt-1 text-xs text-fg-muted">{description}</div>
      </div>
      <Switch aria-label={label} checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  );
}

function SelectField({
  ariaLabel,
  value,
  onChange,
  options,
  allowEmpty = false,
  fallbackOption,
}: {
  ariaLabel: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  allowEmpty?: boolean;
  fallbackOption?: { value: string; label: string };
}) {
  const merged = [...options];
  if (fallbackOption && !merged.some((option) => option.value === fallbackOption.value)) {
    merged.unshift(fallbackOption);
  }
  return (
    <select
      aria-label={ariaLabel}
      value={value}
      onChange={(event) => onChange(event.target.value)}
      className="h-9 rounded-[8px] border border-border-subtle bg-bg px-3 text-sm text-fg"
    >
      {allowEmpty ? <option value="">Default / none</option> : null}
      {merged.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-surface/30 px-3 py-2">
      <div className="text-[11px] uppercase tracking-[0.08em] text-fg-muted">{label}</div>
      <div className="mt-1 text-sm font-medium text-fg">{value}</div>
    </div>
  );
}

function FeedbackLists({
  questions = [],
  warnings = [],
  errors = [],
  unsupported = [],
}: {
  questions?: string[];
  warnings?: string[];
  errors?: string[];
  unsupported?: string[];
}) {
  return (
    <div className="mt-3 grid gap-3 lg:grid-cols-2">
      <FeedbackCard
        title="Questions"
        icon={CircleHelp}
        items={questions}
        empty="No builder questions."
      />
      <FeedbackCard
        title="Warnings"
        icon={AlertTriangle}
        items={warnings}
        empty="No warnings."
      />
      <FeedbackCard title="Errors" icon={FileWarning} items={errors} empty="No errors." />
      <FeedbackCard
        title="Unsupported"
        icon={AlertTriangle}
        items={unsupported}
        empty="No unsupported fields."
      />
    </div>
  );
}

function FeedbackCard({
  title,
  icon: Icon,
  items,
  empty,
}: {
  title: string;
  icon: typeof CircleHelp;
  items: string[];
  empty: string;
}) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-surface/30 p-3">
      <div className="flex items-center gap-2 text-xs font-medium text-fg">
        <Icon className="h-3.5 w-3.5 text-fg-muted" />
        {title}
      </div>
      {items.length === 0 ? (
        <p className="mt-2 text-xs text-fg-muted">{empty}</p>
      ) : (
        <ul className="mt-2 space-y-1 text-xs text-fg-muted">
          {items.map((item) => (
            <li key={item} className="rounded-[6px] bg-bg/50 px-2 py-1">
              {item}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function CodeCard({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-surface/30 p-3">
      <div className="text-xs font-medium text-fg">{title}</div>
      <pre className="mt-2 max-h-72 overflow-auto rounded-[6px] bg-bg/60 p-3 text-[11px] text-fg-muted">
        {body}
      </pre>
    </div>
  );
}

function SelectableList({
  title,
  items,
  selected,
  onToggle,
}: {
  title: string;
  items: { id: string; label: string; meta?: string }[];
  selected: string[];
  onToggle: (next: string[]) => void;
}) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-surface/30 p-3">
      <div className="text-xs font-medium text-fg">{title}</div>
      <div className="mt-2 flex flex-wrap gap-2">
        {items.length === 0 ? (
          <p className="text-xs text-fg-muted">No options available.</p>
        ) : (
          items.map((item) => {
            const active = selected.includes(item.id);
            return (
              <button
                key={item.id}
                type="button"
                className={`rounded-full border px-3 py-1.5 text-xs transition-colors ${
                  active
                    ? "border-brand/40 bg-brand/15 text-fg"
                    : "border-border-subtle bg-bg text-fg-muted hover:text-fg"
                }`}
                onClick={() =>
                  onToggle(
                    active ? selected.filter((value) => value !== item.id) : [...selected, item.id],
                  )
                }
              >
                <span>{item.label}</span>
                {item.meta ? <span className="ml-1 text-[10px] opacity-70">{item.meta}</span> : null}
              </button>
            );
          })
        )}
      </div>
    </div>
  );
}

function UpsertArrayEditor<T>({
  title,
  addLabel,
  rows,
  createRow,
  onChange,
  renderRow,
}: {
  title: string;
  addLabel: string;
  rows: T[];
  createRow: () => T;
  onChange: (rows: T[]) => void;
  renderRow: (row: T, onRowChange: (next: T) => void) => ReactNode;
}) {
  return (
    <div className="rounded-[8px] border border-border-subtle bg-surface/30 p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs font-medium text-fg">{title}</div>
        <Button type="button" size="sm" variant="ghost" onClick={() => onChange([...rows, createRow()])}>
          {addLabel}
        </Button>
      </div>
      <div className="mt-2 space-y-2">
        {rows.length === 0 ? (
          <p className="text-xs text-fg-muted">None configured.</p>
        ) : (
          rows.map((row, index) => (
            <div key={index} className="rounded-[8px] border border-border-subtle bg-bg/50 p-3">
              <div className="mb-2 flex justify-end">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => onChange(rows.filter((_, rowIndex) => rowIndex !== index))}
                >
                  Remove
                </Button>
              </div>
              {renderRow(row, (next) =>
                onChange(rows.map((current, rowIndex) => (rowIndex === index ? next : current)))
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function mergeDraftWithIntake(draft: AgentBuilderDraft, intake: BuilderIntake): AgentBuilderDraft {
  return {
    ...draft,
    profile: {
      ...draft.profile,
      name: draft.profile.name || intake.name,
      slug: draft.profile.slug || intake.slug,
      description: draft.profile.description || intake.description,
      default_model: draft.profile.default_model || intake.preferredModel,
      class: draft.profile.class || intake.requestedLifecycleClass || "advisor",
    },
    durable_instance: {
      ...draft.durable_instance,
      create: intake.createDurable || !!draft.durable_instance.create,
      recipe_id: intake.recipeId || draft.durable_instance.recipe_id,
      lifecycle_class:
        draft.durable_instance.lifecycle_class || intake.requestedLifecycleClass || "advisor",
      provider: draft.durable_instance.provider || intake.preferredProvider,
      model: draft.durable_instance.model || intake.preferredModel,
      runtime_kind: draft.durable_instance.runtime_kind || intake.preferredRuntimeKind,
      work_root: draft.durable_instance.work_root || intake.workRoot,
      workspace_id: draft.durable_instance.workspace_id,
      project_id: draft.durable_instance.project_id,
      start: intake.startNow,
      metadata: draft.durable_instance.metadata ?? {},
    },
    operator_notification: {
      include_links: true,
      target_kind: "operator",
      target_id: "current-user",
      ...draft.operator_notification,
    },
    boot_plan: draft.boot_plan ?? createEmptyBootPlan(),
  };
}

function buildBlankDraftFromIntake(intake: BuilderIntake): AgentBuilderDraft {
  return {
    mode: intake.createDurable ? "create_profile_and_instance" : "create_profile",
    profile: {
      name: intake.name,
      slug: intake.slug,
      description: intake.description,
      system_prompt: "",
      role_tools: "[]",
      role_skills: "[]",
      context_policy: "{}",
      activation_mode: "singleton",
      class: intake.requestedLifecycleClass || "advisor",
      default_state: "sleeping",
      source: "user",
      default_model: intake.preferredModel,
    },
    capabilities: {
      assigned_skill_ids: [],
      prompt_template_ids: [],
      known_tools: [],
      known_skills: [],
      procedures: [],
      knowledge_seeds: [],
      reflex_suggestions: [],
    },
    boot_plan: createEmptyBootPlan(),
    durable_instance: {
      create: intake.createDurable,
      recipe_id: intake.recipeId,
      lifecycle_class: intake.requestedLifecycleClass || "advisor",
      provider: intake.preferredProvider,
      model: intake.preferredModel,
      runtime_kind: intake.preferredRuntimeKind || "api",
      work_root: intake.workRoot,
      start: intake.startNow,
      metadata: {},
    },
    operator_notification: {
      target_kind: "operator",
      target_id: "current-user",
      include_links: true,
    },
  };
}

function buildDryRunRequest(draft: AgentBuilderDraft | null): AgentBuilderDryRunRequest | null {
  if (!draft) return null;
  return {
    schema_version: 1,
    mode: draft.durable_instance.create ? "create_profile_and_instance" : "create_profile",
    profile: draft.profile,
    capabilities: draft.capabilities,
    boot_plan: draft.boot_plan,
    durable_instance: draft.durable_instance,
    operator_notification: draft.operator_notification,
  };
}

async function submitDraft({
  draft,
  dryRun,
  defaultModel,
  activeWorkspaceId,
  activeProjectId,
  skills,
}: {
  draft: AgentBuilderDraft;
  dryRun: AgentBuilderDryRunResponse;
  defaultModel: string;
  activeWorkspaceId: string | null;
  activeProjectId: string | null;
  skills: Skill[];
}) {
  const normalized = dryRun.normalized_profile_payload;
  const profile = await api.createAgentProfile(toCreateProfilePayload(normalized, defaultModel));
  await assignBuilderCapabilities(profile.id, draft.capabilities, skills, normalized);

  let durableInstance: DurableAgentInstance | null = null;
  let launchResult: DurableAgentLaunchResult | null = null;
  const bootPlan = resolveBootPlanForSubmit(profile.id, draft.boot_plan, dryRun.boot_plan_preview);
  if (bootPlan) {
    await api.updateAgentBootPlan(profile.id, bootPlan);
  }
  if (draft.durable_instance.create) {
    if (draft.durable_instance.recipe_id) {
      const recipeRequest = {
        name: emptyToUndefined(profile.name),
        slug: emptyToUndefined(profile.slug),
        profile_id: profile.id,
        provider: emptyToUndefined(draft.durable_instance.provider),
        model: emptyToUndefined(draft.durable_instance.model),
        runtime_kind: emptyToUndefined(String(draft.durable_instance.runtime_kind || "")),
        work_root: emptyToUndefined(draft.durable_instance.work_root),
        workspace_id: activeWorkspaceId ?? undefined,
        project_id: activeProjectId ?? undefined,
        metadata: draft.durable_instance.metadata,
        start: !!draft.durable_instance.start,
      };
      const applied = await api.applyDurableAgentRecipe(draft.durable_instance.recipe_id, recipeRequest);
      durableInstance = applied.instance;
      launchResult = applied.launch_result ?? null;
    } else {
      durableInstance = await api.createDurableAgent({
        name: profile.name,
        slug: profile.slug,
        profile_id: profile.id,
        lifecycle_class:
          draft.durable_instance.lifecycle_class || normalized.class || "advisor",
        provider: draft.durable_instance.provider || "anthropic",
        model: draft.durable_instance.model || normalized.default_model || defaultModel,
        runtime_kind: String(draft.durable_instance.runtime_kind || "api"),
        launch_source_type: "durable_advisor",
        launch_source_id: profile.slug || profile.id,
        work_root: draft.durable_instance.work_root || "",
        metadata_json: JSON.stringify(draft.durable_instance.metadata ?? {}),
      });
      if (draft.durable_instance.start) {
        launchResult = await api.startDurableAgent(durableInstance.id, {
          workspace_id: activeWorkspaceId ?? undefined,
          project_id: activeProjectId ?? undefined,
          wake_payload: {
            reason: "manual",
          },
        });
      }
    }
  }

  return {
    profile,
    durableInstance,
    launchResult,
  };
}

async function assignBuilderCapabilities(
  agentId: string,
  capabilities: AgentBuilderCapabilitiesInput,
  skills: Skill[],
  normalizedProfile: AgentBuilderDryRunResponse["normalized_profile_payload"],
) {
  for (const skillId of capabilities.assigned_skill_ids ?? []) {
    await api.assignSkillToAgent(agentId, { skill_id: skillId });
  }

  const slugSkillIds = new Set(
    (capabilities.assigned_skill_slugs ?? [])
      .map((slug) => skills.find((skill) => skill.slug === slug)?.id)
      .filter(Boolean) as string[],
  );
  for (const skillId of slugSkillIds) {
    if ((capabilities.assigned_skill_ids ?? []).includes(skillId)) continue;
    await api.assignSkillToAgent(agentId, { skill_id: skillId });
  }

  for (const templateId of capabilities.prompt_template_ids ?? []) {
    await api.assignTemplateToAgent(agentId, { template_id: templateId });
  }

  const roleTools = new Set(parseJSONStringArray(normalizedProfile.role_tools));
  for (const tool of capabilities.known_tools ?? []) {
    const toolName = (tool.tool_name ?? "").trim();
    if (!toolName || roleTools.has(toolName)) continue;
    await api.createAgentKnownTool(agentId, {
      ...tool,
      tool_name: toolName,
    });
  }

  const roleSkills = new Set(parseJSONStringArray(normalizedProfile.role_skills));
  for (const skill of capabilities.known_skills ?? []) {
    const skillName = (skill.skill_name ?? "").trim();
    if (!skillName || roleSkills.has(skillName)) continue;
    await api.createAgentKnownSkill(agentId, {
      ...skill,
      skill_name: skillName,
    });
  }

  for (const procedure of capabilities.procedures ?? []) {
    const name = (procedure.name ?? "").trim();
    if (!name || !procedure.body.trim()) continue;
    await api.createAgentProcedure(agentId, {
      ...procedure,
      name,
    });
  }

  for (const seed of capabilities.knowledge_seeds ?? []) {
    const seedKey = (seed.seed_key ?? "").trim();
    if (!seedKey || !seed.body.trim()) continue;
    await api.createAgentKnowledgeSeed(agentId, {
      ...seed,
      seed_key: seedKey,
      tags_json: JSON.stringify(seed.tags ?? []),
    });
  }
}

function toCreateProfilePayload(
  profile: AgentBuilderDryRunResponse["normalized_profile_payload"],
  defaultModel: string,
): CreateAgentProfileRequest {
  return {
    name: profile.name,
    slug: profile.slug,
    avatar: profile.avatar ?? "",
    icon: profile.icon ?? "",
    system_prompt: profile.system_prompt,
    description: profile.description ?? "",
    modes: "",
    default_model: profile.default_model || defaultModel,
    mcp_servers: profile.mcp_servers ?? "[]",
    tool_permissions: profile.tool_permissions ?? "{}",
    can_execute: profile.can_execute ?? true,
    settings: profile.settings ?? "{}",
    tools: profile.tools ?? "[]",
    directories: profile.directories ?? "[]",
    constraints: profile.constraints ?? "{}",
    tags: profile.tags ?? "[]",
    status: profile.status ?? "active",
    source: profile.source ?? "api",
    source_ref: profile.source_ref ?? "",
    parent_dispatch_allowlist: profile.parent_dispatch_allowlist ?? "[]",
    role_tools: profile.role_tools ?? "[]",
    role_skills: profile.role_skills ?? "[]",
    context_policy: profile.context_policy ?? "{}",
    activation_mode: profile.activation_mode ?? "singleton",
    class: profile.class ?? "advisor",
    default_state: profile.default_state ?? "sleeping",
    durable: profile.durable ?? false,
  };
}

function parseJSONStringArray(value?: string): string[] {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
  } catch {
    return [];
  }
}

function emptyToUndefined(value?: string) {
  return value && value.trim() !== "" ? value : undefined;
}

function resolveBootPlanForSubmit(
  agentId: string,
  draftBootPlan: AgentBootPlanDocument | undefined,
  preview: AgentBuilderDryRunResponse["boot_plan_preview"] | undefined,
) {
  const source = preview?.valid ? preview.normalized_plan : draftBootPlan;
  if (!source || !hasBootPlanContent(source)) return null;
  return {
    ...source,
    agent_id: agentId,
    schema_version: source.schema_version || 1,
  };
}

function buildModelOptions(models: ModelRecord[], fallback: { id: string; label: string }[]) {
  if (models.length > 0) {
    return models.map((model) => ({ value: model.model_id, label: model.display_name }));
  }
  return fallback.map((model) => ({ value: model.id, label: model.label }));
}
