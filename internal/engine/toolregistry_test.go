package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/toolregistry"
)

func TestRegistryInstallBuiltinPreservesPackageApproval(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			source := t.TempDir()
			writeToolPackage(t, source, "echo", echoManifest, "#!/bin/sh\ncat\n")
			if err := os.WriteFile(filepath.Join(source, "identity.yaml"), []byte("principal: agent:fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			client := toolregistry.LocalClient(t.TempDir())
			record, err := toolregistry.Publish(t.Context(), client, source, "echo", "msg://agent/agent-mux/agt_fixture")
			if err != nil {
				t.Fatal(err)
			}
			p := fake.New(callWith("tool_install", map[string]any{"ref": record.Ref()}), fake.Text("installed"), callWith("tool__echo", map[string]any{"text": "works"}), fake.Text("done"))
			e, _, id := setupEngine(t, p, "")
			e.cfg.ToolRegistry = &client
			e.cfg.AutoApprove = auto
			var packageAsks int
			e.cfg.Gate = gateFunc(func(_ context.Context, r permission.PermissionRequest) (permission.PermissionDecision, error) {
				if r.Tool == "tool__echo" {
					packageAsks++
					return permission.PermissionDecision{Reason: "test deny package"}, nil
				}
				return permission.PermissionDecision{Allow: true, Scope: permission.ScopeOnce}, nil
			})
			result := toolTurn(t, e, id)
			if result.IsError || !strings.Contains(result.Content, "Installed") {
				t.Fatalf("install: %+v", result)
			}
			pkg, err := authored.LoadTool(e.cfg.Dir, "echo")
			if err != nil {
				t.Fatal(err)
			}
			if e.toolApproved(t.Context(), pkg) {
				t.Fatal("installation granted approval")
			}
			result = toolTurn(t, e, id)
			if auto {
				if result.IsError || !strings.Contains(result.Content, "works") || packageAsks != 0 {
					t.Fatalf("auto result %+v asks=%d", result, packageAsks)
				}
			} else {
				if !result.IsError || packageAsks != 1 {
					t.Fatalf("normal result %+v asks=%d", result, packageAsks)
				}
			}
		})
	}
}
