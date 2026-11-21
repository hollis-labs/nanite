package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/registryagent"
)

func TestAgentsMaterializeCLI(t *testing.T) {
	registry := t.TempDir()
	dir := filepath.Join(t.TempDir(), "agent")
	ref := registryagent.Reference{Authority: "registry", Kind: "agent", ID: "cli-fixture"}
	profile := registryagent.Reference{Authority: "registry", Kind: "launch_profile", ID: "cli-profile"}
	records := []registryagent.Record{
		{Reference: ref, Revision: "r1", Payload: json.RawMessage(`{"schema":"agent/1","id":"cli-fixture","name":"cli-fixture","charter":"Test CLI materialization.","roles":["assistant"],"launch_profile":{"reference":{"authority":"registry","kind":"launch_profile","id":"cli-profile"},"revision":"r1"}}`)},
		{Reference: profile, Revision: "r1", Payload: json.RawMessage(`{"schema":"launch_profile/1","id":"cli-profile","name":"cli-profile","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"source"},"revision":"s1"},"host":"nanite-cli","runtime":{"provider":"openai","model":"test"}}`)},
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registry, "records.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := agentsCommand()
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"materialize", "agent:cli-fixture", dir, "--registry-dir", registry})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "materialized=true") {
		t.Fatal(output.String())
	}
	// Exercise the same root command users invoke, including its validator.
	run, err := start(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(t.Context()) }()
	root := newRoot(run.App, dir)
	root.SetArgs([]string{"validate", dir})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	args := []string{"agents", "materialize", "agent:cli-fixture", dir}
	if !configIndependent(args) || requiresAgent(args) {
		t.Fatal("materializer requires runtime configuration")
	}
	missing := agentsCommand()
	missing.SetArgs(args[1:])
	if err := missing.ExecuteContext(t.Context()); err == nil {
		t.Fatal("missing registry accepted")
	}
}

func TestRegistryCredentialFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "reader")
	for _, value := range []string{"", strings.Repeat("x", 16385)} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := registryTokenFile(file)(t.Context()); err == nil {
			t.Fatal("invalid credential file accepted")
		}
	}
	if err := os.WriteFile(file, []byte(" test-reader\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := registryTokenFile(file)(t.Context()); err != nil || got != "test-reader" {
		t.Fatal("credential resolution failed", err)
	}
	if _, err := registryTokenFile(t.TempDir())(t.Context()); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := registryTokenFile(file + "-missing")(t.Context()); err == nil || strings.Contains(err.Error(), file) {
		t.Fatal("credential path leaked", err)
	}
}

func TestLiveCLIConfiguration(t *testing.T) {
	t.Setenv("NANITE_REGISTRY_URL", "http://example.invalid")
	t.Setenv("NANITE_REGISTRY_PUBLICATION", "draft")
	t.Setenv("NANITE_REGISTRY_TOKEN_FILE", "/unused-reader")
	t.Setenv("NANITE_CASTER_EXECUTABLE", "/unused-caster")
	cmd := agentsCommand()
	materialize, _, err := cmd.Find([]string{"materialize"})
	if err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]string{"registry-url": "http://example.invalid", "publication": "draft", "registry-token-file": "/unused-reader", "caster-executable": "/unused-caster"} {
		got, err := materialize.Flags().GetString(flag)
		if err != nil || got != want {
			t.Fatalf("%s env binding missing", flag)
		}
	}
	cmd.SetArgs([]string{"materialize", "agent:fixture", filepath.Join(t.TempDir(), "agent"), "--registry-dir", t.TempDir()})
	if err := cmd.ExecuteContext(t.Context()); err == nil {
		t.Fatal("ambiguous transport accepted")
	}
}
