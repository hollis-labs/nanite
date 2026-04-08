package mcp

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
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
			Description: "Fetch a URL via HTTP GET and return the response body as text. Returns status code and body truncated at 8000 chars. Note: many news/social sites block automated requests (403/Cloudflare). Works best with APIs, documentation sites, and raw content URLs. Example: web_fetch(url=\"https://api.github.com/repos/hollis-labs/nanite\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "Full URL including https://. Example: https://docs.anthropic.com/en/docs"},
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
		{
			Name:        "base64_encode",
			Description: "Encode a string to base64.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "String to encode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "base64_decode",
			Description: "Decode a base64 string.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "Base64 string to decode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "url_encode",
			Description: "URL percent-encode a string.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "String to encode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "url_decode",
			Description: "Decode a URL percent-encoded string.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "URL-encoded string to decode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "hash",
			Description: "Compute a hash of an input string. Supports sha256 and md5.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input":     map[string]any{"type": "string", "description": "String to hash"},
					"algorithm": map[string]any{"type": "string", "description": "Hash algorithm: sha256 (default) or md5"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "math_eval",
			Description: "Evaluate a basic arithmetic expression with +, -, *, /, ^, parentheses, and floats.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": map[string]any{"type": "string", "description": "Arithmetic expression to evaluate"},
				},
				"required": []string{"expression"},
			},
		},
		{
			Name:        "think",
			Description: "A scratchpad tool for organizing your reasoning. Use this to pause and think through your approach before acting. The thought content is the value — the tool simply acknowledges receipt.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thought": map[string]any{"type": "string", "description": "Your internal reasoning, plan, or analysis"},
				},
				"required": []string{"thought"},
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
	case "base64_encode":
		return g.callBase64Encode(args)
	case "base64_decode":
		return g.callBase64Decode(args)
	case "url_encode":
		return g.callURLEncode(args)
	case "url_decode":
		return g.callURLDecode(args)
	case "hash":
		return g.callHash(args)
	case "math_eval":
		return g.callMathEval(args)
	case "think":
		return g.callThink(args)
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

func (g *GeneralToolsTransport) callBase64Encode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	return textResult(base64.StdEncoding.EncodeToString([]byte(input))), nil
}

func (g *GeneralToolsTransport) callBase64Decode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return errorResult(fmt.Sprintf("decode error: %v", err)), nil
	}
	return textResult(string(decoded)), nil
}

func (g *GeneralToolsTransport) callURLEncode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	return textResult(url.QueryEscape(input)), nil
}

func (g *GeneralToolsTransport) callURLDecode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	decoded, err := url.QueryUnescape(input)
	if err != nil {
		return errorResult(fmt.Sprintf("decode error: %v", err)), nil
	}
	return textResult(decoded), nil
}

func (g *GeneralToolsTransport) callHash(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	algorithm, _ := args["algorithm"].(string)
	if algorithm == "" {
		algorithm = "sha256"
	}

	switch algorithm {
	case "sha256":
		h := sha256.Sum256([]byte(input))
		return textResult(hex.EncodeToString(h[:])), nil
	case "md5":
		h := md5.Sum([]byte(input))
		return textResult(hex.EncodeToString(h[:])), nil
	default:
		return errorResult(fmt.Sprintf("unsupported algorithm %q (use sha256 or md5)", algorithm)), nil
	}
}

func (g *GeneralToolsTransport) callMathEval(args map[string]any) (*ToolResult, error) {
	expr, _ := args["expression"].(string)
	if expr == "" {
		return errorResult("expression is required"), nil
	}

	result, err := evalExpr(expr)
	if err != nil {
		return errorResult(fmt.Sprintf("eval error: %v", err)), nil
	}

	// Format nicely: show integer if whole number.
	if result == float64(int64(result)) && !math.IsInf(result, 0) {
		return textResult(strconv.FormatInt(int64(result), 10)), nil
	}
	return textResult(strconv.FormatFloat(result, 'g', -1, 64)), nil
}

func (g *GeneralToolsTransport) callThink(args map[string]any) (*ToolResult, error) {
	thought, _ := args["thought"].(string)
	if thought == "" {
		return errorResult("thought is required"), nil
	}
	return textResult("Thought recorded."), nil
}

// --- math expression parser (recursive descent) ---

type mathParser struct {
	input string
	pos   int
}

func evalExpr(expr string) (float64, error) {
	p := &mathParser{input: strings.TrimSpace(expr)}
	result, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) {
		return 0, fmt.Errorf("unexpected character at position %d: %q", p.pos, string(p.input[p.pos]))
	}
	return result, nil
}

func (p *mathParser) parseExpr() (float64, error) {
	return p.parseAddSub()
}

func (p *mathParser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (p *mathParser) parseMulDiv() (float64, error) {
	left, err := p.parsePower()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' {
			return left, nil
		}
		p.pos++
		right, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}
}

func (p *mathParser) parsePower() (float64, error) {
	base, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '^' {
		p.pos++
		exp, err := p.parsePower() // right-associative
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '-' {
		p.pos++
		val, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}
	if p.pos < len(p.input) && p.input[p.pos] == '+' {
		p.pos++
		return p.parseUnary()
	}
	return p.parseAtom()
}

func (p *mathParser) parseAtom() (float64, error) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	// Parenthesized expression.
	if p.input[p.pos] == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}

	// Number.
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsDigit(rune(p.input[p.pos])) || p.input[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("unexpected character: %q", string(p.input[p.pos]))
	}
	return strconv.ParseFloat(p.input[start:p.pos], 64)
}

func (p *mathParser) skipSpaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}
