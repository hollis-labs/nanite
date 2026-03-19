package support

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/lib/pq"
)

// KBTransport implements mcp.MCPTransport to provide KB search tools
// backed by the kb_demo Postgres database.
type KBTransport struct {
	db *sql.DB
}

// NewKBTransport opens a connection to the KB database using the given
// connection string and returns a transport that exposes search_kb and
// get_kb_article tools.
func NewKBTransport(connStr string) (*KBTransport, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open kb_demo: %w", err)
	}
	// Verify the connection is alive.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping kb_demo: %w", err)
	}
	return &KBTransport{db: db}, nil
}

// Close shuts down the database connection.
func (t *KBTransport) Close() error {
	if t.db != nil {
		return t.db.Close()
	}
	return nil
}

// ListTools returns the two KB tools: search_kb and get_kb_article.
func (t *KBTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{
			Name:        "search_kb",
			Description: "Search the IT support knowledge base for articles matching a query. Returns relevant KB articles with titles, categories, resolution steps, and confidence scores. Always search before suggesting ticket creation.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Natural language search query describing the IT issue",
					},
					"category": map[string]any{
						"type":        "string",
						"description": "Optional category filter (e.g., 'VPN / GlobalProtect', 'Active Directory', 'Jira')",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results to return (default 5)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "get_kb_article",
			Description: "Fetch the full content of a specific KB article by its ID. Use this after search_kb to get complete resolution steps.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "KB article ID (e.g., 'KB-042')",
					},
				},
				"required": []string{"id"},
			},
		},
	}, nil
}

// CallTool dispatches search_kb or get_kb_article.
func (t *KBTransport) CallTool(ctx context.Context, name string, arguments map[string]any) (*mcp.ToolResult, error) {
	switch name {
	case "search_kb":
		return t.searchKB(ctx, arguments)
	case "get_kb_article":
		return t.getKBArticle(ctx, arguments)
	default:
		return &mcp.ToolResult{
			Content: []mcp.ToolContent{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", name)}},
			IsError: true,
		}, nil
	}
}

// searchKBResult is a single row from kb_smart_search.
type searchKBResult struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Severity    string   `json:"severity"`
	Tags        []string `json:"tags"`
	Related     []string `json:"related"`
	Rank        float64  `json:"rank"`
	Headline    string   `json:"headline"`
	MatchMethod string   `json:"match_method"`
	Confidence  string   `json:"confidence"`
	Body        string   `json:"body"`
	Source      string   `json:"source"` // "helix" or "generated"
}

// searchKBResponse is the JSON envelope returned to the LLM.
type searchKBResponse struct {
	Results      []searchKBResult `json:"results"`
	Query        string           `json:"query"`
	TotalResults int              `json:"total_results"`
}

func (t *KBTransport) searchKB(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return textResult("Error: 'query' parameter is required", true), nil
	}

	// Optional category filter — pass nil to the function if empty.
	var categoryFilter sql.NullString
	if cat, ok := args["category"].(string); ok && cat != "" {
		categoryFilter = sql.NullString{String: cat, Valid: true}
	}

	// Search strategy: helix (real) articles first, then fill with generated.
	// We want 1 primary result + 2 additional. Helix articles are favored.
	scanResults := func(rows *sql.Rows) ([]searchKBResult, error) {
		var out []searchKBResult
		for rows.Next() {
			var r searchKBResult
			if err := rows.Scan(
				&r.ID, &r.Title, &r.Category, &r.Severity,
				pq.Array(&r.Tags), pq.Array(&r.Related),
				&r.Rank, &r.Headline, &r.MatchMethod, &r.Body, &r.Source,
			); err != nil {
				return nil, err
			}
			r.Confidence = classifyConfidence(r.Rank, r.MatchMethod)
			out = append(out, r)
		}
		return out, rows.Err()
	}

	// First: search helix articles only.
	helixRows, err := t.db.QueryContext(ctx,
		`SELECT s.id, s.title, s.category, s.severity, s.tags, s.related, s.rank, s.headline, s.match_method, COALESCE(a.body, ''), COALESCE(a.source, 'generated')
		 FROM kb_smart_search($1, $2, $3, 5, 'helix') s
		 LEFT JOIN kb_articles a ON a.id = s.id`,
		query, categoryFilter, sql.NullString{},
	)
	if err != nil {
		return textResult(fmt.Sprintf("KB search error: %v", err), true), nil
	}
	helixResults, err := scanResults(helixRows)
	helixRows.Close()
	if err != nil {
		return textResult(fmt.Sprintf("KB scan error: %v", err), true), nil
	}

	// Second: search all articles (both sources).
	allRows, err := t.db.QueryContext(ctx,
		`SELECT s.id, s.title, s.category, s.severity, s.tags, s.related, s.rank, s.headline, s.match_method, COALESCE(a.body, ''), COALESCE(a.source, 'generated')
		 FROM kb_smart_search($1, $2, $3, 5, NULL) s
		 LEFT JOIN kb_articles a ON a.id = s.id`,
		query, categoryFilter, sql.NullString{},
	)
	if err != nil {
		return textResult(fmt.Sprintf("KB search error: %v", err), true), nil
	}
	allResults, err := scanResults(allRows)
	allRows.Close()
	if err != nil {
		return textResult(fmt.Sprintf("KB scan error: %v", err), true), nil
	}

	// Merge: helix results first, then fill with non-duplicate results from all.
	seen := make(map[string]bool)
	var results []searchKBResult
	for _, r := range helixResults {
		if len(results) >= 3 {
			break
		}
		seen[r.ID] = true
		results = append(results, r)
	}
	for _, r := range allResults {
		if len(results) >= 3 {
			break
		}
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		results = append(results, r)
	}

	if len(results) == 0 {
		return textResult("No KB articles found matching the query.", false), nil
	}

	// Build compact summary for the LLM (no body — avoids truncation).
	type compactResult struct {
		ID         string  `json:"id"`
		Title      string  `json:"title"`
		Category   string  `json:"category"`
		Severity   string  `json:"severity"`
		Confidence string  `json:"confidence"`
		Rank       float64 `json:"rank"`
		Source     string  `json:"source"`
	}
	compact := make([]compactResult, len(results))
	for i, r := range results {
		compact[i] = compactResult{ID: r.ID, Title: r.Title, Category: r.Category, Severity: r.Severity, Confidence: r.Confidence, Rank: r.Rank, Source: r.Source}
	}
	compactResp := struct {
		Results      []compactResult `json:"results"`
		Query        string          `json:"query"`
		TotalResults int             `json:"total_results"`
	}{Results: compact, Query: query, TotalResults: len(results)}
	compactData, _ := json.MarshalIndent(compactResp, "", "  ")

	// Full response (with body) goes into metadata for envelope injection.
	// Store it in the tool result with a separator the engine can extract.
	fullResp := searchKBResponse{Results: results, Query: query, TotalResults: len(results)}
	fullData, _ := json.Marshal(fullResp)

	result := string(compactData) +
		"\n\n[SYSTEM: KB articles found and will be displayed to the user automatically. Write ONE brief sentence introducing the results. Do NOT call any more tools.]" +
		"\n\n<!--ENVELOPE_DATA:" + string(fullData) + ":ENVELOPE_DATA-->"
	return textResult(result, false), nil
}

// kbArticle is the full article record.
type kbArticle struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Severity string   `json:"severity"`
	Tags     []string `json:"tags"`
	Related  []string `json:"related"`
	Body     string   `json:"body"`
}

func (t *KBTransport) getKBArticle(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return textResult("Error: 'id' parameter is required", true), nil
	}

	var a kbArticle
	err := t.db.QueryRowContext(ctx,
		`SELECT id, title, category, severity, tags, related, body
		 FROM kb_articles WHERE id = $1`, id,
	).Scan(&a.ID, &a.Title, &a.Category, &a.Severity,
		pq.Array(&a.Tags), pq.Array(&a.Related), &a.Body)

	if err == sql.ErrNoRows {
		return textResult(fmt.Sprintf("No KB article found with ID: %s", id), false), nil
	}
	if err != nil {
		return textResult(fmt.Sprintf("KB lookup error: %v", err), true), nil
	}

	data, _ := json.MarshalIndent(a, "", "  ")
	return textResult(string(data), false), nil
}

// classifyConfidence returns a confidence label based on rank and match method.
func classifyConfidence(rank float64, method string) string {
	switch {
	case rank >= 0.3 && (method == "fts_exact" || method == "fts_prefix"):
		return "high"
	case rank < 0.2 && method == "fuzzy":
		return "low"
	default:
		return "medium"
	}
}

// textResult is a convenience for building a single-text ToolResult.
func textResult(text string, isError bool) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.ToolContent{{Type: "text", Text: text}},
		IsError: isError,
	}
}
