package agentworkflow

import "fmt"

// Validate checks only Nanite's product DTO contract: required display
// identity, supported product StepKinds, and verifier configuration. Graph
// semantics deliberately do not live here. The shared go-workflow compiler is
// the sole authority for normalized node identity, dependencies, cycles, and
// executable-plan validation.
func Validate(wf WorkflowDefinition) error {
	if wf.Name == "" {
		return fmt.Errorf("agentworkflow: workflow definition requires a name")
	}
	if !IsSupportedEngine(wf.Engine) {
		return fmt.Errorf("agentworkflow: workflow %q has unsupported engine %q", wf.Name, wf.Engine)
	}
	if len(wf.Steps) == 0 {
		return fmt.Errorf("agentworkflow: workflow %q has no steps", wf.Name)
	}

	for _, s := range wf.Steps {
		if s.ID == "" {
			return fmt.Errorf("agentworkflow: workflow %q has a step with an empty id", wf.Name)
		}
		switch s.Kind {
		case StepKindLLM, StepKindTool, StepKindGate, StepKindFlex, StepKindLoop:
		default:
			return fmt.Errorf("agentworkflow: step %q has unknown kind %q", s.ID, s.Kind)
		}
		if s.Verify != nil {
			if err := validateVerifySpec(s.ID, *s.Verify); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateVerifySpec(stepID string, spec VerifySpec) error {
	switch spec.Mode {
	case VerifyModeEngine:
		if spec.EngineCheck == "" {
			return fmt.Errorf("agentworkflow: step %q verify(mode=engine) requires engine_check", stepID)
		}
	case VerifyModeAgent:
		if spec.ReviewerProvider == "" {
			return fmt.Errorf("agentworkflow: step %q verify(mode=agent) requires reviewer_provider", stepID)
		}
	default:
		return fmt.Errorf("agentworkflow: step %q has unknown verify mode %q", stepID, spec.Mode)
	}
	return nil
}
