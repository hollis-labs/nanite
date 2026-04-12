package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/sandbox"
)

// agentExecFunc is the type of sandbox.AgentExec. It is stored as a package
// variable so tests can stub it without spinning up real sandbox infrastructure.
type agentExecFunc func(sandbox.AgentExecOpts) (*sandbox.ExecResult, error)

var defaultAgentExec agentExecFunc = sandbox.AgentExec

// DevToolsTransport provides built-in developer tools (grep, read, write)
// that operate on local files, scoped to an allow-list of directories.
type DevToolsTransport struct {
	AllowedPaths []string // Absolute directory paths tools may access.

	// agentExec is the sandbox execution entry point. Nil means use the
	// package default (sandbox.AgentExec). Tests override this to capture
	// invocations without running real commands.
	agentExec agentExecFunc
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

// resolveAllowed validates userPath against the configured allow-list using
// pathsafe.ResolveUnder for each root. The first root whose relative path to
// the requested target stays under that root wins. If every root rejects the
// path, the *pathsafe.EscapeError from the last attempt is returned so
// callers can classify via errors.As.
//
// Because pathsafe.ResolveUnder treats absolute user paths as root-relative
// (strip the leading separator and join under root), passing an absolute
// path straight through would double the prefix. To compose correctly we
// first compute the relative path from each root to the absolute target,
// skip roots where the target lies outside (Rel returns "../..."), and then
// delegate the symlink-aware escape check to pathsafe.
func (d *DevToolsTransport) resolveAllowed(userPath string) (string, error) {
	if userPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if len(d.AllowedPaths) == 0 {
		return "", fmt.Errorf("no allowed paths configured")
	}

	abs, err := filepath.Abs(userPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	var lastErr error
	for _, root := range d.AllowedPaths {
		absRoot, absErr := filepath.Abs(root)
		if absErr != nil {
			lastErr = absErr
			continue
		}
		// Resolve symlinks on the root once so macOS /var vs /private/var
		// comparisons work. Fall back to the cleaned path if resolution
		// fails (e.g. root does not exist).
		if real, evalErr := filepath.EvalSymlinks(absRoot); evalErr == nil {
			absRoot = real
		}
		// Resolve symlinks on the target's longest existing ancestor so the
		// Rel computation uses the same canonical form as the root.
		target := abs
		if real, evalErr := filepath.EvalSymlinks(target); evalErr == nil {
			target = real
		}

		rel, relErr := filepath.Rel(absRoot, target)
		if relErr != nil {
			lastErr = relErr
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			// Target is outside this root; try the next root.
			lastErr = &pathsafe.EscapeError{
				Root:     absRoot,
				Attempt:  userPath,
				Resolved: target,
				Cause:    errors.New("resolved path outside root"),
			}
			continue
		}
		// Delegate the final symlink-aware check to pathsafe. This catches
		// symlinks inside the target that point out of the root.
		resolved, resolveErr := pathsafe.ResolveUnder(absRoot, rel)
		if resolveErr == nil {
			return resolved, nil
		}
		lastErr = resolveErr
	}
	// Surface the typed *pathsafe.EscapeError from the final attempt so
	// callers can classify with errors.As. Non-escape errors (e.g. malformed
	// ancestor) propagate too.
	return "", lastErr
}

// pathErrorResult formats a resolveAllowed error into an MCP tool error,
// preserving the *pathsafe.EscapeError type in the textual message.
func pathErrorResult(userPath string, err error) *ToolResult {
	var escape *pathsafe.EscapeError
	if errors.As(err, &escape) {
		return errorResult(fmt.Sprintf("path %q outside allowed directories: %s", userPath, escape.Error()))
	}
	return errorResult(fmt.Sprintf("path %q: %v", userPath, err))
}

// ListTools returns the three dev tools.
func (d *DevToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "dev_read",
			Description: "Read file contents with optional line range. Returns contents with line numbers. All paths must be absolute (start with /). Allowed directories: ~/Projects-apps, ~/Projects. Example: dev_read(path=\"/Users/chris/Projects-apps/mentat/docs/README.md\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "Absolute file path (must start with /). Example: /Users/chris/Projects-apps/mentat/README.md"},
					"offset": map[string]any{"type": "integer", "description": "Start line (1-based, default 1)"},
					"limit":  map[string]any{"type": "integer", "description": "Number of lines to return (default 200)"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "dev_grep",
			Description: "Search file contents matching a regex pattern within a directory. Returns matches with surrounding context lines. Both pattern and directory are required. Directory must be an absolute path. Example: dev_grep(pattern=\"func main\", directory=\"/Users/chris/Projects-apps/mentat\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":   map[string]any{"type": "string", "description": "Regex pattern to search for. Example: TODO|FIXME"},
					"directory": map[string]any{"type": "string", "description": "Absolute directory path to search in. Example: /Users/chris/Projects-apps/mentat"},
					"glob":      map[string]any{"type": "string", "description": "File glob filter (e.g. *.go, *.ts). Default: all files"},
					"context":   map[string]any{"type": "integer", "description": "Lines of context around matches (default 2)"},
				},
				"required": []string{"pattern", "directory"},
			},
		},
		{
			Name:        "dev_write",
			Description: "Write content to a file. Creates parent directories if needed. Overwrites existing content. Path must be absolute. Example: dev_write(path=\"/Users/chris/Projects-apps/mentat/notes.md\", content=\"# Notes\\nContent here\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Absolute file path to write (must start with /)"},
					"content": map[string]any{"type": "string", "description": "File content to write"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "dev_glob",
			Description: "Find files matching a glob pattern within a directory. The 'pattern' and 'directory' are SEPARATE parameters — do NOT combine them. Pattern is relative to directory. Supports ** for recursive matching. Results sorted by modification time (newest first). Example: dev_glob(pattern=\"**/*.md\", directory=\"/Users/chris/Projects-apps/mentat/docs\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":     map[string]any{"type": "string", "description": "Glob pattern RELATIVE to directory. Examples: **/*.md, *.go, src/**/*.ts. Do NOT include the directory path in the pattern."},
					"directory":   map[string]any{"type": "string", "description": "Absolute directory path to search in. Must start with /. Example: /Users/chris/Projects-apps/mentat"},
					"max_results": map[string]any{"type": "integer", "description": "Maximum results to return (default 50)"},
				},
				"required": []string{"pattern", "directory"},
			},
		},
		{
			Name:        "dev_edit",
			Description: "Edit a file by finding and replacing a string. The old_string must appear in the file. If replace_all is false (default), old_string must appear exactly once. Path must be absolute. Example: dev_edit(path=\"/Users/chris/Projects-apps/mentat/config.yaml\", old_string=\"port: 8080\", new_string=\"port: 9090\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string", "description": "Absolute file path to edit (must start with /)"},
					"old_string":  map[string]any{"type": "string", "description": "Exact text to find and replace (must exist in the file)"},
					"new_string":  map[string]any{"type": "string", "description": "Replacement text"},
					"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences (default false — requires old_string to be unique)"},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
		{
			Name:        "dev_bash",
			Description: "Execute a shell command and return stdout + stderr. Use for git, ls, find, build commands, etc. Working directory must be absolute and in allowed paths. Example: dev_bash(command=\"git log --oneline -5\", working_dir=\"/Users/chris/Projects-apps/mentat\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":     map[string]any{"type": "string", "description": "Shell command to execute. Example: ls -la, git status, go build ./..."},
					"working_dir": map[string]any{"type": "string", "description": "Absolute working directory (must be in allowed paths). Defaults to first allowed path if omitted."},
					"timeout":     map[string]any{"type": "integer", "description": "Timeout in seconds (default 30, max 120)"},
				},
				"required": []string{"command"},
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
	case "dev_glob":
		return d.callGlob(args)
	case "dev_edit":
		return d.callEdit(args)
	case "dev_bash":
		return d.callBash(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (d *DevToolsTransport) callRead(args map[string]any) (*ToolResult, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return errorResult("path is required"), nil
	}
	resolved, err := d.resolveAllowed(path)
	if err != nil {
		return pathErrorResult(path, err), nil
	}
	path = resolved

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
	resolvedDir, err := d.resolveAllowed(dir)
	if err != nil {
		return pathErrorResult(dir, err), nil
	}
	dir = resolvedDir

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
	resolved, err := d.resolveAllowed(path)
	if err != nil {
		return pathErrorResult(path, err), nil
	}
	path = resolved

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errorResult(fmt.Sprintf("mkdir: %v", err)), nil
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return errorResult(fmt.Sprintf("write: %v", err)), nil
	}

	return textResult(fmt.Sprintf("wrote %d bytes to %s", len(content), path)), nil
}

func (d *DevToolsTransport) callEdit(args map[string]any) (*ToolResult, error) {
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if path == "" || oldStr == "" {
		return errorResult("path and old_string are required"), nil
	}
	if oldStr == newStr {
		return errorResult("old_string and new_string must be different"), nil
	}
	resolved, err := d.resolveAllowed(path)
	if err != nil {
		return pathErrorResult(path, err), nil
	}
	path = resolved

	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(fmt.Sprintf("read: %v", err)), nil
	}
	content := string(data)

	replaceAll, _ := args["replace_all"].(bool)

	count := strings.Count(content, oldStr)
	if count == 0 {
		return errorResult("old_string not found in file"), nil
	}
	if !replaceAll && count > 1 {
		return errorResult(fmt.Sprintf("old_string appears %d times — provide more context to make it unique, or set replace_all=true", count)), nil
	}

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, oldStr, newStr)
	} else {
		newContent = strings.Replace(content, oldStr, newStr, 1)
	}

	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return errorResult(fmt.Sprintf("write: %v", err)), nil
	}

	// Build a summary showing the line number of the first replacement.
	lines := strings.Split(content, "\n")
	lineNum := 0
	for i, line := range lines {
		if strings.Contains(line, strings.Split(oldStr, "\n")[0]) {
			lineNum = i + 1
			break
		}
	}

	msg := fmt.Sprintf("Replaced %d occurrence(s) in %s", count, path)
	if lineNum > 0 {
		msg += fmt.Sprintf(" (first at line %d)", lineNum)
	}
	return textResult(msg), nil
}

func (d *DevToolsTransport) callGlob(args map[string]any) (*ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	dir, _ := args["directory"].(string)
	if pattern == "" || dir == "" {
		return errorResult("pattern and directory are required"), nil
	}
	resolvedDir, err := d.resolveAllowed(dir)
	if err != nil {
		return pathErrorResult(dir, err), nil
	}
	dir = resolvedDir

	maxResults := intArg(args, "max_results", 50)
	if maxResults < 1 {
		maxResults = 1
	}

	type fileEntry struct {
		path    string
		modTime time.Time
	}
	var matches []fileEntry

	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		if globMatch(pattern, relPath) {
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			matches = append(matches, fileEntry{path: relPath, modTime: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return errorResult(fmt.Sprintf("walk error: %v", err)), nil
	}

	// Sort by modification time, newest first.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime.After(matches[j].modTime)
	})

	if len(matches) == 0 {
		return textResult("no matches found"), nil
	}

	var sb strings.Builder
	count := len(matches)
	if count > maxResults {
		count = maxResults
	}
	fmt.Fprintf(&sb, "Found %d file(s)", len(matches))
	if len(matches) > maxResults {
		fmt.Fprintf(&sb, " (showing first %d)", maxResults)
	}
	sb.WriteString(":\n\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "%s\n", matches[i].path)
	}
	return textResult(sb.String()), nil
}

// globMatch matches a path against a pattern supporting ** for recursive matching.
func globMatch(pattern, path string) bool {
	// Split pattern and path into segments.
	patParts := strings.Split(filepath.ToSlash(pattern), "/")
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	return globMatchParts(patParts, pathParts)
}

func globMatchParts(patParts, pathParts []string) bool {
	if len(patParts) == 0 {
		return len(pathParts) == 0
	}

	if patParts[0] == "**" {
		rest := patParts[1:]
		// ** can match zero or more path segments.
		for i := 0; i <= len(pathParts); i++ {
			if globMatchParts(rest, pathParts[i:]) {
				return true
			}
		}
		return false
	}

	if len(pathParts) == 0 {
		return false
	}

	matched, _ := filepath.Match(patParts[0], pathParts[0])
	if !matched {
		return false
	}
	return globMatchParts(patParts[1:], pathParts[1:])
}

// devBashSessionID is the session ID used for dev_bash sandbox scoping. All
// dev_bash invocations share one session so callers can observe consistent
// resource limits and denylist behavior; the underlying command still runs
// inside the agent sandbox with stripped PATH, filtered environment, and the
// platform OS-level isolation layer.
const devBashSessionID = "dev-bash"

func (d *DevToolsTransport) callBash(args map[string]any) (*ToolResult, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return errorResult("command is required"), nil
	}

	workDir, _ := args["working_dir"].(string)
	if workDir == "" {
		if len(d.AllowedPaths) > 0 {
			workDir = d.AllowedPaths[0]
		}
	} else {
		resolved, err := d.resolveAllowed(workDir)
		if err != nil {
			return pathErrorResult(workDir, err), nil
		}
		workDir = resolved
	}
	_ = workDir // sandbox scopes CWD to its own directory; working_dir is
	// accepted for compatibility and is validated above but the sandbox
	// enforces its own sandboxDir regardless.

	timeout := intArg(args, "timeout", 30)
	if timeout < 1 {
		timeout = 1
	}
	if timeout > 120 {
		timeout = 120
	}

	execFn := d.agentExec
	if execFn == nil {
		execFn = defaultAgentExec
	}

	result, err := execFn(sandbox.AgentExecOpts{
		SessionID: devBashSessionID,
		Command:   "sh",
		Args:      []string{"-c", command},
		Timeout:   time.Duration(timeout) * time.Second,
	})
	if err != nil {
		// sandbox setup / denylist / dir-resolve errors surface here. These
		// are hard rejections (e.g. CheckDenylist match).
		return errorResult(fmt.Sprintf("sandbox error: %v", err)), nil
	}

	var sb strings.Builder
	if result.Stdout != "" {
		sb.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("--- stderr ---\n")
		sb.WriteString(result.Stderr)
	}
	output := sb.String()

	if result.TimedOut {
		return errorResult(fmt.Sprintf("command timed out after %ds\n%s", timeout, output)), nil
	}
	if result.ExitCode != 0 {
		return errorResult(fmt.Sprintf("exit error: exit status %d\n%s", result.ExitCode, output)), nil
	}

	if output == "" {
		output = "(no output)"
	}
	return textResult(output), nil
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
