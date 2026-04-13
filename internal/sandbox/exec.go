package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// maxOutputBytes is the maximum size of stdout/stderr capture (1MB each).
	maxOutputBytes = 1 << 20

	// defaultAgentTimeout is the default timeout for agent-exec commands.
	defaultAgentTimeout = 30 * time.Second
	// maxAgentTimeout is the maximum allowed timeout for agent-exec commands.
	maxAgentTimeout = 5 * time.Minute

	// defaultUserTimeout is the default timeout for user-exec commands.
	defaultUserTimeout = 60 * time.Second
	// maxUserTimeout is the maximum allowed timeout for user-exec commands.
	maxUserTimeout = 10 * time.Minute
)

// essentialBinDirs are the only PATH entries available to agent-exec commands.
var essentialBinDirs = []string{
	"/usr/bin",
	"/bin",
	"/usr/local/bin",
}

// secretKeyPatterns are substrings that identify environment variable names
// containing secrets. Matching is case-insensitive.
var secretKeyPatterns = []string{
	"KEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"CREDENTIAL",
	"AUTH",
}

// minimalEnvKeys are the only inherited env vars for agent-exec (values only).
var minimalEnvKeys = []string{
	"HOME",
	"USER",
	"LANG",
	"TERM",
}

// AgentExecOpts configures an agent-initiated command execution.
type AgentExecOpts struct {
	SessionID    string            // for sandbox directory scoping
	Command      string            // the command to run
	Args         []string          // command arguments
	Timeout      time.Duration     // execution timeout (default 30s, max 5m)
	Env          map[string]string // additional env vars (filtered for secrets)
	NetworkAllow []string          // domains to allow via proxy (empty = deny all network)
}

// UserExecOpts configures a user-initiated command execution.
type UserExecOpts struct {
	Command   string        // the command to run
	Args      []string      // command arguments
	Dir       string        // user's actual working directory
	Timeout   time.Duration // default 60s, max 10m
	Sandboxed bool          // when true, apply OS-level sandbox (seatbelt/bwrap)
}

// ExecResult holds the output and metadata of a sandboxed command execution.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

// AgentExec runs a command in the agent sandbox with full isolation:
// restricted CWD, stripped PATH, environment filtering, and denylist enforcement.
// On supported platforms (macOS), OS-level sandbox-exec isolation is applied.
func AgentExec(opts AgentExecOpts) (*ExecResult, error) {
	// 1. Check denylist.
	fullCmd := opts.Command
	if len(opts.Args) > 0 {
		fullCmd += " " + strings.Join(opts.Args, " ")
	}
	if blocked, reason := CheckDenylist(fullCmd); blocked {
		return nil, fmt.Errorf("sandbox: agent-exec denied: %s", reason)
	}

	// 2. Resolve sandbox directory.
	sandboxDir, err := Dir(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve dir: %w", err)
	}

	// 3. Build restricted environment.
	env := buildAgentEnv(opts.Env)

	// 4. Start network proxy if domains are allowed.
	var proxy *Proxy
	if len(opts.NetworkAllow) > 0 {
		proxy = NewProxy(opts.NetworkAllow)
		if err := proxy.Start(); err != nil {
			return nil, fmt.Errorf("sandbox: start proxy: %w", err)
		}
		defer proxy.Stop()

		// Inject proxy env vars so sandboxed tools (curl, wget, pip, npm, go)
		// route traffic through the allowlisted proxy.
		proxyURL := "http://" + proxy.Addr
		env = append(env,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
		)
	}

	// 5. Build command.
	timeout := clampTimeout(opts.Timeout, defaultAgentTimeout, maxAgentTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
	cmd.Dir = sandboxDir
	cmd.Env = env

	// Apply OS-level sandbox (no-op on unsupported platforms).
	cleanup, err := applyOSSandbox(cmd, sandboxDir, opts.NetworkAllow)
	if err != nil {
		return nil, fmt.Errorf("sandbox: os-level setup: %w", err)
	}
	defer cleanup()

	// Start the command in its own process group, and on ctx cancel send
	// SIGTERM to the whole group before Go's internal SIGKILL. This reaps
	// grandchildren that sandbox-exec (macOS) or a shell interpreter
	// forks — the audit's orphan-process finding (07). WaitDelay bounds
	// the cleanup window so Wait() cannot block forever on an orphaned
	// stdio pipe.
	setProcessGroupKill(cmd)

	return runCmd(ctx, cmd, timeout)
}

// UserExec runs a user-initiated command with guardrails: denylist enforcement
// and environment filtering for secrets, but no CWD restriction or PATH stripping.
func UserExec(opts UserExecOpts) (*ExecResult, error) {
	// 1. Check denylist.
	fullCmd := opts.Command
	if len(opts.Args) > 0 {
		fullCmd += " " + strings.Join(opts.Args, " ")
	}
	if blocked, reason := CheckDenylist(fullCmd); blocked {
		return nil, fmt.Errorf("sandbox: user-exec denied: %s", reason)
	}

	// 2. Build environment — user's full env minus secrets.
	env := filterSecrets(os.Environ())

	// 3. Build command.
	timeout := clampTimeout(opts.Timeout, defaultUserTimeout, maxUserTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = env

	// Apply OS-level sandbox when requested (session/ask modes).
	if opts.Sandboxed {
		cleanup, err := applyOSSandbox(cmd, opts.Dir, nil)
		if err != nil {
			return nil, fmt.Errorf("sandbox: os-level setup: %w", err)
		}
		defer cleanup()
	}

	// See AgentExec: process group + signal cascade to reap grandchildren.
	setProcessGroupKill(cmd)

	return runCmd(ctx, cmd, timeout)
}

// runCmd executes a command and captures stdout/stderr with size limits.
func runCmd(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (*ExecResult, error) {
	var stdout, stderr limitedBuffer
	stdout.max = maxOutputBytes
	stderr.max = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
			result.ExitCode = 124
			return result, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return nil, fmt.Errorf("sandbox: exec: %w", err)
	}

	return result, nil
}

// buildAgentEnv constructs a minimal environment for agent-exec.
func buildAgentEnv(extra map[string]string) []string {
	env := make([]string, 0, len(minimalEnvKeys)+len(extra)+1)

	// Inherit only minimal keys from current process.
	for _, key := range minimalEnvKeys {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
		}
	}

	// Set restricted PATH.
	env = append(env, "PATH="+strings.Join(essentialBinDirs, ":"))

	// Add caller-provided env vars after filtering secrets.
	for k, v := range extra {
		if !isSecretKey(k) {
			env = append(env, k+"="+v)
		}
	}

	return env
}

// filterSecrets removes environment variables whose names match secret patterns.
func filterSecrets(environ []string) []string {
	filtered := make([]string, 0, len(environ))
	for _, entry := range environ {
		eqIdx := strings.IndexByte(entry, '=')
		if eqIdx < 0 {
			continue
		}
		key := entry[:eqIdx]
		if !isSecretKey(key) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// isSecretKey returns true if the env var name matches any secret pattern.
func isSecretKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range secretKeyPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// clampTimeout applies default and maximum bounds to a timeout value.
func clampTimeout(t, defaultVal, maxVal time.Duration) time.Duration {
	if t <= 0 {
		return defaultVal
	}
	if t > maxVal {
		return maxVal
	}
	return t
}

// limitedBuffer is a bytes.Buffer that stops accepting writes after max bytes.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	remaining := lb.max - lb.buf.Len()
	if remaining <= 0 {
		return len(p), nil // discard silently
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) String() string {
	return lb.buf.String()
}
