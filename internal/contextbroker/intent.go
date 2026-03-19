package contextbroker

// Known intent types for the ContextBroker.
// These drive which sources are queried and how results are ranked.
const (
	// IntentResumeTask fetches context for resuming a previously started task.
	// Sources: Engine (task state), Cortex (related records), Session (recent messages).
	IntentResumeTask = "resume_task"

	// IntentBootProject fetches context for starting work on a project.
	// Sources: PCC (project context), Cortex (project records), Engine (active tasks).
	IntentBootProject = "boot_project"

	// IntentReviewSession fetches context for reviewing a past session.
	// Sources: Session (history), Cortex (related decisions).
	IntentReviewSession = "review_session"

	// IntentWriteCode fetches context for writing or modifying code.
	// Sources: PCC (conventions, architecture), Cortex (related implementations).
	IntentWriteCode = "write_code"

	// IntentDebugIssue fetches context for debugging a problem.
	// Sources: Cortex (error context, known pitfalls), PCC (architecture).
	IntentDebugIssue = "debug_issue"

	// IntentPlanFeature fetches context for planning a new feature.
	// Sources: Engine (roadmap, epics), Cortex (ADRs), PCC (architecture).
	IntentPlanFeature = "plan_feature"

	// IntentRecallDecision fetches context for recalling why a decision was made.
	// Sources: Cortex (ADRs, decisions), PCC (decisions file).
	IntentRecallDecision = "recall_decision"

	// IntentCustom is a catch-all for intents that don't fit predefined categories.
	IntentCustom = "custom"
)

// IntentSourcePriority maps intent types to source priority orderings.
// Sources listed first get a larger share of the budget.
var IntentSourcePriority = map[string][]string{
	IntentResumeTask:     {"engine", "cortex", "session", "pcc"},
	IntentBootProject:    {"pcc", "cortex", "engine", "session"},
	IntentReviewSession:  {"session", "cortex", "pcc", "engine"},
	IntentWriteCode:      {"pcc", "cortex", "session", "engine"},
	IntentDebugIssue:     {"cortex", "pcc", "session", "engine"},
	IntentPlanFeature:    {"engine", "cortex", "pcc", "session"},
	IntentRecallDecision: {"cortex", "pcc", "session", "engine"},
	IntentCustom:         {"cortex", "pcc", "engine", "session"},
}

// BudgetForIntent returns a BudgetConfig with source weights tuned
// for the given intent type. Sources listed earlier in the priority
// list get proportionally more budget.
func BudgetForIntent(intentType string, totalTokens int) BudgetConfig {
	priorities, ok := IntentSourcePriority[intentType]
	if !ok {
		priorities = IntentSourcePriority[IntentCustom]
	}

	if totalTokens <= 0 {
		totalTokens = DefaultBudget().MaxTokens
	}

	// Assign decreasing weights: 0.40, 0.30, 0.20, 0.10 for 4 sources.
	weights := []float64{0.40, 0.30, 0.20, 0.10}
	sourceWeights := make(map[string]float64)
	for i, src := range priorities {
		if i < len(weights) {
			sourceWeights[src] = weights[i]
		} else {
			sourceWeights[src] = 0.05
		}
	}

	return BudgetConfig{
		MaxTokens:     totalTokens,
		SourceWeights: sourceWeights,
	}
}
