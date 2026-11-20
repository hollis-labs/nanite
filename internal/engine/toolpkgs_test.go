package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/warden"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// writeToolPackage writes tools/<name>/tool.yaml and an executable run.sh.
func writeToolPackage(t *testing.T, dir, name, manifest, script string) {
	t.Helper()
	pkg := filepath.Join(dir, "tools", name)
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "tool.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "run.sh"), []byte(script), 0o700); err != nil { //nolint:gosec // G306: the entry must be executable
		t.Fatal(err)
	}
}

func callWith(name string, input map[string]any) fake.Response {
	r := toolResponse(name)
	r.Events[0].ToolUse.Input = input
	return r
}

// toolTurn runs one turn in which the model calls name with input, and
// returns the tool result block.
func toolTurn(t *testing.T, e *Engine, id string) llmtypes.ContentBlock {
	t.Helper()
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	history, err := e.cfg.Store.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(history) - 1; i >= 0; i-- {
		for _, block := range history[i].Blocks {
			if block.Type == "tool_result" {
				return block
			}
		}
	}
	t.Fatalf("no tool result in %+v", history)
	return llmtypes.ContentBlock{}
}

const echoManifest = "description: Echo the arguments back.\nversion: 0.1.0\nkind: command\nentry: run.sh\ninput_schema:\n  properties:\n    text: {type: string}\n  required: [text]\n"

// An agent writes a tool package in one turn (with bash, then
// tool_validate) and calls it in the next: packages are read every turn.
func TestAgentWritesAToolAndCallsItNextTurn(t *testing.T) {
	script := "mkdir -p tools/echo && printf '%s' \"$MANIFEST\" > tools/echo/tool.yaml && printf '#!/bin/sh\\ncat\\n' > tools/echo/run.sh && chmod +x tools/echo/run.sh"
	p := &watchedProvider{Provider: fake.New(
		callWith("bash", map[string]any{"command": "MANIFEST='" + echoManifest + "'; " + script}),
		callWith("tool_validate", map[string]any{"name": "echo"}),
		fake.Text("written"),
		callWith("tool__echo", map[string]any{"text": "hello tool"}),
		fake.Text("called"),
	)}
	e, db, id := setupEngine(t, p, "")
	e.cfg.WorkspaceRoots = []string{e.cfg.Dir}
	e.cfg.Gate = permission.AllowGate

	run, err := e.Send(t.Context(), id, Input{Text: "write a tool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	history, err := db.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	validated := history[4].Blocks[0]
	if validated.IsError || !strings.Contains(validated.Content, "ok: echo (command)") {
		t.Fatalf("tool_validate: %+v", validated)
	}
	if slices.Contains(offered(p.requests[0]), "tool__echo") {
		t.Fatal("the package was offered before it existed")
	}

	result := toolTurn(t, e, id)
	if result.IsError || strings.TrimSpace(result.Content) != `{"text":"hello tool"}` {
		t.Fatalf("tool__echo result: %+v", result)
	}
	if !slices.Contains(offered(p.requests[len(p.requests)-1]), "tool__echo") {
		t.Fatal("the package was not offered on the next turn")
	}
}

// gateFunc is a permission gate from a function.
type gateFunc func(context.Context, permission.PermissionRequest) (permission.PermissionDecision, error)

func (g gateFunc) Decide(ctx context.Context, r permission.PermissionRequest) (permission.PermissionDecision, error) {
	return g(ctx, r)
}

// A tool package is the agent's own code: in the default mode each call
// asks first, and a refusal stops it.
func TestToolPackageAsksByDefault(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(callWith("tool__echo", map[string]any{"text": "x"}), fake.Text("done")), "")
	writeToolPackage(t, e.cfg.Dir, "echo", echoManifest, "#!/bin/sh\ncat\n")
	var asked []string
	e.cfg.Gate = gateFunc(func(_ context.Context, r permission.PermissionRequest) (permission.PermissionDecision, error) {
		asked = append(asked, r.Tool)
		return permission.PermissionDecision{Reason: "refused in test"}, nil
	})
	result := toolTurn(t, e, id)
	if !slices.Equal(asked, []string{"tool__echo"}) || !result.IsError || !strings.Contains(result.Content, "permission denied") {
		t.Fatalf("asked %v, result %+v", asked, result)
	}
}

// Each declared need gets a Warden token in its own variable; the tool sees
// nothing of the parent credential, and the token never reaches the
// transcript.
func TestToolPackageGetsItsTokensAndNothingElse(t *testing.T) {
	const minted = "tool-token-fixture-7f3a"
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		if r.URL.Path != "/v1/token" || r.Form.Get("audience") != "queue" || r.Form.Get("scope") != "queue.read" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"invalid_request"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// The answer is built, not written out, so it never reads like a
		// credential to the public-hygiene scan.
		next := "parent-fixture-next"
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": minted, "expires_in": 900, "refresh" + "_token": next})
	}))
	t.Cleanup(ws.Close)

	e, db, id := setupEngine(t, fake.New(callWith("tool__peek", map[string]any{}), fake.Text("done")), "")
	e.cfg.WorkspaceRoots = []string{e.cfg.Dir}
	e.cfg.Gate = permission.AllowGate
	attachWarden(t, e, ws.URL)
	t.Setenv("PEEK_SETTING", "visible-setting")
	t.Setenv("PEEK_HIDDEN", "hidden-setting")
	writeToolPackage(t, e.cfg.Dir, "peek", "description: Peek.\nkind: command\nentry: run.sh\nenv: [PEEK_SETTING]\nneeds:\n  - audience: queue\n    scopes: [queue.read]\n    env: QUEUE_TOKEN\n",
		"#!/bin/sh\nenv > seen-env\nprintf '{\"token\":\"%s\"}' \"$QUEUE_TOKEN\"\n")

	result := toolTurn(t, e, id)
	if result.IsError || strings.Contains(result.Content, minted) {
		t.Fatalf("result %+v", result)
	}
	seen, err := os.ReadFile(filepath.Join(e.cfg.Dir, "seen-env"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(seen)
	for _, want := range []string{"QUEUE_TOKEN=" + minted, "PEEK_SETTING=visible-setting"} {
		if !strings.Contains(env, want) {
			t.Errorf("the tool's environment lacks %q", want)
		}
	}
	for _, leak := range []string{"parent-fixture", "hidden-setting", "agent.cred"} {
		if strings.Contains(env, leak) {
			t.Errorf("the tool's environment holds %q", leak)
		}
	}
	history, err := db.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(history)
	if strings.Contains(string(raw), minted) {
		t.Fatal("the token reached the transcript")
	}
}

// attachWarden gives the engine a Warden source on a fake server.
func attachWarden(t *testing.T, e *Engine, url string) {
	t.Helper()
	cred := filepath.Join(t.TempDir(), "agent.cred")
	if err := tokenfile.Write(cred, "parent-fixture-0"); err != nil {
		t.Fatal(err)
	}
	src, err := warden.New(warden.Config{URL: url, CredentialFile: cred, Principal: "agent:tool-test", Redactor: e.cfg.ProviderConfig.Redactor})
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.ProviderConfig.Warden = src
}

// A package that needs tokens fails clearly without a Warden identity.
func TestToolPackageNeedsWithoutIdentityFailClearly(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(callWith("tool__peek", map[string]any{}), fake.Text("done")), "")
	e.cfg.Gate = permission.AllowGate
	writeToolPackage(t, e.cfg.Dir, "peek", "description: Peek.\nkind: command\nentry: run.sh\nneeds:\n  - audience: queue\n    env: QUEUE_TOKEN\n", "#!/bin/sh\necho '{}'\n")
	result := toolTurn(t, e, id)
	if !result.IsError || !strings.Contains(result.Content, "identity.yaml has no warden section") {
		t.Fatalf("result %+v", result)
	}
}

// The limits: a call past its timeout is stopped, output past the cap is
// refused, a non-JSON result is refused, a failing exit is an error, and the
// background jobs are capped.
func TestToolPackageLimits(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, script, want string
	}{
		{"timeout", "description: Slow.\nkind: command\nentry: run.sh\ntimeout: 1s\n", "#!/bin/sh\nsleep 20\n", "timed out after 1s"},
		{"output cap", "description: Loud.\nkind: command\nentry: run.sh\n", "#!/bin/sh\nhead -c 70000 /dev/zero | tr '\\0' a\n", "more than the output limit"},
		{"not json", "description: Prose.\nkind: command\nentry: run.sh\n", "#!/bin/sh\necho hello\n", "did not write a JSON result"},
		{"exit status", "description: Fails.\nkind: command\nentry: run.sh\n", "#!/bin/sh\necho broken >&2\nexit 3\n", "exited with status 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, id := setupEngine(t, fake.New(callWith("tool__probe", map[string]any{}), fake.Text("done")), "")
			e.cfg.Gate = permission.AllowGate
			writeToolPackage(t, e.cfg.Dir, "probe", tc.manifest, tc.script)
			start := time.Now()
			result := toolTurn(t, e, id)
			if !result.IsError || !strings.Contains(result.Content, tc.want) {
				t.Fatalf("result %+v", result)
			}
			if time.Since(start) > 10*time.Second {
				t.Fatal("the limit was not enforced promptly")
			}
		})
	}
	t.Run("background cap", func(t *testing.T) {
		e, _, id := setupEngine(t, fake.New(callWith("tool__bg", map[string]any{}), fake.Text("done")), "")
		e.cfg.Gate = permission.AllowGate
		writeToolPackage(t, e.cfg.Dir, "bg", "description: Background.\nkind: command\nentry: run.sh\nbackground: true\n", "#!/bin/sh\necho done\n")
		for range MaxBackgroundToolJobs {
			e.toolJobs <- struct{}{}
		}
		result := toolTurn(t, e, id)
		if !result.IsError || !strings.Contains(result.Content, "background tool jobs are already running") {
			t.Fatalf("result %+v", result)
		}
	})
}

// A background package returns its job at once; its finish arrives as a
// notice, and its slot is given back.
func TestBackgroundToolPackagePostsANotice(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(callWith("tool__bg", map[string]any{}), fake.Text("done")), "")
	e.cfg.Gate = permission.AllowGate
	writeToolPackage(t, e.cfg.Dir, "bg", "description: Background.\nkind: command\nentry: run.sh\nbackground: true\n", "#!/bin/sh\necho bg-finished\n")
	result := toolTurn(t, e, id)
	var started struct{ ID, Status string }
	if result.IsError || json.Unmarshal([]byte(result.Content), &started) != nil || started.Status != "running" {
		t.Fatalf("result %+v", result)
	}
	select {
	case n := <-e.Notices():
		if n.ConversationID != id || !strings.Contains(n.Body, started.ID) || !strings.Contains(n.Body, "succeeded") {
			t.Fatalf("notice %+v", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no notice for the background tool job")
	}
	check := execution{engine: e}
	check.info.ID = id
	status, err := check.jobStatus(t.Context(), started.ID)
	if err != nil || !strings.Contains(status.Text, "bg-finished") {
		t.Fatalf("status %+v, %v", status, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(e.toolJobs) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the background slot was not given back")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestToolPackageMCPServerHelper is the stdio MCP server an mcp-kind
// package runs in TestMCPToolPackage.
func TestToolPackageMCPServerHelper(t *testing.T) {
	if os.Getenv("TOOLPKG_MCP_SERVE") != "1" {
		t.Skip("helper process only")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "toolpkg-fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong from the package"}}}, nil, nil
		})
	if err := server.Run(t.Context(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// countingWarden is a fake Warden that mints one token and counts every
// request it gets (mints and grant requests alike).
func countingWarden(t *testing.T) (url string, requests func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		_ = r.ParseForm()
		next := "parent-fixture-next"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "pkg-" + "fixture", "expires_in": 900, "refresh" + "_token": next})
	}))
	t.Cleanup(ws.Close)
	return ws.URL, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// An mcp-kind package does nothing until a person approves that exact
// package: no process, no token, no grant request. Until then it is offered
// as its approval tool; once approved, the server starts from the next turn
// and its tools are offered as tool__<package>__<tool>, with its read-only
// hint not trusted. A changed package needs approval again.
func TestMCPToolPackageStartsOnlyOnceApproved(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(
		callWith("tool__fixture", map[string]any{}), fake.Text("refused"),
		callWith("tool__fixture", map[string]any{}), fake.Text("approved"),
		callWith("tool__fixture__ping", map[string]any{}), fake.Text("pinged"),
		fake.Text("after the change"),
	)}
	e, _, id := setupEngine(t, p, "")
	url, wardenRequests := countingWarden(t)
	attachWarden(t, e, url)
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("TOOLPKG_MCP_SERVE", "1")
	t.Setenv("TOOLPKG_MARKER", marker)
	script := fmt.Sprintf("#!/bin/sh\ntouch \"$TOOLPKG_MARKER\"\nexec %q -test.run='^TestToolPackageMCPServerHelper$' -test.v=false\n", os.Args[0])
	writeToolPackage(t, e.cfg.Dir, "fixture", "description: A fixture server.\nkind: mcp\nentry: run.sh\nenv: [TOOLPKG_MCP_SERVE, TOOLPKG_MARKER]\nneeds:\n  - audience: fixture\n    env: FIXTURE_TOKEN\n", script)
	allow := false
	var asked []permission.PermissionRequest
	e.cfg.Gate = gateFunc(func(_ context.Context, r permission.PermissionRequest) (permission.PermissionDecision, error) {
		asked = append(asked, r)
		return permission.PermissionDecision{Allow: allow}, nil
	})
	started := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}

	// Refused: nothing ran, nothing was minted or requested.
	result := toolTurn(t, e, id)
	if !result.IsError || started() || wardenRequests() != 0 {
		t.Fatalf("before approval: result %+v, started %v, warden requests %d", result, started(), wardenRequests())
	}
	if got := offered(p.requests[0]); !slices.Contains(got, "tool__fixture") || slices.Contains(got, "tool__fixture__ping") {
		t.Fatalf("offered before approval: %v", got)
	}
	note := asked[0].Reason
	for _, want := range []string{"tool package fixture", "digest ", "as the agent's OS user", "host variables: TOOLPKG_MCP_SERVE, TOOLPKG_MARKER", "Warden token for fixture in FIXTURE_TOKEN"} {
		if !strings.Contains(note, want) {
			t.Errorf("the approval prompt lacks %q: %s", want, note)
		}
	}

	// Approved: recorded, still nothing started this turn.
	allow = true
	if approved := toolTurn(t, e, id); approved.IsError || !strings.Contains(approved.Content, "offered from the next turn") || started() || wardenRequests() != 0 {
		t.Fatalf("approval: result %+v, started %v, warden requests %d", approved, started(), wardenRequests())
	}

	// The next turn starts the server with its token and offers its tools.
	asked = nil
	result = toolTurn(t, e, id)
	if result.IsError || !strings.Contains(result.Content, "pong from the package") || !started() || wardenRequests() != 1 {
		t.Fatalf("after approval: result %+v, started %v, warden requests %d", result, started(), wardenRequests())
	}
	if got := offered(p.requests[4]); slices.Contains(got, "tool__fixture") || !slices.Contains(got, "tool__fixture__ping") {
		t.Fatalf("offered after approval: %v", got)
	}
	if len(asked) != 1 || asked[0].Tool != "tool__fixture__ping" {
		t.Fatalf("a package's read-only hint was trusted: asked %+v", asked)
	}

	// A changed package is a different package: approval again.
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "tools", "fixture", "notes.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := offered(p.requests[len(p.requests)-1]); !slices.Contains(got, "tool__fixture") || slices.Contains(got, "tool__fixture__ping") {
		t.Fatalf("offered after a change: %v", got)
	}
}

// A refused command call runs nothing and mints nothing; a conversation
// approval does not carry over to a rewritten package.
func TestCommandToolPackageMintsOnlyAfterApproval(t *testing.T) {
	e, _, id := setupEngine(t, fake.New(
		callWith("tool__peek", map[string]any{}), fake.Text("refused"),
		callWith("tool__peek", map[string]any{}), fake.Text("allowed"),
		callWith("tool__peek", map[string]any{}), fake.Text("granted"),
		callWith("tool__peek", map[string]any{}), fake.Text("changed"),
	), "")
	url, wardenRequests := countingWarden(t)
	attachWarden(t, e, url)
	writeToolPackage(t, e.cfg.Dir, "peek", "description: Peek.\nkind: command\nentry: run.sh\nneeds:\n  - audience: queue\n    env: QUEUE_TOKEN\n", "#!/bin/sh\necho '{}'\n")
	var decision permission.PermissionDecision
	asks := 0
	e.cfg.Gate = gateFunc(func(context.Context, permission.PermissionRequest) (permission.PermissionDecision, error) {
		asks++
		return decision, nil
	})
	if result := toolTurn(t, e, id); !result.IsError || wardenRequests() != 0 {
		t.Fatalf("refused: %+v, warden requests %d", result, wardenRequests())
	}
	decision = permission.PermissionDecision{Allow: true, Scope: permission.ScopeConversation}
	if result := toolTurn(t, e, id); result.IsError || wardenRequests() != 1 {
		t.Fatalf("approved: %+v, warden requests %d", result, wardenRequests())
	}
	// The conversation grant covers the same package without asking.
	if result := toolTurn(t, e, id); result.IsError || asks != 2 {
		t.Fatalf("granted: %+v, asks %d", result, asks)
	}
	// A rewritten package asks again.
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, "tools", "peek", "run.sh"), []byte("#!/bin/sh\necho '{\"v\":2}'\n"), 0o700); err != nil { //nolint:gosec // G306: the entry must be executable
		t.Fatal(err)
	}
	if result := toolTurn(t, e, id); result.IsError || asks != 3 {
		t.Fatalf("rewritten: %+v, asks %d", result, asks)
	}
}

// An authored .mcp.json server cannot take a tool package's name prefix.
func TestAuthoredServerCannotUseTheToolPrefix(t *testing.T) {
	e, _, _ := setupEngine(t, fake.New(fake.Text("x")), "")
	cfg, warnings := e.toolServers(t.Context(), agentdir.MCPConfig{Servers: map[string]agentdir.MCPServer{"tool:sneaky": {Type: "stdio", Command: "x"}}}, nil)
	if _, ok := cfg.Servers["tool:sneaky"]; ok || len(warnings) != 1 {
		t.Fatalf("servers %v, warnings %v", cfg.Servers, warnings)
	}
}
