package agent

import (
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/integrity"
)

// IntegrityReport is the outcome of a conversation integrity check: findings
// ordered by the transcript record they concern. Safe reports whether Resume
// may proceed; Unsafe lists the findings that refuse it.
type IntegrityReport = integrity.Report

// IntegrityFinding is one typed inconsistency. Its Code is stable and
// machine-readable; Evidence holds identifiers and states, never content.
type IntegrityFinding = integrity.Finding

// IntegrityCode is a finding's stable kind, such as "torn_tail".
type IntegrityCode = integrity.Code

// IntegrityRange locates a finding's records: indexes into the replayed
// transcript and their committed cursors, or -1 for the index alone.
type IntegrityRange = integrity.Range

// IntegrityRepair names a deterministic fix for an automatically repairable
// finding, such as "resume_or_discard" or "rebuild_index".
type IntegrityRepair = integrity.Repair

// IntegritySeverity orders findings: IntegrityInfo, IntegrityWarning,
// IntegrityRecoverable (an ordinary interruption Resume or Discard finishes)
// and IntegrityUnsafe (Resume is refused).
type IntegritySeverity = integrity.Severity

const (
	IntegrityInfo        = integrity.Info
	IntegrityWarning     = integrity.Warning
	IntegrityRecoverable = integrity.Recoverable
	IntegrityUnsafe      = integrity.Unsafe
)

// IntegrityError is Resume's refusal through an unsafe finding. It carries
// the conversation and the full report.
type IntegrityError = engine.IntegrityError

// ErrIntegrity is matched by every IntegrityError.
var ErrIntegrity = engine.ErrIntegrity
