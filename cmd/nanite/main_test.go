package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/mcp"
	naniteotel "github.com/hollis-labs/nanite/internal/otel"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type countingCloser struct {
	calls atomic.Int32
	err   error
}

func TestResolveServeHTTPConfig(t *testing.T) {
	t.Run("flag overrides config", func(t *testing.T) {
		cfg := config.HTTPConfig{BindAddress: "127.0.0.1"}
		got, err := resolveServeHTTPConfig(cfg, "0.0.0.0")
		if err != nil {
			t.Fatalf("resolveServeHTTPConfig: %v", err)
		}
		if got.BindAddress != "0.0.0.0" {
			t.Fatalf("BindAddress = %q, want 0.0.0.0", got.BindAddress)
		}
	})

	t.Run("empty flag preserves config", func(t *testing.T) {
		cfg := config.HTTPConfig{BindAddress: "192.0.2.10"}
		got, err := resolveServeHTTPConfig(cfg, "")
		if err != nil {
			t.Fatalf("resolveServeHTTPConfig: %v", err)
		}
		if got.BindAddress != cfg.BindAddress {
			t.Fatalf("BindAddress = %q, want configured %q", got.BindAddress, cfg.BindAddress)
		}
	})

	t.Run("invalid flag is rejected before startup", func(t *testing.T) {
		_, err := resolveServeHTTPConfig(config.HTTPConfig{}, "127.0.0.1:8090")
		if err == nil || !strings.Contains(err.Error(), "host only") {
			t.Fatalf("resolveServeHTTPConfig error = %v, want host-only diagnostic", err)
		}
	})

	t.Run("flag whitespace is rejected rather than trimmed", func(t *testing.T) {
		_, err := resolveServeHTTPConfig(config.HTTPConfig{}, " 0.0.0.0 ")
		if err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
			t.Fatalf("resolveServeHTTPConfig error = %v, want whitespace diagnostic", err)
		}
	})
}

func TestCmdServeRejectsInvalidBindBeforeInitializers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	// resolveDBPathWith now requires an explicit DB location (no silent
	// default) — this test cares about bind-address validation ordering,
	// not DB resolution, so give it one.
	t.Setenv("NANITE_WORKSPACE", "serve-bind-validation-test")

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	var loggingCalled, otelCalled bool
	err := cmdServeWithInitializers(
		[]string{"--bind-address", "localhost:8090"},
		func(slogx.Config) (*slog.Logger, io.Closer, error) {
			loggingCalled = true
			return slog.Default(), &countingCloser{}, nil
		},
		func(context.Context, naniteotel.Config) (func(context.Context) error, error) {
			otelCalled = true
			return func(context.Context) error { return nil }, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "host only") {
		t.Fatalf("cmdServe error = %v, want host-only bind diagnostic", err)
	}
	if loggingCalled || otelCalled {
		t.Fatalf("invalid bind reached startup initializers: logging=%v otel=%v", loggingCalled, otelCalled)
	}
	if !strings.Contains(logs.String(), "invalid HTTP server config") || !strings.Contains(logs.String(), "http.bind_address") {
		t.Fatalf("invalid bind diagnostic missing from startup logs: %s", logs.String())
	}
	if strings.Contains(logs.String(), "nanite listening") {
		t.Fatalf("invalid bind emitted misleading listening log: %s", logs.String())
	}
}

func TestMalformedAgentConfigServeFallbackIsNonNilAndSafe(t *testing.T) {
	root := t.TempDir()
	userConfig := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(userConfig, []byte("tesseract: [not-an-object\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadServeRuntimeConfig(func() (*config.RuntimeConfig, error) {
		return config.LoadFrom(userConfig, filepath.Join(root, "missing-project.yaml"))
	})
	if err == nil {
		t.Fatal("malformed config unexpectedly loaded")
	}
	if cfg == nil {
		t.Fatal("serve fallback returned nil config")
	}
	if cfg.Tesseract.Command != "" || cfg.Tesseract.ServerName != "" {
		t.Fatalf("serve fallback Tesseract config = %+v, want safe embedded defaults", cfg.Tesseract)
	}
	// These are the two startup decisions that previously dereferenced the
	// nil config after config.Load failed.
	serverName := strings.TrimSpace(cfg.Tesseract.ServerName)
	if serverName == "" {
		serverName = "tesseract"
	}
	external := strings.TrimSpace(cfg.Tesseract.Command) != ""
	if serverName != "tesseract" || external {
		t.Fatalf("fallback ownership: server=%q external=%v", serverName, external)
	}
}

func (c *countingCloser) Close() error {
	c.calls.Add(1)
	return c.err
}

func TestCloseLoggingOutputUsesIndependentWriter(t *testing.T) {
	closeErr := errors.New("injected log sink close failure")
	closer := &countingCloser{err: closeErr}
	var stderr bytes.Buffer

	closeLoggingOutput(closer, &stderr)

	if got := closer.calls.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), closeErr.Error()) {
		t.Fatalf("independent stderr = %q, want close failure", stderr.String())
	}
}

func TestCmdServeStartupFailureReturns(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	// SQLite cannot open a directory as a database file. The directory itself
	// is resolvable by go-apppaths, so this reaches a real store.New failure
	// after the logging and OTel cleanup defers have been registered, without
	// requiring a composition-root dependency-injection seam.
	dbPath := t.TempDir()

	logCleanup := &countingCloser{}
	var otelCleanupCalls atomic.Int32

	err := cmdServeWithInitializers(
		[]string{"--db", dbPath, "--dev"},
		func(slogx.Config) (*slog.Logger, io.Closer, error) {
			return slog.Default(), logCleanup, nil
		},
		func(context.Context, naniteotel.Config) (func(context.Context) error, error) {
			return func(context.Context) error {
				otelCleanupCalls.Add(1)
				return nil
			}, nil
		},
	)
	if err == nil {
		t.Fatal("cmdServe returned nil for an unopenable database path")
	}
	if !strings.Contains(err.Error(), "failed to open store") {
		t.Fatalf("cmdServe error = %q, want failed-to-open-store context", err)
	}
	if got := logCleanup.calls.Load(); got != 1 {
		t.Errorf("logging cleanup calls = %d, want 1", got)
	}
	if got := otelCleanupCalls.Load(); got != 1 {
		t.Errorf("OTel cleanup calls = %d, want 1", got)
	}
}

func TestCmdServeLoadsPluginsBeforeRecoveryAndBackgroundWorkers(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(source)

	serverNew := strings.Index(body, "server.New(s, a, *port, *dev, pluginHost, appCfg.HTTP)")
	discover := strings.Index(body, "discoverAndLoadPlugins(pluginHost, mcpManager, s)")
	teamRecovery := strings.Index(body, "container.TeamRunLauncher.ReconcileTeamRuns(context.Background(), 100)")
	workflowRecovery := strings.Index(body, "sharedWorkflowEngine.RecoverActive(context.Background(), workflowStepExecutor, 100)")
	backgroundWorkers := strings.Index(body, "startBackgroundWorkers(daemonLifecycle, container, s)")

	checks := map[string]int{
		"server.New":               serverNew,
		"discoverAndLoadPlugins":   discover,
		"TeamRunLauncher recovery": teamRecovery,
		"workflow recovery":        workflowRecovery,
		"startBackgroundWorkers":   backgroundWorkers,
	}
	for label, index := range checks {
		if index < 0 {
			t.Fatalf("%s not found in main.go", label)
		}
	}

	if serverNew >= discover {
		t.Fatalf("plugin discovery must run after server.New installs the plugin router: server.New=%d discover=%d", serverNew, discover)
	}
	for label, index := range map[string]int{
		"TeamRunLauncher recovery": teamRecovery,
		"workflow recovery":        workflowRecovery,
		"startBackgroundWorkers":   backgroundWorkers,
	} {
		if discover >= index {
			t.Fatalf("plugin discovery must precede %s: discover=%d %s=%d", label, discover, label, index)
		}
	}
}

func TestLoadPersistedMCPServersMalformedJSON(t *testing.T) {
	tests := []struct {
		name        string
		argsJSON    string
		envJSON     string
		warningText string
	}{
		{
			name:        "args",
			argsJSON:    `["leaked-arg", 7]`,
			envJSON:     `[]`,
			warningText: "mcp: malformed args json — ignoring",
		},
		{
			name:        "env",
			argsJSON:    `[]`,
			envJSON:     `["MCP_CONFIG_SENTINEL=leaked", 7]`,
			warningText: "mcp: malformed env json — ignoring",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			scriptPath := filepath.Join(dir, "mcp-server.sh")
			capturePath := scriptPath + ".capture"
			const script = `#!/bin/sh
printf 'argc=%s\n' "$#" > "${0}.capture"
if [ "${MCP_CONFIG_SENTINEL+x}" = x ]; then
  printf 'sentinel=%s\n' "$MCP_CONFIG_SENTINEL" >> "${0}.capture"
else
  printf 'sentinel=<unset>\n' >> "${0}.capture"
fi
# Answer the MCP handshake before anything else. The real SDK client tries
# the SEP-2575 stateless "server/discover" RPC first and only falls back to
# the legacy initialize/initialized dance on any error from it -- so the
# first request answered here is a rejection of that, not initialize
# itself, or the client hangs waiting for a DiscoverResult this stub never
# sends (exactly what happened here before this comment: a client this
# stub can't talk to is not a real regression check). Request ids aren't
# assumed to be 1/2 -- they're echoed back from whatever the client sent.
reply_id() {
  printf '%s' "$1" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p'
}
IFS= read -r discover
printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}\n' "$(reply_id "$discover")"
IFS= read -r initialize
printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"test-mcp","version":"0"}}}\n' "$(reply_id "$initialize")"
IFS= read -r initialized
IFS= read -r request
printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}\n' "$(reply_id "$request")"
`
			if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
				t.Fatalf("write MCP test server: %v", err)
			}

			s, err := storetest.New(t, context.Background(), filepath.Join(dir, "nanite.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Errorf("close store: %v", err)
				}
			})

			serverName := "malformed-" + tt.name
			cfg := &store.MCPServerConfig{
				Name:          serverName,
				TransportType: "stdio",
				Command:       scriptPath,
				Args:          tt.argsJSON,
				Env:           tt.envJSON,
				EnvAllowlist:  `[]`,
				Enabled:       true,
				TrustTier:     store.TrustTierPluginStdio,
			}
			if err := s.CreateMCPServer(context.Background(), cfg); err != nil {
				t.Fatalf("create MCP server config: %v", err)
			}

			var logs bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(oldLogger) })

			manager := mcp.NewManager()
			t.Cleanup(manager.Close)
			loadPersistedMCPServers(s, manager)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := manager.DiscoverServerTools(ctx, serverName); err != nil {
				t.Fatalf("start persisted MCP server: %v", err)
			}

			capture, err := os.ReadFile(capturePath)
			if err != nil {
				t.Fatalf("read MCP subprocess capture: %v", err)
			}
			if got, want := string(capture), "argc=0\nsentinel=<unset>\n"; got != want {
				t.Errorf("MCP subprocess received partially decoded config:\n got %q\nwant %q", got, want)
			}

			logOutput := logs.String()
			if !strings.Contains(logOutput, tt.warningText) {
				t.Errorf("warning log missing field-specific message %q: %s", tt.warningText, logOutput)
			}
			if !strings.Contains(logOutput, serverName) {
				t.Errorf("warning log missing server name %q: %s", serverName, logOutput)
			}
			if !strings.Contains(logOutput, `"err"`) {
				t.Errorf("warning log missing decode error: %s", logOutput)
			}
		})
	}
}

// Phase 4c.6 (CW-20260508-0002): TestRegisterLegacyPTYAliasPrefersRegisteredClaudeProvider
// removed — the registerLegacyPTYAlias function it covered was deleted along
// with provider.PTYBridge / provider.SubprocessBridge registry registrations.
// CLI agents now spawn through internal/runtime/agent.Boot.
