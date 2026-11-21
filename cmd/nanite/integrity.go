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
	for _, f := range refused.Report.Unsafe() {
		if f.Code != "torn_tail" {
			continue
		}
		// Only an incomplete final line is a torn tail: Append writes each
		// record in one write and fsyncs it, so those bytes were never
		// committed and removing them loses nothing.
		c := f.Records.LastCursor
		segment := max(c.Segment, 1)
		_, _ = fmt.Fprintf(w, "The transcript's committed records end at segment %020d.jsonl, byte %d; the bytes after that were never committed.\n"+
			"Remove them (truncate that segment to %d bytes and delete any later segment), then run `nanite rebuild-index`.\n", segment, c.Offset, c.Offset)
		return
	}
	if refused.Interrupted {
		_, _ = fmt.Fprintf(w, "--discard %s closes its interrupted turn; the conversation still takes no new turns, so continue in a new one.\n", refused.Conversation)
		return
	}
	_, _ = fmt.Fprintf(w, "This conversation takes no new turns until an integrity check passes; continue in a new conversation.\n")
}
