package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/mentat/internal/builders"
	"github.com/hollis-labs/mentat/internal/store"
)

// SelfToolsTransport provides self-service tools that let the agent
// create and manage its own skills, agent profiles, and workflows
// through the same store layer the API uses.
type SelfToolsTransport struct {
	Store           *store.Store
	BuilderRegistry *builders.Registry
	BuilderSessions *builders.SessionManager
}

// NewSelfToolsTransport creates a SelfToolsTransport backed by the given store.
func NewSelfToolsTransport(s *store.Store) *SelfToolsTransport {
	return &SelfToolsTransport{
		Store:           s,
		BuilderRegistry: builders.DefaultRegistry(s),
		BuilderSessions: builders.NewSessionManager(),
	}
}

// ListTools returns all self-service tool definitions.
func (st *SelfToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	return selfToolDefinitions(), nil
}

// CallTool dispatches to the appropriate handler based on tool name.
func (st *SelfToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
	switch name {
	case "mentat_create_skill":
		return st.callCreateSkill(args)
	case "mentat_list_skills":
		return st.callListSkills(args)
	case "mentat_update_skill":
		return st.callUpdateSkill(args)
	case "mentat_delete_skill":
		return st.callDeleteSkill(args)
	case "mentat_create_agent":
		return st.callCreateAgent(args)
	case "mentat_list_agents":
		return st.callListAgents(args)
	case "mentat_update_agent":
		return st.callUpdateAgent(args)
	case "mentat_list_workflows":
		return st.callListWorkflows(args)
	case "mentat_create_workflow":
		return st.callCreateWorkflow(args)
	case "mentat_open_sprint_planning":
		return textResult("Sprint planning modal opened in the UI."), nil
	case "mentat_start_builder":
		return st.callStartBuilder(args)
	case "mentat_builder_step":
		return st.callBuilderStep(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

// --- skill handlers ---

func (st *SelfToolsTransport) callCreateSkill(args map[string]any) (*ToolResult, error) {
	name, _ := args["name"].(string)
	slug, _ := args["slug"].(string)
	desc, _ := args["description"].(string)
	if name == "" || slug == "" || desc == "" {
		return errorResult("name, slug, and description are required"), nil
	}

	sk := &store.Skill{
		Name:         name,
		Slug:         slug,
		Description:  desc,
		Category:     strArg(args, "category", "custom"),
		ToolBindings: strArg(args, "tool_bindings", "[]"),
		InputSchema:  strArg(args, "input_schema", "{}"),
	}

	if err := st.Store.CreateSkill(sk); err != nil {
		return errorResult(fmt.Sprintf("create skill: %v", err)), nil
	}

	out, _ := json.Marshal(sk)
	return textResult(fmt.Sprintf("Created skill %q (id=%s)\n%s", sk.Name, sk.ID, string(out))), nil
}

func (st *SelfToolsTransport) callListSkills(args map[string]any) (*ToolResult, error) {
	skills, err := st.Store.ListSkills()
	if err != nil {
		return errorResult(fmt.Sprintf("list skills: %v", err)), nil
	}

	categoryFilter, _ := args["category"].(string)
	if categoryFilter != "" {
		filtered := make([]store.Skill, 0)
		for _, sk := range skills {
			if strings.EqualFold(sk.Category, categoryFilter) {
				filtered = append(filtered, sk)
			}
		}
		skills = filtered
	}

	if len(skills) == 0 {
		return textResult("No skills found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d skill(s):\n\n", len(skills))
	for _, sk := range skills {
		fmt.Fprintf(&sb, "- %s (id=%s, slug=%s, category=%s)\n  %s\n",
			sk.Name, sk.ID, sk.Slug, sk.Category, sk.Description)
	}
	return textResult(sb.String()), nil
}

func (st *SelfToolsTransport) callUpdateSkill(args map[string]any) (*ToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return errorResult("id is required"), nil
	}

	sk, err := st.Store.GetSkill(id)
	if err != nil {
		return errorResult(fmt.Sprintf("get skill: %v", err)), nil
	}
	if sk == nil {
		return errorResult(fmt.Sprintf("skill %q not found", id)), nil
	}

	if v, ok := args["name"].(string); ok && v != "" {
		sk.Name = v
	}
	if v, ok := args["slug"].(string); ok && v != "" {
		sk.Slug = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		sk.Description = v
	}
	if v, ok := args["category"].(string); ok && v != "" {
		sk.Category = v
	}
	if v, ok := args["tool_bindings"].(string); ok && v != "" {
		sk.ToolBindings = v
	}
	if v, ok := args["input_schema"].(string); ok && v != "" {
		sk.InputSchema = v
	}

	if err := st.Store.UpdateSkill(sk); err != nil {
		return errorResult(fmt.Sprintf("update skill: %v", err)), nil
	}
	return textResult(fmt.Sprintf("Updated skill %q (id=%s)", sk.Name, sk.ID)), nil
}

func (st *SelfToolsTransport) callDeleteSkill(args map[string]any) (*ToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return errorResult("id is required"), nil
	}

	if err := st.Store.DeleteSkill(id); err != nil {
		return errorResult(fmt.Sprintf("delete skill: %v", err)), nil
	}
	return textResult(fmt.Sprintf("Deleted skill %s", id)), nil
}

// --- agent handlers ---

func (st *SelfToolsTransport) callCreateAgent(args map[string]any) (*ToolResult, error) {
	name, _ := args["name"].(string)
	slug, _ := args["slug"].(string)
	prompt, _ := args["system_prompt"].(string)
	if name == "" || slug == "" || prompt == "" {
		return errorResult("name, slug, and system_prompt are required"), nil
	}

	a := &store.AgentProfile{
		Name:         name,
		Slug:         slug,
		SystemPrompt: prompt,
		Description:  strArg(args, "description", ""),
		DefaultModel: strArg(args, "default_model", ""),
	}

	if err := st.Store.CreateAgent(a); err != nil {
		return errorResult(fmt.Sprintf("create agent: %v", err)), nil
	}

	out, _ := json.Marshal(a)
	return textResult(fmt.Sprintf("Created agent %q (id=%s)\n%s", a.Name, a.ID, string(out))), nil
}

func (st *SelfToolsTransport) callListAgents(args map[string]any) (*ToolResult, error) {
	agents, err := st.Store.ListAgents()
	if err != nil {
		return errorResult(fmt.Sprintf("list agents: %v", err)), nil
	}

	if len(agents) == 0 {
		return textResult("No agent profiles found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d agent(s):\n\n", len(agents))
	for _, a := range agents {
		model := a.DefaultModel
		if model == "" {
			model = "(default)"
		}
		fmt.Fprintf(&sb, "- %s (id=%s, slug=%s, model=%s)\n  %s\n",
			a.Name, a.ID, a.Slug, model, a.Description)
	}
	return textResult(sb.String()), nil
}

func (st *SelfToolsTransport) callUpdateAgent(args map[string]any) (*ToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return errorResult("id is required"), nil
	}

	a, err := st.Store.GetAgent(id)
	if err != nil {
		return errorResult(fmt.Sprintf("get agent: %v", err)), nil
	}

	if v, ok := args["name"].(string); ok && v != "" {
		a.Name = v
	}
	if v, ok := args["slug"].(string); ok && v != "" {
		a.Slug = v
	}
	if v, ok := args["system_prompt"].(string); ok && v != "" {
		a.SystemPrompt = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		a.Description = v
	}
	if v, ok := args["default_model"].(string); ok && v != "" {
		a.DefaultModel = v
	}

	if err := st.Store.UpdateAgent(a); err != nil {
		return errorResult(fmt.Sprintf("update agent: %v", err)), nil
	}
	return textResult(fmt.Sprintf("Updated agent %q (id=%s)", a.Name, a.ID)), nil
}

// --- workflow handlers ---

func (st *SelfToolsTransport) callListWorkflows(args map[string]any) (*ToolResult, error) {
	workflows, err := st.Store.ListWorkflows()
	if err != nil {
		return errorResult(fmt.Sprintf("list workflows: %v", err)), nil
	}

	if len(workflows) == 0 {
		return textResult("No workflows found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d workflow(s):\n\n", len(workflows))
	for _, w := range workflows {
		enabled := "enabled"
		if !w.IsEnabled {
			enabled = "disabled"
		}
		fmt.Fprintf(&sb, "- %s (id=%s, slug=%s, trigger=%s, %s)\n",
			w.Name, w.ID, w.Slug, w.Trigger, enabled)
	}
	return textResult(sb.String()), nil
}

func (st *SelfToolsTransport) callCreateWorkflow(args map[string]any) (*ToolResult, error) {
	name, _ := args["name"].(string)
	slug, _ := args["slug"].(string)
	definition, _ := args["definition"].(string)
	if name == "" || slug == "" || definition == "" {
		return errorResult("name, slug, and definition are required"), nil
	}

	isEnabled := true
	if v, ok := args["is_enabled"].(bool); ok {
		isEnabled = v
	}

	w := &store.Workflow{
		Name:       name,
		Slug:       slug,
		Trigger:    strArg(args, "trigger", "manual"),
		Definition: definition,
		IsEnabled:  isEnabled,
	}

	if err := st.Store.CreateWorkflow(w); err != nil {
		return errorResult(fmt.Sprintf("create workflow: %v", err)), nil
	}

	out, _ := json.Marshal(w)
	return textResult(fmt.Sprintf("Created workflow %q (id=%s)\n%s", w.Name, w.ID, string(out))), nil
}

// --- builder handlers ---

func (st *SelfToolsTransport) callStartBuilder(args map[string]any) (*ToolResult, error) {
	// Use a fixed session key — builders are per-transport, not per-chat-session.
	// The chat session ID would be better but isn't available here.
	sessionKey := "default"
	result, err := builders.HandleStartBuilder(st.BuilderRegistry, st.BuilderSessions, sessionKey, args)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	return textResult(result), nil
}

func (st *SelfToolsTransport) callBuilderStep(args map[string]any) (*ToolResult, error) {
	sessionKey := "default"
	result, err := builders.HandleBuilderStep(st.BuilderRegistry, st.BuilderSessions, sessionKey, args)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	return textResult(result), nil
}

// --- helpers ---

func strArg(args map[string]any, key, def string) string {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return def
	}
	return v
}
