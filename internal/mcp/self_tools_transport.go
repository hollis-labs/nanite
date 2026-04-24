package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/builders"
	"github.com/hollis-labs/nanite/internal/crossapp"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/service/install"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// WorkBroadcaster notifies connected UI clients that session-scoped todos or
// plans have changed so the Work drawer can refresh. Defined as a local
// interface to avoid importing the service package (which would be circular).
// CW-20260418-0044.
type WorkBroadcaster interface {
	BroadcastWorkChanged()
}

// TodoStoreInterface is the subset of store.Store needed by todo/plan MCP tools.
// Defined here to avoid circular imports with the service package. Uses raw
// store methods instead of the service layer's update structs.
type TodoStoreInterface interface {
	CreateTodo(t *store.Todo) error
	GetTodo(id string) (*store.Todo, error)
	ListTodos(f store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(t *store.Todo) error
	DeleteTodo(id string) error

	CreatePlan(p *store.Plan) error
	GetPlan(id string) (*store.Plan, error)
	ListPlans(f store.PlanFilter) ([]store.Plan, error)
	UpdatePlan(p *store.Plan) error
	UpdatePlanStep(planID, stepID string, updates store.PlanStep) error
	DeletePlan(id string) error
}

// SelfToolsTransport provides self-service tools that let the agent
// create and manage its own skills, agent profiles, and workflows
// through the same store layer the API uses.
type SelfToolsTransport struct {
	Store           *store.Store
	BuilderRegistry *builders.Registry
	BuilderSessions *builders.SessionManager
	TodoStore       TodoStoreInterface // nil-safe; set after construction
	// Messaging is set post-construction from the container; nil-safe.
	Messaging *messaging.Service
	// Subagent is set post-construction from the container; nil-safe.
	Subagent *subagent.Service
	// Work is set post-construction from the container; nil-safe. When set,
	// mutating todo/plan tools fire BroadcastWorkChanged after a successful
	// write so the Work drawer rehydrates. CW-20260418-0044.
	Work WorkBroadcaster
}

// notifyWorkChanged fires a work_changed presence broadcast if a broadcaster
// is wired. Called after every successful mutating todo/plan tool call.
func (st *SelfToolsTransport) notifyWorkChanged() {
	if st.Work != nil {
		st.Work.BroadcastWorkChanged()
	}
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

// messageCallTimeout bounds every unary messaging tool call so a wedged store or
// slow subscriber can't hang the MCP handler forever. Subscription
// handlers use the parent ctx directly (lifetime-scoped) instead of this
// timeout — see callMessageSubscribe when it lands.
const messageCallTimeout = 30 * time.Second

// CallTool dispatches to the appropriate handler based on tool name.
// The request ctx is threaded to every handler; messaging handlers further
// wrap it with a 30s timeout so a wedged service call cannot block the
// MCP stream indefinitely (F06).
func (st *SelfToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	switch name {
	case "nanite_create_skill":
		return st.callCreateSkill(args)
	case "nanite_list_skills":
		return st.callListSkills(args)
	case "nanite_update_skill":
		return st.callUpdateSkill(args)
	case "nanite_delete_skill":
		return st.callDeleteSkill(args)
	case "nanite_create_agent":
		return st.callCreateAgent(args)
	case "nanite_list_agents":
		return st.callListAgents(args)
	case "nanite_update_agent":
		return st.callUpdateAgent(args)
	case "nanite_navigate_engine":
		return st.callNavigateEngine(args)
	case "nanite_refresh_engine":
		return st.callRefreshEngine(args)
	case "nanite_show_giphy":
		return st.callShowGiphy(args)
	case "nanite_show_document":
		return st.callShowDocument(args)
	case "nanite_show_report":
		return st.callShowReport(args)
	case "nanite_start_builder":
		return st.callStartBuilder(args)
	case "nanite_builder_step":
		return st.callBuilderStep(args)
	case "nanite_todo_create":
		return st.callTodoCreate(ctx, args)
	case "nanite_todo_update":
		return st.callTodoUpdate(args)
	case "nanite_todo_list":
		return st.callTodoList(ctx, args)
	case "nanite_plan_create":
		return st.callPlanCreate(ctx, args)
	case "nanite_plan_update":
		return st.callPlanUpdate(args)
	case "nanite_plan_list":
		return st.callPlanList(ctx, args)
	case "nanite_plan_get":
		return st.callPlanGet(args)
	case "nanite_plan_delete":
		return st.callPlanDelete(args)
	case "nanite_install_home":
		return st.callInstallHome(args)
	case "nanite_install_project":
		return st.callInstallProject(args)
	case "nanite_install_rollback":
		return st.callInstallRollback(args)
	case "nanite_install_diff":
		return st.callInstallDiff(args)
	case "nanite_message_send":
		return st.callMessageSend(ctx, args)
	case "nanite_message_inbox":
		return st.callMessageInbox(ctx, args)
	case "nanite_message_thread":
		return st.callMessageThread(ctx, args)
	case "nanite_message_ack":
		return st.callMessageAck(ctx, args)
	case "nanite_message_resolve":
		return st.callMessageResolve(ctx, args)
	case "nanite_message_catch_up":
		return st.callMessageCatchUp(ctx, args)
	case "nanite_handoff_request":
		return st.callHandoffRequest(ctx, args)
	case "nanite_handoff_approve":
		return st.callHandoffApprove(ctx, args)
	case "nanite_handoff_reject":
		return st.callHandoffReject(ctx, args)
	case "nanite_spawn_subagent":
		return st.callSpawnSubagent(ctx, args)
	case "nanite_subagent_status":
		return st.callSubagentStatus(ctx, args)
	case "nanite_subagent_cancel":
		return st.callSubagentCancel(ctx, args)
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
	id := strArg(args, "id", "")
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
	id := strArg(args, "id", "")
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
	id := strArg(args, "id", "")
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

// --- cross-app handlers ---

func (st *SelfToolsTransport) callNavigateEngine(args map[string]any) (*ToolResult, error) {
	page, _ := args["page"].(string)
	if page == "" {
		return errorResult("page is required"), nil
	}

	// Build params map from all optional arguments.
	params := make(map[string]string)
	for _, key := range []string{"id", "project_id", "status", "priority", "sort"} {
		if v, ok := args[key].(string); ok && v != "" {
			params[key] = v
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := crossapp.NavigateEngine(ctx, page, params); err != nil {
		return textResult(fmt.Sprintf("Navigation failed — Engine may be offline: %v", err)), nil
	}

	// CW-20260419-0013 (user-reported via c17): the LLM-coaching trailer
	// ("Tell the user what you navigated to in one sentence. Do NOT call
	// any more tools.") leaked into the user-visible chat surface. Tool
	// result now reports only what happened; the tool description already
	// instructs the LLM how to behave.
	msg := fmt.Sprintf("Navigated Engine GUI to %s", page)
	if len(params) > 0 {
		msg += fmt.Sprintf(" (filters: %v)", params)
	}
	return textResult(msg), nil
}

func (st *SelfToolsTransport) callRefreshEngine(args map[string]any) (*ToolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := crossapp.RefreshEngine(ctx); err != nil {
		return textResult(fmt.Sprintf("Refresh failed — Engine may be offline: %v", err)), nil
	}
	// CW-20260419-0013: LLM-coaching trailer removed (see callNavigateEngine).
	return textResult("Engine GUI data refreshed."), nil
}

func (st *SelfToolsTransport) callShowGiphy(args map[string]any) (*ToolResult, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return errorResult("query is required"), nil
	}

	apiKey := os.Getenv("GIPHY_API_KEY")
	if apiKey == "" {
		// No API key — pick a demo GIF based on query keywords.
		demoGifs := map[string]string{
			"celebration": "https://media.giphy.com/media/g9582DNuQppxC/giphy.gif",
			"success":     "https://media.giphy.com/media/a0h7sAqON67nO/giphy.gif",
			"thumbs up":   "https://media.giphy.com/media/111ebonMs90YLu/giphy.gif",
			"mind blown":  "https://media.giphy.com/media/xT0xeJpnrWC3XWblEk/giphy.gif",
			"happy":       "https://media.giphy.com/media/BlVnrxJgTGsUw/giphy.gif",
			"dance":       "https://media.giphy.com/media/l0MYt5jPR6QX5APm0/giphy.gif",
			"cat":         "https://media.giphy.com/media/JIX9t2j0ZTN9S/giphy.gif",
			"dog":         "https://media.giphy.com/media/4Zo41lhzKt6iZ8xff9/giphy.gif",
			"hamster":     "https://media.giphy.com/media/l2JhIUyUs8KDCCf3W/giphy.gif",
			"running":     "https://media.giphy.com/media/11BAxHG7paxJcI/giphy.gif",
			"coding":      "https://media.giphy.com/media/ZVik7pBtu9dNS/giphy.gif",
			"coffee":      "https://media.giphy.com/media/DrJm6F9poo4aA/giphy.gif",
			"rocket":      "https://media.giphy.com/media/mi6DsSSNKDbUY/giphy.gif",
			"fire":        "https://media.giphy.com/media/j3IxJRLNLZz9sXR7ZA/giphy.gif",
		}
		// Match query words against known keys, fallback to a random one.
		gifURL := ""
		lowerQ := strings.ToLower(query)
		for keyword, url := range demoGifs {
			if strings.Contains(lowerQ, keyword) {
				gifURL = url
				break
			}
		}
		if gifURL == "" {
			// Pick based on hash of query for consistent but varied results.
			keys := make([]string, 0, len(demoGifs))
			for k := range demoGifs {
				keys = append(keys, k)
			}
			h := 0
			for _, c := range query {
				h = h*31 + int(c)
			}
			if h < 0 {
				h = -h
			}
			gifURL = demoGifs[keys[h%len(keys)]]
		}

		envData := map[string]any{
			"title":   fmt.Sprintf("Here's your %s!", query),
			"gif_url": gifURL,
			"source":  "GIPHY (demo mode)",
			"query":   query,
		}
		envJSON, _ := json.Marshal(map[string]any{
			"kind":    "envelope",
			"version": 1,
			"type":    "giphy-modal",
			"data":    envData,
		})
		result := fmt.Sprintf("Found a GIF for %q! (demo mode — set GIPHY_API_KEY for live search)\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", query, string(envJSON))
		return textResult(result), nil
	}

	giphyURL := fmt.Sprintf("https://api.giphy.com/v1/gifs/search?api_key=%s&q=%s&limit=1&rating=g",
		apiKey, url.QueryEscape(query))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, giphyURL, nil)
	if err != nil {
		return errorResult(fmt.Sprintf("create Giphy request: %v", err)), nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errorResult(fmt.Sprintf("Giphy API error: %v", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return errorResult(fmt.Sprintf("read Giphy response: %v", err)), nil
	}

	var giphyResp struct {
		Data []struct {
			Title  string `json:"title"`
			Images struct {
				Original struct {
					URL string `json:"url"`
				} `json:"original"`
				FixedWidth struct {
					URL string `json:"url"`
				} `json:"fixed_width"`
			} `json:"images"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &giphyResp); err != nil {
		return errorResult(fmt.Sprintf("parse Giphy response: %v", err)), nil
	}

	if len(giphyResp.Data) == 0 {
		return textResult(fmt.Sprintf("No GIFs found for %q. Try a different search term.", query)), nil
	}

	gif := giphyResp.Data[0]
	gifURL := gif.Images.Original.URL
	if gifURL == "" {
		gifURL = gif.Images.FixedWidth.URL
	}

	// Build envelope data for the giphy-modal component.
	envData := map[string]any{
		"title":   fmt.Sprintf("Here's your %s!", query),
		"gif_url": gifURL,
		"source":  "GIPHY",
		"query":   query,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "giphy-modal",
		"data":    envData,
	})

	// Return with envelope marker for engine.go to extract.
	result := fmt.Sprintf("Found a GIF for %q!\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", query, string(envJSON))
	return textResult(result), nil
}

// --- envelope injection handlers ---

func (st *SelfToolsTransport) callShowDocument(args map[string]any) (*ToolResult, error) {
	title, _ := args["title"].(string)
	content, _ := args["content"].(string)
	if title == "" || content == "" {
		return errorResult("title and content are required"), nil
	}

	// CW-20260419-0022 (UAT c19): the user expects LIVE data in rendered
	// documents. Reject calls without documented sources so the LLM can't
	// fabricate content from memory / pattern completion without at least
	// declaring what it grounded on. This is a shallow check — it validates
	// shape, not that the cited tool_use_ids were actually called in this
	// generation. The deep check is tracked in the grounding-design ticket.
	sources, sourcesErr := parseSourcesArg(args)
	if sourcesErr != nil {
		return errorResult(sourcesErr.Error()), nil
	}

	format := strArg(args, "format", "markdown")
	downloadFilename := strArg(args, "download_filename", "")

	envData := map[string]any{
		"title":   title,
		"content": content,
		"format":  format,
		"sources": sources,
	}
	if downloadFilename != "" {
		envData["download_filename"] = downloadFilename
		envData["download_enabled"] = true
	}
	if sections, _ := args["sections"].(string); sections != "" {
		var sectionList []string
		for _, s := range strings.Split(sections, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				sectionList = append(sectionList, s)
			}
		}
		envData["sections"] = sectionList
	}

	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "document-viewer",
		"data":    envData,
	})

	result := fmt.Sprintf("Document ready: %s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", title, string(envJSON))
	return textResult(result), nil
}

func (st *SelfToolsTransport) callShowReport(args map[string]any) (*ToolResult, error) {
	title, _ := args["title"].(string)
	metricsStr, _ := args["metrics"].(string)
	if title == "" || metricsStr == "" {
		return errorResult("title and metrics are required"), nil
	}

	var metrics []any
	if err := json.Unmarshal([]byte(metricsStr), &metrics); err != nil {
		return errorResult(fmt.Sprintf("invalid metrics JSON: %v", err)), nil
	}

	// CW-20260419-0022 (UAT c19): see callShowDocument for rationale.
	sources, sourcesErr := parseSourcesArg(args)
	if sourcesErr != nil {
		return errorResult(sourcesErr.Error()), nil
	}

	envData := map[string]any{
		"title":        title,
		"generated_at": time.Now().Format(time.RFC3339),
		"metrics":      metrics,
		"sources":      sources,
	}
	if summary, _ := args["summary"].(string); summary != "" {
		envData["summary"] = summary
	}
	if actionsStr, _ := args["actions"].(string); actionsStr != "" {
		var actions []any
		if err := json.Unmarshal([]byte(actionsStr), &actions); err == nil {
			envData["actions"] = actions
		}
	}

	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "report-card",
		"data":    envData,
	})

	result := fmt.Sprintf("Report: %s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", title, string(envJSON))
	return textResult(result), nil
}

// --- todo/plan handlers ---

func (st *SelfToolsTransport) callTodoCreate(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}
	title, _ := args["title"].(string)
	scope, _ := args["scope"].(string)
	if title == "" || scope == "" {
		return errorResult("title and scope are required"), nil
	}

	// CW-20260418 (c7 scope_id fix): auto-fill scope_id from ctx when the
	// agent omits it. The LLM has no way to know its session_id so for
	// scope=session we must source it from the context stamped by the
	// chat tool executor. Workspace scope legitimately has no scope_id.
	scopeID := strArg(args, "scope_id", "")
	if scope != "workspace" && scopeID == "" {
		if sid := SessionIDFromContext(ctx); sid != "" {
			scopeID = sid
		} else {
			return errorResult(fmt.Sprintf("scope_id is required for scope %q (no current session in context)", scope)), nil
		}
	}

	t := &store.Todo{
		Title:       title,
		Scope:       scope,
		ScopeID:     scopeID,
		Priority:    strArg(args, "priority", "medium"),
		Description: strArg(args, "description", ""),
		ParentID:    strArg(args, "parent_id", ""),
		Labels:      strArg(args, "labels", "[]"),
		CreatedBy:   "agent",
	}

	if err := st.TodoStore.CreateTodo(t); err != nil {
		return errorResult(fmt.Sprintf("create todo: %v", err)), nil
	}

	st.notifyWorkChanged()
	out, _ := json.Marshal(t)
	return textResult(fmt.Sprintf("Created todo %q (id=%s, scope=%s)\n%s", t.Title, t.ID, t.Scope, string(out))), nil
}

func (st *SelfToolsTransport) callTodoUpdate(args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return errorResult("id is required"), nil
	}

	t, err := st.TodoStore.GetTodo(id)
	if err != nil {
		return errorResult(fmt.Sprintf("get todo: %v", err)), nil
	}

	if v, ok := args["title"].(string); ok && v != "" {
		t.Title = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		t.Description = v
	}
	if v, ok := args["status"].(string); ok && v != "" {
		t.Status = v
	}
	if v, ok := args["priority"].(string); ok && v != "" {
		t.Priority = v
	}
	if v, ok := args["labels"].(string); ok && v != "" {
		t.Labels = v
	}

	if err := st.TodoStore.UpdateTodo(t); err != nil {
		return errorResult(fmt.Sprintf("update todo: %v", err)), nil
	}

	st.notifyWorkChanged()
	return textResult(fmt.Sprintf("Updated todo %q (id=%s, status=%s, priority=%s)", t.Title, t.ID, t.Status, t.Priority)), nil
}

func (st *SelfToolsTransport) callTodoList(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}

	// CW-20260418 (c7 scope_id fix): for scope=session, auto-fill scope_id
	// from ctx when omitted so the emitted todo-list envelope carries the
	// REAL session id. Otherwise TodoListCard lazy-fetches with scope_id=""
	// and the drawer renders nothing. Listing itself still works fine with
	// an empty filter, so this is best-effort — no error path.
	scopeArg := strArg(args, "scope", "")
	scopeIDArg := strArg(args, "scope_id", "")
	if scopeArg == "session" && scopeIDArg == "" {
		if sid := SessionIDFromContext(ctx); sid != "" {
			scopeIDArg = sid
		}
	}

	f := store.TodoFilter{
		Scope:    scopeArg,
		ScopeID:  scopeIDArg,
		Status:   strArg(args, "status", ""),
		Priority: strArg(args, "priority", ""),
	}

	todos, err := st.TodoStore.ListTodos(f)
	if err != nil {
		return errorResult(fmt.Sprintf("list todos: %v", err)), nil
	}

	var sb strings.Builder
	if len(todos) == 0 {
		sb.WriteString("No todos found matching filters.")
	} else {
		fmt.Fprintf(&sb, "Found %d todo(s):\n\n", len(todos))
		for _, t := range todos {
			fmt.Fprintf(&sb, "- [%s] %s (id=%s, priority=%s, scope=%s/%s)\n",
				t.Status, t.Title, t.ID, t.Priority, t.Scope, t.ScopeID)
			if t.Description != "" {
				fmt.Fprintf(&sb, "  %s\n", t.Description)
			}
		}
	}

	// Emit a todo-list envelope so the UI renders an interactive card. The
	// TodoListCard component lazy-fetches /api/todos by scope + scope_id, so
	// the envelope only needs to carry the filter coordinates — not the items
	// themselves. We only attach the envelope when scope is present; an empty
	// scope would render an un-scoped card that matches every session.
	// CW-20260418-0045.
	if f.Scope != "" {
		title := strArg(args, "title", "Todos")
		envJSON, _ := json.Marshal(map[string]any{
			"kind":    "envelope",
			"version": 1,
			"type":    "todo-list",
			"data": map[string]any{
				"scope":    f.Scope,
				"scope_id": f.ScopeID,
				"title":    title,
			},
		})
		fmt.Fprintf(&sb, "\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", string(envJSON))
	}

	return textResult(sb.String()), nil
}

func (st *SelfToolsTransport) callPlanCreate(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}
	title, _ := args["title"].(string)
	scope, _ := args["scope"].(string)
	if title == "" || scope == "" {
		return errorResult("title and scope are required"), nil
	}

	// CW-20260418 (c7 scope_id fix): same auto-fill as callTodoCreate.
	scopeID := strArg(args, "scope_id", "")
	if scope != "workspace" && scopeID == "" {
		if sid := SessionIDFromContext(ctx); sid != "" {
			scopeID = sid
		} else {
			return errorResult(fmt.Sprintf("scope_id is required for scope %q (no current session in context)", scope)), nil
		}
	}

	p := &store.Plan{
		Title:       title,
		Scope:       scope,
		ScopeID:     scopeID,
		Description: strArg(args, "description", ""),
		Steps:       strArg(args, "steps", "[]"),
		CreatedBy:   "agent",
	}

	if err := st.TodoStore.CreatePlan(p); err != nil {
		return errorResult(fmt.Sprintf("create plan: %v", err)), nil
	}

	st.notifyWorkChanged()
	out, _ := json.Marshal(p)
	return textResult(fmt.Sprintf("Created plan %q (id=%s, scope=%s)\n%s", p.Title, p.ID, p.Scope, string(out))), nil
}

func (st *SelfToolsTransport) callPlanUpdate(args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return errorResult("id is required"), nil
	}

	// If step_id is provided, update just that step.
	stepID, _ := args["step_id"].(string)
	if stepID != "" {
		stepUpdates := store.PlanStep{
			Status: strArg(args, "status", ""),
			Notes:  strArg(args, "notes", ""),
		}
		if err := st.TodoStore.UpdatePlanStep(id, stepID, stepUpdates); err != nil {
			return errorResult(fmt.Sprintf("update plan step: %v", err)), nil
		}
		st.notifyWorkChanged()
		return textResult(fmt.Sprintf("Updated step %s in plan %s", stepID, id)), nil
	}

	// Otherwise update plan-level fields.
	p, err := st.TodoStore.GetPlan(id)
	if err != nil {
		return errorResult(fmt.Sprintf("get plan: %v", err)), nil
	}

	if v, ok := args["title"].(string); ok && v != "" {
		p.Title = v
	}
	if v, ok := args["status"].(string); ok && v != "" {
		p.Status = v
	}

	if err := st.TodoStore.UpdatePlan(p); err != nil {
		return errorResult(fmt.Sprintf("update plan: %v", err)), nil
	}

	st.notifyWorkChanged()
	return textResult(fmt.Sprintf("Updated plan %q (id=%s, status=%s)", p.Title, p.ID, p.Status)), nil
}

func (st *SelfToolsTransport) callPlanList(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}

	// CW-20260418 (c7 scope_id fix): auto-fill session scope_id from ctx.
	scopeArg := strArg(args, "scope", "")
	scopeIDArg := strArg(args, "scope_id", "")
	if scopeArg == "session" && scopeIDArg == "" {
		if sid := SessionIDFromContext(ctx); sid != "" {
			scopeIDArg = sid
		}
	}

	f := store.PlanFilter{
		Scope:   scopeArg,
		ScopeID: scopeIDArg,
		Status:  strArg(args, "status", ""),
	}

	plans, err := st.TodoStore.ListPlans(f)
	if err != nil {
		return errorResult(fmt.Sprintf("list plans: %v", err)), nil
	}

	if len(plans) == 0 {
		return textResult("No plans found matching filters."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d plan(s):\n\n", len(plans))
	for _, p := range plans {
		steps, _ := p.ParsePlanSteps()
		fmt.Fprintf(&sb, "- [%s] %s (id=%s, scope=%s/%s, steps=%d)\n",
			p.Status, p.Title, p.ID, p.Scope, p.ScopeID, len(steps))
		if p.Description != "" {
			fmt.Fprintf(&sb, "  %s\n", p.Description)
		}
	}
	return textResult(sb.String()), nil
}

func (st *SelfToolsTransport) callPlanGet(args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return errorResult("id is required"), nil
	}

	p, err := st.TodoStore.GetPlan(id)
	if err != nil {
		return errorResult(fmt.Sprintf("get plan: %v", err)), nil
	}

	out, _ := json.Marshal(p)
	return textResult(string(out)), nil
}

func (st *SelfToolsTransport) callPlanDelete(args map[string]any) (*ToolResult, error) {
	if st.TodoStore == nil {
		return errorResult("todo service not available"), nil
	}
	id := strArg(args, "id", "")
	if id == "" {
		return errorResult("id is required"), nil
	}

	if err := st.TodoStore.DeletePlan(id); err != nil {
		return errorResult(fmt.Sprintf("delete plan: %v", err)), nil
	}
	st.notifyWorkChanged()
	return textResult(fmt.Sprintf("Deleted plan %s", id)), nil
}

// --- install handlers ---

func (st *SelfToolsTransport) callInstallHome(args map[string]any) (*ToolResult, error) {
	force, _ := args["force"].(bool)
	svc := install.New()
	report, err := svc.InstallHome(install.InstallHomeOptions{Force: force})
	if err != nil {
		return errorResult(fmt.Sprintf("install home: %v", err)), nil
	}
	return textResult(fmt.Sprintf("install home: created=%d unchanged=%d skipped=%d forced=%d",
		report.Created, report.Unchanged, report.Skipped, report.Forced)), nil
}

func (st *SelfToolsTransport) callInstallProject(args map[string]any) (*ToolResult, error) {
	projectDir, _ := args["project_dir"].(string)
	if projectDir == "" {
		return errorResult("project_dir is required"), nil
	}
	migrate, _ := args["migrate_from_agentrc"].(bool)
	archiveOnly, _ := args["archive_only"].(bool)

	svc := install.New()
	report, err := svc.InstallProject(install.InstallProjectOptions{
		ProjectDir:         projectDir,
		MigrateFromAgentrc: migrate,
		ArchiveOnly:        archiveOnly,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("install project: %v", err)), nil
	}

	summary := fmt.Sprintf("install project %s:", projectDir)
	switch {
	case report.FreshScaffold:
		summary += " fresh scaffold"
	case report.Migrated:
		summary += fmt.Sprintf(" migrated (archive=%s)", report.ArchivePath)
	case report.Adopted:
		summary += " adopted existing"
	case report.ArchiveOnly:
		summary += fmt.Sprintf(" archive-only (archive=%s)", report.ArchivePath)
	}
	if len(report.Warnings) > 0 {
		summary += "\nwarnings:\n  - " + strings.Join(report.Warnings, "\n  - ")
	}
	return textResult(summary), nil
}

func (st *SelfToolsTransport) callInstallRollback(args map[string]any) (*ToolResult, error) {
	projectDir, _ := args["project_dir"].(string)
	if projectDir == "" {
		return errorResult("project_dir is required"), nil
	}
	archivePath, _ := args["archive_path"].(string)

	svc := install.New()
	if err := svc.Rollback(install.RollbackOptions{ProjectDir: projectDir, ArchivePath: archivePath}); err != nil {
		return errorResult(fmt.Sprintf("rollback: %v", err)), nil
	}
	return textResult("rollback complete: " + projectDir), nil
}

func (st *SelfToolsTransport) callInstallDiff(args map[string]any) (*ToolResult, error) {
	// TODO(Plan A Task 15+): implement dry-run mode in internal/service/install
	// that returns an action list without mutating state.
	_ = args
	return textResult("install diff not yet implemented"), nil
}

// --- Messaging handlers ---
//
// The nanite_message_subscribe tool is intentionally not registered here: it
// requires streaming support in mcp-go or a custom server-side handler,
// which is deferred to a follow-up task. See Task 10 notes.

func (st *SelfToolsTransport) callMessageSend(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	msg := messaging.SendInput{
		FromSessionID: strArg(args, "from_session_id", ""),
		FromAgentID:   strArg(args, "from_agent_id", ""),
		ToSessionID:   strArg(args, "to_session_id", ""),
		ToAgentID:     strArg(args, "to_agent_id", ""),
		Channel:       strArg(args, "channel", ""),
		Kind:          strArg(args, "kind", ""),
		PayloadJSON:   strArg(args, "payload_json", ""),
		Subject:       strArg(args, "subject", ""),
		Body:          strArg(args, "body", ""),
		Type:          strArg(args, "type", ""),
		ReplyTo:       strArg(args, "reply_to", ""),
		RegisterAs:    strArg(args, "register_as", ""),
	}
	out, err := st.Messaging.SendMessage(ctx, msg)
	if err != nil {
		return errorResult(fmt.Sprintf("message send: %v", err)), nil
	}
	return textResult(fmt.Sprintf("sent: %s", out.ID)), nil
}

func (st *SelfToolsTransport) callMessageInbox(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	// MVP caller identity: the MCP boundary has no out-of-band caller
	// channel yet, so the same (session_id, agent_id) args serve as both
	// target and caller. Real caller-identity-from-ctx is a post-MVP
	// upgrade; the service still enforces the match, which catches a
	// misconfigured caller that passes different values.
	sessionID := strArg(args, "session_id", "")
	agentID := strArg(args, "agent_id", "")
	inbox, err := st.Messaging.Inbox(
		ctx,
		sessionID,
		agentID,
		messaging.InboxFilter{
			Status:  strArg(args, "status", ""),
			Channel: strArg(args, "channel", ""),
			Kind:    strArg(args, "kind", ""),
		},
		sessionID,
		agentID,
	)
	if err != nil {
		return errorResult(fmt.Sprintf("message inbox: %v", err)), nil
	}
	if inbox == nil {
		inbox = []messaging.Message{}
	}
	data, err := json.Marshal(inbox)
	if err != nil {
		return errorResult(fmt.Sprintf("messaging inbox marshal: %v", err)), nil
	}
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callMessageThread(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	messages, err := st.Messaging.Thread(
		ctx,
		strArg(args, "thread_id", ""),
		strArg(args, "session_id", ""),
		strArg(args, "agent_id", ""),
	)
	if err != nil {
		return errorResult(fmt.Sprintf("message thread: %v", err)), nil
	}
	if messages == nil {
		messages = []messaging.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return errorResult(fmt.Sprintf("messaging thread marshal: %v", err)), nil
	}
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callMessageAck(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.Ack(
		ctx,
		strArg(args, "session_id", ""),
		strArg(args, "agent_id", ""),
		strArg(args, "message_id", ""),
	); err != nil {
		return errorResult(fmt.Sprintf("message ack: %v", err)), nil
	}
	return textResult("acked"), nil
}

func (st *SelfToolsTransport) callMessageResolve(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.Resolve(
		ctx,
		strArg(args, "session_id", ""),
		strArg(args, "agent_id", ""),
		strArg(args, "message_id", ""),
	); err != nil {
		return errorResult(fmt.Sprintf("message resolve: %v", err)), nil
	}
	return textResult("resolved"), nil
}

func (st *SelfToolsTransport) callMessageCatchUp(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	// intArg does not clamp; non-positive values are passed through to
	// the service/store layers, which apply default-20 + store cap.
	limit := intArg(args, "limit", 20)
	messages, err := st.Messaging.RecentForSession(
		ctx,
		strArg(args, "session_id", ""),
		limit,
	)
	if err != nil {
		return errorResult(fmt.Sprintf("message catch_up: %v", err)), nil
	}
	if messages == nil {
		messages = []messaging.Message{}
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return errorResult(fmt.Sprintf("messaging catch_up marshal: %v", err)), nil
	}
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callHandoffRequest(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	id, err := st.Messaging.RequestHandoff(
		ctx,
		strArg(args, "session_id", ""),
		strArg(args, "from_agent_id", ""),
		strArg(args, "to_agent_id", ""),
		strArg(args, "requested_by", ""),
	)
	if err != nil {
		return errorResult(fmt.Sprintf("handoff request: %v", err)), nil
	}
	return textResult("handoff requested: " + id), nil
}

func (st *SelfToolsTransport) callHandoffApprove(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.ApproveHandoff(ctx, strArg(args, "handoff_id", "")); err != nil {
		return errorResult(fmt.Sprintf("handoff approve: %v", err)), nil
	}
	return textResult("approved"), nil
}

func (st *SelfToolsTransport) callHandoffReject(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Messaging == nil {
		return errorResult("messaging service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Messaging.RejectHandoff(
		ctx,
		strArg(args, "handoff_id", ""),
		strArg(args, "reason", ""),
	); err != nil {
		return errorResult(fmt.Sprintf("handoff reject: %v", err)), nil
	}
	return textResult("rejected"), nil
}

// --- subagent handlers (T9) ---

// callSpawnSubagent handles nanite_spawn_subagent. Happy-path: all
// three modes auto-approve for MVP; interactive approval is a
// follow-up (T9.2). The spawn returns a runID the caller can poll
// via nanite_subagent_status or observe via the reply message
// posted back to the parent session on completion.
func (st *SelfToolsTransport) callSpawnSubagent(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Subagent == nil {
		return errorResult("subagent service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	req := subagent.SpawnRequest{
		ParentSessionID: strArg(args, "parent_session_id", ""),
		ParentAgentID:   strArg(args, "parent_agent_id", ""),
		Role:            strArg(args, "role", ""),
		Prompt:          strArg(args, "prompt", ""),
		Mode:            strArg(args, "mode", "sync"),
		Provider:        strArg(args, "provider", ""),
		InputsJSON:      strArg(args, "inputs_json", ""),
		TimeoutSeconds:  intArg(args, "timeout_seconds", 0),
	}
	id, err := st.Subagent.Spawn(ctx, req)
	if err != nil {
		return errorResult(fmt.Sprintf("spawn subagent: %v", err)), nil
	}
	return textResult(fmt.Sprintf("spawned: %s", id)), nil
}

func (st *SelfToolsTransport) callSubagentStatus(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Subagent == nil {
		return errorResult("subagent service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	run, err := st.Subagent.Status(ctx, strArg(args, "run_id", ""))
	if err != nil {
		return errorResult(fmt.Sprintf("subagent status: %v", err)), nil
	}
	data, err := json.Marshal(run)
	if err != nil {
		return errorResult(fmt.Sprintf("subagent status marshal: %v", err)), nil
	}
	return textResult(string(data)), nil
}

func (st *SelfToolsTransport) callSubagentCancel(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if st.Subagent == nil {
		return errorResult("subagent service not configured"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, messageCallTimeout)
	defer cancel()
	if err := st.Subagent.Cancel(ctx, strArg(args, "run_id", "")); err != nil {
		return errorResult(fmt.Sprintf("subagent cancel: %v", err)), nil
	}
	return textResult("cancelled"), nil
}

// --- helpers ---

func strArg(args map[string]any, key, def string) string {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return def
	}
	return v
}

// parseSourcesArg decodes the `sources` argument for nanite_show_document /
// nanite_show_report. Shape: JSON array of objects with at least a
// `tool_use_id` or `tool_name` field. Enforces presence + non-empty + basic
// per-entry shape — this is the cheap grounding check (CW-20260419-0022).
// Validating that the cited tool_use_ids were actually invoked in this
// generation is the deep check tracked separately.
func parseSourcesArg(args map[string]any) ([]map[string]any, error) {
	raw, ok := args["sources"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("sources is required: pass a JSON array of objects like [{\"tool_use_id\":\"...\",\"tool_name\":\"...\"}] citing the tool calls whose results ground this card. If you did not fetch the data, do NOT render the card — respond in plain text instead")
	}
	var sources []map[string]any
	if err := json.Unmarshal([]byte(raw), &sources); err != nil {
		return nil, fmt.Errorf("invalid sources JSON: %v", err)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("sources must contain at least one entry — cite the tool calls whose results ground this card")
	}
	for i, s := range sources {
		id, _ := s["tool_use_id"].(string)
		name, _ := s["tool_name"].(string)
		if id == "" && name == "" {
			return nil, fmt.Errorf("sources[%d] must include tool_use_id or tool_name", i)
		}
	}
	return sources, nil
}
