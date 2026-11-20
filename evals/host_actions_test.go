package evals

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/nanite/agent"
)

func hostScenario(t *testing.T) Scenario {
	t.Helper()
	files := fstest.MapFS{
		"task.md":     {Data: []byte("Run the task.")},
		"prelude.txt": {Data: []byte("Warm up.\n")},
	}
	for _, name := range []string{"agent.md", "host.yaml"} {
		data, err := fs.ReadFile(codeSearch().Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	options := func(opts *agent.Options) {
		registry, err := steer.NewRegistry(steer.Preset{Name: "default"})
		if err != nil {
			t.Fatal(err)
		}
		vocab := steer.NewVocabulary()
		for _, kind := range []string{"run_command", "call_tool"} {
			if err := vocab.Register(kind, nil); err != nil {
				t.Fatal(err)
			}
		}
		opts.Steering = &agent.Steering{Registry: registry, Vocabulary: vocab, Reflexes: func() []steer.Reflex {
			return []steer.Reflex{
				{Name: "save", TriggerKind: "event", Trigger: json.RawMessage(`{"topic":"eval.save"}`), ActionKind: "run_command", Action: json.RawMessage(`{"command":"shared","args":{"file":"note"}}`), Enabled: true},
				{Name: "inspect", TriggerKind: "focus", Trigger: json.RawMessage(`{"view":"notes"}`), ActionKind: "call_tool", Action: json.RawMessage(`{"tool":"shared","input":{"id":7}}`), Enabled: true},
				{Name: "each-message", TriggerKind: "text", Trigger: json.RawMessage(`{"pattern":"."}`), ActionKind: "call_tool", Action: json.RawMessage(`{"tool":"audit"}`), Enabled: true},
			}
		}}
	}
	return Scenario{
		Name: "host-reflexes", Files: files,
		HostStubs:    HostStubs{Commands: map[string]HostResult{"shared": {Text: "saved"}}, Tools: map[string]HostResult{"shared": {Text: "lookup failed", IsError: true}}},
		HostTriggers: []HostTrigger{{Event: "eval.save.child"}, {Focus: "notes"}},
		Arms:         []Arm{{Name: "direct", Mode: Direct, Options: options}, {Name: "subtask", Mode: Subtask, Options: options}},
		Script:       func(Arm) Script { return Script{Root: []Step{Say("Ready."), Say("Done.")}} },
		Grade:        func(answer, _ string) (bool, string) { return answer == "Done.", answer },
	}
}

func TestHostActionsRecordedOffline(t *testing.T) {
	rep, err := Run(t.Context(), Config{Parallel: 2}, []Scenario{hostScenario(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("results: %+v", rep.Results)
	}
	for _, result := range rep.Results {
		if !result.Success {
			t.Fatalf("%s: %s", result.Arm, result.Why)
		}
		actions := result.HostActions
		// One event, one focus, and one task action. The prelude's audit is absent.
		if len(actions) != 3 {
			t.Fatalf("%s actions: %+v", result.Arm, actions)
		}
		if a := actions[0]; a.Source != "event:eval.save.child" || a.Action.Kind != "run_command" || a.Name != "shared" || string(a.Input) != `{"file":"note"}` || !a.Stubbed || a.Result.Text != "saved" || a.Result.IsError || !reflect.DeepEqual(a.Fired, []string{"save"}) {
			t.Fatalf("command: %+v", a)
		}
		if a := actions[1]; a.Source != "focus:notes" || a.Action.Kind != "call_tool" || a.Name != "shared" || string(a.Input) != `{"id":7}` || !a.Stubbed || a.Result.Text != "lookup failed" || !a.Result.IsError || !reflect.DeepEqual(a.Fired, []string{"inspect"}) {
			t.Fatalf("tool: %+v", a)
		}
		if a := actions[2]; a.Source != "turn" || a.Name != "audit" || string(a.Input) != `{}` || a.Stubbed || !a.Result.IsError || !strings.Contains(a.Result.Text, "no host stub") {
			t.Fatalf("record-only: %+v", a)
		}
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var restored Report
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Results[0].HostActions, restored.Results[0].HostActions) {
		t.Fatal("JSON report lost host action evidence")
	}
	// Parallel arms own their evidence, even when they use the same scenario.
	rep.Results[0].HostActions[0].Fired[0] = "changed"
	if rep.Results[1].HostActions[0].Fired[0] != "save" {
		t.Fatal("evidence aliases another run")
	}
}

func TestHostTriggersValidateAndRequireSteering(t *testing.T) {
	for _, trigger := range []HostTrigger{{}, {Event: "x", Focus: "y"}, {Event: " "}} {
		s := hostScenario(t)
		s.HostTriggers = []HostTrigger{trigger}
		rep, err := Run(t.Context(), Config{}, []Scenario{s})
		if err == nil || !strings.Contains(err.Error(), "exactly one") || len(rep.Results) != 0 {
			t.Fatalf("trigger %+v: %+v %v", trigger, rep, err)
		}
	}
	s := hostScenario(t)
	s.Arms = nil
	rep, err := Run(t.Context(), Config{Modes: []Mode{Direct}}, []Scenario{s})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || rep.Results[0].Success || !strings.Contains(rep.Results[0].Why, "steering is not configured") {
		t.Fatalf("missing steering: %+v", rep.Results)
	}
}
