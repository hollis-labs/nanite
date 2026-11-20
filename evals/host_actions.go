package evals

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hollis-labs/libs/agent/steer"
)

// HostResult is a canned host-command or host-tool result. It is recorded
// as evidence; steering host actions do not return results to the model.
type HostResult struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error,omitempty"`
}

// HostStubs answers steering run_command and call_tool actions by their
// command or tool name. These are separate from model-callable Stubs.
// Missing stubs are recorded as errors; no real host action is executed.
type HostStubs struct {
	Commands map[string]HostResult `json:"commands,omitempty"`
	Tools    map[string]HostResult `json:"tools,omitempty"`
}

// HostTrigger fires one event or focus change after the prelude and before
// the task, in slice order. Exactly one field must be set. Configure the
// reflex vocabulary and definitions through Arm.Options' Steering.
type HostTrigger struct {
	Event string `json:"event,omitempty"`
	Focus string `json:"focus,omitempty"`
}

// HostAction records a fired host action and its stubbed answer. Source is
// turn, event:<topic>, or focus:<view>. Fired contains the reflex names from
// that steering result (not a one-to-one mapping to actions).
type HostAction struct {
	Source  string          `json:"source"`
	Fired   []string        `json:"fired,omitempty"`
	Action  steer.Action    `json:"action"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Stubbed bool            `json:"stubbed"`
	Result  HostResult      `json:"result"`
}

func checkHostTriggers(triggers []HostTrigger) error {
	for i, trigger := range triggers {
		if (trigger.Event == "") == (trigger.Focus == "") ||
			(trigger.Event != "" && strings.TrimSpace(trigger.Event) == "") ||
			(trigger.Focus != "" && strings.TrimSpace(trigger.Focus) == "") {
			return fmt.Errorf("evals: host trigger %d needs exactly one nonblank event or focus", i)
		}
	}
	return nil
}

func (stubs HostStubs) record(source string, fired []string, actions []steer.Action) []HostAction {
	var out []HostAction
	for _, action := range actions {
		// Keep evidence detached from steering's slices and raw JSON.
		action.Spec = slices.Clone(action.Spec)
		evidence := HostAction{Source: source, Fired: slices.Clone(fired), Action: action}
		var spec struct {
			Command string          `json:"command"`
			Tool    string          `json:"tool"`
			Args    json.RawMessage `json:"args"`
			Input   json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(action.Spec, &spec); err != nil {
			evidence.Result = HostResult{Text: "invalid host action spec", IsError: true}
		} else {
			switch action.Kind {
			case "run_command":
				evidence.Name, evidence.Input = spec.Command, spec.Args
				evidence.Result, evidence.Stubbed = stubs.Commands[evidence.Name]
			case "call_tool":
				evidence.Name, evidence.Input = spec.Tool, spec.Input
				evidence.Result, evidence.Stubbed = stubs.Tools[evidence.Name]
			}
			if !evidence.Stubbed {
				evidence.Result = HostResult{Text: fmt.Sprintf("no host stub for %s %q", action.Kind, evidence.Name), IsError: true}
			}
		}
		if len(evidence.Input) == 0 {
			evidence.Input = json.RawMessage(`{}`)
		}
		out = append(out, evidence)
	}
	return out
}
