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

	// WorkingDir is the directory the command should execute in. When
	// non-empty:
	//   - cmd.Dir is set to WorkingDir (instead of the sandbox scoping dir)
	//   - The OS sandbox profile is widened to permit read+write under
	//     WorkingDir (seatbelt subpath on darwin; bwrap --bind on linux)
	// When empty, cmd.Dir falls back to the sandbox scoping dir (status quo
	// before CW-20260504-0003).
	//
	// The CALLER is responsible for validating WorkingDir against the
	// permission gate (e.g. resolveAllowed in dev_tools). AgentExec does
	// NOT re-validate; it trusts the caller's resolved path. This keeps
	// the gate authority in one place (the dev_tools permission check)
	// and the sandbox enforcement as belt-and-braces.
	WorkingDir string
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

	// SandboxIsolated reports whether real OS-level sandbox isolation
	// (seatbelt on darwin, bwrap on linux) was actually applied to this
	// execution. Always true on darwin (sandbox-exec ships with the OS)
	// and true on linux whenever bwrap was found. False ONLY when the
	// platform's isolation tool was unavailable AND the operator
	// explicitly opted in to degraded execution via
	// NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1 — by default (AD-01,
	// TASKS/audit-remediation/ARCHITECT-DECISIONS.md), a missing
	// isolation tool is a hard error and AgentExec/UserExec never return
	// an ExecResult at all, so this field can never silently read "true
	// shaped" for an unsandboxed run. See internal/sandbox/degraded.go.
	SandboxIsolated bool `json:"sandbox_isolated"`
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
	var proxyAddr string
	if len(opts.NetworkAllow) > 0 {
		proxy = NewProxy(opts.NetworkAllow)
		if err := proxy.Start(); err != nil {
			return nil, fmt.Errorf("sandbox: start proxy: %w", err)
		}
		defer proxy.Stop()
		proxyAddr = proxy.Addr

		// Inject proxy env vars so sandboxed tools (curl, wget, pip, npm, go)
		// route traffic through the allowlisted proxy. AD-02
		// (TASKS/audit-remediation/ARCHITECT-DECISIONS.md): on linux this
		// address is reachable from inside the sandbox's own (now always
		// unshared) network namespace via the netns bridge applyOSSandbox
		// wires below — see netns_bridge_linux.go. These env vars need no
		// change; they already point at the right host:port.
		proxyURL := "http://" + proxyAddr
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
	// CW-20260504-0003: when the caller provided an explicit WorkingDir
	// (already permission-gated upstream), execute the command there so
	// the agent operates on the user-granted path. Otherwise default to
	// the sandbox scoping dir (legacy behavior). The OS sandbox profile
	// receives WorkingDir as an additional allowed write subpath; reads
	// are unrestricted on darwin and bind-mounted on linux.
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	} else {
		cmd.Dir = sandboxDir
	}
	cmd.Env = env

	// Apply OS-level sandbox. AD-01 (TASKS/audit-remediation/
	// ARCHITECT-DECISIONS.md): on Linux without bwrap and on platforms
	// with no OS sandbox at all, this now returns a non-nil error by
	// default (fail closed) instead of silently succeeding unsandboxed —
	// unless the operator opted in via NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC,
	// in which case it succeeds with isolated=false and this function
	// still surfaces that on the returned ExecResult.
	cleanup, isolated, err := applyOSSandbox(cmd, sandboxDir, opts.WorkingDir, opts.NetworkAllow, proxyAddr)
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

	result, runErr := runCmd(ctx, cmd, timeout)
	if result != nil {
		result.SandboxIsolated = isolated
	}
	return result, runErr
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

	// Apply OS-level sandbox when requested (session/ask modes). UserExec
	// already runs in the user's chosen directory; no extra write-allow
	// path is needed because opts.Dir is itself the writable root. UserExec
	// never configures a network allowlist, so proxyAddr is always empty —
	// the AD-02 netns bridge never engages here.
	//
	// AD-01: when opts.Sandboxed is true and the platform's isolation tool
	// is unavailable, this now fails closed by default (see AgentExec's
	// comment above for the full policy). When opts.Sandboxed is false
	// (YOLO mode), applyOSSandbox is never called at all — that is an
	// explicit, already-disclosed user opt-out, a different case in kind
	// from a silent degradation, per this task's own scope notes.
	var isolated bool
	if opts.Sandboxed {
		cleanup, isolatedVerdict, err := applyOSSandbox(cmd, opts.Dir, "", nil, "")
		if err != nil {
			return nil, fmt.Errorf("sandbox: os-level setup: %w", err)
		}
		defer cleanup()
		isolated = isolatedVerdict
	}

	// See AgentExec: process group + signal cascade to reap grandchildren.
	setProcessGroupKill(cmd)

	result, runErr := runCmd(ctx, cmd, timeout)
	if result != nil {
		result.SandboxIsolated = isolated
	}
	return result, runErr
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
