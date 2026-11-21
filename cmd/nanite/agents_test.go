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
