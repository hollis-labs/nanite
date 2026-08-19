import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  Bot,
  Boxes,
  CirclePause,
  CirclePlay,
  ExternalLink,
  RefreshCcw,
  Square,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import {
  applyRecipeInputToRequest,
  initialRecipeInputValues,
  isStandardRecipeInput,
  type RecipeInputValueMap,
} from "@/lib/durable-agent-recipe-inputs";
import { compactProviderModelLabel } from "@/lib/sidebar-session";
import type {
  AgentSchedule,
  DurableAgentEvent,
  DurableAgentInstance,
	  DurableAgentLaunchPlan,
	  DurableAgentLaunchResult,
	  DurableAgentRecipe,
	  DurableAgentRecipeInput,
	  DurableAgentRecipePlan,
	  DurableAgentRecipeRequest,
  DurableAgentSessionAttachmentState,
  DurableAgentWakeDueItem,
} from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useLayoutStore } from "@/stores/useLayoutStore";

export function DurableAgentAdminPanel() {
  const queryClient = useQueryClient();
  const [includeArchived, setIncludeArchived] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const dueWakeQuery = useQuery({
    queryKey: ["durable-agent-wake-due"],
    queryFn: () => api.listDurableAgentDueWake(),
  });
  const runDueMutation = useMutation({
    mutationFn: () => api.runDurableAgentDueWake(false),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["durable-agent-wake-due"],
      });
      void queryClient.invalidateQueries({ queryKey: ["durable-agents"] });
    },
  });

  const agentsQuery = useQuery({
    queryKey: ["durable-agents", includeArchived],
    queryFn: () => api.listDurableAgents(includeArchived),
  });
  const agents = agentsQuery.data ?? [];
  const effectiveSelectedId = selectedId ?? agents[0]?.id ?? null;

  useEffect(() => {
    if (!selectedId && agents.length > 0) setSelectedId(agents[0].id);
    if (
      selectedId &&
      agents.length > 0 &&
      !agents.some((agent) => agent.id === selectedId)
    ) {
      setSelectedId(agents[0].id);
    }
  }, [agents, selectedId]);

  useEffect(() => {
    if (!agentsQuery.isLoading && !agentsQuery.isError && agents.length === 0) {
      setCreateOpen(true);
    }
  }, [agents.length, agentsQuery.isError, agentsQuery.isLoading]);

  const selectedAgent =
    agents.find((agent) => agent.id === effectiveSelectedId) ?? null;

  const refreshAgents = () => {
    void queryClient.invalidateQueries({ queryKey: ["durable-agents"] });
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-[18px] font-semibold text-fg">Durable Agents</h2>
          <p className="mt-1 max-w-2xl text-[13px] text-fg-muted">
            Manage durable-agent instances, lifecycle, attached sessions, and
            launch policies.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => runDueMutation.mutate()}
            disabled={runDueMutation.isPending}
          >
            <RefreshCcw className="mr-1.5 h-3.5 w-3.5" />
            Run due wake pass
          </Button>
          <Button type="button" size="sm" onClick={() => setCreateOpen(true)}>
            <Boxes className="mr-1.5 h-3.5 w-3.5" />
            New agent
          </Button>
          <label className="flex items-center gap-2 rounded-[6px] border border-border-subtle bg-bg-elevated px-3 py-2 text-[12px] text-fg-secondary">
            <input
              type="checkbox"
              checked={includeArchived}
              onChange={(event) => setIncludeArchived(event.target.checked)}
            />
            Include archived
          </label>
        </div>
      </div>

      <CreateFromRecipeCard
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(agent) => {
          setSelectedId(agent.id);
          setCreateOpen(false);
        }}
      />

      <WakeQueueCard
        items={dueWakeQuery.data ?? []}
        loading={dueWakeQuery.isLoading}
        error={dueWakeQuery.isError}
      />

      <div className="grid gap-4 lg:grid-cols-[340px_minmax(0,1fr)]">
        <section className="rounded-[8px] border border-border-subtle bg-bg-elevated">
          <div className="flex items-center justify-between border-b border-divider px-3 py-2">
            <h3 className="text-[13px] font-semibold text-fg">Agent list</h3>
            <Button variant="ghost" size="sm" onClick={refreshAgents}>
              <RefreshCcw className="mr-1.5 h-3.5 w-3.5" />
              Refresh
            </Button>
          </div>
          <div className="max-h-[620px] overflow-y-auto p-2">
            {agentsQuery.isLoading ? (
              <EmptyState>Loading durable agents...</EmptyState>
            ) : null}
            {agentsQuery.isError ? (
              <EmptyState>Could not load durable agents.</EmptyState>
            ) : null}
            {!agentsQuery.isLoading &&
            !agentsQuery.isError &&
            agents.length === 0 ? (
              <EmptyState>
                No durable agents yet. Use New agent to create one from a
                recipe.
              </EmptyState>
            ) : null}
            <div className="space-y-1">
              {agents.map((agent) => (
                <button
                  key={agent.id}
                  type="button"
                  onClick={() => setSelectedId(agent.id)}
                  className={`w-full rounded-[7px] border px-3 py-2 text-left transition-colors ${
                    effectiveSelectedId === agent.id
                      ? "border-brand/40 bg-surface text-fg"
                      : "border-transparent text-fg-secondary hover:bg-surface hover:text-fg"
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <Bot className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
                    <span className="truncate text-[13px] font-medium">
                      {agent.name || agent.slug || agent.id}
                    </span>
                    <StatusPill value={agent.status} />
                  </div>
                  <div className="mt-1 truncate font-mono text-[11px] text-fg-muted">
                    {agent.lifecycle_class} / {agent.runtime_kind}
                  </div>
                  <div className="mt-1 truncate text-[11px] text-fg-muted">
                    {compactProviderModelLabel(agent.provider, agent.model)}
                  </div>
                  <div className="mt-1 truncate font-mono text-[10px] text-fg-faint">
                    current {agent.current_session_id || "none"} / updated{" "}
                    {formatTime(agent.updated_at)}
                  </div>
                </button>
              ))}
            </div>
          </div>
        </section>

        <DurableAgentDetail agent={selectedAgent} />
      </div>
    </div>
  );
}

function DurableAgentDetail({ agent }: { agent: DurableAgentInstance | null }) {
  const queryClient = useQueryClient();
  const setActiveSession = useAppStore((state) => state.setActiveSession);
  const activeProjectId = useAppStore((state) => state.activeProjectId);
  const setCurrentPage = useLayoutStore((state) => state.setCurrentPage);
  const agentID = agent?.id ?? "";

  const detailQuery = useQuery({
    queryKey: ["durable-agent", agentID],
    queryFn: () => api.getDurableAgent(agentID),
    enabled: agentID !== "",
  });
  const sessionsQuery = useQuery({
    queryKey: ["durable-agent-sessions", agentID],
    queryFn: () => api.listDurableAgentSessions(agentID),
    enabled: agentID !== "",
  });
  const launchPlanQuery = useQuery({
    queryKey: ["durable-agent-launch-plan", agentID],
    queryFn: () => api.getDurableAgentLaunchPlan(agentID),
    enabled: agentID !== "",
  });
  const eventsQuery = useQuery({
    queryKey: ["durable-agent-events", agentID],
    queryFn: () => api.listDurableAgentEvents(agentID),
    enabled: agentID !== "",
  });
  const schedulesQuery = useQuery({
    queryKey: ["durable-agent-schedules", agentID],
    queryFn: () => api.listDurableAgentSchedules(agentID),
    enabled: agentID !== "",
  });

  const current = detailQuery.data ?? agent;
  const currentSessionState =
    sessionsQuery.data?.find(
      (session) => session.session_id === current?.current_session_id,
    ) ?? null;

  const openSession = (sessionId?: string) => {
    if (!sessionId) return;
    setActiveSession(sessionId);
    setCurrentPage("chat");
    window.location.hash = "#chat";
  };

  const onLifecycleSuccess = (
    result: DurableAgentInstance | DurableAgentLaunchResult,
  ) => {
    void queryClient.invalidateQueries({ queryKey: ["sessions"] });
    void queryClient.invalidateQueries({ queryKey: ["durable-agents"] });
    void queryClient.invalidateQueries({
      queryKey: ["durable-agent", agentID],
    });
    void queryClient.invalidateQueries({
      queryKey: ["durable-agent-sessions", agentID],
    });
    void queryClient.invalidateQueries({
      queryKey: ["durable-agent-events", agentID],
    });
    if ("session" in result && result.session?.id) {
      void queryClient.invalidateQueries({
        queryKey: ["session", result.session.id],
      });
      openSession(result.session.id);
    }
  };

  const startMutation = useMutation({
    mutationFn: () =>
      api.startDurableAgent(agentID, {
        project_id: activeProjectId ?? undefined,
        wake_payload: { reason: "manual" },
      }),
    onSuccess: onLifecycleSuccess,
  });
  const resumeMutation = useMutation({
    mutationFn: () =>
      api.resumeDurableAgent(agentID, {
        project_id: activeProjectId ?? undefined,
        wake_payload: { reason: "lifecycle_resume" },
      }),
    onSuccess: onLifecycleSuccess,
  });
  const pauseMutation = useMutation({
    mutationFn: () => api.requestDurableAgentPause(agentID),
    onSuccess: onLifecycleSuccess,
  });
  const stopMutation = useMutation({
    mutationFn: () => api.requestDurableAgentStop(agentID),
    onSuccess: onLifecycleSuccess,
  });
  const archiveMutation = useMutation({
    mutationFn: () => api.archiveDurableAgent(agentID),
    onSuccess: onLifecycleSuccess,
  });
  const wakeMutation = useMutation({
    mutationFn: () => api.wakeDurableAgent(agentID, {}),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["durable-agents"] });
      void queryClient.invalidateQueries({
        queryKey: ["durable-agent", agentID],
      });
      void queryClient.invalidateQueries({
        queryKey: ["durable-agent-sessions", agentID],
      });
      void queryClient.invalidateQueries({
        queryKey: ["durable-agent-events", agentID],
      });
      void queryClient.invalidateQueries({
        queryKey: ["durable-agent-wake-due"],
      });
      void queryClient.invalidateQueries({
        queryKey: ["durable-agent-schedules", agentID],
      });
    },
  });

  if (!current) {
    return (
      <section className="rounded-[8px] border border-border-subtle bg-bg-elevated p-8">
        <EmptyState>Select a durable agent to inspect it.</EmptyState>
      </section>
    );
  }

  const lifecycleBusy =
    startMutation.isPending ||
    resumeMutation.isPending ||
    pauseMutation.isPending ||
    stopMutation.isPending ||
    archiveMutation.isPending ||
    wakeMutation.isPending;
  const lifecycleError =
    getErrorMessage(startMutation.error) ??
    getErrorMessage(resumeMutation.error) ??
    getErrorMessage(pauseMutation.error) ??
    getErrorMessage(stopMutation.error) ??
    getErrorMessage(archiveMutation.error) ??
    getErrorMessage(wakeMutation.error);

  return (
    <section className="min-w-0 rounded-[8px] border border-border-subtle bg-bg-elevated">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-divider px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-[15px] font-semibold text-fg">
              {current.name || current.slug || current.id}
            </h3>
            <StatusPill value={current.status} />
          </div>
          <p className="mt-1 truncate font-mono text-[11px] text-fg-muted">
            {current.id}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            onClick={() => startMutation.mutate()}
            disabled={lifecycleBusy}
            aria-label="Start durable agent"
          >
            <CirclePlay className="mr-1.5 h-3.5 w-3.5" />
            Start
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => resumeMutation.mutate()}
            disabled={lifecycleBusy}
            aria-label="Resume durable agent"
          >
            <CirclePlay className="mr-1.5 h-3.5 w-3.5" />
            Resume
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => pauseMutation.mutate()}
            disabled={lifecycleBusy}
            aria-label="Request pause durable agent"
          >
            <CirclePause className="mr-1.5 h-3.5 w-3.5" />
            Pause
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => stopMutation.mutate()}
            disabled={lifecycleBusy}
            aria-label="Request stop durable agent"
          >
            <Square className="mr-1.5 h-3.5 w-3.5" />
            Stop
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={() => archiveMutation.mutate()}
            disabled={lifecycleBusy}
            aria-label="Archive durable agent"
          >
            <Archive className="mr-1.5 h-3.5 w-3.5" />
            Archive
          </Button>
          {(current.lifecycle_class === "process" ||
            current.lifecycle_class === "template") && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => wakeMutation.mutate()}
              disabled={lifecycleBusy}
              aria-label="Wake durable agent now"
            >
              <CirclePlay className="mr-1.5 h-3.5 w-3.5" />
              Wake now
            </Button>
          )}
        </div>
      </div>
      {lifecycleError ? <InlineError message={lifecycleError} /> : null}

      <div className="space-y-4 p-4">
        <InfoSection title="Overview">
          <FactGrid>
            <Fact label="Name" value={current.name} />
            <Fact label="Slug" value={current.slug} mono />
            <Fact label="Profile" value={current.profile_id} mono />
            <Fact label="Lifecycle" value={current.lifecycle_class} mono />
            <Fact label="Status" value={current.status} mono />
            <Fact
              label="Provider / model"
              value={compactProviderModelLabel(current.provider, current.model)}
            />
            <Fact label="Runtime kind" value={current.runtime_kind} mono />
            <Fact
              label="Launch source"
              value={current.launch_source_type}
              mono
            />
            <Fact
              label="Launch source ID"
              value={current.launch_source_id}
              mono
            />
            <Fact
              label="Current session"
              value={current.current_session_id}
              mono
            />
            <Fact
              label="Current runtime"
              value={currentSessionState?.runtime_state || "none"}
              mono
            />
            <Fact
              label="Current halt"
              value={currentSessionState?.halted_reason || "none"}
              mono
            />
            <Fact label="Work root" value={current.work_root} mono wide />
            {current.failure_reason ? (
              <Fact
                label="Failure reason"
                value={current.failure_reason}
                wide
              />
            ) : null}
            {currentSessionState?.runtime_failure_reason ? (
              <Fact
                label="Runtime failure"
                value={currentSessionState.runtime_failure_reason}
                wide
              />
            ) : null}
          </FactGrid>
        </InfoSection>

        <InfoSection title="Activity">
          <DurableAgentActivity
            events={eventsQuery.data ?? []}
            loading={eventsQuery.isLoading}
            error={eventsQuery.isError}
            refreshing={eventsQuery.isFetching && !eventsQuery.isLoading}
            onRefresh={() =>
              void queryClient.invalidateQueries({
                queryKey: ["durable-agent-events", agentID],
              })
            }
            onOpenSession={openSession}
          />
        </InfoSection>

        <InfoSection title="Sessions">
          <AttachedSessions
            sessions={sessionsQuery.data ?? []}
            loading={sessionsQuery.isLoading}
            error={sessionsQuery.isError}
            onOpenSession={openSession}
          />
        </InfoSection>

        {(current.lifecycle_class === "process" ||
          current.lifecycle_class === "template") && (
          <InfoSection title="Schedules">
            <SchedulesView
              schedules={schedulesQuery.data ?? []}
              loading={schedulesQuery.isLoading}
              error={schedulesQuery.isError}
            />
          </InfoSection>
        )}

        <InfoSection title="Launch policy">
          <LaunchPlanView
            plan={launchPlanQuery.data}
            loading={launchPlanQuery.isLoading}
            error={launchPlanQuery.isError}
          />
        </InfoSection>
      </div>
    </section>
  );
}

function WakeQueueCard({
  items,
  loading,
  error,
}: {
  items: DurableAgentWakeDueItem[];
  loading: boolean;
  error: boolean;
}) {
  if (loading) return <EmptyState>Loading due wake queue...</EmptyState>;
  if (error) return <EmptyState>Could not load due wake queue.</EmptyState>;
  return (
    <section className="rounded-[8px] border border-border-subtle bg-bg-elevated p-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h3 className="text-[13px] font-semibold text-fg">Due wake queue</h3>
          <p className="mt-1 text-[12px] text-fg-muted">
            {items.length === 0
              ? "No due scheduled wakes right now."
              : `${items.length} wake item${items.length === 1 ? "" : "s"} ready or awaiting scope.`}
          </p>
        </div>
      </div>
    </section>
  );
}

function CreateFromRecipeCard({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (agent: DurableAgentInstance) => void;
}) {
  const queryClient = useQueryClient();
  const setActiveSession = useAppStore((state) => state.setActiveSession);
  const setCurrentPage = useLayoutStore((state) => state.setCurrentPage);
  const activeProjectId = useAppStore((state) => state.activeProjectId);
  const [recipeId, setRecipeId] = useState("");
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [profileId, setProfileId] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [runtimeKind, setRuntimeKind] = useState("");
  const [workRoot, setWorkRoot] = useState("");
  const [wakePrompt, setWakePrompt] = useState("");
  const [start, setStart] = useState(true);
  const [plan, setPlan] = useState<DurableAgentRecipePlan | null>(null);
  const [inputValues, setInputValues] = useState<RecipeInputValueMap>({});

  const recipesQuery = useQuery({
    queryKey: ["durable-agent-recipes"],
    queryFn: api.listDurableAgentRecipes,
  });
  const profilesQuery = useQuery({
    queryKey: ["agents"],
    queryFn: () => api.listAgents(),
  });
  const recipes = recipesQuery.data ?? [];
  const selectedRecipe =
    recipes.find((recipe) => recipe.id === recipeId) ?? null;

  useEffect(() => {
    if (!recipeId && recipes.length > 0) setRecipeId(recipes[0].id);
  }, [recipeId, recipes]);

  useEffect(() => {
    if (!selectedRecipe) return;
    setInputValues(initialRecipeInputValues(selectedRecipe));
    if (!profileId && selectedRecipe.profile_id)
      setProfileId(selectedRecipe.profile_id);
    if (!provider) setProvider(selectedRecipe.provider);
    if (!model) setModel(selectedRecipe.model);
    if (!runtimeKind) setRuntimeKind(String(selectedRecipe.runtime_kind));
    if (!workRoot) setWorkRoot(selectedRecipe.work_root ?? "");
  }, [selectedRecipe, profileId, provider, model, runtimeKind, workRoot]);

  const request = useMemo<DurableAgentRecipeRequest>(() => {
    const wakePayload =
      wakePrompt.trim() || selectedRecipe?.wake_defaults
        ? {
            ...(selectedRecipe?.wake_defaults ?? { reason: "manual" }),
            ...(wakePrompt.trim() ? { prompt: wakePrompt.trim() } : {}),
          }
        : undefined;
    const next: DurableAgentRecipeRequest = {
      name: name.trim() || undefined,
      slug: slug.trim() || undefined,
      profile_id: profileId || undefined,
      provider: provider || undefined,
      model: model || undefined,
      runtime_kind: runtimeKind || undefined,
      work_root: workRoot.trim() || undefined,
      project_id: activeProjectId ?? undefined,
      wake_payload: wakePayload,
      start,
    };
    for (const input of selectedRecipe?.inputs ?? []) {
      next &&
        applyRecipeInputToRequest(next, input, inputValues[input.id]);
    }
    return next;
  }, [
    activeProjectId,
    inputValues,
    model,
    name,
    profileId,
    provider,
    runtimeKind,
    selectedRecipe?.wake_defaults,
    slug,
    start,
    wakePrompt,
    workRoot,
  ]);

  const dryRunMutation = useMutation({
    mutationFn: () => api.dryRunDurableAgentRecipe(recipeId, request),
    onSuccess: setPlan,
  });
  const applyMutation = useMutation({
    mutationFn: () => api.applyDurableAgentRecipe(recipeId, request),
    onSuccess: (result) => {
      setPlan(result.plan);
      onCreated(result.instance);
      void queryClient.invalidateQueries({ queryKey: ["durable-agents"] });
      if (result.launch_result?.session?.id) {
        setActiveSession(result.launch_result.session.id);
        setCurrentPage("chat");
        window.location.hash = "#chat";
      }
    },
  });

  return (
    <section className="rounded-[8px] border border-border-subtle bg-bg-elevated">
      <button
        type="button"
        onClick={() => onOpenChange(!open)}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
      >
        <span>
          <span className="block text-[14px] font-semibold text-fg">
            Create from recipe
          </span>
          <span className="mt-1 block text-[12px] text-fg-muted">
            Compile a recipe into a durable-agent instance, then optionally
            start it.
          </span>
        </span>
        <Boxes className="h-4 w-4 text-fg-muted" />
      </button>

      {open ? (
        <div className="grid gap-4 border-t border-divider p-4 lg:grid-cols-[minmax(0,1fr)_320px]">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Recipe" htmlFor="durable-recipe-id">
              <select
                id="durable-recipe-id"
                aria-label="Recipe"
                value={recipeId}
                disabled={recipes.length === 0 || recipesQuery.isLoading}
                onChange={(event) => {
                  setRecipeId(event.target.value);
                  setPlan(null);
                }}
                className="h-9 w-full rounded-[6px] border border-border-subtle bg-bg px-2 text-[13px] text-fg"
              >
                {recipes.length === 0 ? (
                  <option value="">No recipes available</option>
                ) : null}
                {recipes.map((recipe) => (
                  <option key={recipe.id} value={recipe.id}>
                    {recipe.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Profile" htmlFor="durable-profile-id">
              <select
                id="durable-profile-id"
                aria-label="Profile"
                value={profileId}
                onChange={(event) => setProfileId(event.target.value)}
                className="h-9 w-full rounded-[6px] border border-border-subtle bg-bg px-2 text-[13px] text-fg"
              >
                <option value="">Recipe default</option>
                {(profilesQuery.data ?? []).map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name || profile.slug || profile.id}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Name" htmlFor="durable-name">
              <Input
                id="durable-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </Field>
            <Field label="Slug" htmlFor="durable-slug">
              <Input
                id="durable-slug"
                value={slug}
                onChange={(event) => setSlug(event.target.value)}
              />
            </Field>
            <Field label="Provider" htmlFor="durable-provider">
              <Input
                id="durable-provider"
                value={provider}
                onChange={(event) => setProvider(event.target.value)}
              />
            </Field>
            <Field label="Model" htmlFor="durable-model">
              <Input
                id="durable-model"
                value={model}
                onChange={(event) => setModel(event.target.value)}
              />
            </Field>
            <Field label="Runtime kind" htmlFor="durable-runtime-kind">
              <Input
                id="durable-runtime-kind"
                value={runtimeKind}
                onChange={(event) => setRuntimeKind(event.target.value)}
              />
            </Field>
            <Field label="Work root" htmlFor="durable-work-root">
              <Input
                id="durable-work-root"
                value={workRoot}
                onChange={(event) => setWorkRoot(event.target.value)}
              />
            </Field>
            <Field label="Wake prompt" htmlFor="durable-wake-prompt">
              <Textarea
                id="durable-wake-prompt"
                value={wakePrompt}
                onChange={(event) => setWakePrompt(event.target.value)}
                className="min-h-20"
              />
            </Field>
            {(selectedRecipe?.inputs ?? [])
              .filter((input) => !isStandardRecipeInput(input))
              .map((input) => (
                <RecipeInputField
                  key={input.id}
                  input={input}
                  value={inputValues[input.id]}
                  onChange={(value) =>
                    setInputValues((current) => ({ ...current, [input.id]: value }))
                  }
                />
              ))}
            <div className="flex items-end">
              <label className="flex items-center gap-2 text-[13px] text-fg-secondary">
                <input
                  type="checkbox"
                  checked={start}
                  onChange={(event) => setStart(event.target.checked)}
                />
                Start after apply
              </label>
            </div>
            <div className="flex gap-2 sm:col-span-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => dryRunMutation.mutate()}
                disabled={
                  !recipeId ||
                  dryRunMutation.isPending ||
                  recipesQuery.isLoading
                }
              >
                Dry run
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={() => applyMutation.mutate()}
                disabled={
                  !recipeId ||
                  applyMutation.isPending ||
                  (plan ? !plan.ready : false)
                }
              >
                Apply
              </Button>
            </div>
          </div>

          <RecipePreview
            recipe={selectedRecipe}
            plan={plan}
            loading={dryRunMutation.isPending || applyMutation.isPending}
            error={dryRunMutation.isError || applyMutation.isError}
          />
        </div>
      ) : null}
    </section>
  );
}

function AttachedSessions({
  sessions,
  loading,
  error,
  onOpenSession,
}: {
  sessions: DurableAgentSessionAttachmentState[];
  loading: boolean;
  error: boolean;
  onOpenSession: (sessionId: string) => void;
}) {
  if (loading) return <EmptyState>Loading attached sessions...</EmptyState>;
  if (error) return <EmptyState>Could not load attached sessions.</EmptyState>;
  if (sessions.length === 0)
    return <EmptyState>No sessions are attached.</EmptyState>;

  return (
    <div className="overflow-hidden rounded-[7px] border border-border-subtle">
      <table className="w-full text-left text-[12px]">
        <thead className="bg-surface text-fg-muted">
          <tr>
            <th className="px-3 py-2 font-medium">Relation</th>
            <th className="px-3 py-2 font-medium">Session</th>
            <th className="px-3 py-2 font-medium">Runtime</th>
            <th className="px-3 py-2 font-medium">Failure / halt</th>
            <th className="px-3 py-2 font-medium">Provider / model</th>
            <th className="px-3 py-2 font-medium" />
          </tr>
        </thead>
        <tbody>
          {sessions.map((session) => (
            <tr
              key={`${session.session_id}:${session.relation}`}
              className="border-t border-divider"
            >
              <td className="px-3 py-2 font-mono">{session.relation}</td>
              <td className="px-3 py-2 font-mono">{session.session_id}</td>
              <td className="px-3 py-2 font-mono">
                {session.runtime_state || "none"}
              </td>
              <td className="px-3 py-2 text-[11px] text-fg-muted">
                {session.runtime_failure_reason ||
                  session.halted_reason ||
                  "None"}
              </td>
              <td className="px-3 py-2">
                {compactProviderModelLabel(session.provider, session.model)}
              </td>
              <td className="px-3 py-2 text-right">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => onOpenSession(session.session_id)}
                >
                  <ExternalLink className="mr-1.5 h-3.5 w-3.5" />
                  Open
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SchedulesView({
  schedules,
  loading,
  error,
}: {
  schedules: AgentSchedule[];
  loading: boolean;
  error: boolean;
}) {
  if (loading) return <EmptyState>Loading schedules...</EmptyState>;
  if (error) return <EmptyState>Could not load schedules.</EmptyState>;
  if (schedules.length === 0)
    return <EmptyState>No schedules configured.</EmptyState>;

  return (
    <div className="overflow-hidden rounded-[7px] border border-border-subtle">
      <table className="w-full text-left text-[12px]">
        <thead className="bg-surface text-fg-muted">
          <tr>
            <th className="px-3 py-2 font-medium">Name</th>
            <th className="px-3 py-2 font-medium">Kind</th>
            <th className="px-3 py-2 font-medium">Spec</th>
            <th className="px-3 py-2 font-medium">Status</th>
            <th className="px-3 py-2 font-medium">Last fired</th>
            <th className="px-3 py-2 font-medium">Count</th>
          </tr>
        </thead>
        <tbody>
          {schedules.map((schedule) => (
            <tr key={schedule.id} className="border-t border-divider">
              <td className="px-3 py-2">{schedule.name}</td>
              <td className="px-3 py-2 font-mono">{schedule.schedule_kind}</td>
              <td className="px-3 py-2 font-mono">
                {schedule.schedule_spec || "one-shot"}
              </td>
              <td className="px-3 py-2 font-mono">{schedule.status}</td>
              <td className="px-3 py-2 font-mono">
                {schedule.last_fired_at || "never"}
              </td>
              <td className="px-3 py-2 font-mono">{schedule.fired_count}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function DurableAgentActivity({
  events,
  loading,
  error,
  refreshing,
  onRefresh,
  onOpenSession,
}: {
  events: DurableAgentEvent[];
  loading: boolean;
  error: boolean;
  refreshing: boolean;
  onRefresh: () => void;
  onOpenSession: (sessionId: string) => void;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3">
        <p className="text-[12px] text-fg-muted">
          Newest lifecycle events first.
        </p>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={onRefresh}
          disabled={loading || refreshing}
        >
          <RefreshCcw
            className={`mr-1.5 h-3.5 w-3.5 ${refreshing ? "animate-spin" : ""}`}
          />
          Refresh
        </Button>
      </div>
      {loading ? <EmptyState>Loading activity...</EmptyState> : null}
      {error ? <EmptyState>Could not load activity.</EmptyState> : null}
      {!loading && !error && events.length === 0 ? (
        <EmptyState>No durable-agent activity yet.</EmptyState>
      ) : null}
      {!loading && !error && events.length > 0 ? (
        <div className="divide-y divide-divider overflow-hidden rounded-[7px] border border-border-subtle">
          {events.map((event) => (
            <DurableAgentActivityItem
              key={event.id}
              event={event}
              onOpenSession={onOpenSession}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function DurableAgentActivityItem({
  event,
  onOpenSession,
}: {
  event: DurableAgentEvent;
  onOpenSession: (sessionId: string) => void;
}) {
  const metadata = parseMetadata(event.metadata_json);
  const selectedMetadata = selectedActivityMetadata(metadata);
  const tone =
    event.event_type.endsWith("_failed") || event.status_after === "failed"
      ? "border-danger/30 bg-danger/10 text-danger"
      : event.event_type.endsWith("_succeeded") ||
          event.event_type === "session_attached"
        ? "border-success/30 bg-success/10 text-success"
        : "border-warning/30 bg-warning/10 text-warning";

  return (
    <div className="grid gap-2 bg-bg px-3 py-2 text-[12px] md:grid-cols-[minmax(0,1fr)_auto]">
      <div className="min-w-0 space-y-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span
            className={`rounded-full border px-2 py-0.5 font-mono text-[10px] ${tone}`}
          >
            {formatEventType(event.event_type)}
          </span>
          <span className="font-mono text-[11px] text-fg-muted">
            {formatTime(event.created_at)}
          </span>
          <span className="rounded-full border border-border-subtle bg-surface px-2 py-0.5 font-mono text-[10px] text-fg-muted">
            {event.source || "unknown"}
          </span>
        </div>
        {formatTransition(event) ? (
          <div className="font-mono text-[11px] text-fg-muted">
            {formatTransition(event)}
          </div>
        ) : null}
        {event.message ? (
          <div className="break-words text-fg">{event.message}</div>
        ) : null}
        {metadata.failure_reason ? (
          <div className="break-words text-danger">
            Failure: {String(metadata.failure_reason)}
          </div>
        ) : null}
        {selectedMetadata.length > 0 ? (
          <div className="flex flex-wrap gap-1.5">
            {selectedMetadata.map(([key, value]) => (
              <span
                key={key}
                className="max-w-full break-words rounded-[6px] border border-border-subtle bg-bg-elevated px-2 py-0.5 font-mono text-[10px] text-fg-muted"
              >
                {key}: {String(value)}
              </span>
            ))}
          </div>
        ) : null}
      </div>
      {event.session_id ? (
        <div className="flex min-w-0 items-start gap-2 md:justify-end">
          <span className="min-w-0 break-all font-mono text-[11px] text-fg-muted">
            {event.session_id}
          </span>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-label={`Open activity session ${event.session_id}`}
            onClick={() => onOpenSession(event.session_id)}
          >
            <ExternalLink className="mr-1.5 h-3.5 w-3.5" />
            Open
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function LaunchPlanView({
  plan,
  loading,
  error,
}: {
  plan?: DurableAgentLaunchPlan;
  loading: boolean;
  error: boolean;
}) {
  if (loading) return <EmptyState>Loading launch plan...</EmptyState>;
  if (error) return <EmptyState>Launch plan is unavailable.</EmptyState>;
  if (!plan) return <EmptyState>No launch plan returned.</EmptyState>;

  return (
    <FactGrid>
      <Fact label="Session policy" value={plan.session_policy} mono />
      <Fact label="Attachment relation" value={plan.attachment_relation} mono />
      <Fact label="Lifecycle" value={plan.lifecycle_class} mono />
      <Fact label="Launch source" value={plan.launch_source_type} mono />
      <Fact label="Runtime kind" value={plan.runtime_kind} mono />
      <Fact
        label="Provider / model"
        value={compactProviderModelLabel(plan.provider, plan.model)}
      />
      <Fact label="Work root" value={plan.work_root} mono wide />
      <Fact label="Wake reason" value={plan.wake_payload?.reason} mono />
      <Fact label="Wake prompt" value={plan.wake_payload?.prompt} wide />
    </FactGrid>
  );
}

function RecipePreview({
  recipe,
  plan,
  loading,
  error,
}: {
  recipe: DurableAgentRecipe | null;
  plan: DurableAgentRecipePlan | null;
  loading: boolean;
  error: boolean;
}) {
  return (
    <div className="rounded-[7px] border border-border-subtle bg-bg p-3">
      <h4 className="text-[13px] font-semibold text-fg">Preview</h4>
      {loading ? <EmptyState>Compiling recipe...</EmptyState> : null}
      {error ? <EmptyState>Recipe request failed.</EmptyState> : null}
      {!loading && !error && !plan && recipe ? (
        <div className="mt-3 space-y-2 text-[12px] text-fg-muted">
          <div className="font-medium text-fg">{recipe.name}</div>
          <div>{recipe.description}</div>
          <div className="font-mono text-[11px]">
            {recipe.kind} / {recipe.lifecycle_class} / {recipe.runtime_kind}
          </div>
        </div>
      ) : null}
      {plan ? (
        <div className="mt-3 space-y-2 text-[12px]">
          <div className={plan.ready ? "text-success" : "text-warning"}>
            {plan.ready ? "Ready to apply" : "Needs attention"}
          </div>
          <div className="font-mono text-[11px] text-fg-muted">
            {plan.session_policy} / {plan.launch_policy.runtime_kind}
          </div>
          {plan.missing_requirements?.length ? (
            <div className="text-warning">
              Missing: {plan.missing_requirements.join(", ")}
            </div>
          ) : null}
          {plan.unsupported?.length ? (
            <div className="text-danger">
              Unsupported: {plan.unsupported.join(", ")}
            </div>
          ) : null}
          <div className="rounded-[6px] border border-border-subtle bg-bg-elevated px-2 py-1 font-mono text-[11px] text-fg-muted">
            {plan.instance.name || plan.instance.slug || plan.instance.id}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function InfoSection({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <div>
      <h4 className="mb-2 text-[13px] font-semibold text-fg">{title}</h4>
      {children}
    </div>
  );
}

function FactGrid({ children }: { children: ReactNode }) {
  return <dl className="grid gap-2 md:grid-cols-2">{children}</dl>;
}

function Fact({
  label,
  value,
  mono = false,
  wide = false,
}: {
  label: string;
  value?: string | null;
  mono?: boolean;
  wide?: boolean;
}) {
  return (
    <div className={wide ? "md:col-span-2" : undefined}>
      <dt className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-fg-muted">
        {label}
      </dt>
      <dd
        className={`mt-1 min-h-7 break-words rounded-[6px] border border-border-subtle bg-bg px-2 py-1.5 text-[12px] text-fg ${
          mono ? "font-mono" : ""
        }`}
      >
        {value || "None"}
      </dd>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor?: string;
  children: ReactNode;
}) {
  return (
    <div className="block space-y-1.5">
      <label
        htmlFor={htmlFor}
        className="block font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-fg-muted"
      >
        {label}
      </label>
      {children}
    </div>
  );
}

function RecipeInputField({
  input,
  value,
  onChange,
}: {
  input: DurableAgentRecipeInput;
  value: string | boolean | undefined;
  onChange: (value: string | boolean) => void;
}) {
  const help = input.help || input.description;
  const textValue =
    typeof value === "boolean"
      ? value
        ? "true"
        : "false"
      : String(value ?? "");

  let control: ReactNode;
  if (input.type === "textarea") {
    control = (
      <Textarea
        value={textValue}
        onChange={(event) => onChange(event.target.value)}
        className="min-h-20"
        placeholder={input.placeholder}
      />
    );
  } else if (input.type === "select") {
    control = (
      <select
        value={textValue}
        onChange={(event) => onChange(event.target.value)}
        className="h-9 w-full rounded-[6px] border border-border-subtle bg-bg px-2 text-[13px] text-fg"
      >
        <option value="">{input.placeholder || "Select..."}</option>
        {(input.options ?? []).map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    );
  } else if (input.type === "boolean") {
    control = (
      <label className="flex h-9 items-center gap-2 text-[13px] text-fg-secondary">
        <input
          type="checkbox"
          checked={Boolean(value)}
          onChange={(event) => onChange(event.target.checked)}
        />
        Enabled
      </label>
    );
  } else {
    control = (
      <Input
        value={textValue}
        onChange={(event) => onChange(event.target.value)}
        placeholder={input.placeholder}
      />
    );
  }

  return (
    <Field
      label={`${input.label}${input.required ? " *" : ""}`}
      htmlFor={undefined}
    >
      {control}
      {help ? <p className="text-[11px] text-fg-muted">{help}</p> : null}
    </Field>
  );
}

function StatusPill({ value }: { value: string }) {
  const className =
    value === "active"
      ? "border-success/30 bg-success/10 text-success"
      : value === "failed"
        ? "border-danger/30 bg-danger/10 text-danger"
        : value === "archived"
          ? "border-border-subtle bg-surface text-fg-muted"
          : "border-warning/30 bg-warning/10 text-warning";
  return (
    <span
      className={`ml-auto rounded-full border px-2 py-0.5 font-mono text-[10px] ${className}`}
    >
      {value || "unknown"}
    </span>
  );
}

function formatEventType(value: string): string {
  return value
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function formatTransition(event: DurableAgentEvent): string {
  if (!event.status_before && !event.status_after) return "";
  if (event.status_before && event.status_after) {
    return `${event.status_before} -> ${event.status_after}`;
  }
  return event.status_after || event.status_before;
}

function parseMetadata(raw: string): Record<string, unknown> {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw) as unknown;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}

function selectedActivityMetadata(
  metadata: Record<string, unknown>,
): Array<[string, unknown]> {
  return ["relation", "session_reused"]
    .filter((key) => key in metadata && metadata[key] !== "")
    .map((key) => [key, metadata[key]]);
}

function EmptyState({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-[7px] border border-dashed border-border-subtle bg-bg px-3 py-6 text-center text-[12px] text-fg-muted">
      {children}
    </div>
  );
}

function InlineError({ message }: { message: string }) {
  return (
    <div
      role="alert"
      className="mx-4 mt-4 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-2 text-[12px] text-danger"
    >
      {message}
    </div>
  );
}

function getErrorMessage(error: unknown): string | null {
  if (!error) return null;
  if (error instanceof Error && error.message.trim() !== "") {
    return error.message;
  }
  return "Request failed.";
}

function formatTime(value: string): string {
  if (!value) return "unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}
