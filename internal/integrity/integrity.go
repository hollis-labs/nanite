// Package integrity checks one conversation's transcript for semantic
// consistency before anything resumes or repairs it.
//
// The transcript package already verifies the byte layer: the hash chain,
// contiguous segments and complete lines. This package checks what those
// records say: that turns start, end and resume in a legal order, that every
// tool call has exactly one result in its own turn, that a compaction never
// splits a tool step, and that the SQLite index agrees with the transcript.
//
// Validate is pure. It reads only its Input: no files, network, model calls
// or wall clock. The same Input always yields the same ordered findings.
//
// # Severity
//
// A finding's Severity says what a caller may do next:
//
//   - Info: worth recording; nothing to do.
//   - Warning: inconsistent bookkeeping that resuming does not depend on.
//   - Recoverable: an ordinary interruption. The turn stopped before it
//     ended; Resume or Discard finishes it, filling any missing tool results
//     with SyntheticResult. This is not corruption.
//   - Unsafe: the transcript, or the index built from it, contradicts
//     itself. Resuming would send a model history that is wrong or that a
//     provider rejects. A caller must not resume through it.
//
// AutoRepair marks a finding whose fix is deterministic and loses nothing
// that was committed, and Repair names that fix. Only Recoverable findings
// and an index that is merely behind the transcript are auto-repairable;
// every other Unsafe finding needs an operator decision.
//
// # Unknown record kinds
//
// A transcript is shared with newer and older Nanite versions and with
// Claude-shape readers. The rule for records this version does not know:
//
//   - A system or summary record with an unknown subtype is skipped and
//     reported as Info (CodeUnknownSideband). Sideband records annotate the
//     conversation; the index projection ignores unknown subtypes too, so
//     skipping one cannot change the history a model is sent.
//   - A record whose type is not user, assistant, system or summary is Unsafe
//     (CodeUnknownRecordType). No Nanite writer emits one, and a message type
//     this version cannot read may carry history the model must see.
//   - Unknown content block types inside a message (images, thinking,
//     future kinds) are accepted as they are; only tool_use and tool_result
//     blocks take part in pairing.
package integrity

import (
	"fmt"

	"github.com/hollis-labs/nanite/internal/transcript"
)

// ClosingPromptPrefix starts the Name of the text block in a closing-reply
// host note. The engine persists the note with the user role; it is not the
// person's message and never starts a turn.
const ClosingPromptPrefix = "nanite_closing_reply:"

// SyntheticResult is the content of the tool result recovery writes for a
// call an interrupted turn never answered.
const SyntheticResult = "interrupted before completion; not retried"

// Severity orders findings from harmless to blocking.
type Severity int

const (
	Info Severity = iota
	Warning
	Recoverable
	Unsafe
)

func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Warning:
		return "warning"
	case Recoverable:
		return "recoverable"
	case Unsafe:
		return "unsafe"
	}
	return fmt.Sprintf("severity(%d)", int(s))
}

// MarshalText makes a Severity read as its name in JSON and logs.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Code is a finding's stable, machine-readable kind. Codes are never reused
// or renamed; a new kind gets a new code.
type Code string

const (
	// Byte layer, from transcript.Replay.
	CodeTornTail      Code = "torn_tail"      // the last record is incomplete or unreadable
	CodeHistoryGap    Code = "history_gap"    // a segment is missing or committed history changed
	CodeInvalidRecord Code = "invalid_record" // a record cannot be read as the shape its type requires

	// Record kinds.
	CodeUnknownRecordType Code = "unknown_record_type" // a record type no Nanite writer emits
	CodeUnknownSideband   Code = "unknown_sideband"    // a sideband subtype this version does not know
	CodeSessionMismatch   Code = "session_mismatch"    // a record names another conversation
	CodeDuplicateRecordID Code = "duplicate_record_id" // two records share one UUID where it must be unique
	CodeConversationMeta  Code = "conversation_record" // the conversation record is missing, repeated or names another ID

	// Turn lifecycle.
	CodeUnknownTurn              Code = "unknown_turn"               // a record names a turn that never started
	CodeIllegalTurnTransition    Code = "illegal_turn_transition"    // start, interrupt or resume out of order
	CodeOverlappingTurns         Code = "overlapping_turns"          // a turn started while another was unfinished
	CodeDuplicateTurnOutcome     Code = "duplicate_turn_outcome"     // a turn finished twice with the same status
	CodeConflictingTurnOutcome   Code = "conflicting_turn_outcome"   // a turn finished twice with different statuses
	CodeInterruptedTurn          Code = "interrupted_turn"           // a turn stopped before it ended
	CodeTurnMissingUserMessage   Code = "turn_missing_user_message"  // a turn does not open with its own user message
	CodeMessageAfterOutcome      Code = "message_after_outcome"      // a message was written after its turn finished
	CodeMessageWithoutTurn       Code = "message_without_turn"       // a message names no turn
	CodeIllegalRoleOrdering      Code = "illegal_role_ordering"      // roles out of the order a turn writes them
	CodeToolUseWithoutResult     Code = "tool_use_without_result"    // a tool call has no result
	CodeOrphanToolResult         Code = "orphan_tool_result"         // a tool result answers no open call of its turn
	CodeDuplicateToolResult      Code = "duplicate_tool_result"      // a tool call has more than one result
	CodeCompactionUnknownMessage Code = "compaction_unknown_message" // a compaction covers a message the transcript does not have before it
	CodeCompactionSplitsToolStep Code = "compaction_splits_tool_step"

	// Index projection (nanite.db) against the transcript.
	CodeIndexBehind        Code = "index_behind"        // the index stops at an earlier committed record
	CodeIndexDisagreement  Code = "index_disagreement"  // the index says something the transcript does not
	CodeNoticeUnknownTurn  Code = "notice_unknown_turn" // a notice was marked delivered to a turn the transcript does not have
	CodeStaleInterruptHold Code = "stale_interrupt_hold"
)

// Repair names a deterministic fix for an AutoRepair finding.
type Repair string

const (
	// RepairResume: Resume or Discard the interrupted turn. Either fills
	// unanswered calls with SyntheticResult before it continues or closes.
	RepairResume Repair = "resume_or_discard"
	// RepairReplayIndex: replay the transcript suffix into the index, as
	// opening a journal does.
	RepairReplayIndex Repair = "replay_index"
	// RepairRebuildIndex: rebuild the index from the transcripts
	// (`nanite rebuild-index`).
	RepairRebuildIndex Repair = "rebuild_index"
	// RepairClearHold: clear the stale interrupted-turn hold.
	RepairClearHold Repair = "clear_hold"
)

// Range locates the records a finding is about. First and Last are indexes
// into Input.Entries; FirstCursor and LastCursor are those records'
// committed cursors. A finding about the index alone, or about the byte layer
// before any record, has First and Last of -1 and zero cursors.
type Range struct {
	First       int               `json:"first"`
	Last        int               `json:"last"`
	FirstCursor transcript.Cursor `json:"first_cursor"`
	LastCursor  transcript.Cursor `json:"last_cursor"`
}

// Finding is one typed inconsistency. Evidence holds identifiers and states
// only (record, turn, message and tool-call IDs, statuses, roles); it never
// carries message text or tool content.
type Finding struct {
	Code       Code              `json:"code"`
	Severity   Severity          `json:"severity"`
	Records    Range             `json:"records"`
	Evidence   map[string]string `json:"evidence,omitempty"`
	AutoRepair bool              `json:"auto_repair"`
	Repair     Repair            `json:"repair,omitempty"`
	Detail     string            `json:"detail"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s (%s) records %d..%d: %s", f.Code, f.Severity, f.Records.First, f.Records.Last, f.Detail)
}

// Report is the outcome of one validation.
type Report struct {
	ConversationID string    `json:"conversation_id"`
	Records        int       `json:"records"`
	Findings       []Finding `json:"findings"`
	// Truncated is set when more than MaxFindings were found; the report
	// keeps the first MaxFindings in order.
	Truncated bool `json:"truncated,omitempty"`
}

// MaxFindings bounds a report, so a badly damaged transcript cannot produce
// an unbounded one.
const MaxFindings = 256

// Worst is the highest severity in the report, or Info when it is clean.
func (r Report) Worst() Severity {
	worst := Info
	for _, f := range r.Findings {
		worst = max(worst, f.Severity)
	}
	return worst
}

// Safe reports whether nothing in the report blocks resuming.
func (r Report) Safe() bool { return r.Worst() < Unsafe }

// Unsafe returns the findings that block resuming, in order.
func (r Report) Unsafe() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == Unsafe {
			out = append(out, f)
		}
	}
	return out
}
