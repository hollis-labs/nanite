package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestLiveAgyPerToolAllowlist drives a real agy turn through the real agy
// launch code path (Setup -> AmendEnv -> SpawnWorkdir -> the launch's extra
// argv) with two stub MCP servers: Nanite's own (planted as server "nanite")
// and a host mcp_server granted only ok_tool. It spends real model tokens, so
// it is skipped unless NANITE_LIVE_AGY=1. NANITE_LIVE_AGY_OUT names a directory
// for the evidence (stream, stderr, stub log); default is a temp dir.
//
// Not covered: go-agent-wrapper's process management; the argv is assembled
// from the same helpers selectAdapter uses and exec'd directly.
func TestLiveAgyPerToolAllowlist(t *testing.T) {
	if os.Getenv("NANITE_LIVE_AGY") != "1" {
		t.Skip("set NANITE_LIVE_AGY=1 to run the live agy proof (spends model tokens)")
	}
	// Real home for auth and the agy binary; the data dir is a throwaway.
	// testhome points HOME at a temp dir for the package, so ask for the real one.
	realHome := os.Getenv("NANITE_LIVE_AGY_REAL_HOME")
	if realHome == "" {
		t.Skip("set NANITE_LIVE_AGY_REAL_HOME to the home holding the agy login")
	}
	out := os.Getenv("NANITE_LIVE_AGY_OUT")
	if out == "" {
		out = t.TempDir()
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", realHome)
	oldRoot := agyGeminiRoot
	agyGeminiRoot = func() string { return filepath.Join(out, "data", "agy-gemini") }
	t.Cleanup(func() { agyGeminiRoot = oldRoot })

	stub := filepath.Join(out, "stub.py")
	log := filepath.Join(out, "stub.log")
	if err := os.WriteFile(stub, []byte(liveStubPy), 0o600); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(out, "nanite-own")
	if err := os.WriteFile(own, []byte("#!/bin/sh\nexec python3 -I '"+stub+"' own '"+log+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	params := SetupParams{
		SessionID:    "live-agy",
		Mode:         ModeLongLived,
		AgentProfile: &store.AgentProfile{Name: "Live", Slug: "live-agy", ID: "live-agy"},
		BootContent:  "live proof",
		MCPConfig:    MCPConfig{BinaryPath: own, DBPath: filepath.Join(out, "unused.db")},
		HostMCP: &HostMCPLaunch{
			Servers: []HostMCPServer{{Name: "stub", Command: "python3", Args: []string{"-I", stub, "host", log}}},
			Tools:   []string{"ok_tool"},
		},
	}
	layout := bootdirLayoutFor("agy")
	boot, err := layout.Setup(params)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })

	env := layout.AmendEnv(composeEnv(params.AgentProfile, Options{}), boot)
	argv := []string{"--model", "gemini-3.8-flash-low", "--print-timeout", "150s", "--output-format", "stream-json"}
	argv = append(argv, agyPermissionArgs(&Dependencies{}, "antigravity")...)
	argv = append(argv, addDirArgs("agy", out, nil)...)
	argv = append(argv, "-p", "Call the MCP tool ok_tool from the stub server, then call the MCP tool own_tool from the nanite server, then call the MCP tool no_tool from the stub server. A denied call ends your turn, so make the no_tool call last. Use no other tools. Reply with one line per tool: the tool name and either its result text or the denial message.")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(realHome, ".local/bin/agy"), argv...) //nolint:gosec // live proof, fixed argv
	cmd.Dir = layout.SpawnWorkdir(boot, "")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdout, _ := os.Create(filepath.Join(out, "stream.jsonl"))
	stderr, _ := os.Create(filepath.Join(out, "stderr.txt"))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	_ = stdout.Close()
	_ = stderr.Close()
	t.Logf("agy exit: %v; HOME=%s", runErr, env["HOME"])

	calls, _ := os.ReadFile(log) //nolint:gosec // G304: the test's own log
	got := string(calls)
	t.Logf("stub tools/call log:\n%s", got)
	for _, want := range []string{"host ok_tool", "own own_tool"} {
		if !strings.Contains(got, want) {
			t.Errorf("stub log lacks %q: the granted tool did not run", want)
		}
	}
	if strings.Contains(got, "no_tool") {
		t.Error("no_tool ran: a denied tool executed")
	}
}

const liveStubPy = `import json, sys
role, log = sys.argv[1], sys.argv[2]
TOOLS = {"host": ["ok_tool", "no_tool"], "own": ["own_tool"]}[role]
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    try:
        m = json.loads(line)
    except ValueError:
        continue
    mid, meth = m.get("id"), m.get("method")
    if mid is None:
        continue
    if meth == "initialize":
        send({"jsonrpc": "2.0", "id": mid, "result": {"protocolVersion": m["params"].get("protocolVersion", "2024-11-05"),
              "capabilities": {"tools": {}}, "serverInfo": {"name": role, "version": "0"}}})
    elif meth == "tools/list":
        send({"jsonrpc": "2.0", "id": mid, "result": {"tools": [
            {"name": t, "description": "stub tool " + t, "inputSchema": {"type": "object", "properties": {}}} for t in TOOLS]}})
    elif meth == "tools/call":
        name = m["params"]["name"]
        with open(log, "a") as f:
            f.write(role + " " + name + "\n")
        send({"jsonrpc": "2.0", "id": mid, "result": {"content": [{"type": "text", "text": name + " executed"}]}})
    else:
        send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "method not found"}})
`

// TestLiveAgyPathAllowlist drives one real agy turn through the real launch
// path to prove the non-MCP path rules (agy_path_allow.go): a file write and a
// read inside the allowed root (and a subdirectory of it) execute, a write
// outside every root and a shell command (no command rule exists) are denied.
// One model turn, cheapest model; skipped unless NANITE_LIVE_AGY=1 (same env
// as TestLiveAgyPerToolAllowlist). The evidence lands in NANITE_LIVE_AGY_OUT.
func TestLiveAgyPathAllowlist(t *testing.T) {
	if os.Getenv("NANITE_LIVE_AGY") != "1" {
		t.Skip("set NANITE_LIVE_AGY=1 to run the live agy proof (spends model tokens)")
	}
	realHome := os.Getenv("NANITE_LIVE_AGY_REAL_HOME")
	if realHome == "" {
		t.Skip("set NANITE_LIVE_AGY_REAL_HOME to the home holding the agy login")
	}
	out := os.Getenv("NANITE_LIVE_AGY_OUT")
	if out == "" {
		out = t.TempDir()
	}
	out, _ = filepath.EvalSymlinks(out)
	work := filepath.Join(out, "work")
	outside := filepath.Join(out, "outside")
	for _, d := range []string{work, filepath.Join(work, "sub"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", realHome)
	oldRoot := agyGeminiRoot
	agyGeminiRoot = func() string { return filepath.Join(out, "data", "agy-gemini") }
	t.Cleanup(func() { agyGeminiRoot = oldRoot })

	params := SetupParams{
		SessionID:        "live-agy-path",
		Mode:             ModeLongLived,
		AgentProfile:     &store.AgentProfile{Name: "Live", Slug: "live-agy-path", ID: "live-agy-path"},
		BootContent:      "live proof",
		CLIWritableRoots: []string{work},
	}
	layout := bootdirLayoutFor("agy")
	boot, err := layout.Setup(params)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })
	t.Logf("rules: %q", agyAllowRules(params))

	env := layout.AmendEnv(composeEnv(params.AgentProfile, Options{}), boot)
	argv := []string{"--model", "gemini-3.8-flash-low", "--print-timeout", "150s", "--output-format", "stream-json"}
	argv = append(argv, agyPermissionArgs(&Dependencies{}, "antigravity")...)
	argv = append(argv, addDirArgs("agy", work, nil)...)
	argv = append(argv, "-p", "Do these in order and stop at the first one that is refused. 1) Write a file "+filepath.Join(work, "ok.txt")+" containing exactly: ok. 2) Write a file "+filepath.Join(work, "sub", "deep.txt")+" containing exactly: deep. 3) Read "+filepath.Join(work, "ok.txt")+" back. 4) Write a file "+filepath.Join(outside, "denied.txt")+" containing exactly: no. 5) Run the shell command: echo hi. A denied step ends your turn, so do the steps in this order. Use no other tools. Reply with one line per step: done, or the denial message.")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(realHome, ".local/bin/agy"), argv...) //nolint:gosec // live proof, fixed argv
	cmd.Dir = layout.SpawnWorkdir(boot, "")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdout, _ := os.Create(filepath.Join(out, "stream.jsonl"))
	stderr, _ := os.Create(filepath.Join(out, "stderr.txt"))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	_ = stdout.Close()
	_ = stderr.Close()
	t.Logf("agy exit: %v; HOME=%s", runErr, env["HOME"])

	read := func(p string) string {
		b, _ := os.ReadFile(p) //nolint:gosec // G304: the test's own files
		return strings.TrimSpace(string(b))
	}
	if got := read(filepath.Join(work, "ok.txt")); got != "ok" {
		t.Errorf("ok.txt = %q: the write under the allowed root did not run", got)
	}
	t.Logf("sub/deep.txt = %q (a path rule covering subdirectories)", read(filepath.Join(work, "sub", "deep.txt")))
	if _, err := os.Stat(filepath.Join(outside, "denied.txt")); err == nil {
		t.Error("denied.txt exists: a write outside every allowed root executed")
	}
}
