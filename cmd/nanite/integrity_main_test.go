package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/integrity"
)

// main prints a refused run's findings after the error line.
func TestGuardedMainPrintsIntegrityFindings(t *testing.T) {
	refused := &agent.IntegrityError{Conversation: "c1", Report: integrity.Report{Findings: []integrity.Finding{{
		Code: integrity.CodeOrphanToolResult, Severity: integrity.Unsafe, Records: integrity.Range{First: 3, Last: 3},
		Evidence: map[string]string{"tool_use_id": "nobody"}, Detail: "a tool result answers no call of the step before it",
	}}}}
	var stderr bytes.Buffer
	if code := guardedMain(context.Background(), func(context.Context) error { return refused }, &stderr); code != 1 {
		t.Fatalf("exit code %d", code)
	}
	for _, want := range []string{"nanite: conversation c1 failed integrity checks", "unsafe orphan_tool_result (records 3-3)", "tool_use_id=nobody", "closed to new turns"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
}
