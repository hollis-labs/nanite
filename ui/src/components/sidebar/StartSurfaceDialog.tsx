import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, MessageSquare, Play, Wand2 } from "lucide-react";
import type React from "react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import {
  applyRecipeInputToRequest,
  initialRecipeInputValues,
  isStandardRecipeInput,
  type RecipeInputValueMap,
} from "@/lib/durable-agent-recipe-inputs";
import type {
  DurableAgentRecipeInput,
  DurableAgentRecipePlan,
  ModelRecord,
  ProviderConfig,
  StartSurfaceCapabilitiesResponse,
  StartSurfacePrefill,
} from "@/lib/types";
import { cn } from "@/lib/utils";

type StartPath = "chat" | "durable" | "recipe";
type DurableAction = "start" | "resume";

interface StartSurfaceDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  projectId: string | null;
  defaultProvider?: string;
  defaultModel?: string;
  defaultAgent?: string;
  prefill?: StartSurfacePrefill | null;
  onSessionStarted: (sessionId: string) => void;
}

const PATHS: Array<{ id: StartPath; label: string; icon: typeof MessageSquare }> = [
  { id: "chat", label: "Chat", icon: MessageSquare },
  { id: "durable", label: "Durable", icon: Bot },
  { id: "recipe", label: "Recipe", icon: Wand2 },
];

export function StartSurfaceDialog({
  open,
  onOpenChange,
  projectId,
  defaultProvider,
  defaultModel,
  defaultAgent,
  prefill,
  onSessionStarted,
}: StartSurfaceDialogProps) {
  const queryClient = useQueryClient();
  const [path, setPath] = useState<StartPath>("chat");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [agentId, setAgentId] = useState("");
  const [durableAgentId, setDurableAgentId] = useState("");
  const [durableAction, setDurableAction] = useState<DurableAction>("start");
  const [durablePrompt, setDurablePrompt] = useState("");
  const [recipeId, setRecipeId] = useState("");
  const [recipeName, setRecipeName] = useState("");
  const [recipeSlug, setRecipeSlug] = useState("");
  const [recipeProfileId, setRecipeProfileId] = useState("");
  const [recipeProvider, setRecipeProvider] = useState("");
  const [recipeModel, setRecipeModel] = useState("");
  const [recipeRuntimeKind, setRecipeRuntimeKind] = useState("");
  const [recipeWorkRoot, setRecipeWorkRoot] = useState("");
  const [recipeWakePrompt, setRecipeWakePrompt] = useState("");
  const [recipePlan, setRecipePlan] = useState<DurableAgentRecipePlan | null>(null);
  const [recipeInputValues, setRecipeInputValues] = useState<RecipeInputValueMap>({});
  const [error, setError] = useState<string | null>(null);

  const capabilities = useQuery({
    queryKey: ["start-surface-capabilities"],
    queryFn: () => api.getStartSurfaceCapabilities(),
    enabled: open,
  });

  const caps = capabilities.data;
  const chatProviders = useMemo(() => filterChatProviders(caps), [caps]);
  const chatModels = useMemo(
    () => filterModelsForProvider(caps?.models ?? [], provider),
    [caps, provider],
  );
  const recipeProviders = useMemo(() => filterChatProviders(caps), [caps]);
  const recipeModels = useMemo(
    () => filterModelsForProvider(caps?.models ?? [], recipeProvider),
    [caps, recipeProvider],
  );
  const recipeRuntimeKinds = caps?.runtime_kinds ?? [];

  useEffect(() => {
    if (!open || !prefill) return;
    if (prefill.path) setPath(prefill.path);
    if (prefill.provider) setProvider(prefill.provider);
    if (prefill.model) setModel(prefill.model);
    if (prefill.agent_id) {
      setAgentId(prefill.agent_id);
      setRecipeProfileId(prefill.agent_id);
    }
    if (prefill.durable_agent_id) {
      setDurableAgentId(prefill.durable_agent_id);
      setPath("durable");
    }
    if (prefill.durable_prompt) setDurablePrompt(prefill.durable_prompt);
  }, [open, prefill]);

  useEffect(() => {
    if (!caps) return;
    const profiles = caps.profiles ?? [];
    const durableAgents = caps.durable_agents ?? [];
    const recipes = caps.recipes ?? [];
    setProvider(
      (current) => current || prefill?.provider || defaultProvider || chatProviders[0]?.id || "",
    );
    setAgentId((current) => current || prefill?.agent_id || defaultAgent || profiles[0]?.id || "");
    setDurableAgentId(
      (current) => current || prefill?.durable_agent_id || durableAgents[0]?.id || "",
    );
    setRecipeId((current) => current || recipes[0]?.id || "");
    setRecipeProfileId(
      (current) => current || prefill?.agent_id || defaultAgent || profiles[0]?.id || "",
    );
  }, [caps, chatProviders, defaultAgent, defaultProvider, prefill]);

  useEffect(() => {
    const recipe = selectedRecipe(caps, recipeId);
    if (!recipe) return;
    setRecipeInputValues(initialRecipeInputValues(recipe));
    setRecipeProvider((current) => current || recipe.provider || recipeProviders[0]?.id || "");
    setRecipeRuntimeKind(
      (current) => current || String(recipe.runtime_kind || recipeRuntimeKinds[0]?.value || ""),
    );
  }, [caps, recipeId, recipeProviders, recipeRuntimeKinds]);

  useEffect(() => {
    if (!caps || !recipeProvider) return;
    const models = filterModelsForProvider(caps.models, recipeProvider);
    setRecipeModel((current) => {
      if (current && models.some((candidate) => candidate.model_id === current)) return current;
      const recipeDefault = selectedRecipe(caps, recipeId)?.model;
      if (recipeDefault && models.some((candidate) => candidate.model_id === recipeDefault)) {
        return recipeDefault;
      }
      return models[0]?.model_id || "";
    });
  }, [caps, recipeId, recipeProvider]);

  useEffect(() => {
    if (!caps || !provider) return;
    const models = filterModelsForProvider(caps.models, provider);
    setModel((current) => {
      if (current && models.some((candidate) => candidate.model_id === current)) return current;
      const preferredModel = prefill?.model || defaultModel;
      if (preferredModel && models.some((candidate) => candidate.model_id === preferredModel)) {
        return preferredModel;
      }
      return models[0]?.model_id || "";
    });
  }, [caps, defaultModel, prefill, provider]);

  const complete = (sessionId?: string) => {
    void queryClient.invalidateQueries({ queryKey: ["sessions"] });
    void queryClient.invalidateQueries({ queryKey: ["start-surface-capabilities"] });
    if (sessionId) onSessionStarted(sessionId);
    onOpenChange(false);
  };

  const createChat = useMutation({
    mutationFn: () =>
      api.createSession({
        project_id: projectId ?? undefined,
        provider: provider || undefined,
        model: model || undefined,
        agent_id: agentId || undefined,
      }),
    onSuccess: (session) => complete(session.id),
    onError: (err) => setError(errorMessage(err)),
  });

  const wakeDurable = useMutation({
    mutationFn: async () => {
      if (!durableAgentId) throw new Error("No durable agent is selected.");
      const body = {
        project_id: projectId ?? undefined,
        wake_payload: {
          reason: durableAction === "resume" ? "lifecycle_resume" : "manual",
          prompt: durablePrompt,
        },
      };
      return durableAction === "resume"
        ? api.resumeDurableAgent(durableAgentId, body)
        : api.startDurableAgent(durableAgentId, body);
    },
    onSuccess: (result) =>
      complete(result.session?.id || result.instance.current_session_id || undefined),
    onError: (err) => setError(errorMessage(err)),
  });

  const dryRunRecipe = useMutation({
    mutationFn: () => {
      if (!recipeId) throw new Error("No recipe is selected.");
      return api.dryRunDurableAgentRecipe(recipeId, recipeRequest);
    },
    onSuccess: (plan) => {
      setRecipePlan(plan);
      setError(null);
    },
    onError: (err) => setError(errorMessage(err)),
  });

  const applyRecipe = useMutation({
    mutationFn: () => {
      if (!recipeId) throw new Error("No recipe is selected.");
      return api.applyDurableAgentRecipe(recipeId, recipeRequest);
    },
    onSuccess: (result) =>
      complete(
        result.launch_result?.session?.id || result.instance.current_session_id || undefined,
      ),
    onError: (err) => setError(errorMessage(err)),
  });

  const recipeRequest = useMemo(() => {
    const next = {
      name: recipeName || selectedRecipe(caps, recipeId)?.name,
      slug:
        recipeSlug || slugify(recipeName || selectedRecipe(caps, recipeId)?.name || recipeId),
      profile_id: recipeProfileId || undefined,
      provider: recipeProvider || undefined,
      model: recipeModel || undefined,
      runtime_kind: recipeRuntimeKind || undefined,
      work_root: recipeWorkRoot || undefined,
      project_id: projectId ?? undefined,
      start: true,
      wake_payload: {
        reason: "manual",
        ...(recipeWakePrompt.trim() ? { prompt: recipeWakePrompt.trim() } : {}),
      },
    };
    const recipe = selectedRecipe(caps, recipeId);
    for (const input of recipe?.inputs ?? []) {
      applyRecipeInputToRequest(next, input, recipeInputValues[input.id]);
    }
    return next;
  }, [
    caps,
    projectId,
    recipeId,
    recipeInputValues,
    recipeModel,
    recipeName,
    recipeProfileId,
    recipeProvider,
    recipeRuntimeKind,
    recipeSlug,
    recipeWakePrompt,
    recipeWorkRoot,
  ]);

  const pending =
    createChat.isPending ||
    wakeDurable.isPending ||
    dryRunRecipe.isPending ||
    applyRecipe.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[min(720px,calc(100vh-2rem))] overflow-hidden sm:max-w-2xl">
        <DialogHeader className="border-b border-border-subtle px-5 pt-5 pb-3">
          <DialogTitle>Start</DialogTitle>
          <DialogDescription>
            Choose the session shape before the conversation begins.
          </DialogDescription>
        </DialogHeader>

        <div className="grid min-h-[420px] min-w-0 grid-cols-[148px_minmax(0,1fr)]">
          <div className="min-h-0 border-r border-border-subtle bg-bg px-2 py-3">
            {PATHS.map((item) => {
              const Icon = item.icon;
              return (
                <button
                  type="button"
                  key={item.id}
                  onClick={() => {
                    setPath(item.id);
                    setError(null);
                  }}
                  className={cn(
                    "mb-1 flex h-9 w-full items-center gap-2 rounded-[6px] px-2 text-left text-[12px] font-medium transition-colors",
                    path === item.id
                      ? "bg-surface text-fg"
                      : "text-fg-muted hover:bg-surface hover:text-fg-secondary",
                  )}
                >
                  <Icon className="size-3.5" strokeWidth={1.9} />
                  {item.label}
                </button>
              );
            })}
          </div>

          <div className="no-scrollbar min-h-0 min-w-0 overflow-y-auto px-5 py-4">
            {capabilities.isLoading ? (
              <div className="text-[12px] text-fg-muted">Loading start options...</div>
            ) : capabilities.isError ? (
              <InlineError message="Could not load start options." />
            ) : (
              <>
                {path === "chat" && (
                  <ChatStartPane
                    providers={chatProviders}
                    models={chatModels}
                    profiles={caps?.profiles ?? []}
                    provider={provider}
                    model={model}
                    agentId={agentId}
                    setProvider={setProvider}
                    setModel={setModel}
                    setAgentId={setAgentId}
                    onStart={() => createChat.mutate()}
                    pending={pending}
                  />
                )}
                {path === "durable" && (
                  <DurableStartPane
                    agents={caps?.durable_agents ?? []}
                    selected={durableAgentId}
                    action={durableAction}
                    prompt={durablePrompt}
                    setSelected={setDurableAgentId}
                    setAction={setDurableAction}
                    setPrompt={setDurablePrompt}
                    onStart={() => wakeDurable.mutate()}
                    pending={pending}
                  />
                )}
                {path === "recipe" && (
                  <RecipeStartPane
                    capabilities={caps}
                    providers={recipeProviders}
                    models={recipeModels}
                    runtimeKinds={recipeRuntimeKinds}
                    recipeId={recipeId}
                    profileId={recipeProfileId}
                    name={recipeName}
                    slug={recipeSlug}
                    provider={recipeProvider}
                    model={recipeModel}
                    runtimeKind={recipeRuntimeKind}
                    workRoot={recipeWorkRoot}
                    wakePrompt={recipeWakePrompt}
                    inputValues={recipeInputValues}
                    plan={recipePlan}
                    setRecipeId={setRecipeId}
                    setProfileId={setRecipeProfileId}
                    setName={setRecipeName}
                    setSlug={setRecipeSlug}
                    setProvider={setRecipeProvider}
                    setModel={setRecipeModel}
                    setRuntimeKind={setRecipeRuntimeKind}
                    setWorkRoot={setRecipeWorkRoot}
                    setWakePrompt={setRecipeWakePrompt}
                    setInputValues={setRecipeInputValues}
                    onDryRun={() => dryRunRecipe.mutate()}
                    onApply={() => applyRecipe.mutate()}
                    pending={pending}
                  />
                )}
                {error && <InlineError message={error} />}
              </>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function ChatStartPane({
  providers,
  models,
  profiles,
  provider,
  model,
  agentId,
  setProvider,
  setModel,
  setAgentId,
  onStart,
  pending,
}: {
  providers: ProviderConfig[];
  models: ModelRecord[];
  profiles: StartSurfaceCapabilitiesResponse["profiles"];
  provider: string;
  model: string;
  agentId: string;
  setProvider: (value: string) => void;
  setModel: (value: string) => void;
  setAgentId: (value: string) => void;
  onStart: () => void;
  pending: boolean;
}) {
  return (
    <section className="space-y-4">
      <PaneHeader title="Chat with a model" detail="Create a normal API-backed chat session." />
      <Field label="Provider">
        <NativeSelect
          aria-label="Provider"
          value={provider}
          onChange={setProvider}
          disabled={providers.length === 0}
        >
          {providers.length === 0 ? <option value="">No providers available</option> : null}
          {providers.map((item) => (
            <option key={item.id} value={item.id}>
              {item.name || item.id}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <Field label="Model">
        <NativeSelect
          aria-label="Model"
          value={model}
          onChange={setModel}
          disabled={models.length === 0}
        >
          {models.length === 0 ? <option value="">No models available</option> : null}
          {models.map((item) => (
            <option key={item.id} value={item.model_id}>
              {item.display_name || item.model_id}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <Field label="Persona">
        <NativeSelect
          aria-label="Persona"
          value={agentId}
          onChange={setAgentId}
          disabled={profiles.length === 0}
        >
          <option value="">Default persona</option>
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name || profile.slug || profile.id}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <Button size="sm" onClick={onStart} disabled={pending || providers.length === 0}>
        <Play className="mr-1.5 size-3.5" />
        Start chat
      </Button>
    </section>
  );
}

function DurableStartPane({
  agents,
  selected,
  action,
  prompt,
  setSelected,
  setAction,
  setPrompt,
  onStart,
  pending,
}: {
  agents: StartSurfaceCapabilitiesResponse["durable_agents"];
  selected: string;
  action: DurableAction;
  prompt: string;
  setSelected: (value: string) => void;
  setAction: (value: DurableAction) => void;
  setPrompt: (value: string) => void;
  onStart: () => void;
  pending: boolean;
}) {
  const agent = agents.find((item) => item.id === selected);
  return (
    <section className="space-y-4">
      <PaneHeader
        title="Wake durable agent"
        detail="Start or resume an existing durable-agent instance."
      />
      <Field label="Agent">
        <NativeSelect
          aria-label="Durable agent"
          value={selected}
          onChange={setSelected}
          disabled={agents.length === 0}
        >
          {agents.length === 0 ? <option value="">No durable agents available</option> : null}
          {agents.map((item) => (
            <option key={item.id} value={item.id}>
              {item.name || item.slug || item.id}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <fieldset className="flex gap-2" aria-label="Durable action">
        {(["start", "resume"] as DurableAction[]).map((item) => (
          <button
            type="button"
            key={item}
            onClick={() => setAction(item)}
            className={cn(
              "h-8 rounded-[6px] border px-3 text-[12px] capitalize",
              action === item
                ? "border-fg-muted bg-surface text-fg"
                : "border-border-subtle text-fg-muted hover:bg-surface",
            )}
          >
            {item}
          </button>
        ))}
      </fieldset>
      <Field label="Wake prompt">
        <Textarea
          value={prompt}
          onChange={(event) => setPrompt(event.target.value)}
          placeholder="Optional first wake instruction"
          className="min-h-20 text-[13px]"
        />
      </Field>
      {agent && (
        <MetadataLine value={`${agent.lifecycle_class} / ${agent.provider} / ${agent.model}`} />
      )}
      <Button size="sm" onClick={onStart} disabled={pending || agents.length === 0}>
        <Bot className="mr-1.5 size-3.5" />
        {action === "resume" ? "Resume agent" : "Start agent"}
      </Button>
    </section>
  );
}

function RecipeStartPane({
  capabilities,
  providers,
  models,
  runtimeKinds,
  recipeId,
  profileId,
  name,
  slug,
  provider,
  model,
  runtimeKind,
  workRoot,
  wakePrompt,
  inputValues,
  plan,
  setRecipeId,
  setProfileId,
  setName,
  setSlug,
  setProvider,
  setModel,
  setRuntimeKind,
  setWorkRoot,
  setWakePrompt,
  setInputValues,
  onDryRun,
  onApply,
  pending,
}: {
  capabilities?: StartSurfaceCapabilitiesResponse;
  providers: ProviderConfig[];
  models: ModelRecord[];
  runtimeKinds: StartSurfaceCapabilitiesResponse["runtime_kinds"];
  recipeId: string;
  profileId: string;
  name: string;
  slug: string;
  provider: string;
  model: string;
  runtimeKind: string;
  workRoot: string;
  wakePrompt: string;
  inputValues: RecipeInputValueMap;
  plan: DurableAgentRecipePlan | null;
  setRecipeId: (value: string) => void;
  setProfileId: (value: string) => void;
  setName: (value: string) => void;
  setSlug: (value: string) => void;
  setProvider: (value: string) => void;
  setModel: (value: string) => void;
  setRuntimeKind: (value: string) => void;
  setWorkRoot: (value: string) => void;
  setWakePrompt: (value: string) => void;
  setInputValues: React.Dispatch<React.SetStateAction<RecipeInputValueMap>>;
  onDryRun: () => void;
  onApply: () => void;
  pending: boolean;
}) {
  const recipes = capabilities?.recipes ?? [];
  const profiles = capabilities?.profiles ?? [];
  const workRootHints = capabilities?.work_root_hints ?? [];
  const recipe = selectedRecipe(capabilities, recipeId);

  return (
    <section className="space-y-4">
      <PaneHeader
        title="Create from recipe"
        detail="Preview then create a durable-agent instance."
      />
      <Field label="Recipe">
        <NativeSelect
          aria-label="Recipe"
          value={recipeId}
          onChange={setRecipeId}
          disabled={recipes.length === 0}
        >
          {recipes.length === 0 ? <option value="">No recipes available</option> : null}
          {recipes.map((item) => (
            <option key={item.id} value={item.id}>
              {item.name}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Name">
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={recipe?.name || "Agent name"}
          />
        </Field>
        <Field label="Slug">
          <Input
            value={slug}
            onChange={(event) => setSlug(event.target.value)}
            placeholder={slugify(name || recipe?.name || "")}
          />
        </Field>
      </div>
      <Field label="Profile">
        <NativeSelect
          aria-label="Recipe profile"
          value={profileId}
          onChange={setProfileId}
          disabled={profiles.length === 0}
        >
          {profiles.length === 0 ? <option value="">No profiles available</option> : null}
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name || profile.slug || profile.id}
            </option>
          ))}
        </NativeSelect>
      </Field>
      <div className="grid grid-cols-3 gap-3">
        <Field label="Provider">
          <NativeSelect
            aria-label="Recipe provider"
            value={provider}
            onChange={setProvider}
            disabled={providers.length === 0}
          >
            {provider && !providers.some((item) => item.id === provider) ? (
              <option value={provider}>{provider}</option>
            ) : null}
            {providers.length === 0 ? <option value="">No providers available</option> : null}
            {providers.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name || item.id}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Model">
          <NativeSelect
            aria-label="Recipe model"
            value={model}
            onChange={setModel}
            disabled={models.length === 0}
          >
            {model && !models.some((item) => item.model_id === model) ? (
              <option value={model}>{model}</option>
            ) : null}
            {models.length === 0 ? <option value="">No models available</option> : null}
            {models.map((item) => (
              <option key={item.id} value={item.model_id}>
                {item.display_name || item.model_id}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <Field label="Runtime kind">
          <NativeSelect
            aria-label="Recipe runtime kind"
            value={runtimeKind}
            onChange={setRuntimeKind}
            disabled={runtimeKinds.length === 0}
          >
            {runtimeKind && !runtimeKinds.some((item) => item.value === runtimeKind) ? (
              <option value={runtimeKind}>{runtimeKind}</option>
            ) : null}
            {runtimeKinds.length === 0 ? <option value="">No runtime kinds available</option> : null}
            {runtimeKinds.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label || item.value}
              </option>
            ))}
          </NativeSelect>
        </Field>
      </div>
      <Field label="Work root">
        <PathInput
          ariaLabel="Recipe work root"
          value={workRoot}
          onChange={setWorkRoot}
          hints={workRootHints}
          placeholder="Optional working directory"
        />
      </Field>
      <Field label="Wake prompt">
        <Textarea
          value={wakePrompt}
          onChange={(event) => setWakePrompt(event.target.value)}
          className="min-h-20"
          placeholder="Optional kickoff instructions"
        />
      </Field>
      {(recipe?.inputs ?? [])
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
      {recipe && (
        <MetadataLine
          value={`${recipe.kind} / ${recipe.lifecycle_class} / ${recipe.runtime_kind}`}
        />
      )}
      <div className="flex gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onDryRun}
          disabled={pending || recipes.length === 0}
        >
          Dry run
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={onApply}
          disabled={pending || recipes.length === 0 || (plan ? !plan.ready : false)}
        >
          Apply and start
        </Button>
      </div>
      {plan && (
        <div className="rounded-[6px] border border-border-subtle bg-bg p-3 text-[12px]">
          <div className="font-medium text-fg">
            {plan.ready ? "Ready to apply" : "Needs attention"}
          </div>
          {plan.missing_requirements?.length ? (
            <div className="mt-2 text-warning">Missing: {plan.missing_requirements.join(", ")}</div>
          ) : null}
          {plan.unsupported?.length ? (
            <div className="mt-2 text-danger">Unsupported: {plan.unsupported.join(", ")}</div>
          ) : null}
          <div className="mt-2 font-mono text-[11px] text-fg-muted">
            {plan.session_policy} / {plan.launch_policy.runtime_kind}
          </div>
        </div>
      )}
    </section>
  );
}

function PathInput({
  value,
  onChange,
  hints,
  placeholder,
  ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  hints: StartSurfaceCapabilitiesResponse["work_root_hints"];
  placeholder?: string;
  ariaLabel: string;
}) {
  const datalistId = "start-surface-work-root-hints";
  return (
    <>
      <Input
        list={hints.length > 0 ? datalistId : undefined}
        aria-label={ariaLabel}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
      />
      {hints.length > 0 ? (
        <datalist id={datalistId}>
          {hints.map((hint) => (
            <option key={hint.id} value={hint.path || ""}>
              {hint.label}
            </option>
          ))}
        </datalist>
      ) : null}
      {hints.length > 0 ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {hints.map((hint) => (
            <button
              key={hint.id}
              type="button"
              onClick={() => onChange(hint.path || "")}
              className="rounded-[6px] border border-border-subtle bg-bg px-2 py-1 text-[11px] text-fg-muted transition-colors hover:bg-surface hover:text-fg-secondary"
            >
              {hint.label}
            </button>
          ))}
        </div>
      ) : null}
    </>
  );
}

function PaneHeader({ title, detail }: { title: string; detail: string }) {
  return (
    <div>
      <h3 className="text-[13px] font-semibold text-fg">{title}</h3>
      <p className="mt-1 text-[12px] text-fg-muted">{detail}</p>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="block space-y-1.5">
      <span className="font-mono text-[10px] font-semibold uppercase tracking-[0.14em] text-fg-muted">
        {label}
      </span>
      {children}
    </div>
  );
}

function NativeSelect({
  value,
  onChange,
  children,
  disabled,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  children: React.ReactNode;
  disabled?: boolean;
  "aria-label": string;
}) {
  return (
    <select
      aria-label={ariaLabel}
      value={value}
      disabled={disabled}
      onChange={(event) => onChange(event.target.value)}
      className="h-9 w-full rounded-[6px] border border-border-subtle bg-bg px-2 text-[13px] text-fg outline-none focus:border-ring disabled:opacity-60"
    >
      {children}
    </select>
  );
}

function InlineError({ message }: { message: string }) {
  return (
    <div className="mt-4 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-2 text-[12px] text-danger">
      {message}
    </div>
  );
}

function MetadataLine({ value }: { value: string }) {
  return (
    <p className="rounded-[6px] border border-border-subtle bg-bg px-3 py-2 font-mono text-[11px] text-fg-muted">
      {value}
    </p>
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

  let control: React.ReactNode;
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
      <NativeSelect
        aria-label={input.label}
        value={textValue}
        onChange={onChange}
      >
        <option value="">{input.placeholder || "Select..."}</option>
        {(input.options ?? []).map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </NativeSelect>
    );
  } else if (input.type === "boolean") {
    control = (
      <label className="flex items-center gap-2 text-[13px] text-fg-secondary">
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
    <Field label={`${input.label}${input.required ? " *" : ""}`}>
      {control}
      {help ? <p className="text-[11px] text-fg-muted">{help}</p> : null}
    </Field>
  );
}

function filterChatProviders(caps?: StartSurfaceCapabilitiesResponse): ProviderConfig[] {
  return caps?.providers ?? [];
}

function filterModelsForProvider(models: ModelRecord[], provider: string): ModelRecord[] {
  return models.filter((model) => model.provider_id === provider);
}

function selectedRecipe(caps: StartSurfaceCapabilitiesResponse | undefined, id: string) {
  return caps?.recipes?.find((recipe) => recipe.id === id);
}

function slugify(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : "Request failed.";
}
