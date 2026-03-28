package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/conduit/internal/builders"
	"github.com/hollis-labs/conduit/internal/crossapp"
	"github.com/hollis-labs/conduit/internal/store"
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
	case "conduit_create_skill":
		return st.callCreateSkill(args)
	case "conduit_list_skills":
		return st.callListSkills(args)
	case "conduit_update_skill":
		return st.callUpdateSkill(args)
	case "conduit_delete_skill":
		return st.callDeleteSkill(args)
	case "conduit_create_agent":
		return st.callCreateAgent(args)
	case "conduit_list_agents":
		return st.callListAgents(args)
	case "conduit_update_agent":
		return st.callUpdateAgent(args)
	case "conduit_open_sprint_planning":
		return textResult("Sprint planning modal opened in the UI."), nil
	case "conduit_navigate_engine":
		return st.callNavigateEngine(args)
	case "conduit_refresh_engine":
		return st.callRefreshEngine(args)
	case "conduit_show_giphy":
		return st.callShowGiphy(args)
	case "conduit_run_report":
		return st.callRunReport(args)
	case "conduit_show_document":
		return st.callShowDocument(args)
	case "conduit_show_report":
		return st.callShowReport(args)
	case "conduit_show_task_disposition":
		return st.callShowTaskDisposition(args)
	case "conduit_show_sprint_planning_review":
		return st.callShowSprintPlanningReview(args)
	case "conduit_start_builder":
		return st.callStartBuilder(args)
	case "conduit_builder_step":
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
		return textResult(fmt.Sprintf("Navigation sent for %q (Engine may be offline: %v). Tell the user briefly and stop.", page, err)), nil
	}

	msg := fmt.Sprintf("Done. Engine GUI navigated to %s", page)
	if len(params) > 0 {
		msg += fmt.Sprintf(" with filters %v", params)
	}
	msg += ". Tell the user what you navigated to in one sentence. Do NOT call any more tools."
	return textResult(msg), nil
}

func (st *SelfToolsTransport) callRefreshEngine(args map[string]any) (*ToolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := crossapp.RefreshEngine(ctx); err != nil {
		return textResult(fmt.Sprintf("Refresh sent (Engine may be offline: %v). Do NOT call any more tools.", err)), nil
	}
	return textResult("Engine GUI data refreshed. Do NOT call any more tools."), nil
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

func (st *SelfToolsTransport) callRunReport(args map[string]any) (*ToolResult, error) {
	reportType := strArg(args, "report_type", "executive-summary")
	description := strArg(args, "description", "Generating report...")
	content := strArg(args, "content", "")

	runID := fmt.Sprintf("RUN-%d", time.Now().UnixMilli())

	if content == "" {
		// Demo simulation: generate a placeholder report in markdown.
		content = fmt.Sprintf(`# %s Report

**Generated:** %s

## Overview

This is a simulated report for demonstration purposes. In production, this would be generated by Hadron blueprint execution.

## Key Metrics

- **Tasks completed:** 12/18 (67%%)
- **Sprint velocity:** 34 points
- **Blocked items:** 2
- **Code coverage:** 78%%

## Summary

The portfolio is on track. Two blocked items require attention from the platform team.`,
			reportType, time.Now().Format(time.RFC3339))
	}

	// Build the notification envelope.
	preview := content
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}

	envData := map[string]any{
		"title":          fmt.Sprintf("Report Complete: %s", reportType),
		"run_id":         runID,
		"blueprint_id":   fmt.Sprintf("bp-%s", reportType),
		"status":         "done",
		"completed_at":   time.Now().Format(time.RFC3339),
		"has_output":     true,
		"output_preview": preview,
		"prompt_text":    "Would you like to see the full report?",
		"report_content": content,
	}
	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "task-complete-notification",
		"data":    envData,
	})

	log.Printf("crossapp: report %s generated (type=%s, len=%d)", runID, reportType, len(content))

	result := fmt.Sprintf("Report %s is ready. %s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", runID, description, string(envJSON))
	return textResult(result), nil
}

// --- envelope injection handlers ---

func (st *SelfToolsTransport) callShowDocument(args map[string]any) (*ToolResult, error) {
	title, _ := args["title"].(string)
	content, _ := args["content"].(string)
	if title == "" || content == "" {
		return errorResult("title and content are required"), nil
	}

	format := strArg(args, "format", "markdown")
	downloadFilename := strArg(args, "download_filename", "")

	envData := map[string]any{
		"title":   title,
		"content": content,
		"format":  format,
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

	envData := map[string]any{
		"title":        title,
		"generated_at": time.Now().Format(time.RFC3339),
		"metrics":      metrics,
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

func (st *SelfToolsTransport) callShowTaskDisposition(args map[string]any) (*ToolResult, error) {
	title, _ := args["title"].(string)
	tasksStr, _ := args["tasks"].(string)
	if title == "" || tasksStr == "" {
		return errorResult("title and tasks are required"), nil
	}

	var tasks []any
	if err := json.Unmarshal([]byte(tasksStr), &tasks); err != nil {
		return errorResult(fmt.Sprintf("invalid tasks JSON: %v", err)), nil
	}

	actions := []string{"Approve", "Archive", "Pause", "Done", "Skip"}
	if actionsStr, _ := args["actions"].(string); actionsStr != "" {
		var customActions []string
		if err := json.Unmarshal([]byte(actionsStr), &customActions); err == nil && len(customActions) > 0 {
			actions = customActions
		}
	}

	envData := map[string]any{
		"title":   title,
		"tasks":   tasks,
		"actions": actions,
	}
	if desc, _ := args["description"].(string); desc != "" {
		envData["description"] = desc
	}

	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "task-disposition",
		"data":    envData,
	})

	result := fmt.Sprintf("Task disposition card ready: %s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", title, string(envJSON))
	return textResult(result), nil
}

func (st *SelfToolsTransport) callShowSprintPlanningReview(args map[string]any) (*ToolResult, error) {
	title, _ := args["title"].(string)
	sprintsStr, _ := args["sprints"].(string)
	tasksStr, _ := args["tasks"].(string)
	if title == "" || sprintsStr == "" || tasksStr == "" {
		return errorResult("title, sprints, and tasks are required"), nil
	}

	var sprints []any
	if err := json.Unmarshal([]byte(sprintsStr), &sprints); err != nil {
		return errorResult(fmt.Sprintf("invalid sprints JSON: %v", err)), nil
	}
	var tasks []any
	if err := json.Unmarshal([]byte(tasksStr), &tasks); err != nil {
		return errorResult(fmt.Sprintf("invalid tasks JSON: %v", err)), nil
	}

	envData := map[string]any{
		"title":   title,
		"sprints": sprints,
		"tasks":   tasks,
	}
	if desc, _ := args["description"].(string); desc != "" {
		envData["description"] = desc
	}
	if ps, ok := args["page_size"].(float64); ok && ps > 0 {
		envData["page_size"] = int(ps)
	}

	envJSON, _ := json.Marshal(map[string]any{
		"kind":    "envelope",
		"version": 1,
		"type":    "sprint-planning-review",
		"data":    envData,
	})

	result := fmt.Sprintf("Sprint planning review card ready: %s (%d sprints, %d tasks)\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->",
		title, len(sprints), len(tasks), string(envJSON))
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
