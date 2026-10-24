package agentpolicy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
)

var ErrUnsupportedExecution = errors.New("definition semantics are not applied by native execution")

// HostCapabilities is the vocabulary a definition's requirements.requires and
// requirements.uses are checked against. Nanite publishes no capability names
// yet, so the default vocabulary is empty: a definition that requires one is
// refused, naming it, and one that merely uses one is told it is unmet.
func HostCapabilities(string) bool { return false }

// Disposition is what a launch does with one definition field.
type Disposition string

const (
	// Applied: the field changes what runs.
	Applied Disposition = "applied"
	// Recorded: the field is stored, digest-covered and shown, and changes
	// nothing at run time. Ignoring it cannot weaken a safety property.
	Recorded Disposition = "recorded"
	// Unmet: the field was understood but this host cannot honor it; a launch
	// proceeds without it and says so. Used only where that weakens nothing.
	Unmet Disposition = "unmet"
	// Refused: the launch fails and names the field.
	Refused Disposition = "refused"
)

// FieldReport is one declared field and what a launch does with it.
type FieldReport struct {
	Field       string      `json:"field"`
	Disposition Disposition `json:"disposition"`
	Detail      string      `json:"detail,omitempty"`
}

// ExecutionReport lists every field d declares and its disposition, in a stable
// order. A report with a Refused entry is exactly when ValidateExecution fails.
// Fields d does not declare are omitted.
func ExecutionReport(d *agentdef.Definition) []FieldReport { return ExecutionReportWith(d, nil) }

// ExecutionReportWith is ExecutionReport for a launch that holds skill grants:
// delivered lists the requested skills actually granted at their pinned digest
// and planted for the session. Those are Applied; any other requested skill
// stays Unmet.
func ExecutionReportWith(d *agentdef.Definition, delivered map[string]bool) []FieldReport {
	var out []FieldReport
	add := func(field string, disp Disposition, format string, args ...any) {
		out = append(out, FieldReport{Field: field, Disposition: disp, Detail: fmt.Sprintf(format, args...)})
	}
	if d == nil {
		return []FieldReport{{Field: "definition", Disposition: Refused, Detail: "empty definition"}}
	}
	if n := len(d.Behavior.Hooks); n > 0 {
		// Nanite ships no hook registry (DefaultBootDirHooks is empty), so every
		// name is unknown, and the author asked for a gate that would not run.
		add("behavior.hooks", Refused, "hook %s is not provided by this host", strings.Join(quoted(d.Behavior.Hooks), ", "))
	}
	if n := len(d.Capabilities); n > 0 {
		add("capabilities", Recorded, "%d advertised service(s) stored for discovery", n)
	}
	var unmetRequires []string
	for _, r := range d.Requirements.Requires {
		if !HostCapabilities(r) {
			unmetRequires = append(unmetRequires, r)
		}
	}
	if len(unmetRequires) > 0 {
		add("requirements.requires", Refused, "required capability %s is not provided by this host", strings.Join(quoted(unmetRequires), ", "))
	} else if len(d.Requirements.Requires) > 0 {
		add("requirements.requires", Applied, "every required capability is provided")
	}
	if len(d.Requirements.Uses) > 0 {
		var unmet []string
		for _, u := range d.Requirements.Uses {
			if !HostCapabilities(u) {
				unmet = append(unmet, u)
			}
		}
		if len(unmet) > 0 {
			add("requirements.uses", Unmet, "capability %s is not provided by this host", strings.Join(quoted(unmet), ", "))
		} else {
			add("requirements.uses", Recorded, "every used capability is provided")
		}
	}
	if n := len(d.Requirements.Tools); n > 0 {
		add("requirements.tools", Recorded, "%d tool request(s) stay pending until an operator grants them; a request is never a grant", n)
	}
	if n := len(d.Requirements.Skills); n > 0 {
		var got, missing []string
		for _, sk := range d.Requirements.Skills {
			if delivered[sk.Name] {
				got = append(got, sk.Name)
			} else {
				missing = append(missing, sk.Name)
			}
		}
		switch {
		case len(missing) == 0:
			add("requirements.skills", Applied, "%d skill(s) are granted at their pinned digest and planted for the session", len(got))
		case len(got) == 0:
			add("requirements.skills", Unmet, "%d skill(s) are stored and digest-pinned but not delivered: no skill grant covers them for this host revision", len(missing))
		default:
			add("requirements.skills", Unmet, "%d skill(s) are planted; %d are not delivered because no skill grant covers them: %s", len(got), len(missing), strings.Join(quoted(missing), ", "))
		}
	}
	if n := len(d.Requirements.Resources); n > 0 {
		add("requirements.resources", Applied, "%d file(s) are planted for the session", n)
	}
	if n := len(d.HarnessProfile.Steering); n > 0 {
		add("harness_profile.steering", Applied, "%d document(s) are appended after the instructions, in order", n)
	}
	if n := len(d.HarnessProfile.Context.Sources); n > 0 {
		add("harness_profile.context.sources", Applied, "%d file(s) are planted as reference files and listed in the instructions", n)
	}
	if d.HarnessProfile.Context.Policy != nil {
		add("harness_profile.context.policy", Recorded, "an opaque pinned document, stored and digest-covered")
	}
	if p := d.HarnessProfile.Permissions.Profile; p != "default" && p != "read-only" {
		add("harness_profile.permissions.profile", Refused, "permission profile %q is not supported", p)
	}
	if len(d.HarnessProfile.Approvals) > 0 {
		add("harness_profile.approvals", Refused, "ignoring an approval policy would silently weaken oversight")
	}
	if len(d.HarnessProfile.Escalation) > 0 {
		add("harness_profile.escalation", Refused, "ignoring an escalation policy would silently weaken oversight")
	}
	if d.Continuity.Mode != agentdef.Ephemeral {
		add("continuity.mode", Refused, "continuity %q needs durable-agent enrollment, which a native launch does not perform", d.Continuity.Mode)
	} else {
		if d.Continuity.MemoryPolicy != nil {
			add("continuity.memory_policy", Recorded, "stored and digest-covered; an ephemeral launch keeps no memory")
		}
		if d.Continuity.RecoveryStrategy != nil {
			add("continuity.recovery_strategy", Recorded, "stored and digest-covered; an ephemeral launch is not recovered")
		}
	}
	if d.Continuity.Mode != agentdef.Ephemeral {
		if d.Continuity.MemoryPolicy != nil {
			add("continuity.memory_policy", Refused, "only recorded on an ephemeral definition")
		}
		if d.Continuity.RecoveryStrategy != nil {
			add("continuity.recovery_strategy", Refused, "only recorded on an ephemeral definition")
		}
	}
	return out
}

func quoted(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// ValidateExecution is shared by native view admission and actor projection.
// Storage can retain an artifact without claiming its requested semantics run;
// this is where a launch is refused for a field it cannot honor (see
// ExecutionReport for the disposition of every field).
func ValidateExecution(d *agentdef.Definition) error {
	if d == nil {
		return ErrUnsupportedExecution
	}
	if err := d.Validate(Option()); err != nil {
		return fmt.Errorf("%w: %w", ErrUnsupportedExecution, err)
	}
	var refused []string
	for _, f := range ExecutionReport(d) {
		if f.Disposition == Refused {
			refused = append(refused, f.Field+": "+f.Detail)
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("%w: %s", ErrUnsupportedExecution, strings.Join(refused, "; "))
	}
	return nil
}
