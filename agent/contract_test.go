package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
)

func TestSlotWriterContract(t *testing.T) {
	s := Slots{store: slots.NewStore()}
	entry := SlotEntry{Owner: "engine", Key: "demo", Content: "context"}
	for _, name := range []string{"engine", "file", "Bad", ""} {
		w := s.Owner(name)
		if err := w.Put(SlotContext, entry); err == nil {
			t.Errorf("Put accepted owner %q", name)
		}
		if err := w.Replace(SlotContext, entry); err == nil {
			t.Errorf("Replace accepted owner %q", name)
		}
	}
	w := s.Owner("valid")
	for _, slot := range []SlotName{SlotAgent, SlotPermissions, SlotUniversal, SlotHandoff, SlotConversation} {
		if err := w.Put(slot, entry); err == nil {
			t.Errorf("Put accepted %q", slot)
		}
		if err := w.Replace(slot, entry); err == nil {
			t.Errorf("Replace accepted %q", slot)
		}
	}
	if err := w.Put(SlotContext, entry); err != nil {
		t.Fatal(err)
	}
	if got := s.store.Snapshot().Entries(SlotContext)[0].Owner; got != "valid" {
		t.Fatalf("owner = %q", got)
	}
	names := SlotNames()
	names[0] = SlotContext
	if SlotNames()[0] != SlotUniversal {
		t.Fatal("slot names are mutable")
	}
}

func TestOpenEnforcesDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	definition := `---
schema_version: "2"
definition_id: def:locking-test
revision: "1"
name: locking-test
description: Directory lock fixture.
behavior:
  purpose: Test the directory lock.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Test instructions.
`
	for name, raw := range map[string]string{"agent.md": definition, "host.yaml": "kind: embedded\nlifetime: application\napplication_id: test\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	opts := Options{Dir: dir}
	first, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Open(ctx, opts); err == nil {
		_ = second.Close()
		t.Fatal("second Open succeeded with the lock held")
	} else if !strings.Contains(err.Error(), "lock agent data directory") {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestToolAndSkillGroups(t *testing.T) {
	var tools ToolSet
	good := Tool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{}, nil }}
	if err := tools.SetGroup("one", good); err != nil {
		t.Fatal(err)
	}
	if err := tools.SetGroup("two", good); err == nil {
		t.Fatal("accepted duplicate tool across groups")
	}
	bad := good
	bad.InputSchema = json.RawMessage(`{"type":`)
	if err := tools.SetGroup("one", bad); err == nil {
		t.Fatal("accepted invalid schema JSON")
	}

	var skills SkillSet
	old := Skill{Name: "previous", Description: "original"}
	if err := skills.SetGroup("notes", old); err != nil {
		t.Fatal(err)
	}
	replacement := Skill{Name: "next", Description: "replacement"}
	if err := skills.SetGroup("notes", replacement, replacement); err == nil {
		t.Fatal("accepted duplicate skills")
	}
	if got := skills.groups.Listing(); got != "- previous: original" {
		t.Fatalf("failed update changed group: %q", got)
	}
	if err := skills.SetGroup("notes", replacement); err != nil {
		t.Fatal(err)
	}
	if got := skills.groups.Listing(); got != "- next: replacement" {
		t.Fatalf("replacement listing = %q", got)
	}
}

// offeredTools opens the agent in dir with the given built-in option, runs
// one turn against a recording provider and returns the tool names it saw.
func offeredTools(t *testing.T, dir string, builtins []BuiltinGroup) []string {
	t.Helper()
	p := &recordingProvider{Provider: fake.New(fake.Text("ok"))}
	a, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: p, Builtins: builtins})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range p.last.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

type recordingProvider struct {
	llmcontracts.Provider
	last llmtypes.ChatRequest
}

func (p *recordingProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.last = req
	return p.Provider.StreamChat(ctx, req)
}

func agentDirOfKind(t *testing.T, profile string) string {
	t.Helper()
	dir := t.TempDir()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:kind-test\nrevision: \"1\"\nname: kind-test\ndescription: Kind test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest.\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEmbeddedAgentGetsNoBuiltinsUnlessTheHostOptsIn(t *testing.T) {
	const embedded = "kind: embedded\nlifetime: application\napplication_id: kind-test\n"
	const stable = "kind: stable\nlifetime: on-demand\nseat_id: kind-test\n"
	paging := []string{"fetch_tool_result", "search_tool_result"}

	if got := offeredTools(t, agentDirOfKind(t, embedded), nil); !slices.Equal(got, paging) {
		t.Fatalf("embedded agent with no option was offered %v, want only %v", got, paging)
	}
	got := offeredTools(t, agentDirOfKind(t, embedded), []BuiltinGroup{BuiltinFilesRead})
	if !slices.Contains(got, "read_file") || slices.Contains(got, "bash") || slices.Contains(got, "write_file") {
		t.Fatalf("embedded agent with the read group was offered %v", got)
	}
	if got := offeredTools(t, agentDirOfKind(t, embedded), AllBuiltins()); !slices.Contains(got, "bash") || !slices.Contains(got, "write_file") {
		t.Fatalf("embedded agent with every group was offered %v", got)
	}
	if got := offeredTools(t, agentDirOfKind(t, stable), nil); !slices.Contains(got, "bash") || !slices.Contains(got, "write_file") {
		t.Fatalf("stable agent with no option was offered %v", got)
	}
}

func TestSubtaskSettingsPrecedence(t *testing.T) {
	file := agentdir.Runtime{SubtaskLimit: 6, TurnTokenBudget: 9000, SubtaskResultCap: 3000}
	for _, tc := range []struct {
		name string
		opts Options
		file agentdir.Runtime
		want subtaskConfig
	}{
		{"defaults", Options{}, agentdir.Runtime{}, subtaskConfig{}},
		{"host.yaml", Options{}, file, subtaskConfig{limit: 6, turnBudget: 9000, resultCap: 3000}},
		{"options win", Options{SubtaskLimit: 2, TurnTokenBudget: 100, SubtaskResultCap: 500}, file, subtaskConfig{limit: 2, turnBudget: 100, resultCap: 500}},
		{"per field", Options{TurnTokenBudget: 100}, file, subtaskConfig{limit: 6, turnBudget: 100, resultCap: 3000}},
	} {
		got, err := subtaskSettings(tc.opts, tc.file)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v; want %+v", tc.name, got, err, tc.want)
		}
	}
}

func TestOpenRefusesAHostYAMLSubtaskLimitOverEight(t *testing.T) {
	dir := t.TempDir()
	definition := `---
schema_version: "2"
definition_id: def:limit-test
revision: "1"
name: limit-test
description: Subtask limit fixture.
behavior:
  purpose: Test the subtask limit.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: ephemeral
---
Test instructions.
`
	for name, raw := range map[string]string{"agent.md": definition, "host.yaml": "kind: embedded\nlifetime: application\napplication_id: test\nruntime:\n  subtask_limit: 9\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a, err := Open(context.Background(), Options{Dir: dir})
	if err == nil {
		_ = a.Close()
		t.Fatal("a host.yaml subtask_limit of 9 was accepted")
	}
	if !strings.Contains(err.Error(), "subtask_limit 9") || !strings.Contains(err.Error(), "host.yaml") {
		t.Fatalf("unclear refusal: %v", err)
	}
}
