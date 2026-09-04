package service

import "context"

// ExternalWorkflowStepResult is the product result of one external framework
// invocation. External engines implement this dedicated step seam and do not
// participate in workflow lifecycle selection.
type ExternalWorkflowStepResult struct {
	Output  string
	IsError bool
}

type ExternalWorkflowStepEngine interface {
	ExecuteWorkflowStep(context.Context, string, string, map[string]any, string) (ExternalWorkflowStepResult, error)
}
