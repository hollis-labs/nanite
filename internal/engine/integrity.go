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
// conversation has a run: a transcript being written can look torn.
func (e *Engine) CheckIntegrity(ctx context.Context, id string) (integrity.Report, error) {
	if _, err := e.cfg.Store.Conversation(ctx, id); err != nil {
		return integrity.Report{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active[id] != nil {
		return integrity.Report{}, ErrBusy
	}
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

// runGate decides whether a run may start on a conversation. Resume always
// validates first, before anything reads the history or assembles context.
// Any other run validates only a conversation an earlier check marked
// blocked, so an unsafe history never reaches a model through Send after a
// Discard either; a passing check clears the mark. It holds the engine lock
// while it validates, so no run, Discard or Record writes the transcript
// meanwhile.
func (e *Engine) runGate(ctx context.Context, id string, resume bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active[id] != nil {
		// The run would be refused anyway; validating a transcript a run is
		// writing could report a transient torn tail instead.
		return ErrBusy
	}
	if !resume {
		mark, err := e.cfg.Store.Setting(ctx, id, store.SettingIntegrity)
		if err != nil || mark == "" {
			return err
		}
	}
	stage := "send"
	if resume {
		stage = "resume"
	}
	return e.validateLocked(ctx, id, stage)
}

// validateLocked validates a conversation with e.mu held, records the
// outcome as the conversation's mark, and refuses an unsafe one.
func (e *Engine) validateLocked(ctx context.Context, id, stage string) error {
	report, err := e.cfg.Store.ValidateConversation(ctx, id, nil)
	if err != nil {
		return err
	}
	e.ObserveIntegrity(ctx, id, stage, report)
	if err := recovery.MarkBlocked(ctx, e.cfg.Store, id, !report.Safe()); err != nil {
		return err
	}
	if !report.Safe() {
		return &IntegrityError{Conversation: id, Report: report}
	}
	return nil
}
