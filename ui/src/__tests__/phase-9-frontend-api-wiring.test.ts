import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import type {
  DurableAgentEvent,
  DurableAgentInstance,
  DurableAgentLaunchPlan,
  DurableAgentRecipe,
  SessionDetailsResponse,
  StartSurfaceCapabilitiesResponse,
} from "@/lib/types";

type FetchCall = [string, RequestInit | undefined];

const jsonResponse = (body: unknown, ok = true, status = 200) =>
  ({
    ok,
    status,
    json: async () => body,
  }) as Response;

function installFetch(body: unknown = {}) {
  const mock = vi.fn(async () => jsonResponse(body));
  vi.stubGlobal("fetch", mock);
  return mock;
}

function lastFetchCall(mock: ReturnType<typeof installFetch>): FetchCall {
  const call = mock.mock.calls.at(-1) as unknown[] | undefined;
  if (!call) throw new Error("fetch was not called");
  return [String(call[0]), call[1] as RequestInit | undefined];
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("phase 9 API route wiring", () => {
  it("wires harness runtime API endpoints", async () => {
    const fetchMock = installFetch({});

    await api.getHarnessInitialize();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/initialize",
      undefined,
    ]);

    await api.getHarnessCapabilities();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/capabilities",
      undefined,
    ]);

    const createBody = {
      provider: "anthropic",
      model: "claude-sonnet-4",
      title: "External session",
    };
    await api.createHarnessSession(createBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/sessions",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(createBody),
      },
    ]);

    await api.getHarnessSession("session/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/sessions/session%2F1",
      undefined,
    ]);

    const turnBody = { content: "hello", cycle_kind: "wake", effort: "high" };
    await api.sendHarnessTurn("session/1", turnBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/sessions/session%2F1/turns",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(turnBody),
      },
    ]);

    await api.cancelHarnessTurn("session/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/sessions/session%2F1/cancel",
      { method: "POST" },
    ]);

    await api.listHarnessDurableAgents();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/durable-agents",
      undefined,
    ]);

    await api.getHarnessDurableAgent("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/durable-agents/agent%2F1",
      undefined,
    ]);

    const durableBody = {
      wake_payload: { reason: "manual" },
    };
    await api.startHarnessDurableAgent("agent/1", durableBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/durable-agents/agent%2F1/start",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(durableBody),
      },
    ]);

    await api.resumeHarnessDurableAgent("agent/1", durableBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/durable-agents/agent%2F1/resume",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(durableBody),
      },
    ]);

    await api.wakeHarnessDurableAgent("agent/1", durableBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/harness/v1/durable-agents/agent%2F1/wake",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(durableBody),
      },
    ]);
  });

  it("fetches start-surface capabilities and session details", async () => {
    const fetchMock = installFetch({});

    await api.getStartSurfaceCapabilities();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/start-surface/capabilities",
      undefined,
    ]);

    await api.getSessionDetails("session/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/sessions/session%2F1/details",
      undefined,
    ]);
  });

  it("wires durable-agent instance endpoints", async () => {
    const fetchMock = installFetch({});

    await api.listDurableAgents();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents",
      undefined,
    ]);

    await api.listDurableAgents(true);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents?include_archived=true",
      undefined,
    ]);

    await api.getDurableAgent("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent%2F1",
      undefined,
    ]);

    const createBody = {
      id: "agent-1",
      name: "Agent",
      slug: "agent",
      profile_id: "profile-1",
      lifecycle_class: "advisor",
      provider: "anthropic",
      model: "claude-sonnet-4",
      runtime_kind: "api",
      launch_source_type: "durable_advisor",
      launch_source_id: "recipe-1",
      work_root: "/tmp/work",
      metadata_json: "{}",
    };
    await api.createDurableAgent(createBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(createBody),
      },
    ]);

    const updateBody = { name: "Renamed", work_root: "/tmp/next" };
    await api.updateDurableAgent("agent-1", updateBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1",
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(updateBody),
      },
    ]);

    await api.archiveDurableAgent("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/archive",
      { method: "POST" },
    ]);
  });

  it("wires durable-agent lifecycle, launch, and session endpoints", async () => {
    const fetchMock = installFetch({});

    await api.requestDurableAgentStart("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/start-request",
      { method: "POST" },
    ]);

    await api.requestDurableAgentStop("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/stop-request",
      { method: "POST" },
    ]);

    await api.requestDurableAgentPause("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/pause-request",
      { method: "POST" },
    ]);

    await api.requestDurableAgentResume("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/resume-request",
      { method: "POST" },
    ]);

    await api.getDurableAgentLaunchPlan("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/launch-plan",
      undefined,
    ]);

    const startBody = {
      project_id: "project-1",
      wake_payload: { reason: "manual", prompt: "wake up" },
    };
    await api.startDurableAgent("agent-1", startBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/start",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(startBody),
      },
    ]);

    await api.resumeDurableAgent("agent-1", startBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/resume",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(startBody),
      },
    ]);

    await api.listDurableAgentSessions("agent-1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/sessions",
      undefined,
    ]);

    await api.listDurableAgentEvents("agent/1", 25);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent%2F1/events?limit=25",
      undefined,
    ]);

    const attachBody = { session_id: "session-1", relation: "primary" };
    await api.attachDurableAgentSession("agent-1", attachBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agents/agent-1/sessions",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(attachBody),
      },
    ]);
  });

  it("wires reflex capability endpoints", async () => {
    const fetchMock = installFetch({});

    await api.listAgentReflexes("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/reflexes",
      undefined,
    ]);

    const createBody = {
      name: "clean-status-without-tools",
      trigger_kind: "predicate",
      trigger_spec:
        '{"kind":"tool_calls_window","window":2,"op":"=","value":0}',
      action_kind: "inject_reminder",
      action_spec: '{"body":"Ground yourself."}',
      priority: 75,
    };
    await api.createAgentReflex("agent/1", createBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/reflexes",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(createBody),
      },
    ]);

    const patchBody = { status: "paused", priority: 10 };
    await api.patchAgentReflex("agent/1", "reflex/1", patchBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/reflexes/reflex%2F1",
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(patchBody),
      },
    ]);

    await api.deleteAgentReflex("agent/1", "reflex/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/reflexes/reflex%2F1",
      { method: "DELETE" },
    ]);

    const validateBody = {
      trigger_kind: "predicate",
      trigger_spec: '{"kind":"mail_unread_count","op":">=","value":1}',
      action_kind: "inject_reminder",
      action_spec: '{"body":"Check mail."}',
      agent_id: "agent-1",
    };
    await api.validateReflex(validateBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/reflexes/validate",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(validateBody),
      },
    ]);

    await api.listPendingReflexes();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/pending/reflexes",
      undefined,
    ]);

    await api.listPendingReflexes("pending");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/pending/reflexes?status=pending",
      undefined,
    ]);

    await api.approvePendingReflex("pending/1", { reviewed_by: "operator-ui" });
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/pending/reflexes/pending%2F1/approve",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reviewed_by: "operator-ui" }),
      },
    ]);

    await api.rejectPendingReflex("pending/1", {
      reviewed_by: "operator-ui",
      reason: "nope",
    });
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/pending/reflexes/pending%2F1/reject",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reviewed_by: "operator-ui", reason: "nope" }),
      },
    ]);
  });

  it("wires agent profile parity and builder endpoints", async () => {
    const fetchMock = installFetch({});

    const createProfileBody = {
      name: "Project Advisor",
      slug: "project-advisor",
      system_prompt: "You advise on the project.",
      role_tools: '["task_execute"]',
      role_skills: '["planner"]',
      context_policy: '{"window":"session"}',
      class: "advisor",
      activation_mode: "singleton",
      default_state: "sleeping",
    };
    await api.createAgentProfile(createProfileBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(createProfileBody),
      },
    ]);

    const updateProfileBody = {
      role_tools: '["task_execute","memory_search"]',
      role_skills: '["planner","writer"]',
      context_policy: '{"window":"durable"}',
      class: "process",
      activation_mode: "shared",
      default_state: "active",
    };
    await api.updateAgentProfile("agent/1", updateProfileBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent/1",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(updateProfileBody),
      },
    ]);

    const dryRunBody = {
      schema_version: 1,
      mode: "create_profile_and_instance" as const,
      profile: {
        name: "Project Advisor",
        slug: "project-advisor",
        system_prompt: "You advise on the project.",
      },
      durable_instance: {
        create: true,
        lifecycle_class: "advisor",
        provider: "anthropic",
        model: "claude-sonnet-4",
        runtime_kind: "api",
        work_root: "/tmp/project-advisor",
      },
    };
    await api.agentBuilderDryRun(dryRunBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agent-builder/dry-run",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(dryRunBody),
      },
    ]);

    const draftBody = {
      schema_version: 1,
      intake_text: "Create a durable project advisor for the repo.",
      preferred_provider: "anthropic",
      preferred_model: "claude-sonnet-4",
      requested_lifecycle_class: "advisor",
    };
    await api.agentBuilderDraft(draftBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agent-builder/draft",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(draftBody),
      },
    ]);

    const reviewBody = {
      schema_version: 1,
      current_draft: {
        mode: "create_profile" as const,
        profile: {
          name: "Project Advisor",
          slug: "project-advisor",
          system_prompt: "You advise on the project.",
        },
        capabilities: {},
        durable_instance: {},
        operator_notification: {},
      },
    };
    await api.agentBuilderReview(reviewBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agent-builder/review",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(reviewBody),
      },
    ]);
  });

  it("wires agent capability CRUD endpoints", async () => {
    const fetchMock = installFetch({});

    await api.listAgentKnownTools("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-tools",
      undefined,
    ]);

    await api.getAgentKnownTool("agent/1", "tool/name");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-tools/tool%2Fname",
      undefined,
    ]);

    const knownToolBody = {
      tool_name: "tool/name",
      pinned: true,
      sort_order: 3,
      ttl_seconds: 120,
      reason: "operator pin",
    };
    await api.createAgentKnownTool("agent/1", knownToolBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-tools",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(knownToolBody),
      },
    ]);

    await api.updateAgentKnownTool("agent/1", "tool/name", knownToolBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-tools/tool%2Fname",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(knownToolBody),
      },
    ]);

    await api.deleteAgentKnownTool("agent/1", "tool/name");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-tools/tool%2Fname",
      { method: "DELETE" },
    ]);

    const knownSkillBody = {
      skill_name: "advisor",
      pinned: false,
      ttl_seconds: 0,
      reason: "manual",
    };
    await api.listAgentKnownSkills("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-skills",
      undefined,
    ]);

    await api.getAgentKnownSkill("agent/1", "advisor");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-skills/advisor",
      undefined,
    ]);

    await api.createAgentKnownSkill("agent/1", knownSkillBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-skills",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(knownSkillBody),
      },
    ]);

    await api.updateAgentKnownSkill("agent/1", "advisor", knownSkillBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-skills/advisor",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(knownSkillBody),
      },
    ]);

    await api.deleteAgentKnownSkill("agent/1", "advisor");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/known-skills/advisor",
      { method: "DELETE" },
    ]);

    const procedureBody = { name: "checklist", body: "Step 1", scope: "agent" };
    await api.listAgentProcedures("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/procedures",
      undefined,
    ]);

    await api.getAgentProcedure("agent/1", "checklist");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/procedures/checklist",
      undefined,
    ]);

    await api.createAgentProcedure("agent/1", procedureBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/procedures",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(procedureBody),
      },
    ]);

    await api.updateAgentProcedure("agent/1", "checklist", procedureBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/procedures/checklist",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(procedureBody),
      },
    ]);

    await api.deleteAgentProcedure("agent/1", "checklist");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/procedures/checklist",
      { method: "DELETE" },
    ]);

    const seedBody = {
      seed_key: "boot-conventions",
      namespace: "user/demo",
      body: "Prefer rg.",
      tags: ["shell"],
    };
    await api.listAgentKnowledgeSeeds("agent/1");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/knowledge-seeds",
      undefined,
    ]);

    await api.getAgentKnowledgeSeed("agent/1", "boot/conventions");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/knowledge-seeds/boot%2Fconventions",
      undefined,
    ]);

    await api.createAgentKnowledgeSeed("agent/1", seedBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/knowledge-seeds",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(seedBody),
      },
    ]);

    await api.updateAgentKnowledgeSeed("agent/1", "boot/conventions", seedBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/knowledge-seeds/boot%2Fconventions",
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(seedBody),
      },
    ]);

    await api.deleteAgentKnowledgeSeed("agent/1", "boot/conventions");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/knowledge-seeds/boot%2Fconventions",
      { method: "DELETE" },
    ]);

    await api.markAgentKnowledgeSeedApplied("agent/1", "boot/conventions");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/agents/agent%2F1/knowledge-seeds/boot%2Fconventions/mark-applied",
      { method: "POST" },
    ]);
  });

  it("wires durable-agent recipe endpoints", async () => {
    const fetchMock = installFetch({});

    await api.listDurableAgentRecipes();
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agent-recipes",
      undefined,
    ]);

    await api.getDurableAgentRecipe("project/advisor");
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agent-recipes/project%2Fadvisor",
      undefined,
    ]);

    const recipeBody = {
      name: "Advisor",
      slug: "advisor",
      profile_id: "profile-1",
      start: true,
      wake_payload: { reason: "manual", facts: { project: "agridd" } },
    };
    await api.dryRunDurableAgentRecipe("project-advisor", recipeBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agent-recipes/project-advisor/dry-run",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(recipeBody),
      },
    ]);

    await api.applyDurableAgentRecipe("project-advisor", recipeBody);
    expect(lastFetchCall(fetchMock)).toEqual([
      "/api/durable-agent-recipes/project-advisor/apply",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(recipeBody),
      },
    ]);
  });
});

describe("phase 9 response shape fixtures", () => {
  const durableAgent: DurableAgentInstance = {
    id: "agent-1",
    name: "Project Advisor",
    slug: "project-advisor",
    profile_id: "profile-1",
    lifecycle_class: "advisor",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_kind: "api",
    launch_source_type: "durable_advisor",
    launch_source_id: "project-advisor",
    work_root: "",
    status: "sleeping",
    current_session_id: "",
    failure_reason: "",
    metadata_json: "{}",
    created_at: "2026-05-23T00:00:00Z",
    updated_at: "2026-05-23T00:00:00Z",
  };

  const launchPlan: DurableAgentLaunchPlan = {
    instance_id: durableAgent.id,
    lifecycle_class: durableAgent.lifecycle_class,
    session_policy: "reuse_latest_or_create",
    launch_source_type: durableAgent.launch_source_type,
    provider: durableAgent.provider,
    model: durableAgent.model,
    runtime_kind: durableAgent.runtime_kind,
    work_root: durableAgent.work_root,
    attachment_relation: "primary",
    wake_payload: { reason: "manual", facts: { scope: "project" } },
  };

  const recipe: DurableAgentRecipe = {
    id: "project-advisor",
    schema_version: 1,
    kind: "project_advisor",
    name: "Project Advisor",
    description: "Reusable project advisor.",
    lifecycle_class: "advisor",
    profile_rule: "operator_selected",
    provider: "anthropic",
    model: "claude-sonnet-4",
    runtime_kind: "api",
    launch_source_type: "durable_advisor",
    wake_defaults: { reason: "manual" },
    inputs: [
      {
        id: "name",
        label: "Name",
        type: "string",
        required: true,
        maps_to: "durable_agent.name",
      },
      {
        id: "provider",
        label: "Provider",
        type: "provider",
        default: "anthropic",
        maps_to: "durable_agent.provider",
      },
    ],
    tags: ["advisor"],
  };

  const durableEvent: DurableAgentEvent = {
    id: "event-1",
    instance_id: durableAgent.id,
    event_type: "start_succeeded",
    status_before: "start_requested",
    status_after: "active",
    session_id: "session-1",
    source: "api",
    message: "Started.",
    metadata_json: "{}",
    created_at: "2026-05-23T00:01:00Z",
  };

  it("covers capabilities, recipe, launch, and session-details contracts", () => {
    const capabilities: StartSurfaceCapabilitiesResponse = {
      schema_version: 1,
      lifecycle_classes: [{ value: "advisor", label: "Advisor" }],
      durable_statuses: [{ value: "sleeping", label: "Sleeping" }],
      launch_sources: [{ value: "durable_advisor", label: "Durable advisor" }],
      attachment_relations: [{ value: "primary", label: "Primary" }],
      runtime_kinds: [
        {
          value: "api",
          label: "API",
          managed_automation: true,
          product_supported: true,
        },
      ],
      recipe_kinds: [{ value: "project_advisor", label: "Project advisor" }],
      wake_reasons: [{ value: "manual", label: "Manual" }],
      session_policies: [
        { value: "reuse_latest_or_create", label: "Reuse latest or create" },
      ],
      recipes: [recipe],
      durable_agents: [durableAgent],
      profiles: [],
      providers: [],
      models: [],
      boot_profiles: [],
      work_root_hints: [
        { id: "operator-provided", label: "Operator provided" },
      ],
    };

    const details: SessionDetailsResponse = {
      session: {
        id: "session-1",
        short_code: "c1",
        title: "Advisor",
        custom_name: "",
        project_id: "project-1",
        context_type: "durable_agent",
        context_id: durableAgent.id,
        provider: durableAgent.provider,
        model: durableAgent.model,
        status: "active",
        is_pinned: false,
        sort_order: 0,
        message_count: 1,
        tags: "[]",
        last_activity: "2026-05-23T00:00:00Z",
        created_at: "2026-05-23T00:00:00Z",
      },
      durable_attachments: [
        {
          instance_id: durableAgent.id,
          session_id: "session-1",
          relation: "primary",
          attached_at: "2026-05-23T00:00:00Z",
          session_status: "active",
          provider: "anthropic",
          model: "claude-sonnet-4",
          runtime_state: "none",
        },
      ],
      current_durable_agent: durableAgent,
      activity_state: "online",
      last_activity_at: "2026-05-23T00:00:00Z",
      last_useful_activity_at: "2026-05-23T00:00:00Z",
      halt: { is_halted: false },
      usage: null,
      recent_durable_events: [],
      runtime: { state: "none" },
      boot_source: "durable_agent",
      immutable_start_fields: ["provider", "model", "runtime_kind"],
      checkpoint: { status: "unknown" },
    };

    expect(capabilities.recipes[0]?.id).toBe("project-advisor");
    expect(capabilities.recipes[0]?.inputs?.[0]?.id).toBe("name");
    expect(launchPlan.attachment_relation).toBe("primary");
    expect(durableEvent.event_type).toBe("start_succeeded");
    expect(details.current_durable_agent?.id).toBe("agent-1");
  });
});
