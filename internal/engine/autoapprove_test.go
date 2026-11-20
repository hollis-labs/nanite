package engine

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/subagent"
)

type autoDenyPDP struct{}

func (autoDenyPDP) Evaluate(context.Context, policy.Request) (policy.Decision, error) {
	return policy.Decision{Effect: policy.Deny}, nil
}

func TestAutoApproveRootAndChild(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			p := &routedProvider{root: fake.New(callWith("deploy", map[string]any{"payload": "do-not-log-this"}), subtaskCall("worker", "ship it", false), fake.Text("done")), children: map[string]*fake.Provider{"ship it": fake.New(callAs("deploy", "child-1"), fake.Text("shipped"))}}
			e, _, id := setupEngine(t, p, "")
			ran := askedTool(t, e)
			var logs bytes.Buffer
			e.cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			e.cfg.AutoApprove = auto
			gate := &recordingGate{decide: allowFor(permission.ScopeOnce)}
			e.cfg.Gate = gate
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			var autos, asks int
			for ev := range run.Events() {
				switch a := ev.(type) {
				case events.AutoApproved:
					autos++
					if strings.Contains(a.InputSummary, "do-not-log-this") {
						t.Fatal("input leaked")
					}
				case events.PermissionAsked:
					asks++
				}
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			if ran.Load() != 2 {
				t.Fatalf("ran %d", ran.Load())
			}
			if auto {
				if len(gate.asked()) != 0 || asks != 0 || autos != 2 || strings.Count(logs.String(), "auto-approved tool call") != 2 {
					t.Fatalf("gate=%d asks=%d autos=%d logs=%s", len(gate.asked()), asks, autos, logs.String())
				}
			} else if len(gate.asked()) != 2 || autos != 0 {
				t.Fatalf("gate=%d autos=%d", len(gate.asked()), autos)
			}
			if strings.Contains(logs.String(), "do-not-log-this") {
				t.Fatal("log leaked input")
			}
		})
	}
}

func TestAutoApproveKeepsExplicitDenies(t *testing.T) {
	for _, source := range []string{"authored", "pdp"} {
		t.Run(source, func(t *testing.T) {
			e, _, id := setupEngine(t, fake.New(callAs("deploy", "denied"), fake.Text("done")), "")
			ran := askedTool(t, e)
			e.cfg.AutoApprove = true
			if source == "pdp" {
				e.cfg.PDP = autoDenyPDP{}
			} else if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: deploy\n      behavior: deny\n"), 0600); err != nil {
				t.Fatal(err)
			}
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			for ev := range run.Events() {
				if _, ok := ev.(events.AutoApproved); ok {
					t.Fatal("deny auto-approved")
				}
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			if ran.Load() != 0 {
				t.Fatal("denied tool ran")
			}
		})
	}
}

func TestAutoApproveCommandPackage(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			e, _, id := setupEngine(t, fake.New(callWith("tool__echo", map[string]any{"text": "first"}), fake.Text("done"), callWith("tool__echo", map[string]any{"text": "changed"}), fake.Text("done")), "")
			e.cfg.AutoApprove = auto
			var logs bytes.Buffer
			e.cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			writeToolPackage(t, e.cfg.Dir, "echo", echoManifest, "#!/bin/sh\ncat\n")
			for i := range 2 {
				if i == 1 {
					writeToolPackage(t, e.cfg.Dir, "echo", echoManifest, "#!/bin/sh\n# new digest\ncat\n")
				}
				result := toolTurn(t, e, id)
				if result.IsError == auto {
					t.Fatalf("auto=%v result=%+v", auto, result)
				}
			}
			if auto && strings.Count(logs.String(), "auto mode tool package call") != 2 {
				t.Fatal(logs.String())
			}
			if files, err := os.ReadDir(e.cfg.ToolApprovalDir); err == nil && len(files) != 0 {
				t.Fatal("auto wrote approval")
			}
		})
	}
}

func TestAutoApproveMCPPackageDoesNotPersistApproval(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(callWith("tool__fixture__ping", map[string]any{}), fake.Text("done"), callWith("tool__fixture__ping", map[string]any{}), fake.Text("done"), callWith("tool__fixture__ping", map[string]any{}), fake.Text("done"))}
	e, _, id := setupEngine(t, p, "")
	e.cfg.AutoApprove = true
	var logs bytes.Buffer
	e.cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("TOOLPKG_MCP_SERVE", "1")
	t.Setenv("TOOLPKG_MARKER", marker)
	script := fmt.Sprintf("#!/bin/sh\ntouch \"$TOOLPKG_MARKER\"\nexec %q -test.run='^TestToolPackageMCPServerHelper$' -test.v=false\n", os.Args[0])
	manifest := "description: Fixture.\nkind: mcp\nentry: run.sh\nenv: [TOOLPKG_MCP_SERVE, TOOLPKG_MARKER]\n"
	writeToolPackage(t, e.cfg.Dir, "fixture", manifest, script)
	for i := range 2 {
		if i == 1 {
			writeToolPackage(t, e.cfg.Dir, "fixture", manifest, script+"\n# new digest\n")
		}
		result := toolTurn(t, e, id)
		if result.IsError || !strings.Contains(result.Content, "pong from the package") {
			t.Fatalf("run%d %+v", i, result)
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if strings.Count(logs.String(), "auto mode tool package call") != 2 {
		t.Fatal(logs.String())
	}
	if files, err := os.ReadDir(e.cfg.ToolApprovalDir); err == nil && len(files) != 0 {
		t.Fatal("auto wrote an approval")
	}
	e.cfg.AutoApprove = false
	if result := toolTurn(t, e, id); !result.IsError {
		t.Fatal("package still allowed with auto off")
	}
}

func TestAutoApproveHostDispatch(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			p := &routedProvider{root: fake.New(), children: map[string]*fake.Provider{"work": fake.New(returnCall("ok", map[string]any{"answer": "done"}))}}
			e, _, id := setupEngine(t, p, "")
			e.cfg.AutoApprove = auto
			if err := os.WriteFile(filepath.Join(e.cfg.Dir, "permissions.yaml"), []byte("permissions:\n  rules:\n    - tool: subtask\n      behavior: ask\n"), 0600); err != nil {
				t.Fatal(err)
			}
			gate := &recordingGate{decide: func(context.Context, permission.PermissionRequest) permission.PermissionDecision {
				return permission.PermissionDecision{Reason: "test deny"}
			}}
			e.cfg.Gate = gate
			run, err := e.Dispatch(t.Context(), id, []subagent.Request{researchStart("work")})
			if err != nil {
				t.Fatal(err)
			}
			seen, result := dispatchWait(t, run)
			autos := 0
			for _, ev := range seen {
				if _, ok := ev.(events.AutoApproved); ok {
					autos++
				}
			}
			if len(result.Dispatched) != 1 || result.Dispatched[0].IsError == auto {
				t.Fatalf("auto=%v result=%+v", auto, result)
			}
			if auto {
				if len(gate.asked()) != 0 || autos != 1 {
					t.Fatalf("asked=%d auto=%d", len(gate.asked()), autos)
				}
			} else if len(gate.asked()) != 1 || autos != 0 {
				t.Fatalf("asked=%d auto=%d", len(gate.asked()), autos)
			}
		})
	}
}

func TestAutoModeCannotCallPackageApprovalTool(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			e, _, id := setupEngine(t, fake.New(callWith("tool__fixture", map[string]any{}), fake.Text("done")), "")
			e.cfg.AutoApprove = auto
			e.cfg.Gate = permission.AllowGate
			writeToolPackage(t, e.cfg.Dir, "fixture", "description: Fixture.\nkind: mcp\nentry: run.sh\n", "#!/bin/sh\nexit 0\n")
			result := toolTurn(t, e, id)
			files, err := os.ReadDir(e.cfg.ToolApprovalDir)
			if auto {
				if !result.IsError || !strings.Contains(result.Content, "tool unavailable") {
					t.Fatalf("approval tool returned %+v", result)
				}
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(files) != 0 {
					t.Fatal("auto mode persisted approval")
				}
			} else if result.IsError || err != nil || len(files) != 1 {
				t.Fatalf("normal approval: %+v files=%d err=%v", result, len(files), err)
			}
		})
	}
}
