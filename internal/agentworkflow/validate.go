package agentworkflow

import "fmt"

// Validate checks a WorkflowDefinition is well-formed before it's handed to
// a WorkflowEngine: known step kinds, a well-formed verify modifier where
// present, and — via Levels — no duplicate ids, no unknown DependsOn
// references, and no cycles (design doc: "DAG only, no cycles"; matches
// Hadron's and Torque's pattern of never accepting a cyclic dependency
// graph). Callers should run this at load/registration time, not at
// execution time.
func Validate(wf WorkflowDefinition) error {
	if wf.Name == "" {
		return fmt.Errorf("agentworkflow: workflow definition requires a name")
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

	if _, err := Levels(wf.Steps); err != nil {
		return fmt.Errorf("agentworkflow: workflow %q: %w", wf.Name, err)
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
