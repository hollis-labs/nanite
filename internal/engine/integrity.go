package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/integrity"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrIntegrity is matched (errors.Is) by every IntegrityError.
var ErrIntegrity = errors.New("conversation failed integrity checks")

// IntegrityError refuses Resume through an unsafe integrity finding. Report
// holds every finding, in order; Report.Unsafe() lists the blocking ones.
// Discard remains available unless the transcript refuses writes.
type IntegrityError struct {
	Conversation string
	Report       integrity.Report
}

func (e *IntegrityError) Error() string {
	unsafe := e.Report.Unsafe()
	codes := make([]string, 0, len(unsafe))
	seen := map[integrity.Code]bool{}
	for _, f := range unsafe {
		if !seen[f.Code] {
			seen[f.Code] = true
			codes = append(codes, string(f.Code))
		}
	}
	return fmt.Sprintf("conversation %s failed integrity checks (%d unsafe finding(s): %s); resume refused, discard is still available",
		e.Conversation, len(unsafe), strings.Join(codes, ", "))
}

func (e *IntegrityError) Unwrap() error { return ErrIntegrity }

// CheckIntegrity validates a conversation's transcript and index without
// writing or emitting anything. It refuses with ErrBusy while the
// conversation has a run or another check: a transcript being written can
// look torn.
func (e *Engine) CheckIntegrity(ctx context.Context, id string) (integrity.Report, error) {
	if _, err := e.cfg.Store.Conversation(ctx, id); err != nil {
		return integrity.Report{}, err
	}
	if err := e.claimCheck(id); err != nil {
		return integrity.Report{}, err
	}
	defer e.releaseCheck(id)
	return e.cfg.Store.ValidateConversation(ctx, id, nil)
}

// ObserveIntegrity reports a check recovery or a run ran: a runtime event
// (counts and severities only) and, when something is wrong, a log line.
func (e *Engine) ObserveIntegrity(ctx context.Context, conversation, stage string, report integrity.Report) {
	disposition := recovery.DispositionOf(report)
	codes := map[string]int{}
	for _, f := range report.Findings {
		codes[string(f.Code)]++
	}
	worst := report.Worst()
	if worst > integrity.Info {
		log := e.cfg.Logger.Info
		if disposition == recovery.Blocked {
			log = e.cfg.Logger.Warn
		}
		log("nanite: conversation integrity", "conversation", conversation, "stage", stage,
			"disposition", string(disposition), "severity", worst.String(), "findings", len(report.Findings))
	}
	event := events.IntegrityChecked{Conversation: conversation, Stage: stage, Disposition: string(disposition), Severity: worst.String(), Codes: codes}
	_ = e.panics.Call("integrity event sink", func() error {
		e.runtime.Observe(ctx, conversation, "", event)
		return nil
	})
}

// busyLocked reports, with e.mu held, whether a run or an integrity check
// has the conversation. Every writer of a transcript checks it.
func (e *Engine) busyLocked(id string) bool { return e.active[id] != nil || e.checking[id] }

// claimCheck reserves a conversation for an integrity check, so no run,
// Discard or Record writes its transcript while the check reads it without
// holding e.mu (a check replays the whole transcript; other conversations
// must not wait for it).
func (e *Engine) claimCheck(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.busyLocked(id) {
		return ErrBusy
	}
	e.checking[id] = true
	return nil
}

func (e *Engine) releaseCheck(id string) {
	e.mu.Lock()
	delete(e.checking, id)
	e.mu.Unlock()
}

// runGate decides whether a run may start on a conversation. Resume always
// validates first, before anything reads the history or assembles context.
// Any other run (Send, Dispatch, Record) validates only a conversation an
// earlier check marked blocked, so an unsafe history never reaches a model
// after a Discard either; a passing check clears the mark. The event is
// emitted with no lock held: a host's sink may call back into the engine.
func (e *Engine) runGate(ctx context.Context, id string, resume bool) error {
	if !resume {
		mark, err := e.cfg.Store.Setting(ctx, id, store.SettingIntegrity)
		if err != nil || mark == "" {
			return err
		}
	}
	if err := e.claimCheck(id); err != nil {
		return err
	}
	report, err := e.validate(ctx, id)
	e.releaseCheck(id)
	stage := "send"
	if resume {
		stage = "resume"
	}
	if checkRan(err) {
		e.ObserveIntegrity(ctx, id, stage, report)
	}
	return err
}

// validate checks a conversation whose transcript nothing is writing (the
// caller holds a check claim, e.mu, or the conversation's run), records the
// outcome as the conversation's mark, and refuses an unsafe one. It emits
// nothing; the caller reports the check once no lock is held.
func (e *Engine) validate(ctx context.Context, id string) (integrity.Report, error) {
	report, err := e.cfg.Store.ValidateConversation(ctx, id, nil)
	if err != nil {
		return report, err
	}
	if err := recovery.MarkBlocked(ctx, e.cfg.Store, id, !report.Safe()); err != nil {
		return report, err
	}
	if !report.Safe() {
		return report, &IntegrityError{Conversation: id, Report: report}
	}
	return report, nil
}

// checkRan reports whether validate completed: a pass or a typed refusal,
// not a store error, so only checks that ran are reported.
func checkRan(err error) bool {
	var refused *IntegrityError
	return err == nil || errors.As(err, &refused)
}
