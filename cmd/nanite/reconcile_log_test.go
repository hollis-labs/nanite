package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
)

func TestReconcileFailureLoggerLogsOnStateChangeOnly(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	l := newReconcileFailureLogger("Team workflow reconciliation")
	refused := service.TeamRunReconcileReport{Failures: []error{errors.New("verified actor binding is required")}}
	for i := 0; i < 10; i++ {
		l.observe(refused)
	}
	if n := strings.Count(buf.String(), "verified actor binding is required"); n != 1 {
		t.Fatalf("repeated identical failure logged %d times, want 1:\n%s", n, buf.String())
	}
	l.observe(service.TeamRunReconcileReport{Failures: []error{errors.New("verified actor binding is required"), errors.New("another")}})
	if !strings.Contains(buf.String(), "another") {
		t.Fatalf("changed failure set not logged:\n%s", buf.String())
	}
	l.observe(service.TeamRunReconcileReport{})
	if !strings.Contains(buf.String(), "failures cleared") {
		t.Fatalf("clearing not logged:\n%s", buf.String())
	}
	before := buf.Len()
	l.observe(service.TeamRunReconcileReport{})
	if buf.Len() != before {
		t.Fatal("quiet state logged again")
	}
	l.observe(refused)
	if strings.Count(buf.String(), "verified actor binding is required") < 3 {
		t.Fatalf("recurrence after clearing not logged:\n%s", buf.String())
	}
}
