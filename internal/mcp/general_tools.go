package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GeneralToolsTransport provides general-purpose utility tools
// that are not path-scoped.
type GeneralToolsTransport struct{}

// NewGeneralToolsTransport creates a GeneralToolsTransport.
func NewGeneralToolsTransport() *GeneralToolsTransport {
	return &GeneralToolsTransport{}
}

// ListTools returns the general utility tools.
func (g *GeneralToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "web_fetch",
			Description: "Fetch a URL via HTTP GET. Returns status code and response body (text only, truncated at 8000 chars).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "URL to fetch"},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "json_parse",
			Description: "Extract a value from a JSON string using dot-notation path (e.g. .data.items[0].name).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"json": map[string]any{"type": "string", "description": "JSON string to parse"},
					"path": map[string]any{"type": "string", "description": "Dot-notation path (e.g. .data.items[0].name)"},
				},
				"required": []string{"json", "path"},
			},
		},
		{
			Name:        "datetime",
			Description: "Get current UTC time, or compute date math (e.g. +3d, -1h, +30m, -2w).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"operation": map[string]any{"type": "string", "description": "Date math expression (e.g. +3d, -1h). Omit for current time."},
				},
			},
		},
	}, nil
}

// CallTool dispatches to the appropriate handler.
func (g *GeneralToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
	switch name {
	case "web_fetch":
		return g.callWebFetch(args)
	case "json_parse":
		return g.callJSONParse(args)
	case "datetime":
		return g.callDatetime(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (g *GeneralToolsTransport) callWebFetch(args map[string]any) (*ToolResult, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return errorResult("url is required"), nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return errorResult(fmt.Sprintf("fetch error: %v", err)), nil
	}
	defer resp.Body.Close()

	// Read up to 8000 chars.
	limited := io.LimitReader(resp.Body, 8000)
	body, err := io.ReadAll(limited)
	if err != nil {
		return errorResult(fmt.Sprintf("read error: %v", err)), nil
	}

	result := fmt.Sprintf("Status: %d %s\n\n%s", resp.StatusCode, resp.Status, string(body))
	return textResult(result), nil
}

func (g *GeneralToolsTransport) callJSONParse(args map[string]any) (*ToolResult, error) {
	jsonStr, _ := args["json"].(string)
	pathStr, _ := args["path"].(string)
	if jsonStr == "" || pathStr == "" {
		return errorResult("json and path are required"), nil
	}

	// Parse JSON into any.
	var data any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return errorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	// Navigate the path.
	parts := parseDotPath(pathStr)
	current := data
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part.key]
			if !ok {
				return errorResult(fmt.Sprintf("key %q not found", part.key)), nil
			}
			current = val
		case []any:
			if part.index < 0 || part.index >= len(v) {
				return errorResult(fmt.Sprintf("index %d out of range (length %d)", part.index, len(v))), nil
			}
			current = v[part.index]
		default:
			return errorResult(fmt.Sprintf("cannot navigate into %T at %q", current, part.key)), nil
		}
	}

	// Format the result.
	switch v := current.(type) {
	case string:
		return textResult(v), nil
	case nil:
		return textResult("null"), nil
	default:
		out, _ := json.Marshal(v)
		return textResult(string(out)), nil
	}
}

type pathSegment struct {
	key   string
	index int // -1 if not an array access
}

// parseDotPath parses ".data.items[0].name" into segments.
func parseDotPath(path string) []pathSegment {
	// Strip leading dot.
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil
	}

	var segments []pathSegment
	parts := strings.Split(path, ".")
	for _, part := range parts {
		// Check for array index: "items[0]"
		if idx := strings.Index(part, "["); idx >= 0 {
			key := part[:idx]
			indexStr := strings.TrimSuffix(part[idx+1:], "]")
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				// Treat as plain key.
				segments = append(segments, pathSegment{key: part, index: -1})
				continue
			}
			if key != "" {
				segments = append(segments, pathSegment{key: key, index: -1})
			}
			segments = append(segments, pathSegment{key: "", index: index})
		} else {
			segments = append(segments, pathSegment{key: part, index: -1})
		}
	}
	return segments
}

func (g *GeneralToolsTransport) callDatetime(args map[string]any) (*ToolResult, error) {
	now := time.Now().UTC()

	op, _ := args["operation"].(string)
	if op == "" {
		return textResult(now.Format(time.RFC3339)), nil
	}

	duration, err := parseDateMath(op)
	if err != nil {
		return errorResult(err.Error()), nil
	}

	result := now.Add(duration)
	return textResult(result.Format(time.RFC3339)), nil
}

// parseDateMath parses expressions like "+3d", "-1h", "+30m", "-2w".
func parseDateMath(expr string) (time.Duration, error) {
	if len(expr) < 2 {
		return 0, fmt.Errorf("invalid date math expression: %q", expr)
	}

	sign := 1
	rest := expr
	if expr[0] == '+' {
		rest = expr[1:]
	} else if expr[0] == '-' {
		sign = -1
		rest = expr[1:]
	}

	if len(rest) < 2 {
		return 0, fmt.Errorf("invalid date math expression: %q", expr)
	}

	unit := rest[len(rest)-1]
	numStr := rest[:len(rest)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid number in date math: %q", numStr)
	}

	var multiplier time.Duration
	switch unit {
	case 's':
		multiplier = time.Second
	case 'm':
		multiplier = time.Minute
	case 'h':
		multiplier = time.Hour
	case 'd':
		multiplier = 24 * time.Hour
	case 'w':
		multiplier = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown unit %q (use s/m/h/d/w)", string(unit))
	}

	return time.Duration(sign*n) * multiplier, nil
}
