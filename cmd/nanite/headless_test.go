package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// gatewayAgent is an agent directory whose model is a local OpenAI-compatible stub that answers
// "reply N" to the Nth request.
func gatewayAgent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.yaml"), []byte("kind: stable\nlifetime: on-demand\nseat_id: local-agent\nruntime:\n  provider: openai\n  model: test-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := n.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta, finish string) {
			fin := "null"
			if finish != "" {
				fin = `"` + finish + `"`
			}
			d, _ := json.Marshal(map[string]string{"content": delta})
			if delta == "" {
				d = []byte("{}")
			}
			_, _ = fmt.Fprintf(w, `data: {"id":"f","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n", d, fin)
		}
		chunk(fmt.Sprintf("reply %d", k), "")
		chunk("", "stop")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	key := filepath.Join(t.TempDir(), "mock-key")
	if err := os.WriteFile(key, []byte("unit-test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(key)
	t.Setenv("NANITE_GATEWAY_URL", server.URL+"/v1")
	t.Setenv("NANITE_GATEWAY_KEY_REF", "file://"+resolved)
	return dir
}

func runRoot(t *testing.T, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	ctx := context.Background()
	started, err := start(ctx, filepath.Join(dir, ".nanite"))
	if err != nil {
		t.Fatal(err)
	}
	defer started.Stop(ctx)
	root := newRoot(started.App, dir)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.ExecuteContext(ctx)
	return out.String(), err
}

func TestHeadlessStreamJSONLikeClaude(t *testing.T) {
	dir := gatewayAgent(t)
	const sid = "3f2b8a52-9c1e-4d7a-8f0e-2b6c1d9e4a70"
	in := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"one"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"two"}}` + "\n"
	// The argv exo's claude adapter uses, less the flags of later slices.
	out, err := runRoot(t, dir, in, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--session-id", sid)
	if err != nil {
		t.Fatal(err)
	}
	var kinds, results []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var m struct {
			Type, Subtype, Result, Model string
			SessionID                    string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if m.SessionID != sid {
			t.Fatalf("session_id %q in %s", m.SessionID, line)
		}
		kinds = append(kinds, m.Type)
		if m.Type == "result" {
			results = append(results, m.Result)
		}
		if m.Type == "system" && m.Model != "test-model" {
			t.Fatalf("init model: %s", line)
		}
	}
	if strings.Join(kinds, " ") != "system assistant result assistant result" || strings.Join(results, ",") != "reply 1,reply 2" {
		t.Fatalf("stream:\n%s", out)
	}
	// The same id again continues that conversation.
	out, err = runRoot(t, dir, "", "-p", "again", "--output-format", "json", "--session-id", sid)
	if err != nil || !strings.Contains(out, `"result":"reply 3"`) || !strings.Contains(out, sid) {
		t.Fatalf("again: %v %s", err, out)
	}
	// -p alone takes the prompt as an argument; the old -p "text" keeps its text output.
	if out, err := runRoot(t, dir, "", "-p", "--output-format", "text", "as an argument"); err != nil || out != "reply 4\n" {
		t.Fatalf("argument: %v %q", err, out)
	}
	if out, err := runRoot(t, dir, "", "-p", "classic"); err != nil || out != "reply 5\n" {
		t.Fatalf("classic: %v %q", err, out)
	}
	if _, err := runRoot(t, dir, "", "-p", "x", "--session-id", sid, "--continue"); err == nil {
		t.Fatal("--session-id with --continue ran")
	}
}

func TestExtractDirLeavesABareP(t *testing.T) {
	_, args, err := extractDir([]string{"-p", "--input-format", "stream-json", "-C", t.TempDir(), "--session-id", "s"})
	if err != nil || strings.Join(args, " ") != "-p --input-format stream-json --session-id s" {
		t.Fatalf("%v %q", err, args)
	}
	_, args, _ = extractDir([]string{"-p", "hello there", "--output", "json"})
	if strings.Join(args, "|") != "-p|hello there|--output|json" {
		t.Fatalf("%q", args)
	}
	if hasHelpFlag([]string{"-p", "--help"}) != true || hasHelpFlag([]string{"-p", "--", "--help"}) {
		t.Fatal("help scan")
	}
}
