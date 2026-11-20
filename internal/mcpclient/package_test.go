package mcpclient

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	mcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/client"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/tools"
)

func TestPackageRecheckedAtMCPStartAndReconnect(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			dir := t.TempDir()
			pkgDir := filepath.Join(dir, "tools", "fixture")
			if err := os.MkdirAll(pkgDir, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "starts")
			script := fmt.Sprintf("#!/bin/sh\n[ -z \"$NANITE_TOOL_CHECK\" ] || exit 90\nprintf 'started\\n' >> %q\nexport NANITE_MCP_TEST_SERVER=1\nexec %q -test.run='^TestMCPTransports$' -test.v=false\n", marker, os.Args[0])
			entry := filepath.Join(pkgDir, "run.sh")
			if err := os.WriteFile(entry, []byte(script), 0700); err != nil { //nolint:gosec // G306: the test package entry must be executable.
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "tool.yaml"), []byte("description: d\nkind: mcp\nentry: run.sh\n"), 0600); err != nil {
				t.Fatal(err)
			}
			pkg, err := authored.LoadTool(dir, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			check, err := json.Marshal(PackageCheck{Dir: dir, Name: pkg.Name, Digest: pkg.Digest})
			if err != nil {
				t.Fatal(err)
			}
			client := New(dir, nil, redact.NewScope(), slog.Default(), nil)
			defer func() { _ = client.Close() }()
			lease, warnings := client.Reconcile(t.Context(), agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"tool:fixture": {Type: "stdio", Command: entry, Env: map[string]string{PackageCheckEnv: string(check)}}}})
			defer lease.Close()
			if len(warnings) != 0 {
				t.Fatal(warnings)
			}
			var offered []tools.Tool
			if started {
				offered, warnings = lease.Tools(t.Context())
				if len(offered) != 1 || len(warnings) != 0 {
					t.Fatalf("initial discovery: %v %v", offered, warnings)
				}
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "changed"), []byte("changed after approval"), 0600); err != nil {
				t.Fatal(err)
			}
			if started {
				client.pool.Invalidate(lease.servers[0].key)
				result, err := offered[0].Handler(t.Context(), tools.Call{Input: json.RawMessage(`{}`)})
				if err == nil && !result.IsError {
					t.Fatal("changed package reconnected")
				}
			} else {
				got, warnings := lease.Tools(t.Context())
				if len(got) != 0 || len(warnings) == 0 {
					t.Fatalf("changed package started: %v %v", got, warnings)
				}
			}
			raw, err := os.ReadFile(marker) //nolint:gosec // G304: marker is in a test-owned temporary directory.
			if started {
				if err != nil || strings.Count(string(raw), "started") != 1 {
					t.Fatalf("restarted changed package: %q %v", raw, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("started changed package: %q %v", raw, err)
			}
		})
	}
}

func TestPackageStartupChecksResolvedURL(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "tools", "fixture")
	if err := os.MkdirAll(pkgDir, 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(pkgDir, "run.sh")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil { //nolint:gosec // G306: the test package entry must be executable.
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "tool.yaml"), []byte("description: d\nkind: mcp\nentry: run.sh\nenv: [QUEUE_URL]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUEUE_URL", "https://queue.example/jobs")
	pkg, err := authored.LoadTool(dir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	check, err := json.Marshal(PackageCheck{Dir: dir, Name: pkg.Name, Digest: pkg.Digest})
	if err != nil {
		t.Fatal(err)
	}
	cfg := mcp.ServerConfig{Command: entry, Env: map[string]string{PackageCheckEnv: string(check), "QUEUE_URL": "https://" + "owner:private-fixture" + "@queue.example/jobs"}}
	if _, err := commandEnv(cfg); err == nil || strings.Contains(err.Error(), "private-fixture") {
		t.Fatalf("unsafe resolved URL: %v", err)
	}
	cfg.Env["QUEUE_URL"] = "https://queue.example/jobs"
	env, err := commandEnv(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range env {
		if strings.HasPrefix(value, PackageCheckEnv+"=") {
			t.Fatal("startup check leaked")
		}
	}
}
