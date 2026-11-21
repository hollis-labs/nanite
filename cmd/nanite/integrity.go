package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/hollis-labs/nanite/agent"
)

// printIntegrity lists the findings behind a refused resume, one per line,
// and the ways forward. Findings carry identifiers, never content.
func printIntegrity(w io.Writer, err error) {
	var refused *agent.IntegrityError
	if !errors.As(err, &refused) {
		return
	}
	for _, f := range refused.Report.Findings {
		if f.Severity < agent.IntegrityWarning {
			continue
		}
		var evidence []string
		for _, k := range slices.Sorted(maps.Keys(f.Evidence)) {
			evidence = append(evidence, k+"="+f.Evidence[k])
		}
		where := "index"
		if f.Records.First >= 0 {
			where = fmt.Sprintf("records %d-%d", f.Records.First, f.Records.Last)
		}
		_, _ = fmt.Fprintf(w, "  %s %s (%s): %s %s\n", f.Severity, f.Code, where, f.Detail, strings.Join(evidence, " "))
	}
	if refused.Report.Truncated {
		_, _ = fmt.Fprintf(w, "  ... more findings not shown\n")
	}
	_, _ = fmt.Fprintf(w, "Discard the turn with --discard %s, or repair the transcript and run `nanite rebuild-index`.\n", refused.Conversation)
}
