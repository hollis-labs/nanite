import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AgentCapabilitiesPanel } from "@/components/settings/agents/AgentCapabilitiesPanel";
import { api } from "@/lib/api";
import type {
  AgentKnowledgeSeed,
  AgentKnownSkill,
  AgentKnownTool,
  AgentProcedure,
  AgentProfile,
  PromptTemplate,
  Skill,
  ToolLoadItem,
} from "@/lib/types";

const queryClients: QueryClient[] = [];

afterEach(() => {
  cleanup();
  for (const client of queryClients) client.clear();
  queryClients.length = 0;
  vi.restoreAllMocks();
});

function renderWithClient(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  queryClients.push(client);
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

function agent(overrides: Partial<AgentProfile> = {}): AgentProfile {
  return {
    id: "agent-1",
    name: "Operator",
    slug: "operator",
    avatar: "",
    icon: "",
    system_prompt: "",
    description: "Capability admin target",
    modes: "[]",
    default_model: "gpt-5",
    mcp_servers: "[]",
    tool_permissions: '{"allow":["mcp__docs__search"],"deny":["mcp__danger__*"]}',
    can_execute: true,
    settings: "{}",
    created_at: "2026-05-24T00:00:00Z",
    updated_at: "2026-05-24T00:00:00Z",
    agent_hash: "",
    version: 1,
    tools: '["mcp__docs__search"]',
    directories: "[]",
    constraints: "{}",
    tags: "[]",
    status: "active",
    source: "user",
    source_ref: "",
    ...overrides,
  };
}

function skill(overrides: Partial<Skill> = {}): Skill {
  return {
    id: "skill-1",
    name: "Docs Search",
    slug: "docs-search",
    category: "research",
    description: "Look up docs",
    icon: "",
    tool_bindings: "[]",
    input_schema: "{}",
    is_builtin: false,
    settings: "{}",
    created_at: "2026-05-24T00:00:00Z",
    updated_at: "2026-05-24T00:00:00Z",
    source: "user",
    ...overrides,
  };
}

function template(overrides: Partial<PromptTemplate> = {}): PromptTemplate {
  return {
    id: "template-1",
    name: "Escalation Guard",
    slug: "escalation-guard",
    scope: "system",
    template: "Stay grounded.",
    variables: "[]",
    priority: 20,
    icon: "",
    is_builtin: false,
    created_at: "2026-05-24T00:00:00Z",
    updated_at: "2026-05-24T00:00:00Z",
    ...overrides,
  };
}

function knownTool(overrides: Partial<AgentKnownTool> = {}): AgentKnownTool {
  return {
    agent_id: "agent-1",
    tool_name: "mcp__docs__search",
    pinned: true,
    sort_order: 5,
    activation_count: 3,
    last_used_at: "2026-05-24T12:00:00Z",
    added_at: "2026-05-24T10:00:00Z",
    ttl_seconds: 300,
    reason: "Core lookup path",
    ...overrides,
  };
}

function knownSkill(overrides: Partial<AgentKnownSkill> = {}): AgentKnownSkill {
  return {
    agent_id: "agent-1",
    skill_name: "Docs Search",
    pinned: true,
    activation_count: 2,
    last_used_at: "2026-05-24T12:00:00Z",
    added_at: "2026-05-24T10:00:00Z",
    ttl_seconds: 120,
    reason: "Use before guessing",
    ...overrides,
  };
}

function procedure(overrides: Partial<AgentProcedure> = {}): AgentProcedure {
  return {
    agent_id: "agent-1",
    name: "handoff",
    body: "Summarize blockers before handoff.",
    scope: "agent",
    created_at: "2026-05-24T10:00:00Z",
    updated_at: "2026-05-24T12:00:00Z",
    ...overrides,
  };
}

function seed(overrides: Partial<AgentKnowledgeSeed> = {}): AgentKnowledgeSeed {
  return {
    agent_id: "agent-1",
    seed_key: "repo-layout",
    namespace: "project",
    body: "UI lives under ui/src.",
    tags_json: '["repo","ui"]',
    applied_at: "",
    created_at: "2026-05-24T10:00:00Z",
    ...overrides,
  };
}

function tool(overrides: Partial<ToolLoadItem> = {}): ToolLoadItem {
  return {
    name: "mcp__docs__search",
    description: "Search docs",
    load_type: "auto",
    load_type_source: "system",
    enabled: true,
    ...overrides,
  };
}

function mockBaseQueries() {
  vi.spyOn(api, "listAgentKnownTools").mockResolvedValue([]);
  vi.spyOn(api, "listAgentKnownSkills").mockResolvedValue([]);
  vi.spyOn(api, "listAgentProcedures").mockResolvedValue([]);
  vi.spyOn(api, "listAgentKnowledgeSeeds").mockResolvedValue([]);
  vi.spyOn(api, "fetchAllToolsWithLoadType").mockResolvedValue([tool()]);
}

function renderPanel({
  profile = agent(),
  agentSkills = [],
  availableSkills = [],
  agentTemplates = [],
  availableTemplates = [],
  onAssignSkill = vi.fn(),
  onRemoveSkill = vi.fn(),
  onAssignTemplate = vi.fn(),
  onRemoveTemplate = vi.fn(),
  onUpdateAgent = vi.fn(),
}: {
  profile?: AgentProfile;
  agentSkills?: Skill[];
  availableSkills?: Skill[];
  agentTemplates?: PromptTemplate[];
  availableTemplates?: PromptTemplate[];
  onAssignSkill?: ReturnType<typeof vi.fn>;
  onRemoveSkill?: ReturnType<typeof vi.fn>;
  onAssignTemplate?: ReturnType<typeof vi.fn>;
  onRemoveTemplate?: ReturnType<typeof vi.fn>;
  onUpdateAgent?: ReturnType<typeof vi.fn>;
} = {}) {
  return {
    ...renderWithClient(
      <AgentCapabilitiesPanel
        agent={profile}
        isReadOnly={profile.source === "internal"}
        agentSkills={agentSkills}
        availableSkills={availableSkills}
        onAssignSkill={onAssignSkill}
        onRemoveSkill={onRemoveSkill}
        agentTemplates={agentTemplates}
        availableTemplates={availableTemplates}
        onAssignTemplate={onAssignTemplate}
        onRemoveTemplate={onRemoveTemplate}
        onUpdateAgent={onUpdateAgent}
      />,
    ),
    onAssignSkill,
    onRemoveSkill,
    onAssignTemplate,
    onRemoveTemplate,
    onUpdateAgent,
  };
}

describe("Phase 22 agent capability admin", () => {
  it("renders sections and keeps skill/template assignment wiring intact", async () => {
    mockBaseQueries();
    const assignedSkill = skill();
    const availableSkill = skill({ id: "skill-2", name: "Repo Memory", category: "memory" });
    const assignedTemplate = template();
    const availableTemplate = template({
      id: "template-2",
      name: "Mode Reminder",
      slug: "mode-reminder",
      scope: "mode",
      priority: 9,
    });
    const view = renderPanel({
      agentSkills: [assignedSkill],
      availableSkills: [availableSkill],
      agentTemplates: [assignedTemplate],
      availableTemplates: [availableTemplate],
    });

    expect(await screen.findByText("Skills (1)")).toBeTruthy();
    expect(screen.getByText("Prompt Templates (1)")).toBeTruthy();
    expect(screen.getByText("Known Tools (0)")).toBeTruthy();
    expect(screen.getByText("Knowledge Seeds (0)")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Remove skill Docs Search"));
    expect(view.onRemoveSkill).toHaveBeenCalledWith("skill-1");

    fireEvent.click(screen.getByLabelText("Remove template Escalation Guard"));
    expect(view.onRemoveTemplate).toHaveBeenCalledWith("template-1");

    fireEvent.click(screen.getAllByRole("button", { name: "Assign" })[0]);
    await screen.findByText("Available Skills");
    fireEvent.click(
      within(screen.getByText("Repo Memory").parentElement?.parentElement as HTMLElement).getByRole("button", {
        name: "Assign",
      }),
    );
    expect(view.onAssignSkill).toHaveBeenCalledWith("skill-2");

    fireEvent.click(screen.getAllByRole("button", { name: "Assign" })[1]);
    await screen.findByText("Available Prompt Templates");
    fireEvent.click(
      within(
        screen.getByText("Mode Reminder").parentElement?.parentElement as HTMLElement,
      ).getByRole("button", { name: "Assign" }),
    );
    expect(view.onAssignTemplate).toHaveBeenCalledWith("template-2");
  });

  it("creates, updates, and deletes known tools", async () => {
    vi.spyOn(api, "listAgentKnownTools").mockResolvedValue([knownTool()]);
    vi.spyOn(api, "listAgentKnownSkills").mockResolvedValue([]);
    vi.spyOn(api, "listAgentProcedures").mockResolvedValue([]);
    vi.spyOn(api, "listAgentKnowledgeSeeds").mockResolvedValue([]);
    vi.spyOn(api, "fetchAllToolsWithLoadType").mockResolvedValue([tool()]);
    const createSpy = vi.spyOn(api, "createAgentKnownTool").mockResolvedValue(knownTool({ tool_name: "mcp__repo__ls" }));
    const updateSpy = vi.spyOn(api, "updateAgentKnownTool").mockResolvedValue(knownTool({ reason: "Updated" }));
    const deleteSpy = vi.spyOn(api, "deleteAgentKnownTool").mockResolvedValue({ status: "ok" });

    renderPanel();

    expect(await screen.findByRole("button", { name: "Edit mcp__docs__search" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Add Known Tool" }));
    fireEvent.change(screen.getByLabelText("Known tool name"), { target: { value: "mcp__repo__ls" } });
    fireEvent.change(screen.getByLabelText("Known tool sort order"), { target: { value: "7" } });
    fireEvent.change(screen.getByLabelText("Known tool ttl seconds"), { target: { value: "90" } });
    fireEvent.change(screen.getByLabelText("Known tool reason"), { target: { value: "List files first" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith("agent-1", {
        tool_name: "mcp__repo__ls",
        pinned: true,
        sort_order: 7,
        ttl_seconds: 90,
        reason: "List files first",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Edit mcp__docs__search" }));
    fireEvent.change(screen.getByLabelText("Known tool reason"), { target: { value: "Updated" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateSpy).toHaveBeenCalledWith("agent-1", "mcp__docs__search", {
        tool_name: "mcp__docs__search",
        pinned: true,
        sort_order: 5,
        ttl_seconds: 300,
        reason: "Updated",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete mcp__docs__search" }));
    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("agent-1", "mcp__docs__search"));
  });

  it("creates, updates, and deletes known skills", async () => {
    mockBaseQueries();
    vi.spyOn(api, "listAgentKnownSkills").mockResolvedValue([knownSkill()]);
    const createSpy = vi.spyOn(api, "createAgentKnownSkill").mockResolvedValue(
      knownSkill({ skill_name: "Repo Memory" }),
    );
    const updateSpy = vi.spyOn(api, "updateAgentKnownSkill").mockResolvedValue(
      knownSkill({ reason: "Updated" }),
    );
    const deleteSpy = vi.spyOn(api, "deleteAgentKnownSkill").mockResolvedValue({ status: "ok" });

    renderPanel({ agentSkills: [skill()], availableSkills: [skill({ id: "skill-2", name: "Repo Memory" })] });

    expect(await screen.findByText("Docs Search")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Add Known Skill" }));
    fireEvent.change(screen.getByLabelText("Known skill name"), { target: { value: "Repo Memory" } });
    fireEvent.change(screen.getByLabelText("Known skill ttl seconds"), { target: { value: "45" } });
    fireEvent.change(screen.getByLabelText("Known skill reason"), { target: { value: "Keep warm" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith("agent-1", {
        skill_name: "Repo Memory",
        pinned: true,
        ttl_seconds: 45,
        reason: "Keep warm",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Edit Docs Search" }));
    fireEvent.change(screen.getByLabelText("Known skill reason"), { target: { value: "Updated" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateSpy).toHaveBeenCalledWith("agent-1", "Docs Search", {
        skill_name: "Docs Search",
        pinned: true,
        ttl_seconds: 120,
        reason: "Updated",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete Docs Search" }));
    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("agent-1", "Docs Search"));
  });

  it("creates, updates, and deletes procedures", async () => {
    mockBaseQueries();
    vi.spyOn(api, "listAgentProcedures").mockResolvedValue([procedure()]);
    const createSpy = vi.spyOn(api, "createAgentProcedure").mockResolvedValue(
      procedure({ name: "triage", body: "Triage alerts." }),
    );
    const updateSpy = vi.spyOn(api, "updateAgentProcedure").mockResolvedValue(
      procedure({ body: "Updated body" }),
    );
    const deleteSpy = vi.spyOn(api, "deleteAgentProcedure").mockResolvedValue({ status: "ok" });

    renderPanel();

    expect(await screen.findByText("handoff")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "New Procedure" }));
    fireEvent.change(screen.getByLabelText("Procedure name"), { target: { value: "triage" } });
    fireEvent.change(screen.getByLabelText("Procedure body"), { target: { value: "Triage alerts." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith("agent-1", {
        name: "triage",
        body: "Triage alerts.",
        scope: "agent",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Edit handoff" }));
    fireEvent.change(screen.getByLabelText("Procedure body"), { target: { value: "Updated body" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateSpy).toHaveBeenCalledWith("agent-1", "handoff", {
        name: "handoff",
        body: "Updated body",
        scope: "agent",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Delete handoff" }));
    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("agent-1", "handoff"));
  });

  it("creates, updates, deletes, and marks knowledge seeds applied", async () => {
    mockBaseQueries();
    vi.spyOn(api, "listAgentKnowledgeSeeds").mockResolvedValue([seed()]);
    const createSpy = vi.spyOn(api, "createAgentKnowledgeSeed").mockResolvedValue(
      seed({ seed_key: "ops-runbook", namespace: "ops" }),
    );
    const updateSpy = vi.spyOn(api, "updateAgentKnowledgeSeed").mockResolvedValue(
      seed({ body: "Updated body" }),
    );
    const deleteSpy = vi.spyOn(api, "deleteAgentKnowledgeSeed").mockResolvedValue({ status: "ok" });
    const markSpy = vi.spyOn(api, "markAgentKnowledgeSeedApplied").mockResolvedValue(
      seed({ applied_at: "2026-05-24T12:30:00Z" }),
    );

    renderPanel();

    expect(await screen.findByText("repo-layout")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "New Seed" }));
    fireEvent.change(screen.getByLabelText("Knowledge seed key"), { target: { value: "ops-runbook" } });
    fireEvent.change(screen.getByLabelText("Knowledge seed namespace"), { target: { value: "ops" } });
    fireEvent.change(screen.getByLabelText("Knowledge seed tags"), { target: { value: "runbook, ops" } });
    fireEvent.change(screen.getByLabelText("Knowledge seed body"), { target: { value: "Escalate via runbook." } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(createSpy).toHaveBeenCalledWith("agent-1", {
        seed_key: "ops-runbook",
        namespace: "ops",
        body: "Escalate via runbook.",
        tags: ["runbook", "ops"],
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Edit seed repo-layout" }));
    fireEvent.change(screen.getByLabelText("Knowledge seed body"), { target: { value: "Updated body" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateSpy).toHaveBeenCalledWith("agent-1", "repo-layout", {
        seed_key: "repo-layout",
        namespace: "project",
        body: "Updated body",
        tags: ["repo", "ui"],
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Mark seed repo-layout applied" }));
    await waitFor(() => expect(markSpy).toHaveBeenCalledWith("agent-1", "repo-layout"));

    fireEvent.click(screen.getByRole("button", { name: "Delete seed repo-layout" }));
    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith("agent-1", "repo-layout"));
  });

  it("disables mutation controls for read-only agents", async () => {
    mockBaseQueries();
    vi.spyOn(api, "listAgentKnownTools").mockResolvedValue([knownTool()]);
    vi.spyOn(api, "listAgentKnowledgeSeeds").mockResolvedValue([seed()]);
    const removeSkill = vi.fn();
    const updateAgent = vi.fn();

    renderPanel({
      profile: agent({ source: "internal" }),
      agentSkills: [skill()],
      availableSkills: [skill({ id: "skill-2", name: "Repo Memory" })],
      agentTemplates: [template()],
      availableTemplates: [template({ id: "template-2", name: "Mode Reminder" })],
      onRemoveSkill: removeSkill,
      onUpdateAgent: updateAgent,
    });

    expect(await screen.findByRole("button", { name: "Edit mcp__docs__search" })).toBeTruthy();
    expect((screen.getAllByRole("button", { name: "Assign" })[0] as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByLabelText("Remove skill Docs Search") as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Add Known Tool" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Edit mcp__docs__search" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Mark seed repo-layout applied" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByLabelText("Remove skill Docs Search"));
    expect(removeSkill).not.toHaveBeenCalled();
    expect(updateAgent).not.toHaveBeenCalled();
  });

  it("shows loading, empty, and error states", async () => {
    vi.spyOn(api, "listAgentKnownTools").mockRejectedValue(new Error("known tools boom"));
    vi.spyOn(api, "listAgentKnownSkills").mockResolvedValue([]);
    vi.spyOn(api, "listAgentProcedures").mockResolvedValue([]);
    vi.spyOn(api, "listAgentKnowledgeSeeds").mockResolvedValue([]);
    vi.spyOn(api, "fetchAllToolsWithLoadType").mockRejectedValue(new Error("tool catalog boom"));

    renderPanel();

    expect(await screen.findByText("known tools boom")).toBeTruthy();
    expect(screen.getByText("No known skills recorded for this agent.")).toBeTruthy();
    expect(screen.getByText("No procedures configured for this agent.")).toBeTruthy();
    expect(screen.getByText("No knowledge seeds configured for this agent.")).toBeTruthy();
    expect(screen.getByText("Failed to load tools: tool catalog boom")).toBeTruthy();
  });
});
