package contextbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// ContextGate is a specialized ContextSource that fires on session start
// and provides essential context for the duration of the session.
type ContextGate interface {
	ContextSource

	// SessionStart triggers gate activation and caches context for session duration.
	// Returns true if gate should provide context, false to skip gracefully.
	SessionStart(ctx context.Context, intent Intent) (bool, error)

	// IsCached returns true if the gate has cached data for the current session.
	IsCached() bool

	// ClearCache clears any cached session data.
	ClearCache()
}

// HadronBlueprintGate provides project-scoped blueprint awareness on session start.
// It fetches blueprints from Hadron, filters by project scope, includes essential
// cross-cutting blueprints, and formats them as compact one-liners within a 500-token cap.
type HadronBlueprintGate struct {
	MCP        MCPCaller
	ServerName string // MCP server name (default: "hadron")

	// Session cache
	mutex       sync.RWMutex
	cachedItems []ContextItem
	cacheTime   time.Time
	cacheIntent Intent
}

// NewHadronBlueprintGate creates a new HadronBlueprintGate.
func NewHadronBlueprintGate(mcp MCPCaller) *HadronBlueprintGate {
	return &HadronBlueprintGate{
		MCP:        mcp,
		ServerName: "hadron",
	}
}

func (g *HadronBlueprintGate) Name() string {
	return "hadron-blueprints"
}

func (g *HadronBlueprintGate) SessionStart(ctx context.Context, intent Intent) (bool, error) {
	if g.MCP == nil {
		slog.Info("hadron-blueprint-gate: no MCP caller configured, skipping gracefully")
		return false, nil
	}

	// Fetch and cache blueprints for session
	items, err := g.fetchBlueprints(ctx, intent, 500) // Hard cap at 500 tokens
	if err != nil {
		slog.Warn("hadron-blueprint-gate: session start failed", "err", err)
		return false, nil // Graceful no-op on error
	}

	g.mutex.Lock()
	g.cachedItems = items
	g.cacheTime = time.Now()
	g.cacheIntent = intent
	g.mutex.Unlock()

	return len(items) > 0, nil
}

func (g *HadronBlueprintGate) IsCached() bool {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return len(g.cachedItems) > 0
}

func (g *HadronBlueprintGate) ClearCache() {
	g.mutex.Lock()
	g.cachedItems = nil
	g.cacheTime = time.Time{}
	g.cacheIntent = Intent{}
	g.mutex.Unlock()
}

func (g *HadronBlueprintGate) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	if len(g.cachedItems) == 0 {
		return nil, nil // Return empty if no cached data
	}

	// Return cached items within budget
	var items []ContextItem
	usedTokens := 0
	for _, item := range g.cachedItems {
		if usedTokens+item.TokenEstimate > budget {
			break
		}
		items = append(items, item)
		usedTokens += item.TokenEstimate
	}

	return items, nil
}

// fetchBlueprints retrieves blueprints from Hadron and filters them.
func (g *HadronBlueprintGate) fetchBlueprints(ctx context.Context, intent Intent, maxTokens int) ([]ContextItem, error) {
	input := map[string]any{
		"limit": 25, // Get enough to filter from
	}

	result, err := g.MCP.ExecuteToolOnServer(ctx, g.ServerName, "hadron_blueprints_list", input)
	if err != nil {
		return nil, fmt.Errorf("hadron_blueprints_list: %w", err)
	}

	return g.parseAndFilterBlueprints(result, intent, maxTokens)
}

// parseAndFilterBlueprints converts blueprint list to filtered ContextItems.
func (g *HadronBlueprintGate) parseAndFilterBlueprints(raw string, intent Intent, maxTokens int) ([]ContextItem, error) {
	var response struct {
		Items []struct {
			Name        string   `json:"name"`
			Path        string   `json:"path"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		// Return raw as fallback if parsing fails
		tokens := EstimateTokens(raw)
		if tokens > maxTokens {
			return nil, nil
		}
		return []ContextItem{{
			Source:        "hadron-blueprints",
			Key:           "blueprints-raw",
			Content:       fmt.Sprintf("Available blueprints: %s", raw),
			TokenEstimate: tokens,
			Relevance:     0.3,
		}}, nil
	}

	// Filter blueprints by project scope and relevance
	var filtered []blueprintInfo
	projectScope := strings.ToLower(intent.Scope)

	for _, bp := range response.Items {
		info := blueprintInfo{
			Name:        bp.Name,
			Description: bp.Description,
			Tags:        bp.Tags,
		}

		// Calculate relevance
		relevance := g.calculateRelevance(bp, projectScope, intent.Keywords)
		if relevance > 0.1 {
			info.Relevance = relevance
			filtered = append(filtered, info)
		}
	}

	// Sort by relevance (highest first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Relevance > filtered[j].Relevance
	})

	// Convert to compact one-liner format within token budget
	return g.formatCompactBlueprints(filtered, maxTokens)
}

// calculateRelevance determines blueprint relevance based on project scope and keywords.
func (g *HadronBlueprintGate) calculateRelevance(bp struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}, projectScope string, keywords []string) float64 {
	name := strings.ToLower(bp.Name)
	desc := strings.ToLower(bp.Description)

	var relevance float64

	// Essential cross-cutting blueprints (always relevant)
	if strings.Contains(name, "health") || strings.Contains(name, "audit") ||
	   strings.Contains(name, "cleanup") || strings.Contains(name, "backup") {
		relevance += 0.6
	}

	// Project-specific blueprints
	if projectScope != "" {
		if strings.Contains(name, projectScope) || strings.Contains(desc, projectScope) {
			relevance += 0.8
		}
		// Match common project patterns
		if (projectScope == "mentat" || projectScope == "nanite") &&
		   (strings.Contains(name, "build") || strings.Contains(name, "test")) {
			relevance += 0.4
		}
	}

	// Keyword matching
	for _, keyword := range keywords {
		kw := strings.ToLower(keyword)
		if strings.Contains(name, kw) || strings.Contains(desc, kw) {
			relevance += 0.3
		}
		// Tag matching
		for _, tag := range bp.Tags {
			if strings.Contains(strings.ToLower(tag), kw) {
				relevance += 0.2
			}
		}
	}

	// Common development blueprints
	if strings.Contains(name, "build") || strings.Contains(name, "test") ||
	   strings.Contains(name, "lint") || strings.Contains(name, "deploy") {
		relevance += 0.3
	}

	return relevance
}

// formatCompactBlueprints creates compact one-liner format within token budget.
func (g *HadronBlueprintGate) formatCompactBlueprints(blueprints []blueprintInfo, maxTokens int) ([]ContextItem, error) {
	if len(blueprints) == 0 {
		return nil, nil
	}

	// Create compact summary format
	var lines []string
	usedTokens := 0
	baseTokens := EstimateTokens("Available blueprints: ")

	for _, bp := range blueprints {
		// One-liner format: "name — description"
		line := fmt.Sprintf("• %s — %s", bp.Name, truncateDescription(bp.Description, 60))
		lineTokens := EstimateTokens(line)

		if baseTokens+usedTokens+lineTokens > maxTokens {
			break
		}

		lines = append(lines, line)
		usedTokens += lineTokens

		// Limit to reasonable number of blueprints
		if len(lines) >= 10 {
			break
		}
	}

	if len(lines) == 0 {
		return nil, nil
	}

	content := "Available blueprints:\n" + strings.Join(lines, "\n")
	totalTokens := EstimateTokens(content)

	return []ContextItem{{
		Source:        "hadron-blueprints",
		Key:           "session-blueprints",
		Content:       content,
		TokenEstimate: totalTokens,
		Relevance:     0.7, // High relevance for session-start context
		Metadata: map[string]string{
			"type":       "blueprint-registry",
			"count":      fmt.Sprintf("%d", len(lines)),
			"gate_type":  "session-start",
			"cache_time": time.Now().Format(time.RFC3339),
		},
	}}, nil
}

// truncateDescription truncates description to max length with ellipsis.
func truncateDescription(desc string, maxLen int) string {
	if len(desc) <= maxLen {
		return desc
	}
	if maxLen <= 3 {
		return "..."
	}
	return desc[:maxLen-3] + "..."
}

// blueprintInfo holds filtered blueprint information.
type blueprintInfo struct {
	Name        string
	Description string
	Tags        []string
	Relevance   float64
}