package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/sandbox"
)

// TestDevBash_RoutesThroughSandbox verifies that dev_bash does not fall back
// to exec.CommandContext directly and instead goes through sandbox.AgentExec.
// A stub replaces the sandbox entry point and captures the invocation so the
// test can assert sh -c <command> with the expected args was forwarded.
func TestDevBash_RoutesThroughSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash tests require unix shell")
	}
	dt, _ := tempDevTools(t)

	var captured atomic.Pointer[sandbox.AgentExecOpts]
	dt.agentExec = func(opts sandbox.AgentExecOpts) (*sandbox.ExecResult, error) {
		cp := opts
		captured.Store(&cp)
		return &sandbox.ExecResult{Stdout: "hello\n", ExitCode: 0}, nil
	}

	result, err := dt.CallTool(context.Background(), "dev_bash", map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].Text)
	}
	got := captured.Load()
	if got == nil {
		t.Fatal("sandbox.AgentExec stub was not invoked")
	}
	if got.Command != "sh" {
		t.Errorf("expected Command=sh, got %q", got.Command)
	}
	if len(got.Args) != 2 || got.Args[0] != "-c" || got.Args[1] != "echo hello" {
		t.Errorf("expected Args=[-c, echo hello], got %v", got.Args)
	}
	if got.SessionID == "" {
		t.Error("expected non-empty SessionID for sandbox scoping")
	}
	if got.Timeout == 0 {
		t.Error("expected non-zero Timeout to enforce per-call budget")
	}
}

// TestDevBash_DenylistedCommandReturnsSandboxError verifies that a denylisted
// command never reaches direct exec (the sandbox error surfaces in the tool
// result). This is the regression surface for the pre-fix RCE where
// exec.CommandContext ran whatever the agent asked for.
func TestDevBash_DenylistedCommandReturnsSandboxError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash tests require unix shell")
	}
	dt, _ := tempDevTools(t)
	// Stub returns an error shaped like the real denylist failure so the
	// test does not need to bring up the full sandbox directory machinery.
	dt.agentExec = func(opts sandbox.AgentExecOpts) (*sandbox.ExecResult, error) {
		return nil, fmt.Errorf("sandbox: agent-exec denied: fork bomb")
	}
	result, err := dt.CallTool(context.Background(), "dev_bash", map[string]any{
		"command": ":(){ :|:& };:",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for denylisted command")
	}
	if !strings.Contains(result.Content[0].Text, "sandbox error") {
		t.Errorf("expected sandbox error surfacing, got: %s", result.Content[0].Text)
	}
}

// TestDevRead_SymlinkEscape verifies that a symlink pointing outside the
// allowed root is rejected with an *pathsafe.EscapeError. This is the
// regression surface for the pre-fix isAllowed check that did
// filepath.EvalSymlinks(path) but then compared the cleaned *attempted* path
// rather than the resolved target — letting a symlink `evil` -> `/etc/passwd`
// slip past.
func TestDevRead_SymlinkEscape(t *testing.T) {
	dt, dir := tempDevTools(t)

	// Set up an allowed file inside the root...
	allowed := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(allowed, []byte("allowed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ...and a symlink inside the root pointing to /etc/hosts (outside).
	outside := "/etc/hosts"
	if _, err := os.Stat(outside); err != nil {
		t.Skipf("%s not present on this system: %v", outside, err)
	}
	link := filepath.Join(dir, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	result, err := dt.CallTool(context.Background(), "dev_read", map[string]any{
		"path": link,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected error for symlink escape, got: %s", result.Content[0].Text)
	}
	// The surfaced error must mention the pathsafe escape path so callers
	// can recognize it. The typed *pathsafe.EscapeError is formatted into
	// the text via pathErrorResult.
	if !strings.Contains(result.Content[0].Text, "pathsafe") {
		t.Errorf("expected pathsafe escape error in output, got: %s", result.Content[0].Text)
	}
}

// TestResolveAllowed_ReturnsEscapeErrorType verifies that errors.As unwraps
// the *pathsafe.EscapeError from resolveAllowed, so higher-level code that
// wants to classify errors (telemetry, audit logs) can do so.
func TestResolveAllowed_ReturnsEscapeErrorType(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dt := NewDevToolsTransport([]string{real})

	_, err = dt.resolveAllowed("/etc/hosts")
	if err == nil {
		t.Fatal("expected error for path outside root")
	}
	var escape *pathsafe.EscapeError
	if !errors.As(err, &escape) {
		t.Fatalf("expected *pathsafe.EscapeError, got %T: %v", err, err)
	}
}

// --- web_fetch SSRF ---

// testResolver returns the provided IPs regardless of the hostname queried.
func testResolver(ips ...string) ssrfResolver {
	return func(_ context.Context, host string) ([]net.IP, error) {
		parsed := make([]net.IP, 0, len(ips))
		for _, s := range ips {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil, fmt.Errorf("test bug: invalid IP %q", s)
			}
			parsed = append(parsed, ip)
		}
		return parsed, nil
	}
}

func TestWebFetch_RejectsFileScheme(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "file:///etc/passwd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for file:// scheme")
	}
	if !strings.Contains(result.Content[0].Text, "scheme") {
		t.Errorf("expected scheme rejection message, got: %s", result.Content[0].Text)
	}
}

func TestWebFetch_RejectsEC2IMDS(t *testing.T) {
	gt := newGeneralTools()
	gt.resolver = testResolver("169.254.169.254")
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "http://metadata.example.com/latest/meta-data/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for link-local IP")
	}
	if !strings.Contains(result.Content[0].Text, "blocked") {
		t.Errorf("expected SSRF block, got: %s", result.Content[0].Text)
	}
}

func TestWebFetch_RejectsLocalhostName(t *testing.T) {
	gt := newGeneralTools()
	// No resolver stub: localhost rejection happens by name before DNS.
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "http://localhost:6379/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for localhost")
	}
	if !strings.Contains(result.Content[0].Text, "localhost") {
		t.Errorf("expected localhost rejection, got: %s", result.Content[0].Text)
	}
}

func TestWebFetch_RejectsLocalhostSubdomain(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "http://api.localhost/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for *.localhost")
	}
}

func TestWebFetch_RejectsLoopbackIPv4(t *testing.T) {
	gt := newGeneralTools()
	gt.resolver = testResolver("127.0.0.1")
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "http://loopback.example.com:22/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for 127.0.0.1")
	}
}

func TestWebFetch_RejectsLoopbackIPv6(t *testing.T) {
	gt := newGeneralTools()
	gt.resolver = testResolver("::1")
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "http://[::1]/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for ::1")
	}
}

func TestWebFetch_RejectsRFC1918(t *testing.T) {
	cases := []string{"10.0.0.5", "172.16.5.5", "192.168.1.1"}
	for _, ip := range cases {
		t.Run(ip, func(t *testing.T) {
			gt := newGeneralTools()
			gt.resolver = testResolver(ip)
			result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
				"url": "http://internal.example.com/",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("expected error for private IP %s", ip)
			}
		})
	}
}

// TestWebFetch_PinsDialToResolvedIP verifies the resolve-then-dial pattern:
// the hostname is resolved once, the validated IP is used literally for the
// dial, and DNS rebinding between the check and the dial cannot swap the
// destination. The dialer stub captures the target address the transport
// hands it after validation.
func TestWebFetch_PinsDialToResolvedIP(t *testing.T) {
	// Real httptest server on 127.0.0.1; we pretend it answers for a
	// public-looking hostname by stubbing the resolver and dialer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "public body")
	}))
	defer srv.Close()

	// The real server binds to 127.0.0.1:PORT; parse the port.
	parsed, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	// We claim "public.example.com" resolves to 203.0.113.9, but the dialer
	// is hooked so the actual TCP connection lands on the httptest loopback.
	var dialed atomic.Value // string
	gt := newGeneralTools()
	gt.resolver = testResolver("203.0.113.9")
	gt.dialer = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed.Store(addr)
		// Redirect the dial to the real test server.
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, parsed.String())
	}

	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": "http://public.example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "public body") {
		t.Errorf("expected body in output, got: %s", result.Content[0].Text)
	}
	got, _ := dialed.Load().(string)
	if !strings.HasPrefix(got, "203.0.113.9:") {
		t.Errorf("expected dial pinned to resolved IP 203.0.113.9, got %q", got)
	}
}
