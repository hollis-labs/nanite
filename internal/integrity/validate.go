package integrity

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/hollis-labs/nanite/internal/transcript"
)

// turn is one turn's state as the walk reaches each record.
type turn struct {
	id            string
	start         int // record index of the start record
	status        string
	userMessageID string
	outcome       string // first terminal status recorded, "" while unfinished
	messages      int
	// Tool pairing is per model step: a step's calls are answered by the
	// tool results that follow it, before the next step. Providers may
	// reuse an ID in a later step or turn (some number calls per response),
	// so an ID is only ever matched against the current step.
	pending    []int           // calls of the current step awaiting a result, in call order (indexes into walker.calls)
	stepCalls  map[string]int  // ID -> call of the current step
	answered   map[string]bool // IDs of the current step already answered
	superseded map[string]bool // IDs earlier steps left unanswered
	// answeredBefore holds IDs answered in an earlier step of this turn.
	// recovery.ResumePlan pairs by ID across the whole turn, so it cannot
	// answer a reused ID left pending at an interruption.
	answeredBefore map[string]bool
}

func (t *turn) unfinished() bool { return t.outcome == "" }

// call is one tool_use block.
type call struct {
	id      string
	turn    string
	record  int
	message int // position of its message among messages
	result  int // position of the message holding its result, -1 if none
}

type walker struct {
	in       Input
	findings []Finding
	highest  Severity
	dropped  int
	lastKept bool

	turns     map[string]*turn
	turnOrder []string
	open      []string       // unfinished turns, in start order
	messageAt map[string]int // message ID -> record index
	msgOrder  map[string]int // message ID -> position among messages
	messages  int
	calls     []call
	callTurns map[string]string // tool_use ID -> the turn that last called it (evidence only)

	conversationRecords int
	noConversation      bool // reported a record before the conversation record
	compactions         []compactionRef
}

type compactionRef struct {
	record  int
	id      string
	through string
}

// Validate checks one conversation. It is pure and deterministic: the same
// Input always yields the same Report, findings ordered by the first record
// they concern and then in detection order. It expects a quiescent
// transcript; a writer appending while the entries were read can make the
// tail look torn.
func Validate(in Input) Report {
	w := &walker{in: in, turns: map[string]*turn{}, messageAt: map[string]int{}, msgOrder: map[string]int{}, callTurns: map[string]string{}}
	// Byte-layer findings first, so the report bound never drops them.
	w.byteLayer()
	for i, entry := range in.Entries {
		w.record(i, entry)
	}
	w.finish()
	sound := w.highest < Unsafe
	if !sound {
		// Resume and Discard open the journal and replay it; on a transcript
		// with an unsafe finding of its own they fail or write onto it.
		w.unmark(RepairResume)
	}
	w.compare(sound)
	if replayed(in.ReplayErr) {
		w.notices()
		w.hold()
	}

	// Byte-layer and index-only findings (First == -1) sort after the
	// records; a torn tail is after every committed record by definition.
	end := len(in.Entries)
	position := func(f Finding) int {
		if f.Records.First < 0 {
			return end
		}
		return f.Records.First
	}
	sort.SliceStable(w.findings, func(i, j int) bool { return position(w.findings[i]) < position(w.findings[j]) })
	return Report{ConversationID: in.ConversationID, Records: len(in.Entries), Findings: w.findings, Highest: w.highest, Truncated: w.dropped > 0}
}

// replayed reports whether Entries hold the whole committed history: replay
// succeeded or stopped only at a torn tail.
func replayed(err error) bool { return err == nil || errors.Is(err, transcript.ErrTorn) }

func (w *walker) at(i int) Range {
	if i < 0 || i >= len(w.in.Entries) {
		return Range{First: -1, Last: -1}
	}
	c := w.in.Entries[i].Cursor
	return Range{First: i, Last: i, FirstCursor: c, LastCursor: c}
}

func (w *walker) span(first, last int) Range {
	r := w.at(first)
	if last >= 0 && last < len(w.in.Entries) && last >= first {
		r.Last, r.LastCursor = last, w.in.Entries[last].Cursor
	}
	return r
}

// add records a finding. Past MaxFindings it only counts it, so a badly
// damaged transcript cannot grow the report without bound; Highest still
// includes every finding.
func (w *walker) add(code Code, severity Severity, records Range, detail string, evidence ...string) {
	w.highest = max(w.highest, severity)
	if len(w.findings) >= MaxFindings {
		w.dropped++
		w.lastKept = false
		return
	}
	f := Finding{Code: code, Severity: severity, Records: records, Detail: detail}
	if len(evidence) > 0 {
		f.Evidence = make(map[string]string, len(evidence)/2)
		for i := 0; i+1 < len(evidence); i += 2 {
			f.Evidence[evidence[i]] = evidence[i+1]
		}
	}
	w.findings = append(w.findings, f)
	w.lastKept = true
}

// repairable marks the finding just added as automatically repairable.
func (w *walker) repairable(repair Repair) {
	if !w.lastKept {
		return
	}
	f := &w.findings[len(w.findings)-1]
	f.AutoRepair, f.Repair = true, repair
}

// unmark withdraws a repair from every finding that offered it.
func (w *walker) unmark(repair Repair) {
	for i := range w.findings {
		if w.findings[i].Repair == repair {
			w.findings[i].AutoRepair, w.findings[i].Repair = false, ""
		}
	}
}

// record follows projectEntry: a known sideband subtype is applied whatever
// the record's type, then a user or assistant record is a message.
func (w *walker) record(i int, entry transcript.Entry) {
	var r record
	if err := json.Unmarshal(entry.Record, &r); err != nil {
		w.add(CodeInvalidRecord, Unsafe, w.at(i), "the record cannot be read as the store reads it (shape or timestamp)")
		return
	}
	if w.in.ConversationID != "" && r.SessionID != w.in.ConversationID {
		w.add(CodeSessionMismatch, Unsafe, w.at(i), "the record belongs to another conversation", "record", r.UUID, "session", r.SessionID)
		return
	}
	known := w.sideband(i, r)
	switch r.Type {
	case "user", "assistant":
		w.message(i, r)
	case "system", "summary":
		if !known {
			w.add(CodeUnknownSideband, Info, w.at(i), "a sideband subtype this version does not know; skipped", "record", r.UUID, "subtype", r.Subtype)
		}
	default:
		w.add(CodeUnknownRecordType, Unsafe, w.at(i), "no Nanite writer emits this record type; it may carry history this version cannot read",
			"record", r.UUID, "type", r.Type)
	}
}

// needsConversation reports, once, a record the index cannot hold because
// no conversation record precedes it: its row references the conversation.
// The walk goes on so the other checks still run.
func (w *walker) needsConversation(i int, what string) bool {
	if w.conversationRecords == 0 && !w.noConversation {
		w.noConversation = true
		w.add(CodeConversationMeta, Unsafe, w.at(i), "a "+what+" comes before any conversation record; the index cannot hold it")
	}
	return true
}

// sideband applies a known subtype and reports whether it knew it.
func (w *walker) sideband(i int, r record) bool {
	switch r.Subtype {
	case subtypeConversation:
		w.conversationRecords++
		p, err := decodePayload[conversationPayload](r.Payload)
		switch {
		case err != nil:
			w.add(CodeInvalidRecord, Unsafe, w.at(i), "the conversation record has no readable payload", "record", r.UUID)
		case w.in.ConversationID != "" && p.ID != w.in.ConversationID:
			w.add(CodeConversationMeta, Unsafe, w.at(i), "the conversation record names another conversation", "conversation", p.ID)
		case w.conversationRecords > 1:
			w.add(CodeConversationMeta, Warning, w.at(i), "the conversation record is repeated; the first one stands", "conversation", p.ID)
		}
	case subtypeTurnStart, subtypeTurnFinish, subtypeTurnInt, subtypeTurnResume:
		p, err := decodePayload[turnPayload](r.Payload)
		if err != nil || p.ID == "" {
			w.add(CodeInvalidRecord, Unsafe, w.at(i), "the turn record has no readable turn", "record", r.UUID, "subtype", r.Subtype)
			return true
		}
		w.turnRecord(i, r.Subtype, p)
	case subtypeCompaction:
		p, err := decodePayload[compactionPayload](r.Payload)
		if err != nil {
			w.add(CodeInvalidRecord, Unsafe, w.at(i), "the compaction record has no readable payload", "record", r.UUID)
			return true
		}
		// Compaction telemetry without a summary (older development
		// transcripts) covers nothing and is not projected.
		if p.ThroughMessageID == "" || p.Summary == "" {
			return true
		}
		if !w.needsConversation(i, "compaction") {
			return true
		}
		if _, known := w.turns[p.TurnID]; !known {
			// compactions.turn_id references turns: the projection refuses it.
			w.add(CodeUnknownTurn, Unsafe, w.at(i), "the compaction names a turn that never started; the index cannot hold it",
				"compaction", r.UUID, "turn", p.TurnID)
			return true
		}
		if _, seen := w.messageAt[p.ThroughMessageID]; !seen {
			w.add(CodeCompactionUnknownMessage, Unsafe, w.at(i), "the compaction covers through a message the transcript does not have before it; history after the summary cannot be located",
				"compaction", r.UUID, "through", p.ThroughMessageID)
			return true
		}
		w.compactions = append(w.compactions, compactionRef{record: i, id: r.UUID, through: p.ThroughMessageID})
	case subtypePermission:
	default:
		return false
	}
	return true
}

func (w *walker) turnRecord(i int, subtype string, p turnPayload) {
	t := w.turns[p.ID]
	switch subtype {
	case subtypeTurnStart:
		if !w.needsConversation(i, "turn") {
			return
		}
		if t != nil {
			w.add(CodeIllegalTurnTransition, Unsafe, w.span(t.start, i), "the turn started twice", "turn", p.ID)
			return
		}
		for _, id := range w.open {
			other := w.turns[id]
			w.add(CodeOverlappingTurns, Unsafe, w.span(other.start, i), "a turn started while an earlier turn had not ended",
				"turn", p.ID, "unfinished_turn", other.id, "unfinished_status", other.status)
		}
		w.turns[p.ID] = &turn{id: p.ID, start: i, status: statusRunning, userMessageID: p.UserMessageID,
			stepCalls: map[string]int{}, answered: map[string]bool{}, superseded: map[string]bool{}, answeredBefore: map[string]bool{}}
		w.turnOrder = append(w.turnOrder, p.ID)
		w.open = append(w.open, p.ID)
		return
	case subtypeTurnInt, subtypeTurnResume, subtypeTurnFinish:
		if t == nil {
			// The projection updates no row, so nothing downstream reads it.
			w.add(CodeUnknownTurn, Warning, w.at(i), "a turn record names a turn that never started", "turn", p.ID, "subtype", subtype)
			return
		}
	}
	switch subtype {
	case subtypeTurnInt:
		if t.status != statusRunning {
			w.add(CodeIllegalTurnTransition, Warning, w.span(t.start, i), "an interrupt for a turn that was not running; the projection ignores it",
				"turn", t.id, "status", t.status)
			return
		}
		t.status = statusInterrupted
	case subtypeTurnResume:
		if t.status != statusInterrupted {
			w.add(CodeIllegalTurnTransition, Warning, w.span(t.start, i), "a resume for a turn that was not interrupted; the projection ignores it",
				"turn", t.id, "status", t.status)
			return
		}
		t.status = statusRunning
	case subtypeTurnFinish:
		if !terminal(p.Status) {
			w.add(CodeInvalidRecord, Unsafe, w.span(t.start, i), "a turn finished with a status that is not terminal", "turn", t.id, "status", p.Status)
			return
		}
		if t.outcome != "" {
			if p.Status == t.outcome {
				w.add(CodeDuplicateTurnOutcome, Warning, w.span(t.start, i), "the turn finished twice with the same status", "turn", t.id, "status", p.Status)
			} else {
				w.add(CodeConflictingTurnOutcome, Unsafe, w.span(t.start, i), "the turn finished twice with different statuses",
					"turn", t.id, "first_status", t.outcome, "status", p.Status)
			}
			t.status = p.Status
			return
		}
		for _, c := range t.pending {
			w.add(CodeToolUseWithoutResult, Unsafe, w.span(w.calls[c].record, i), "the turn finished with a tool call unanswered; the next request would pair a call with no result",
				"turn", t.id, "tool_use_id", w.calls[c].id)
		}
		t.pending = nil
		t.outcome, t.status = p.Status, p.Status
		w.open = slices.DeleteFunc(w.open, func(id string) bool { return id == t.id })
	}
}

func (w *walker) message(i int, r record) {
	m, err := decodeMessage(r)
	switch {
	case err != nil:
		w.add(CodeInvalidRecord, Unsafe, w.at(i), "the message body is not a role and an array of readable content blocks", "record", r.UUID)
		return
	case m.role != r.Type || m.id == "":
		w.add(CodeInvalidRecord, Unsafe, w.at(i), "the message role or UUID does not match its record", "record", r.UUID, "type", r.Type, "role", m.role)
		return
	}
	if !w.needsConversation(i, "message") {
		return
	}
	if first, dup := w.messageAt[m.id]; dup {
		w.add(CodeDuplicateRecordID, Unsafe, w.span(first, i), "two messages share one UUID; the index keeps only the first", "message", m.id)
		return
	}
	w.messageAt[m.id] = i
	w.msgOrder[m.id] = w.messages
	position := w.messages
	w.messages++
	k := m.kind()

	t := w.turns[m.turn]
	switch {
	case m.turn == "":
		// messages.turn_id references turns: the projection refuses it.
		w.add(CodeMessageWithoutTurn, Unsafe, w.at(i), "the message names no turn; the index cannot hold it", "message", m.id)
		return
	case t == nil:
		w.add(CodeUnknownTurn, Unsafe, w.at(i), "the message names a turn that never started; the index cannot hold it", "message", m.id, "turn", m.turn)
		return
	case !t.unfinished():
		w.add(CodeMessageAfterOutcome, Unsafe, w.span(t.start, i), "the message was written after its turn finished", "message", m.id, "turn", t.id, "status", t.outcome)
		return
	case t.status == statusInterrupted && k != kindTool:
		// Only Discard writes while a turn is interrupted, and only the
		// synthetic results for its unanswered calls.
		w.add(CodeIllegalRoleOrdering, Unsafe, w.span(t.start, i), "a message other than recovery results was written while its turn was interrupted",
			"message", m.id, "turn", t.id, "role", m.indexRole())
		return
	}
	t.messages++
	if t.messages == 1 && (k != kindOwner || m.id != t.userMessageID) {
		w.add(CodeTurnMissingUserMessage, Unsafe, w.span(t.start, i), "the turn does not open with its own user message",
			"turn", t.id, "expected", t.userMessageID, "found", m.id, "role", m.indexRole())
	}

	switch k {
	case kindOwner:
		if t.messages > 1 {
			w.add(CodeIllegalRoleOrdering, Unsafe, w.span(t.start, i), "a second person's message inside one turn", "message", m.id, "turn", t.id)
		}
	case kindClosing:
		if len(t.pending) > 0 {
			c := w.calls[t.pending[0]]
			w.add(CodeIllegalRoleOrdering, Unsafe, w.span(c.record, i), "a closing host note was written while tool calls were unanswered",
				"message", m.id, "turn", t.id, "tool_use_id", c.id)
		}
	case kindAssistant:
		w.step(i, position, t, m)
	case kindTool:
		for _, b := range m.blocks {
			if b.Type == "tool_result" {
				w.result(i, position, t, m, b.ToolUseID)
			}
		}
	}
}

// step starts a model step: calls the previous step left unanswered are
// reported, and this step's calls become pending.
func (w *walker) step(i, position int, t *turn, m message) {
	for _, c := range t.pending {
		t.superseded[w.calls[c].id] = true
		w.add(CodeToolUseWithoutResult, Unsafe, w.span(w.calls[c].record, i), "a later model step went past a tool call with no result",
			"turn", t.id, "tool_use_id", w.calls[c].id, "message", m.id)
	}
	t.pending = nil
	for id := range t.answered {
		t.answeredBefore[id] = true
	}
	clear(t.stepCalls)
	clear(t.answered)
	for _, b := range m.blocks {
		if b.Type != "tool_use" {
			continue
		}
		if b.ID == "" {
			w.add(CodeInvalidRecord, Unsafe, w.at(i), "a tool call has no ID", "message", m.id)
			continue
		}
		if _, dup := t.stepCalls[b.ID]; dup {
			w.add(CodeDuplicateRecordID, Unsafe, w.at(i), "two tool calls in one step share an ID; their results cannot be told apart",
				"tool_use_id", b.ID, "message", m.id)
			continue
		}
		delete(t.superseded, b.ID)
		w.calls = append(w.calls, call{id: b.ID, turn: t.id, record: i, message: position, result: -1})
		t.stepCalls[b.ID] = len(w.calls) - 1
		t.pending = append(t.pending, len(w.calls)-1)
		w.callTurns[b.ID] = t.id
	}
}

func (w *walker) result(i, position int, t *turn, m message, id string) {
	c, current := t.stepCalls[id]
	switch {
	case current && t.answered[id]:
		w.add(CodeDuplicateToolResult, Unsafe, w.span(w.calls[c].record, i), "a tool call has more than one result", "message", m.id, "turn", t.id, "tool_use_id", id)
		return
	case current:
		t.answered[id] = true
		w.calls[c].result = position
		t.pending = slices.DeleteFunc(t.pending, func(p int) bool { return p == c })
		return
	case t.superseded[id]:
		w.add(CodeOrphanToolResult, Unsafe, w.at(i), "a tool result arrived after a later model step", "message", m.id, "turn", t.id, "tool_use_id", id)
		return
	}
	evidence := []string{"message", m.id, "turn", t.id, "tool_use_id", id}
	if other, ok := w.callTurns[id]; ok && other != t.id {
		evidence = append(evidence, "call_turn", other)
	}
	w.add(CodeOrphanToolResult, Unsafe, w.at(i), "a tool result answers no call of the step before it", evidence...)
}

func (w *walker) byteLayer() {
	err := w.in.ReplayErr
	if err == nil {
		return
	}
	last := len(w.in.Entries) - 1
	after := Range{First: -1, Last: -1}
	if last >= 0 {
		c := w.in.Entries[last].Cursor
		after.FirstCursor, after.LastCursor = c, c
	}
	switch {
	case errors.Is(err, transcript.ErrTorn):
		w.add(CodeTornTail, Unsafe, after, "the transcript ends in an incomplete or unreadable record after the last committed one; the writer refuses to append until it is handled")
	case errors.Is(err, transcript.ErrGap):
		w.add(CodeHistoryGap, Unsafe, after, "a transcript segment is missing or committed history changed")
	case errors.Is(err, transcript.ErrInvalid):
		w.add(CodeInvalidRecord, Unsafe, after, "the transcript could not be replayed: invalid record or cursor")
	default:
		w.add(CodeInvalidRecord, Unsafe, after, "the transcript could not be replayed", "error", err.Error())
	}
}

// finish reports what the walk leaves open: unfinished turns (an ordinary
// interruption), their unanswered calls, a missing conversation record and
// compactions that split a tool step.
func (w *walker) finish() {
	if len(w.in.Entries) > 0 && w.conversationRecords == 0 && !w.noConversation && replayed(w.in.ReplayErr) {
		w.add(CodeConversationMeta, Unsafe, w.at(0), "the transcript has no conversation record; the index has no conversation to hold its turns")
	}
	last := len(w.in.Entries) - 1
	for _, id := range w.open {
		t := w.turns[id]
		w.add(CodeInterruptedTurn, Recoverable, w.span(t.start, last), "the turn stopped before it ended; resume or discard it",
			"turn", t.id, "status", t.status)
		w.repairable(RepairResume)
		if t.messages == 0 {
			// ResumePlan finds no user message and records the turn failed.
			w.add(CodeTurnMissingUserMessage, Recoverable, w.at(t.start), "the turn stopped before its user message was stored; resuming records it failed",
				"turn", t.id, "expected", t.userMessageID)
			w.repairable(RepairResume)
		}
		for _, c := range t.pending {
			id := w.calls[c].id
			if t.answeredBefore[id] {
				w.add(CodeToolUseWithoutResult, Unsafe, w.span(w.calls[c].record, last),
					"the turn stopped before this tool call returned, and an earlier step of the turn already answered its ID; recovery pairs by ID and would leave it unanswered",
					"turn", t.id, "tool_use_id", id)
				continue
			}
			w.add(CodeToolUseWithoutResult, Recoverable, w.span(w.calls[c].record, last), "the turn stopped before this tool call returned; recovery answers it with an interrupted result",
				"turn", t.id, "tool_use_id", id)
			w.repairable(RepairResume)
		}
	}
	// One pass per compaction over the calls, which are in message order.
	for _, comp := range w.compactions {
		through := w.msgOrder[comp.through]
		for _, c := range w.calls {
			if c.message > through {
				break
			}
			if c.result > through {
				w.add(CodeCompactionSplitsToolStep, Unsafe, w.span(c.record, comp.record),
					"the compaction summarizes a tool call but keeps its result; the kept history would open with an unpaired result",
					"compaction", comp.id, "through", comp.through, "tool_use_id", c.id)
			}
		}
	}
}

// compare checks the index against the projection of the committed records.
// Its fixes (replay, rebuild) are automatic only for a sound transcript: one
// with no unsafe finding of its own, which the projection can replay.
func (w *walker) compare(sound bool) {
	idx := w.in.Index
	if idx == nil || !replayed(w.in.ReplayErr) {
		// Without the committed records there is nothing to compare with.
		return
	}
	repair := func(r Repair) {
		if sound {
			w.repairable(r)
		}
	}
	n := len(w.in.Entries)
	upto := 0
	if idx.Cursor != nil && *idx.Cursor != (transcript.Cursor{}) {
		upto = -1
		for i, e := range w.in.Entries {
			if e.Cursor == *idx.Cursor {
				upto = i + 1
				break
			}
		}
		if upto < 0 {
			w.add(CodeIndexDisagreement, Unsafe, Range{First: -1, Last: -1, FirstCursor: *idx.Cursor, LastCursor: *idx.Cursor},
				"the index has projected through a cursor the transcript does not have", "index_cursor", cursorString(*idx.Cursor))
			repair(RepairRebuildIndex)
			return
		}
	}
	if upto < n {
		w.add(CodeIndexBehind, Recoverable, w.span(upto, n-1), "the index has not projected the last committed records; replay brings it up to date",
			"projected", fmt.Sprint(upto), "records", fmt.Sprint(n))
		repair(RepairReplayIndex)
	}
	want := Project(w.in.ConversationID, w.in.Entries[:upto])
	disagree := func(at int, detail string, evidence ...string) {
		w.add(CodeIndexDisagreement, Unsafe, w.at(at), detail, evidence...)
		repair(RepairRebuildIndex)
	}
	recordOf := func(id string) int {
		if i, ok := w.messageAt[id]; ok && i < upto {
			return i
		}
		return -1
	}

	wantTurns := map[string]IndexTurn{}
	for _, t := range want.Turns {
		wantTurns[t.ID] = t
	}
	gotTurns := map[string]IndexTurn{}
	for _, t := range idx.Turns {
		gotTurns[t.ID] = t
	}
	for _, t := range want.Turns {
		start := -1
		if s := w.turns[t.ID]; s != nil {
			start = s.start
		}
		got, ok := gotTurns[t.ID]
		switch {
		case !ok:
			disagree(start, "the index is missing a turn", "turn", t.ID)
		case got.Status != t.Status:
			disagree(start, "the index has a different turn status", "turn", t.ID, "transcript_status", t.Status, "index_status", got.Status)
		case got.UserMessageID != t.UserMessageID:
			disagree(start, "the index has a different user message for the turn", "turn", t.ID)
		}
	}
	for _, t := range idx.Turns {
		if _, ok := wantTurns[t.ID]; !ok {
			disagree(-1, "the index has a turn the transcript does not", "turn", t.ID, "index_status", t.Status)
		}
	}

	wantMsgs := map[string]IndexMessage{}
	position := map[string]int{}
	for i, m := range want.Messages {
		wantMsgs[m.ID] = m
		position[m.ID] = i
	}
	gotMsgs := map[string]IndexMessage{}
	for _, m := range idx.Messages {
		gotMsgs[m.ID] = m
	}
	for _, m := range want.Messages {
		got, ok := gotMsgs[m.ID]
		switch {
		case !ok:
			disagree(recordOf(m.ID), "the index is missing a message", "message", m.ID, "turn", m.TurnID)
		case got.TurnID != m.TurnID || got.Role != m.Role:
			disagree(recordOf(m.ID), "the index has a different turn or role for a message",
				"message", m.ID, "transcript_role", m.Role, "index_role", got.Role, "transcript_turn", m.TurnID, "index_turn", got.TurnID)
		}
	}
	var common []string
	for _, m := range idx.Messages {
		if _, ok := wantMsgs[m.ID]; !ok {
			disagree(-1, "the index has a message the transcript does not", "message", m.ID, "turn", m.TurnID)
			continue
		}
		common = append(common, m.ID)
	}
	for i := 1; i < len(common); i++ {
		if position[common[i-1]] > position[common[i]] {
			disagree(recordOf(common[i]), "the index orders messages differently from the transcript",
				"message", common[i], "before", common[i-1])
			break
		}
	}

	wantComp := map[string]IndexCompaction{}
	for _, c := range want.Compactions {
		wantComp[c.ID] = c
	}
	gotComp := map[string]IndexCompaction{}
	for _, c := range idx.Compactions {
		gotComp[c.ID] = c
	}
	for _, c := range want.Compactions {
		if got, ok := gotComp[c.ID]; !ok || got.ThroughMessageID != c.ThroughMessageID {
			disagree(-1, "the index is missing a compaction or has it cover different messages", "compaction", c.ID, "through", c.ThroughMessageID)
		}
	}
	for _, c := range idx.Compactions {
		if _, ok := wantComp[c.ID]; !ok {
			disagree(-1, "the index has a compaction the transcript does not", "compaction", c.ID)
		}
	}
}

func (w *walker) notices() {
	if w.in.Index == nil {
		return
	}
	for _, n := range w.in.Index.Notices {
		if n.DeliveredTurnID == "" {
			continue
		}
		if _, ok := w.turns[n.DeliveredTurnID]; !ok {
			// The notice is neither pending nor handled: a durable source
			// waiting on its handling turn would wait forever.
			w.add(CodeNoticeUnknownTurn, Warning, Range{First: -1, Last: -1}, "a notice is marked delivered to a turn the transcript does not have",
				"notice", n.ID, "turn", n.DeliveredTurnID)
		}
	}
}

func (w *walker) hold() {
	h := w.in.Hold
	if h == nil || h.TurnID == "" {
		return
	}
	t := w.turns[h.TurnID]
	if t != nil && t.unfinished() {
		return
	}
	status := "unknown"
	if t != nil {
		status = t.status
	}
	w.add(CodeStaleInterruptHold, Warning, Range{First: -1, Last: -1}, "the saved interrupted-turn hold names a turn that is not unfinished",
		"turn", h.TurnID, "status", status)
	w.repairable(RepairClearHold)
}

func cursorString(c transcript.Cursor) string {
	return fmt.Sprintf("%d:%d:%s", c.Segment, c.Offset, c.Hash)
}
