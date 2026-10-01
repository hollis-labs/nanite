package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpserver"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/selftools"
)

// mcpServeHelperEnv makes this test binary act as `nanite mcp` (see
// TestMCPServeHelperProcess); mcpServeHelperArgsEnv carries its arguments.
const (
	mcpServeHelperEnv     = "NANITE_TEST_MCP_SERVE_HELPER"
	mcpServeHelperArgsEnv = "NANITE_TEST_MCP_SERVE_ARGS"
)

// TestMCPServeHelperProcess is not a test of its own. startMCPServe runs this
// binary with mcpServeHelperEnv set, and this function then serves
// `nanite mcp` on stdio until the client disconnects.
func TestMCPServeHelperProcess(t *testing.T) {
	if os.Getenv(mcpServeHelperEnv) != "1" {
		t.Skip("runs only as startMCPServe's child process")
	}
	cmdMCPServe(strings.Fields(os.Getenv(mcpServeHelperArgsEnv)))
	os.Exit(0)
}

// startMCPServe runs `nanite mcp args...` as a child of this test binary,
// inside wrap (a sandbox command line, or nil), and connects an MCP client
// to it. env is added to the child's environment after NANITE_API_URL and
// NANITE_MCP_SELF_TOOLS are cleared from it. The child's stderr is returned
// for failure messages.
func startMCPServe(t *testing.T, wrap, env []string, args ...string) (*mcpsdk.ClientSession, *bytes.Buffer, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := append(slices.Clone(wrap), exe, "-test.run=^TestMCPServeHelperProcess$")
	// #nosec G204 -- the test binary itself, re-run as a helper process.
	cmd := exec.Command(argv[0], argv[1:]...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NANITE_API_URL=") && !strings.HasPrefix(kv, runtimeagent.SelfToolsScopeEnv+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, mcpServeHelperEnv+"=1", mcpServeHelperArgsEnv+"="+strings.Join(args, " "))
	cmd.Env = append(cmd.Env, env...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, stderr, err
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, stderr, nil
}

// harnessStub stands in for the live harness's POST /api/tools/call and
// records the tool names forwarded to it.
type harnessStub struct {
	mu       sync.Mutex
	names    []string
	sessions []string
}

func (h *harnessStub) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string `json:"session_id"`
			Name      string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		h.mu.Lock()
		h.names = append(h.names, req.Name)
		h.sessions = append(h.sessions, req.SessionID)
		h.mu.Unlock()
		_ = json.NewEncoder(w).Encode(mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: "ok:" + req.Name}}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (h *harnessStub) forwarded() ([]string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.names), slices.Clone(h.sessions)
}

// readOnlyDBDir is a way of making dir read-only to the `nanite mcp` child:
// a sandbox command line to wrap it in, or a chmod done here.
type readOnlyDBDir struct {
	name string
	wrap func(t *testing.T, dir string) []string
}

func readOnlyDBDirs(t *testing.T) []readOnlyDBDir {
	t.Helper()
	modes := []readOnlyDBDir{{
		name: "chmod 0500",
		wrap: func(t *testing.T, dir string) []string {
			if os.Geteuid() == 0 {
				t.Skip("root ignores directory permissions")
			}
			// #nosec G302 -- read and search only: the point is that nothing can be created here.
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			// #nosec G302 -- restores the owner's write bit so t.TempDir can clean up.
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			return nil
		},
	}}
	bwrapArgs := func(dir string) []string {
		// The shape task-nanite-1 measured CW-20261001-0143 with: the host
		// filesystem and network, the database's directory bound read-only.
		return []string{"bwrap", "--unshare-user", "--unshare-pid", "--dev-bind", "/", "/", "--proc", "/proc", "--ro-bind", dir, dir, "--"}
	}
	probeDir := t.TempDir()
	// #nosec G204 -- fixed probe command line.
	if out, err := exec.Command(bwrapArgs(probeDir)[0], append(bwrapArgs(probeDir)[1:], "true")...).CombinedOutput(); err == nil {
		modes = append(modes, readOnlyDBDir{name: "bwrap --ro-bind", wrap: func(_ *testing.T, dir string) []string { return bwrapArgs(dir) }})
	} else {
		t.Logf("bwrap unavailable here, covering chmod only: %v %s", err, out)
	}
	return modes
}

func sortedNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// CW-20261001-0188: an agent's `nanite mcp` runs inside its sandbox, where
// the database's directory is to be read-only (CW-20261001-0143). With
// NANITE_API_URL it must start there, open nothing in that directory, and
// serve its launch's self tools through the API: a chat launch every self
// tool, a subagent the bare-store set. Without NANITE_API_URL the same
// setup still cannot open the database, which shows the directory really
// is read-only to the child.
func TestMCPServe_APIModeRunsWithReadOnlyDBDir(t *testing.T) {
	devTools, _ := mcp.NewDevToolsTransport(nil).ListTools(context.Background())
	allSelf, _ := (&selftools.SelfToolsTransport{}).ListTools(context.Background())
	wantFor := map[mcpserver.SelfToolScope][]string{
		mcpserver.ScopeHarness: append(sortedNames(append(slices.Clone(allSelf), devTools...)), "fetch_tool_result", "search_tool_result"),
		mcpserver.ScopeStore:   sortedNames(append(selftools.BareStoreTools(), devTools...)),
	}
	for _, want := range wantFor {
		slices.Sort(want)
	}

	for _, ro := range readOnlyDBDirs(t) {
		t.Run(ro.name, func(t *testing.T) {
			launches := []struct {
				name  string
				env   []string
				scope mcpserver.SelfToolScope
			}{
				{"chat launch", nil, mcpserver.ScopeHarness},
				{"subagent launch", []string{runtimeagent.SelfToolsScopeEnv + "=" + runtimeagent.SelfToolsScopeStore}, mcpserver.ScopeStore},
			}
			for _, launch := range launches {
				t.Run(launch.name, func(t *testing.T) {
					dir := filepath.Join(t.TempDir(), "workspace")
					if err := os.Mkdir(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					wrap := ro.wrap(t, dir)
					harness := &harnessStub{}
					env := append([]string{"NANITE_API_URL=" + harness.start(t)}, launch.env...)

					cs, stderr, err := startMCPServe(t, wrap, env, "--db", filepath.Join(dir, "main.db"), "--session", "sess-ro")
					if err != nil {
						t.Fatalf("nanite mcp did not start: %v\nstderr:\n%s", err, stderr)
					}
					listed, err := cs.ListTools(context.Background(), nil)
					if err != nil {
						t.Fatalf("ListTools: %v", err)
					}
					var got []string
					for _, tool := range listed.Tools {
						got = append(got, tool.Name)
					}
					slices.Sort(got)
					if want := wantFor[launch.scope]; !slices.Equal(got, want) {
						t.Errorf("listed %v\nwant %v", got, want)
					}

					res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "agent_list", Arguments: map[string]any{}})
					if err != nil || res.IsError {
						t.Fatalf("agent_list: err %v, result %#v\nstderr:\n%s", err, res, stderr)
					}
					res, err = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "subagent_spawn", Arguments: map[string]any{}})
					if err != nil {
						t.Fatalf("subagent_spawn: %v", err)
					}
					names, sessions := harness.forwarded()
					wantForwarded := []string{"agent_list", "subagent_spawn"}
					if launch.scope == mcpserver.ScopeStore {
						wantForwarded = []string{"agent_list"}
						if !res.IsError {
							t.Errorf("subagent_spawn on a subagent launch should be refused, got %#v", res)
						}
					}
					if !slices.Equal(names, wantForwarded) {
						t.Errorf("forwarded %v, want %v", names, wantForwarded)
					}
					for _, s := range sessions {
						if s != "sess-ro" {
							t.Errorf("forwarded session_id %q, want sess-ro", s)
						}
					}

					entries, err := os.ReadDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 0 {
						t.Errorf("nanite mcp left %d entries in the database's directory, want none", len(entries))
					}
				})
			}

			t.Run("without NANITE_API_URL the database cannot open", func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "workspace")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				wrap := ro.wrap(t, dir)
				_, stderr, err := startMCPServe(t, wrap, nil, "--db", filepath.Join(dir, "main.db"), "--session", "sess-ro")
				if err == nil {
					t.Fatal("local-mode nanite mcp started with a read-only database directory; the setup is not read-only")
				}
				if !strings.Contains(stderr.String(), "open db") {
					t.Errorf("stderr = %q, want the open db failure", stderr)
				}
			})
		})
	}
}

func TestSelfToolScope(t *testing.T) {
	for raw, want := range map[string]mcpserver.SelfToolScope{
		"":                               mcpserver.ScopeHarness,
		runtimeagent.SelfToolsScopeStore: mcpserver.ScopeStore,
		"harness-typo":                   mcpserver.ScopeStore,
	} {
		if got := selfToolScope(raw); got != want {
			t.Errorf("selfToolScope(%q) = %v, want %v", raw, got, want)
		}
	}
}
