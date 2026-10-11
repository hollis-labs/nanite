package main

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/service"
)

// reconcileFailureLogger turns a periodic reconciler's per-pass failure list
// into state-change logging. A refusal that cannot succeed until something
// external exists (CW-20261011-0042: no verified actor issuer yet) used to log
// a WARN on every 5 s pass for the life of the process; it now logs when the
// set of failures changes and once when they clear. The refusal itself is
// untouched.
type reconcileFailureLogger struct {
	what string
	last string
}

func newReconcileFailureLogger(what string) *reconcileFailureLogger {
	return &reconcileFailureLogger{what: what}
}

func (l *reconcileFailureLogger) observe(report service.TeamRunReconcileReport) {
	msgs := make([]string, 0, len(report.Failures))
	for _, err := range report.Failures {
		msgs = append(msgs, err.Error())
	}
	sort.Strings(msgs)
	state := strings.Join(msgs, "\n")
	if state == l.last {
		return
	}
	l.last = state
	if len(msgs) == 0 {
		slog.Info(l.what + " failures cleared")
		return
	}
	for _, m := range msgs {
		slog.Warn(l.what+" failed (logged again only if this changes)", "err", m)
	}
}
