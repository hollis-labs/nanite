package crossapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// UICommand represents a command to send to the Engine GUI via SSE.
type UICommand struct {
	Type   string            `json:"type"`
	Target string            `json:"target,omitempty"`
	Params map[string]string `json:"params,omitempty"`
	Data   map[string]any    `json:"data,omitempty"`
}

var httpClient = &http.Client{Timeout: 5 * time.Second}

// engineAPIURL returns the Engine API base URL from env or default.
// The UI commands SSE endpoint lives on the same server as the API (port 8085).
func engineAPIURL() string {
	if u := os.Getenv("ENGINE_API_URL"); u != "" {
		return u
	}
	return "http://127.0.0.1:8085"
}

// SendUICommand POSTs a UI command to the Engine API's SSE broadcast endpoint.
func SendUICommand(ctx context.Context, cmd UICommand) error {
	body, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal UI command: %w", err)
	}

	url := engineAPIURL() + "/v1/ui-commands"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Warn("crossapp: Engine UI command failed (Engine may be offline)", "err", err)
		return fmt.Errorf("post UI command: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		slog.Warn("crossapp: Engine returned error for UI command", "status", resp.StatusCode, "type", cmd.Type)
		return fmt.Errorf("engine returned %d", resp.StatusCode)
	}

	slog.Info("crossapp: UI command sent", "type", cmd.Type, "target", cmd.Target, "params", cmd.Params)
	return nil
}

// pageRoutes maps friendly page names to Engine GUI hash routes.
var pageRoutes = map[string]string{
	"ops-dashboard": "#/ops",
	"tasks":         "#/operations/tasks",
	"task-detail":   "#/operations/tasks/view",
	"sprints":       "#/operations/sprints",
	"sprint-detail": "#/operations/sprints/view",
	"kanban":        "#/kanban/board",
	"epics":         "#/operations/epics",
	"projects":      "#/operations/projects",
	"activity":      "#/dashboards/activity",
	"inspector":     "#/observability/inspector",
}

// NavigateEngine sends a navigate command for a named page with optional filters.
func NavigateEngine(ctx context.Context, page string, params map[string]string) error {
	route, ok := pageRoutes[page]
	if !ok {
		return fmt.Errorf("unknown page %q — valid pages: ops-dashboard, tasks, task-detail, sprints, sprint-detail, kanban, epics, projects, activity, inspector", page)
	}

	cmd := UICommand{
		Type:   "navigate",
		Target: route,
	}
	if len(params) > 0 {
		cmd.Params = params
	}
	return SendUICommand(ctx, cmd)
}

// RefreshEngine sends a refresh command to reload data in the Engine GUI.
func RefreshEngine(ctx context.Context) error {
	return SendUICommand(ctx, UICommand{Type: "refresh"})
}
