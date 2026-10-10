package agentpolicy

import (
	"errors"
	"fmt"

	"github.com/hollis-labs/substrate/mesh/agentdef"
)

var ErrUnsupportedExecution = errors.New("definition semantics are not applied by native execution")

// ValidateExecution is shared by native view admission and actor projection.
// Storage can retain an artifact without claiming its requested semantics run.
func ValidateExecution(d *agentdef.Definition) error {
	if d == nil {
		return ErrUnsupportedExecution
	}
	if err := d.Validate(Option()); err != nil {
		return fmt.Errorf("%w: %w", ErrUnsupportedExecution, err)
	}
	if len(d.Behavior.Hooks)+len(d.Capabilities)+len(d.Requirements.Requires)+len(d.Requirements.Uses)+len(d.Requirements.Tools)+len(d.Requirements.Skills)+len(d.Requirements.Resources)+len(d.HarnessProfile.Steering)+len(d.HarnessProfile.Context.Sources)+len(d.HarnessProfile.Approvals)+len(d.HarnessProfile.Escalation) > 0 || d.HarnessProfile.Context.Policy != nil || d.Continuity.Mode != agentdef.Ephemeral || d.Continuity.MemoryPolicy != nil || d.Continuity.RecoveryStrategy != nil {
		return fmt.Errorf("%w: references, hooks, capability requests and continuity policies are not applied", ErrUnsupportedExecution)
	}
	if p := d.HarnessProfile.Permissions.Profile; p != "default" && p != "read-only" {
		return fmt.Errorf("%w: permission profile %q", ErrUnsupportedExecution, p)
	}
	return nil
}
