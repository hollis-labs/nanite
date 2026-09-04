package contextbroker

// Known intent types for the ContextBroker.
// These drive which sources are queried and how results are ranked.
const (
	// IntentResumeTask fetches context for resuming a previously started task.
	// Sources: Tesseract (related records), Session (recent messages), PCC (project context).
	IntentResumeTask = "resume_task"

	// IntentBootProject fetches context for starting work on a project.
	// Sources: PCC (project context), Tesseract (project records), Session (recent messages).
	IntentBootProject = "boot_project"

	// IntentReviewSession fetches context for reviewing a past session.
	// Sources: Session (history), Tesseract (related decisions).
	IntentReviewSession = "review_session"

	// IntentWriteCode fetches context for writing or modifying code.
	// Sources: PCC (conventions, architecture), Tesseract (related implementations).
	IntentWriteCode = "write_code"

	// IntentDebugIssue fetches context for debugging a problem.
	// Sources: Tesseract (error context, known pitfalls), PCC (architecture).
	IntentDebugIssue = "debug_issue"

	// IntentPlanFeature fetches context for planning a new feature.
	// Sources: Tesseract (ADRs), PCC (architecture), Session (recent messages).
	IntentPlanFeature = "plan_feature"

	// IntentRecallDecision fetches context for recalling why a decision was made.
	// Sources: Tesseract (ADRs, decisions), PCC (decisions file).
	IntentRecallDecision = "recall_decision"

	// IntentCustom is a catch-all for intents that don't fit predefined categories.
	IntentCustom = "custom"
)
