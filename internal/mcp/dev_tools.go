package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/safego"
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
// Entries with a leading ~/ are expanded to the user's home directory before
// being absolutized so config-supplied paths like "~/Projects-apps" work.
func NewDevToolsTransport(allowedPaths []string) *DevToolsTransport {
	cleaned := make([]string, 0, len(allowedPaths))
	for _, p := range allowedPaths {
		expanded := expandHome(p)
		abs, err := filepath.Abs(expanded)
		if err == nil {
			cleaned = append(cleaned, abs)
		}
	}
	return &DevToolsTransport{AllowedPaths: cleaned}
}

// expandHome replaces a leading ~/ or bare ~ with the user's home directory.
// Returns the input unchanged when no leading tilde is present or when the
// home directory cannot be resolved. This is intentionally permissive: an
// unresolved tilde will fail downstream path-safety checks, not silently
// accept.
//
// CW-20260430-0005: Go's filepath package does not expand the shell tilde,
// so an LLM-supplied "~/Projects-apps/nanite" was being passed through
// filepath.Abs as a literal which produced "/cwd/~/Projects-apps/nanite" and
// blew the allow-list. Tilde expansion at the boundary fixes the
// canonicalization gap without weakening the symlink-aware escape check.
//
// CW-20260502-0014: routes through permission.HomeDir so $HOME-less
// launchd-spawned services still expand ~/ via the passwd record. Without
// this fallback the literal tilde flowed straight into the EscapeError and
// the path-grant store never registered ~/ mentions (c127 reproduction).
func expandHome(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		home, err := permission.HomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		home, err := permission.HomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
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
//
// Trust-agent extension (CW-20260430-0009): when the static AllowedPaths
// list rejects a path, we consult the session-scoped path-grant store
// stamped on ctx via permission.WithPathGrants. Explicit-mention grants
// (Q1-Q3 of the locked design) widen the allow-list per session without
// requiring ahead-of-time config. The pathsafe escape check still runs on
// the candidate so traversal protection is unaffected.
func (d *DevToolsTransport) resolveAllowed(ctx context.Context, userPath string) (string, error) {
	if userPath == "" {
		return "", fmt.Errorf("path is required")
	}

	// Expand a leading ~ in the user-supplied path. Go's filepath package
	// treats ~ as a literal, but agents (and humans) commonly write
	// ~/Projects-apps/... expecting shell-style expansion. Without this
	// step filepath.Abs("~/Projects") becomes "/cwd/~/Projects" and trips
	// the escape check even on roots that should accept it.
	userPath = expandHome(userPath)

	abs, err := filepath.Abs(userPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	if len(d.AllowedPaths) == 0 {
		// No static allow-list configured. Fall through to the session-
		// grant check; if that also rejects, we report a typed escape
		// error rather than the legacy "no allowed paths configured"
		// string so the caller's classification (errors.As) still works.
		if resolved, ok := d.tryResolveViaSessionGrant(ctx, abs, userPath); ok {
			return resolved, nil
		}
		return "", &pathsafe.EscapeError{
			Root:     "",
			Attempt:  userPath,
			Resolved: abs,
			Cause:    errors.New("no allowed paths configured for this session"),
		}
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
	// Trust-agent fallback (CW-20260430-0009): the static AllowedPaths
	// list rejected. Consult the session-scoped grant store; if a prior
	// explicit user-message mention granted access to this path (or its
	// parent), accept it.
	if resolved, ok := d.tryResolveViaSessionGrant(ctx, abs, userPath); ok {
		return resolved, nil
	}
	// Surface the typed *pathsafe.EscapeError from the final attempt so
	// callers can classify with errors.As. Non-escape errors (e.g. malformed
	// ancestor) propagate too.
	return "", lastErr
}

// tryResolveViaSessionGrant runs the path-safety escape check using the
// matching session grant as the "root" so the symlink-aware safety net
// stays in the loop. Returns (cleanedAbs, true) on success; ("", false)
// when the ctx carries no grant store, the session has no matching grant,
// or the safety check fails.
//
// abs is the already-tilde-expanded, filepath.Abs'd candidate; userPath is
// kept around for the EscapeError diagnostic when a downstream call needs
// it (this helper does not raise such errors itself — it just signals
// allow/no-match).
//
// Emits a structured INFO log on every call regardless of outcome so the
// path-grant resolution boundary is observable in production. Without
// this, a silent miss looks identical to a silent never-stamped-ctx —
// the c138 reproduction (Glass-8 partial regression) was invisible until
// this log was added. Tool calls are low-frequency enough that volume
// is not a concern.
func (d *DevToolsTransport) tryResolveViaSessionGrant(ctx context.Context, abs, _ string) (string, bool) {
	sessionID, checker := permission.PathGrantsFromContext(ctx)
	hadChecker := checker != nil

	var (
		bucketSize    int
		matched       bool
		kind          = permission.LookupKindNone
		viaSessionID  string
	)
	if hadChecker && sessionID != "" {
		bucketSize = checker.BucketSize(sessionID)
		matched, kind, viaSessionID = checker.LookupPath(sessionID, abs)
	}

	slog.Info("permission: dev_tools grant-resolution",
		"session_id", sessionID,
		"had_checker", hadChecker,
		"candidate_abs", abs,
		"bucket_size", bucketSize,
		"match_found", matched,
		"match_kind", string(kind),
		"match_via_session_id", viaSessionID,
	)

	if !matched {
		return "", false
	}
	// Resolve symlinks on the existing-ancestor of the target so the
	// downstream open()/MkdirAll() observes the same canonical form
	// pathsafe would. Match the per-root logic above.
	target := abs
	if real, evalErr := filepath.EvalSymlinks(target); evalErr == nil {
		target = real
	}
	return filepath.Clean(target), true
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

// allowedDirsSummary returns a comma-separated list of configured allowed
// directories, or the string "configured allowed directories" when none are
// set (e.g. during early construction before paths are provided). This is
// used in LLM-facing tool descriptions so they reflect the actual workspace
// rather than hardcoded workstation paths.
func (d *DevToolsTransport) allowedDirsSummary() string {
	if len(d.AllowedPaths) == 0 {
		return "configured allowed directories"
	}
	return strings.Join(d.AllowedPaths, ", ")
}

// exampleRootPath returns the first allowed path as an example base, or a
// neutral placeholder when no paths are configured yet.
func (d *DevToolsTransport) exampleRootPath() string {
	if len(d.AllowedPaths) > 0 {
		return d.AllowedPaths[0]
	}
	return "/path/to/project"
}

// tildeAcceptanceNote returns the shared LLM-facing instruction included
// in every dev_* tool description: paths beginning with ~/ are accepted
// and expanded server-side to the session user's actual home directory,
// and the agent must pass user-supplied ~/ paths verbatim rather than
// fabricating an absolute path with a guessed username.
//
// Background (CW-fix-dev-glob-grant): smoke sessions surfaced a
// non-deterministic LLM failure where the agent, faced with "must start
// with /" and a user message containing ~/Projects-apps, would convert
// the tilde to /Users/<fabricated-name>/Projects-apps. The path-grant
// store had the correct grants registered against the real user's home,
// so the lookup missed and dev_* failed. Telling the agent up-front
// that ~/ is acceptable removes the impulse to invent.
func tildeAcceptanceNote() string {
	home, err := permission.HomeDir()
	homeHint := "the session user's home directory"
	if err == nil && home != "" {
		homeHint = home + " (the session user's home directory)"
	}
	return fmt.Sprintf("Paths starting with ~/ are accepted and expanded server-side to %s. "+
		"When the user mentions a ~/ path, pass it VERBATIM (e.g. ~/Projects-apps); "+
		"do NOT substitute a username — fabricated paths like /Users/<name>/... where <name> is guessed will fail.",
		homeHint)
}

// ListTools returns the dev tools with descriptions derived from the
// configured AllowedPaths so LLM-facing content reflects the actual workspace.
func (d *DevToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	exRoot := d.exampleRootPath()
	allowedDirs := d.allowedDirsSummary()
	tildeNote := tildeAcceptanceNote()
	return []Tool{
		{
			Name:        "dev_read",
			Description: fmt.Sprintf("Read file contents with optional line range. Returns contents with line numbers. Paths must be absolute (start with / or ~/). %s Glob/search before read on unfamiliar paths — dev_read on a non-existent path wastes a round-trip. Allowed directories: %s. Example: dev_read(path=%q)", tildeNote, allowedDirs, filepath.Join(exRoot, "docs", "README.md")),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": fmt.Sprintf("Absolute file path (must start with / or ~/). Example: %s", filepath.Join(exRoot, "README.md"))},
					"offset": map[string]any{"type": "integer", "description": "Start line (1-based, default 1)"},
					"limit":  map[string]any{"type": "integer", "description": "Number of lines to return (default 200)"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "dev_grep",
			Description: fmt.Sprintf("Search file contents matching a regex pattern within a directory. Returns matches with surrounding context lines. Both pattern and directory are required. Directory must be an absolute path (start with / or ~/). %s Example: dev_grep(pattern=\"func main\", directory=%q)", tildeNote, exRoot),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":   map[string]any{"type": "string", "description": "Regex pattern to search for. Example: TODO|FIXME"},
					"directory": map[string]any{"type": "string", "description": fmt.Sprintf("Absolute directory path to search in (must start with / or ~/). Example: %s", exRoot)},
					"glob":      map[string]any{"type": "string", "description": "File glob filter (e.g. *.go, *.ts). Default: all files"},
					"context":   map[string]any{"type": "integer", "description": "Lines of context around matches (default 2)"},
				},
				"required": []string{"pattern", "directory"},
			},
		},
		{
			Name:        "dev_write",
			Description: fmt.Sprintf("Write content to a file. Creates parent directories if needed. Overwrites existing content. Path must be absolute (start with / or ~/). %s Example: dev_write(path=%q, content=\"# Notes\\nContent here\")", tildeNote, filepath.Join(exRoot, "notes.md")),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Absolute file path to write (must start with / or ~/)"},
					"content": map[string]any{"type": "string", "description": "File content to write"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "dev_glob",
			Description: fmt.Sprintf("Find files matching a glob pattern within a directory. The 'pattern' and 'directory' are SEPARATE parameters — do NOT combine them. Pattern is relative to directory. Supports ** for recursive matching. Results sorted by modification time (newest first). Directory must be absolute (start with / or ~/). %s Example: dev_glob(pattern=\"**/*.md\", directory=%q)", tildeNote, filepath.Join(exRoot, "docs")),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":     map[string]any{"type": "string", "description": "Glob pattern RELATIVE to directory. Examples: **/*.md, *.go, src/**/*.ts. Do NOT include the directory path in the pattern."},
					"directory":   map[string]any{"type": "string", "description": fmt.Sprintf("Absolute directory path to search in (must start with / or ~/). Example: %s", exRoot)},
					"max_results": map[string]any{"type": "integer", "description": "Maximum results to return (default 50)"},
				},
				"required": []string{"pattern", "directory"},
			},
		},
		{
			Name:        "dev_edit",
			Description: fmt.Sprintf("Edit a file by finding and replacing a string. The old_string must appear in the file. If replace_all is false (default), old_string must appear exactly once. Path must be absolute (start with / or ~/). %s Example: dev_edit(path=%q, old_string=\"port: 8080\", new_string=\"port: 9090\")", tildeNote, filepath.Join(exRoot, "config.yaml")),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string", "description": "Absolute file path to edit (must start with / or ~/)"},
					"old_string":  map[string]any{"type": "string", "description": "Exact text to find and replace (must exist in the file)"},
					"new_string":  map[string]any{"type": "string", "description": "Replacement text"},
					"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences (default false — requires old_string to be unique)"},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
		{
			Name:        "dev_bash",
			Description: fmt.Sprintf("Execute a shell command and return stdout + stderr. Use for git, ls, find, build commands, etc. Working directory must be absolute and in allowed paths (start with / or ~/). %s Example: dev_bash(command=\"git log --oneline -5\", working_dir=%q)", tildeNote, exRoot),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":     map[string]any{"type": "string", "description": "Shell command to execute. Example: ls -la, git status, go build ./..."},
					"working_dir": map[string]any{"type": "string", "description": "Absolute working directory (must start with / or ~/, and be in allowed paths). Defaults to first allowed path if omitted."},
					"timeout":     map[string]any{"type": "integer", "description": "Timeout in seconds (default 30, max 120)"},
				},
				"required": []string{"command"},
			},
		},
	}, nil
}

// DevToolProviderDefinitions returns all dev tool definitions as
// provider.ToolDefinition, suitable for registering as builtins so
// they appear in every session's tool list regardless of broker selection.
func DevToolProviderDefinitions() []provider.ToolDefinition {
	tools, _ := (&DevToolsTransport{}).ListTools(context.Background())
	defs := make([]provider.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = provider.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return defs
}

// Tunable bounds for dev tools. These are package constants so tests and
// callers have a single place to reason about memory / CPU budgets.
const (
	// devBashStreamCap is the per-stream (stdout, stderr) byte cap applied when
	// assembling the dev_bash tool result. Output beyond the cap is discarded
	// and a truncation marker is appended. 1 MiB per stream is ample for human
	// inspection and bounds the steady-state memory a runaway command can
	// wedge into the tool result envelope.
	devBashStreamCap = 1 << 20 // 1 MiB

	// devGrepPerFileCap skips any file larger than this during dev_grep. Text
	// files beyond 10 MiB are almost always generated blobs (minified JS,
	// sqlite dumps, log rolls) that the grep handler has no business slurping
	// into memory.
	devGrepPerFileCap = 10 << 20 // 10 MiB

	// devGrepResultCap bounds the total bytes of match output returned from
	// dev_grep. Walking huge trees can otherwise return arbitrarily large
	// payloads that blow past LLM context budgets.
	devGrepResultCap = 1 << 20 // 1 MiB

	// devGrepPatternCap bounds the length of a user-supplied regex pattern.
	// Go's RE2 engine is linear-time in input length, but a 10 MB pattern is
	// still a clear abuse signal.
	devGrepPatternCap = 4 << 10 // 4 KiB

	// devGrepFileBudget caps the number of files dev_grep will inspect per
	// call. Monorepos can have hundreds of thousands of files; this is a soft
	// liveness bound that surfaces a truncation marker rather than silently
	// blocking on a multi-minute walk.
	devGrepFileBudget = 10_000
)

// CallTool dispatches to the appropriate handler.
func (d *DevToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch name {
	case "dev_read":
		return d.callRead(ctx, args)
	case "dev_grep":
		return d.callGrep(ctx, args)
	case "dev_write":
		return d.callWrite(ctx, args)
	case "dev_glob":
		return d.callGlob(ctx, args)
	case "dev_edit":
		return d.callEdit(ctx, args)
	case "dev_bash":
		return d.callBash(ctx, args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (d *DevToolsTransport) callRead(ctx context.Context, args map[string]any) (*ToolResult, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return errorResult("path is required"), nil
	}
	resolved, err := d.resolveAllowed(ctx, path)
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
		if err := ctx.Err(); err != nil {
			return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
		}
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

func (d *DevToolsTransport) callGrep(ctx context.Context, args map[string]any) (*ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	dir, _ := args["directory"].(string)
	if pattern == "" || dir == "" {
		return errorResult("pattern and directory are required"), nil
	}
	if len(pattern) > devGrepPatternCap {
		return errorResult(fmt.Sprintf("pattern too long: %d bytes (max %d)", len(pattern), devGrepPatternCap)), nil
	}
	resolvedDir, err := d.resolveAllowed(ctx, dir)
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
	if ctxLines < 0 {
		ctxLines = 0
	}
	// Bound context-line ring size so a huge caller-supplied context value
	// cannot blow the ring allocation.
	if ctxLines > 32 {
		ctxLines = 32
	}
	ringSize := ctxLines + 1
	if ringSize < 1 {
		ringSize = 1
	}

	var sb strings.Builder
	matchCount := 0
	filesInspected := 0
	filesSkippedBySize := 0
	truncatedBySize := false
	truncatedByMatches := false
	truncatedByBytes := false
	truncatedByFileBudget := false
	const maxMatches = 100

	// errStopWalk is a sentinel used to short-circuit filepath.Walk when a cap
	// fires. filepath.Walk treats any non-nil error as a stop signal, and we
	// translate the sentinel back to "clean stop" after the walk returns.
	errStopWalk := errors.New("stop walk")

	err = filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			truncatedByMatches = true
			return errStopWalk
		}
		if sb.Len() >= devGrepResultCap {
			truncatedByBytes = true
			return errStopWalk
		}
		if filesInspected >= devGrepFileBudget {
			truncatedByFileBudget = true
			return errStopWalk
		}
		if info.Size() > devGrepPerFileCap {
			filesSkippedBySize++
			truncatedBySize = true
			return nil
		}
		filesInspected++

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		relPath, _ := filepath.Rel(dir, path)
		if relPath == "" {
			relPath = path
		}

		// Ring of recent lines for pre-match context.
		ring := make([]string, ringSize)
		ringStart := 0 // lowest line number currently in the ring
		ringLen := 0
		lineNo := 0
		pendingTrail := 0
		matchLine := 0 // the line number whose post-context we are currently trailing

		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			lineNo++
			line := scanner.Text()

			if re.MatchString(line) {
				matchCount++
				if matchCount > maxMatches {
					truncatedByMatches = true
					break
				}
				fmt.Fprintf(&sb, "--- %s:%d ---\n", relPath, lineNo)
				// Pre-context from ring.
				preStart := ringStart
				if lineNo-ctxLines > preStart {
					preStart = lineNo - ctxLines
				}
				for j := preStart; j < lineNo; j++ {
					idx := (j - ringStart) % ringLen
					if ringLen == 0 {
						break
					}
					fmt.Fprintf(&sb, " %4d\t%s\n", j, ring[idx])
				}
				fmt.Fprintf(&sb, ">%4d\t%s\n", lineNo, line)
				matchLine = lineNo
				pendingTrail = ctxLines
				if sb.Len() >= devGrepResultCap {
					truncatedByBytes = true
					break
				}
				continue
			}
			if pendingTrail > 0 {
				fmt.Fprintf(&sb, " %4d\t%s\n", lineNo, line)
				pendingTrail--
				if pendingTrail == 0 {
					sb.WriteString("\n")
					_ = matchLine
				}
				if sb.Len() >= devGrepResultCap {
					truncatedByBytes = true
					break
				}
			}
			// Push into ring.
			if ringLen < ringSize {
				ring[ringLen] = line
				ringLen++
				if ringStart == 0 {
					ringStart = 1
				}
			} else {
				// Slide window: drop ringStart, append new.
				copy(ring, ring[1:])
				ring[ringLen-1] = line
				ringStart = lineNo - ringLen + 1
			}
		}
		if pendingTrail > 0 {
			sb.WriteString("\n")
		}
		if err := scanner.Err(); err != nil {
			// Swallow scanner errors (oversized single line, binary garbage);
			// continuing the walk is the right behavior for grep.
			_ = err
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
		}
		return errorResult(fmt.Sprintf("walk error: %v", err)), nil
	}

	if matchCount == 0 {
		if truncatedBySize || truncatedByFileBudget {
			return textResult(fmt.Sprintf("no matches found (skipped %d file(s) over %d bytes; inspected %d files)", filesSkippedBySize, devGrepPerFileCap, filesInspected)), nil
		}
		return textResult("no matches found"), nil
	}
	var header strings.Builder
	fmt.Fprintf(&header, "Found %d match(es):\n", matchCount)
	if truncatedByMatches {
		fmt.Fprintf(&header, "(truncated at match cap %d)\n", maxMatches)
	}
	if truncatedByBytes {
		fmt.Fprintf(&header, "(truncated at result-size cap %d bytes)\n", devGrepResultCap)
	}
	if truncatedBySize {
		fmt.Fprintf(&header, "(skipped %d file(s) over per-file cap %d bytes)\n", filesSkippedBySize, devGrepPerFileCap)
	}
	if truncatedByFileBudget {
		fmt.Fprintf(&header, "(stopped after %d files per file-budget cap)\n", devGrepFileBudget)
	}
	header.WriteString("\n")
	return textResult(header.String() + sb.String()), nil
}

func (d *DevToolsTransport) callWrite(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
	}
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return errorResult("path is required"), nil
	}
	resolved, err := d.resolveAllowed(ctx, path)
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

func (d *DevToolsTransport) callEdit(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
	}
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if path == "" || oldStr == "" {
		return errorResult("path and old_string are required"), nil
	}
	if oldStr == newStr {
		return errorResult("old_string and new_string must be different"), nil
	}
	resolved, err := d.resolveAllowed(ctx, path)
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

func (d *DevToolsTransport) callGlob(ctx context.Context, args map[string]any) (*ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	dir, _ := args["directory"].(string)
	if pattern == "" || dir == "" {
		return errorResult("pattern and directory are required"), nil
	}
	resolvedDir, err := d.resolveAllowed(ctx, dir)
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
		if err := ctx.Err(); err != nil {
			return err
		}
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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
		}
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

// deriveDefaultWorkingDir returns a sensible default working_dir for
// dev_bash when the agent omits the argument. Cascade:
//
//  1. Session path-grant best-dir (most specific existing-directory
//     grant for the chat session — typically the directory the user
//     just mentioned with ~/ or / in their message)
//  2. Static AllowedPaths[0] (the configured allow-list root)
//  3. Empty string — caller must surface a guidance error
//
// Returning empty signals "no allowed default available"; callBash
// turns that into a clean error rather than silently falling back to
// the sandbox CWD.
func (d *DevToolsTransport) deriveDefaultWorkingDir(ctx context.Context) string {
	if sessionID, checker := permission.PathGrantsFromContext(ctx); checker != nil && sessionID != "" {
		if best := checker.BestSessionDir(sessionID); best != "" {
			return best
		}
	}
	if len(d.AllowedPaths) > 0 {
		return d.AllowedPaths[0]
	}
	return ""
}

// devBashSessionID is the session ID used for dev_bash sandbox scoping. All
// dev_bash invocations share one session so callers can observe consistent
// resource limits and denylist behavior; the underlying command still runs
// inside the agent sandbox with stripped PATH, filtered environment, and the
// platform OS-level isolation layer.
const devBashSessionID = "dev-bash"

func (d *DevToolsTransport) callBash(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
	}
	command, _ := args["command"].(string)
	if command == "" {
		return errorResult("command is required"), nil
	}

	// CW-fix-dev-glob-grant Stage B: unify the dev_* permission gate.
	// Previously, an empty working_dir skipped resolveAllowed entirely
	// and relied on sandbox CWD enforcement as the sole authority,
	// which gave dev_bash a different permission model than every
	// other dev_* tool. The c138/c140/c141 reproduction surfaced this
	// when dev_bash silently "succeeded" via the bypass while dev_glob
	// failed under the same user intent. Now: if the agent omits
	// working_dir, derive a default from the session path-grant store
	// (most-specific existing-dir grant) or the static AllowedPaths,
	// and run resolveAllowed on the chosen value uniformly. The
	// sandbox CWD enforcement remains as redundancy, not an alternate
	// permission path.
	workDir, _ := args["working_dir"].(string)
	if workDir == "" {
		workDir = d.deriveDefaultWorkingDir(ctx)
		if workDir == "" {
			return errorResult("dev_bash: no working_dir provided and no allowed path available for this session — supply working_dir explicitly, or have the user mention a path with ~/ or / so a session grant is registered"), nil
		}
	}
	resolved, err := d.resolveAllowed(ctx, workDir)
	if err != nil {
		return pathErrorResult(workDir, err), nil
	}
	workDir = resolved
	_ = workDir // sandbox scopes CWD to its own directory; the
	// path-grant gate above is the authoritative allow check, and
	// the sandbox enforces its own sandboxDir as belt-and-braces
	// redundancy.

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

	type execOutcome struct {
		res *sandbox.ExecResult
		err error
	}
	done := make(chan execOutcome, 1)
	safego.Go(ctx, "mcp.dev_bash.exec", func() {
		res, err := execFn(sandbox.AgentExecOpts{
			SessionID: devBashSessionID,
			Command:   "sh",
			Args:      []string{"-c", command},
			Timeout:   time.Duration(timeout) * time.Second,
		})
		done <- execOutcome{res: res, err: err}
	})

	var result *sandbox.ExecResult
	select {
	case <-ctx.Done():
		// Caller cancellation. The sandbox subprocess is still bounded by its
		// own timeout; we return promptly so the caller's goroutine does not
		// stay wedged waiting for the shell. The in-flight goroutine drains
		// into the buffered `done` channel and is garbage-collected.
		return errorResult(fmt.Sprintf("cancelled: %v", ctx.Err())), nil
	case out := <-done:
		result, err = out.res, out.err
	}
	if err != nil {
		// sandbox setup / denylist / dir-resolve errors surface here. These
		// are hard rejections (e.g. CheckDenylist match).
		return errorResult(fmt.Sprintf("sandbox error: %v", err)), nil
	}

	// Cap each stream at devBashStreamCap to bound the envelope size. The
	// sandbox already collects stdout/stderr into strings; trimming at assembly
	// time still prevents the ToolResult from pushing multi-MB payloads into
	// the MCP transport or the LLM prompt.
	stdoutStr := capOutput(result.Stdout, devBashStreamCap)
	stderrStr := capOutput(result.Stderr, devBashStreamCap)

	var sb strings.Builder
	if stdoutStr != "" {
		sb.WriteString(stdoutStr)
	}
	if stderrStr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("--- stderr ---\n")
		sb.WriteString(stderrStr)
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

// capOutput truncates s to at most cap bytes and appends a clear marker when
// truncation occurs. The marker calls out the original size so callers can
// tell whether re-running with a narrower command is appropriate.
func capOutput(s string, cap int) string {
	if cap <= 0 || len(s) <= cap {
		return s
	}
	return s[:cap] + fmt.Sprintf("\n[truncated: %d of %d bytes shown]", cap, len(s))
}

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
