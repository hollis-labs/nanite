package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-secrets/secretstest"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const setupTestKey = "sk-test-never-print-0123456789"

func setupFixture(t *testing.T, withKey bool) (*store.Store, *providerkeys.Manager, string) {
	t.Helper()
	secretstest.Install(t)
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeStoreBestEffort(ctx, st) })
	if _, err = st.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "read"); err != nil {
		t.Fatal(err)
	}
	keys := providerkeys.New(providerkeys.Options{Store: &secretstest.FakeStore{}, EnvNames: service.ProviderKeyEnvName,
		LookupEnv: func(string) (string, bool) { return "", false }})
	if withKey {
		if err = keys.SetKey(ctx, "anthropic-001", strings.NewReader(setupTestKey)); err != nil {
			t.Fatal(err)
		}
	}
	def := filepath.Join(t.TempDir(), "agent.md")
	body := "---\nschema_version: \"2\"\ndefinition_id: def:cli-setup\nrevision: \"1\"\nname: cli-setup\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n"
	if err = os.WriteFile(def, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return st, keys, def
}

func TestAgentSetupYieldsLaunchableSessionBodyAndIsIdempotent(t *testing.T) {
	st, keys, def := setupFixture(t, true)
	args := []string{def, "--provider", "anthropic", "--model", "m", "--tools", "dev.read", "--reason", "test"}
	ws := t.TempDir()
	var first, second bytes.Buffer
	if err := runAgentSetup(context.Background(), st, keys, ws, args, &first); err != nil {
		t.Fatal(err)
	}
	if err := runAgentSetup(context.Background(), st, keys, ws, args, &second); err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	_ = json.Unmarshal(first.Bytes(), &a)
	_ = json.Unmarshal(second.Bytes(), &b)
	if a["created"] != true || b["created"] != false || a["authority_id"] != b["authority_id"] {
		t.Fatalf("first=%v second=%v", a, b)
	}
	req, _ := a["session_request"].(map[string]any)
	body, _ := req["body"].(map[string]any)
	if body["definition_ref"] == nil || body["host_settings"] == nil || req["path"] != "/api/agent/v1/sessions" {
		t.Fatalf("session request = %v", req)
	}
	dirs, _ := a["directories"].([]any)
	if len(dirs) != 1 || dirs[0] != filepath.Join(ws, "def-cli-setup") {
		t.Fatalf("directories = %v, want a per-agent workspace under %s", dirs, ws)
	}
	if fi, err := os.Stat(filepath.Join(ws, "def-cli-setup")); err != nil || !fi.IsDir() {
		t.Fatalf("workspace not created: %v", err)
	}
	if strings.Contains(first.String(), setupTestKey) {
		t.Fatal("output leaked the key")
	}
}

func TestAgentSetupRefusesBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		withKey bool
		extra   []string
		want    string
	}{
		{"no key", false, []string{"--provider", "anthropic", "--model", "m", "--tools", "all"}, "provider set-key anthropic"},
		{"unknown provider", true, []string{"--provider", "nowhere", "--model", "m", "--tools", "all"}, "not a native API provider"},
		{"runtime cli", true, []string{"--provider", "anthropic", "--model", "m", "--runtime", "cli", "--tools", "all"}, "runtime cli"},
		{"no model", true, []string{"--provider", "anthropic", "--tools", "all"}, "--provider and --model"},
		{"no tools flag", true, []string{"--provider", "anthropic", "--model", "m"}, "--tools is required"},
		{"unknown tool", true, []string{"--provider", "anthropic", "--model", "m", "--tools", "no_such"}, "authority tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, keys, def := setupFixture(t, tc.withKey)
			err := runAgentSetup(context.Background(), st, keys, t.TempDir(), append([]string{def}, tc.extra...), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			var n int
			_ = st.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM agent_definitions)+(SELECT COUNT(*) FROM agent_host_settings)+(SELECT COUNT(*) FROM native_authorities)`).Scan(&n)
			if n != 0 {
				t.Fatalf("refusal wrote %d rows", n)
			}
		})
	}
}

func rowsAfterSetup(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	_ = st.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM agent_definitions)+(SELECT COUNT(*) FROM agent_host_settings)+(SELECT COUNT(*) FROM native_authorities)`).Scan(&n)
	return n
}

func TestAgentSetupRefusesUnsafeSlugAndUnlaunchableDefinition(t *testing.T) {
	st, keys, def := setupFixture(t, true)
	ws := t.TempDir()
	base := []string{"--provider", "anthropic", "--model", "m", "--tools", "all"}
	err := runAgentSetup(context.Background(), st, keys, ws, append([]string{def, "--slug", "../outside"}, base...), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "invalid host settings slug") {
		t.Fatalf("path-like slug: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(ws)); len(entries) > 0 {
		for _, e := range entries {
			if e.Name() == "outside" {
				t.Fatal("slug escaped the workspace root")
			}
		}
	}
	// A schema-valid definition the native runtime refuses at launch.
	data, _ := os.ReadFile(def)
	durable := filepath.Join(t.TempDir(), "durable.md")
	if err = os.WriteFile(durable, []byte(strings.Replace(string(data), "mode: ephemeral", "mode: durable", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	err = runAgentSetup(context.Background(), st, keys, ws, append([]string{durable}, base...), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot run on native chat") {
		t.Fatalf("durable continuity: %v", err)
	}
	if n := rowsAfterSetup(t, st); n != 0 {
		t.Fatalf("refusals wrote %d rows", n)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "def-cli-setup")); statErr == nil {
		t.Fatal("a refused setup created a workspace")
	}
}

func TestAgentSetupAcceptsEmbeddedArchetypeID(t *testing.T) {
	st, keys, _ := setupFixture(t, true)
	var out bytes.Buffer
	args := []string{"def:nanite/chat", "--provider", "anthropic", "--model", "m", "--tools", "dev_read"}
	if err := runAgentSetup(context.Background(), st, keys, t.TempDir(), args, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"definition_id":"def:nanite/chat"`) {
		t.Fatalf("output = %s", out.String())
	}
	err := runAgentSetup(context.Background(), st, keys, t.TempDir(), []string{"no-such-archetype", "--provider", "anthropic", "--model", "m", "--tools", "all"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "def:nanite/chat") {
		t.Fatalf("unknown archetype should list the ids: %v", err)
	}
}

func TestAgentSetupRerunReportsEveryGrantAndAddsOnly(t *testing.T) {
	st, keys, def := setupFixture(t, true)
	if _, err := st.UpsertKnownTool(context.Background(), "dev_write", "builtin", "available", "write"); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	run := func(tools string) map[string]any {
		var out bytes.Buffer
		if err := runAgentSetup(context.Background(), st, keys, ws, []string{def, "--provider", "anthropic", "--model", "m", "--tools", tools}, &out); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(out.Bytes(), &m)
		return m
	}
	run("dev_read,dev_write")
	second := run("dev_read")
	got, _ := second["tools"].([]any)
	if second["created"] != false || len(got) != 2 {
		t.Fatalf("second = %v, want created=false and the full grant set [dev_read dev_write]", second)
	}
	if added, _ := second["tools_added"].([]any); len(added) != 0 {
		t.Fatalf("tools_added = %v", added)
	}
}
