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
