package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const nativeAgentDef = `---
name: Imported Reviewer
slug: imported-reviewer
description: Came from outside
---
Imported system prompt.
`

const claudeAgentDef = `---
name: code-reviewer
description: Expert code review specialist
tools: Read, Grep, Bash
model: sonnet
---
You are a code reviewer.
`

func writeAgentSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func decodeImportResponse(t *testing.T, body []byte) ImportAgentResponse {
	t.Helper()
	var out ImportAgentResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode import response: %v; body: %s", err, body)
	}
	return out
}

func postAgentImport(t *testing.T, mux http.Handler, path string, body map[string]string) (*http.Response, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w, out := mgReq(t, mux, "POST", path, string(raw))
	return w.Result(), out
}

func TestRetiredAgentImportCannotInstallOrSyncProfiles(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "import")
	sourceRoot := t.TempDir()
	src := writeAgentSource(t, sourceRoot, "imported-reviewer.md", nativeAgentDef)
	before := retiredAgentState(t, a)
	for _, path := range []string{"/api/agents/install", "/api/agents/" + p.Slug + "/sync", "/api/agents/never-imported/sync"} {
		resp, body := postAgentImport(t, mux, path, map[string]string{"path": src})
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("%s = %d: %s", path, resp.StatusCode, body)
		}
		assertRetiredAgentState(t, a, before)
	}
	raw, err := fs.ReadFile(os.DirFS(sourceRoot), "imported-reviewer.md")
	if err != nil || string(raw) != nativeAgentDef {
		t.Fatal("retired import modified source", err)
	}
}

func TestRetiredAgentImportDoesNotInterpretSourceInputs(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	malformed := writeAgentSource(t, t.TempDir(), "notes.md", "# Just prose, no frontmatter\n")
	root := t.TempDir()
	writeAgentSource(t, filepath.Join(root, ".claude", "agents"), "code-reviewer.md", claudeAgentDef)
	for _, input := range []map[string]string{{}, {"path": malformed}, {"path": root}, {"path": "/does/not/exist"}, {"path": "../escape"}} {
		resp, body := postAgentImport(t, mux, "/api/agents/install", input)
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("retired install = %d: %s", resp.StatusCode, body)
		}
		assertRetiredAgentState(t, a, before)
	}
	retiredAgentRequest(t, mux, "POST", "/api/agents/install", `{`)
	assertRetiredAgentState(t, a, before)
}

func TestRetiredAgentSyncNeverConvertsHistoricalOwnership(t *testing.T) {
	for _, source := range []string{"internal", "user", "plugin", "import"} {
		t.Run(source, func(t *testing.T) {
			a, mux := newTestAPI(t)
			p := retiredAgentHistory(t, a, source)
			before := retiredAgentState(t, a)
			retiredAgentRequest(t, mux, "POST", "/api/agents/"+p.Slug+"/sync", `{"path":"/provenance/only.md"}`)
			assertRetiredAgentState(t, a, before)
		})
	}
}
