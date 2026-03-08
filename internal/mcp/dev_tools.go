package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DevToolsTransport provides built-in developer tools (grep, read, write)
// that operate on local files, scoped to an allow-list of directories.
type DevToolsTransport struct {
	AllowedPaths []string // Absolute directory paths tools may access.
}

// NewDevToolsTransport creates a DevToolsTransport scoped to the given paths.
func NewDevToolsTransport(allowedPaths []string) *DevToolsTransport {
	cleaned := make([]string, 0, len(allowedPaths))
	for _, p := range allowedPaths {
		abs, err := filepath.Abs(p)
		if err == nil {
			cleaned = append(cleaned, abs)
		}
	}
	return &DevToolsTransport{AllowedPaths: cleaned}
}

// isAllowed checks whether a path is under one of the allowed directories.
func (d *DevToolsTransport) isAllowed(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	// Resolve symlinks.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// File may not exist yet (write). Check parent dir.
		real = abs
	}
	for _, allowed := range d.AllowedPaths {
		if strings.HasPrefix(real, allowed+"/") || real == allowed {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside allowed directories", path)
}

// ListTools returns the three dev tools.
func (d *DevToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "dev_read",
			Description: "Read file contents with optional line range. Returns contents with line numbers.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "Absolute file path to read"},
					"offset": map[string]any{"type": "integer", "description": "Start line (1-based, default 1)"},
					"limit":  map[string]any{"type": "integer", "description": "Number of lines to return (default 200)"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "dev_grep",
			Description: "Search files matching a regex pattern within a directory. Returns matches with surrounding context.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":   map[string]any{"type": "string", "description": "Regex pattern to search for"},
					"directory": map[string]any{"type": "string", "description": "Directory to search in"},
					"glob":      map[string]any{"type": "string", "description": "File glob filter (e.g. *.go, *.ts). Default: all files"},
					"context":   map[string]any{"type": "integer", "description": "Lines of context around matches (default 2)"},
				},
				"required": []string{"pattern", "directory"},
			},
		},
		{
			Name:        "dev_write",
			Description: "Write content to a file. Creates parent directories if needed. Overwrites existing content.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Absolute file path to write"},
					"content": map[string]any{"type": "string", "description": "File content to write"},
				},
				"required": []string{"path", "content"},
			},
		},
	}, nil
}

// CallTool dispatches to the appropriate handler.
func (d *DevToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
	switch name {
	case "dev_read":
		return d.callRead(args)
	case "dev_grep":
		return d.callGrep(args)
	case "dev_write":
		return d.callWrite(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (d *DevToolsTransport) callRead(args map[string]any) (*ToolResult, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return errorResult("path is required"), nil
	}
	if err := d.isAllowed(path); err != nil {
		return errorResult(err.Error()), nil
	}

	offset := intArg(args, "offset", 1)
	limit := intArg(args, "limit", 200)
	if offset < 1 {
		offset = 1
	}

	f, err := os.Open(path)
	if err != nil {
		return errorResult(fmt.Sprintf("open: %v", err)), nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var sb strings.Builder
	lineNum := 0
	collected := 0
	for scanner.Scan() {
		lineNum++
		if lineNum < offset {
			continue
		}
		if collected >= limit {
			break
		}
		fmt.Fprintf(&sb, "%6d\t%s\n", lineNum, scanner.Text())
		collected++
	}
	if err := scanner.Err(); err != nil {
		return errorResult(fmt.Sprintf("read error: %v", err)), nil
	}

	if collected == 0 {
		return textResult(fmt.Sprintf("(empty or offset %d beyond end of file at line %d)", offset, lineNum)), nil
	}
	return textResult(sb.String()), nil
}

func (d *DevToolsTransport) callGrep(args map[string]any) (*ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	dir, _ := args["directory"].(string)
	if pattern == "" || dir == "" {
		return errorResult("pattern and directory are required"), nil
	}
	if err := d.isAllowed(dir); err != nil {
		return errorResult(err.Error()), nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return errorResult(fmt.Sprintf("invalid regex: %v", err)), nil
	}

	globFilter, _ := args["glob"].(string)
	ctxLines := intArg(args, "context", 2)

	var sb strings.Builder
	matchCount := 0
	const maxMatches = 100

	err = filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if globFilter != "" {
			matched, _ := filepath.Match(globFilter, filepath.Base(path))
			if !matched {
				return nil
			}
		}
		if matchCount >= maxMatches {
			return filepath.SkipAll
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}

		relPath, _ := filepath.Rel(dir, path)
		if relPath == "" {
			relPath = path
		}

		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			matchCount++
			if matchCount > maxMatches {
				break
			}
			start := i - ctxLines
			if start < 0 {
				start = 0
			}
			end := i + ctxLines + 1
			if end > len(lines) {
				end = len(lines)
			}
			fmt.Fprintf(&sb, "--- %s:%d ---\n", relPath, i+1)
			for j := start; j < end; j++ {
				marker := " "
				if j == i {
					marker = ">"
				}
				fmt.Fprintf(&sb, "%s%4d\t%s\n", marker, j+1, lines[j])
			}
			sb.WriteString("\n")
		}
		return nil
	})
	if err != nil {
		return errorResult(fmt.Sprintf("walk error: %v", err)), nil
	}

	if matchCount == 0 {
		return textResult("no matches found"), nil
	}
	header := fmt.Sprintf("Found %d match(es):\n\n", matchCount)
	return textResult(header + sb.String()), nil
}

func (d *DevToolsTransport) callWrite(args map[string]any) (*ToolResult, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return errorResult("path is required"), nil
	}
	if err := d.isAllowed(path); err != nil {
		return errorResult(err.Error()), nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errorResult(fmt.Sprintf("mkdir: %v", err)), nil
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return errorResult(fmt.Sprintf("write: %v", err)), nil
	}

	return textResult(fmt.Sprintf("wrote %d bytes to %s", len(content), path)), nil
}

// --- helpers ---

func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return def
		}
		return int(i)
	default:
		return def
	}
}

func textResult(text string) *ToolResult {
	return &ToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

func errorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}
