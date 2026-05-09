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
)

// giphySearchEndpoint and giphyHTTPClient are package-level vars so the
// HTTP-mock tests can swap them out. Production callers see the real GIPHY
// endpoint over the default client. Restore in tests with the standard
// defer-restore pattern (see self_tools_giphy_test.go).
var (
	giphySearchEndpoint = "https://api.giphy.com/v1/gifs/search"
	giphyHTTPClient     = http.DefaultClient
)

// giphyDemoGifs is the keyword→URL fallback map used when GIPHY_API_KEY is
// unset. Recovered verbatim from the pre-A3 callShowGiphy handler
// (af781b9, removed in 3187da6) so the development experience is unchanged
// when the new data tool is paired with card_show.
var giphyDemoGifs = map[string]string{
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

// giphySearchHit is the per-result shape returned by giphy_search.
// Field order matches the tool description so the agent gets a stable
// vocabulary it can plug straight into card_show{type:"giphy-modal"}.
type giphySearchHit struct {
	GifURL      string `json:"gif_url"`
	Title       string `json:"title,omitempty"`
	Attribution string `json:"attribution,omitempty"`
	AltText     string `json:"alt_text,omitempty"`
}

// giphyMaxLimit caps the per-request result count. The GIPHY API allows
// higher values but a chat tool is a poor fit for paginated discovery —
// the agent should ask for one or two GIFs and chain into show_card,
// not browse a feed.
const giphyMaxLimit = 10

// callGiphySearch is the handler for giphy_search. Returns a
// structured JSON result the agent can introspect: a single hit object
// (default, limit=1) or {results: [...]} for higher limits. Errors are
// always structured ({error: "...", details?, query?}) so the agent can
// branch on the failure mode instead of pattern-matching prose.
//
// CW-20260428-0020 (A4) — separates the GIPHY data fetch from the display
// path, complementing the generic card_show surface added in A3.
func (st *SelfToolsTransport) callGiphySearch(args map[string]any) (*ToolResult, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return errorResult("query is required"), nil
	}

	limit := 1
	switch v := args["limit"].(type) {
	case float64:
		limit = int(v)
	case int:
		limit = v
	case string:
		if v != "" {
			if n, err := parseLimit(v); err == nil {
				limit = n
			} else {
				return errorResult(fmt.Sprintf("limit must be a number: %v", err)), nil
			}
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > giphyMaxLimit {
		limit = giphyMaxLimit
	}

	apiKey := os.Getenv("GIPHY_API_KEY")
	if apiKey == "" {
		return giphyResultJSON(giphyDemoSearch(query, limit), limit), nil
	}
	hits, errResult := giphyLiveSearch(query, limit, apiKey)
	if errResult != nil {
		return errResult, nil
	}
	return giphyResultJSON(hits, limit), nil
}

// parseLimit accepts integer-only strings; floats are rejected so the agent
// gets a clear error rather than silent truncation.
func parseLimit(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	return n, nil
}

// giphyDemoSearch returns a deterministic set of demo hits keyed off the
// query so the no-API-key path is repeatable in dev/CI. For limit > 1 we
// walk the keyword map in insertion-order-stable form (sorted keys) so
// the result set doesn't change between runs.
func giphyDemoSearch(query string, limit int) []giphySearchHit {
	if limit < 1 {
		limit = 1
	}
	lowerQ := strings.ToLower(query)

	// First pick: keyword match if the query contains a known keyword.
	primary := ""
	for keyword, gifURL := range giphyDemoGifs {
		if strings.Contains(lowerQ, keyword) {
			primary = gifURL
			break
		}
	}
	if primary == "" {
		// Fallback: hash-based pick for stable variety per query string.
		keys := sortedDemoKeys()
		h := 0
		for _, c := range query {
			h = h*31 + int(c)
		}
		if h < 0 {
			h = -h
		}
		primary = giphyDemoGifs[keys[h%len(keys)]]
	}

	hit := giphySearchHit{
		GifURL:      primary,
		Title:       fmt.Sprintf("Here's your %s!", query),
		Attribution: "GIPHY (demo mode)",
		AltText:     query,
	}
	if limit == 1 {
		return []giphySearchHit{hit}
	}

	// limit > 1: pad with additional demo URLs in stable key order, skipping
	// duplicates of the primary pick. Demo mode does not pretend to be a
	// real search ranker — these are just additional placeholder hits.
	out := []giphySearchHit{hit}
	for _, k := range sortedDemoKeys() {
		if len(out) >= limit {
			break
		}
		gifURL := giphyDemoGifs[k]
		if gifURL == primary {
			continue
		}
		out = append(out, giphySearchHit{
			GifURL:      gifURL,
			Title:       fmt.Sprintf("Demo GIF (%s)", k),
			Attribution: "GIPHY (demo mode)",
			AltText:     k,
		})
	}
	return out
}

// sortedDemoKeys returns giphyDemoGifs's keys in stable alphabetical order.
// Used for deterministic demo-mode output and hash-based fallback selection.
func sortedDemoKeys() []string {
	keys := make([]string, 0, len(giphyDemoGifs))
	for k := range giphyDemoGifs {
		keys = append(keys, k)
	}
	// Manual sort to avoid a sort import for this 14-entry map.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// giphyLiveSearch issues the GIPHY API request and parses the response.
// On HTTP / parse / empty-result failures it returns a structured-error
// *ToolResult; on success it returns the parsed hits and a nil result.
func giphyLiveSearch(query string, limit int, apiKey string) ([]giphySearchHit, *ToolResult) {
	endpoint := fmt.Sprintf("%s?api_key=%s&q=%s&limit=%d&rating=g",
		giphySearchEndpoint, url.QueryEscape(apiKey), url.QueryEscape(query), limit)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, giphyErrorJSON("http_error", fmt.Sprintf("create request: %v", err), query)
	}
	resp, err := giphyHTTPClient.Do(req)
	if err != nil {
		return nil, giphyErrorJSON("http_error", err.Error(), query)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, giphyErrorJSON("http_error", fmt.Sprintf("status %d", resp.StatusCode), query)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, giphyErrorJSON("http_error", fmt.Sprintf("read body: %v", err), query)
	}

	var giphyResp struct {
		Data []struct {
			Title  string `json:"title"`
			AltText string `json:"alt_text"`
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
		return nil, giphyErrorJSON("parse_error", err.Error(), query)
	}

	if len(giphyResp.Data) == 0 {
		return nil, giphyErrorJSON("no_results", "", query)
	}

	out := make([]giphySearchHit, 0, len(giphyResp.Data))
	for _, gif := range giphyResp.Data {
		gifURL := gif.Images.Original.URL
		if gifURL == "" {
			gifURL = gif.Images.FixedWidth.URL
		}
		alt := gif.AltText
		if alt == "" {
			alt = gif.Title
		}
		out = append(out, giphySearchHit{
			GifURL:      gifURL,
			Title:       gif.Title,
			Attribution: "GIPHY",
			AltText:     alt,
		})
	}
	return out, nil
}

// giphyResultJSON serialises hits into the agent-facing shape: a single
// object for limit=1 (the common case), or {results:[...]} for limit>1.
// The shape difference is deliberate — keeping the limit=1 case flat lets
// the agent plug `gif_url` straight into card_show without
// indexing into a 1-element array.
func giphyResultJSON(hits []giphySearchHit, limit int) *ToolResult {
	if limit <= 1 || len(hits) == 1 {
		body, _ := json.Marshal(hits[0])
		return textResult(string(body))
	}
	body, _ := json.Marshal(map[string]any{"results": hits})
	return textResult(string(body))
}

// giphyErrorJSON returns a structured-error *ToolResult so the agent can
// branch on the failure mode programmatically. IsError stays false here:
// the result is a successful tool call carrying a descriptive payload —
// the GIPHY service being down is not an MCP-level error.
func giphyErrorJSON(code, details, query string) *ToolResult {
	payload := map[string]any{
		"error": code,
		"query": query,
	}
	if details != "" {
		payload["details"] = details
	}
	body, _ := json.Marshal(payload)
	return textResult(string(body))
}
