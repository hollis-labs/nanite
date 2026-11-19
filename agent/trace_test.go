package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestTraceFilesAndSink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for name, raw := range map[string]string{
		"agent.md":  "---\nschema_version: \"2\"\ndefinition_id: def:trace\nrevision: \"1\"\nname: trace\ndescription: Trace fixture.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: example\ncontinuity:\n  mode: durable\n---\nAnswer.\n",
		"host.yaml": "kind: stable\nlifetime: on-demand\nseat_id: trace\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data := t.TempDir()
	var records []agent.TraceRecord
	a, err := agent.Open(ctx, agent.Options{Dir: root, DataDir: data, GlobalSkillsDir: "-", Provider: fake.New(fake.Text("hello")), TraceFiles: true,
		TraceSink: func(_ context.Context, rec agent.TraceRecord) { records = append(records, rec) }})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.NewConversation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(ctx, agent.Input{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(data, "traces", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("trace files %v", files)
	}
	raw, err := os.ReadFile(files[0]) // #nosec G304 -- A file under the test's temporary directory.
	if err != nil || strings.Count(string(raw), "\n") != 1 || !strings.Contains(string(raw), `"run_id":"`+run.ID()+`"`) {
		t.Fatalf("trace file %q, %v", raw, err)
	}
	if len(records) != 1 || records[0].RunID != run.ID() {
		t.Fatalf("sink records %+v", records)
	}
}
