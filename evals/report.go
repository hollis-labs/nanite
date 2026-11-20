package evals

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSON renders the results as indented JSON. Without wall it leaves the wall
// times out, so an offline report is the same on every run.
func JSON(results []Result, wall bool) ([]byte, error) {
	out := make([]Result, len(results))
	copy(out, results)
	if !wall {
		for i := range out {
			out[i].WallMS = 0
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	return append(data, '\n'), err
}

// Markdown renders the results as a table, then one line per scenario
// comparing the subtask run's parent context with the direct run's.
func Markdown(results []Result, wall bool) string {
	var b strings.Builder
	b.WriteString("| Scenario | Mode | Success | Parent context tokens | Total tokens | Est. cost (USD) | Wall | Approvals | Parent tool calls | Subtasks |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range results {
		if r.Skipped != "" {
			fmt.Fprintf(&b, "| %s | %s | skipped: %s | | | | | | | |\n", r.Scenario, r.Mode, r.Skipped)
			continue
		}
		ok := "yes"
		if !r.Success {
			ok = "no: " + strings.ReplaceAll(r.Why, "|", "/")
		}
		w := "-"
		if wall {
			w = fmt.Sprintf("%.1fs", float64(r.WallMS)/1000)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %.4f | %s | %d | %d | %d |\n",
			r.Scenario, r.Mode, ok, r.ParentContextTokens, r.TotalTokens, r.CostUSD, w, r.Approvals, r.ParentToolCalls, r.Subtasks)
	}
	b.WriteString("\n")
	for _, d := range pairs(results) {
		fmt.Fprintf(&b, "- %s: the subtask run's parent context is %.0f%% of the direct run's; its total tokens are %.0f%%.\n",
			d.name, 100*ratio(d.sub.ParentContextTokens, d.direct.ParentContextTokens), 100*ratio(d.sub.TotalTokens, d.direct.TotalTokens))
	}
	return b.String()
}

type pair struct {
	name        string
	direct, sub Result
}

// pairs matches each scenario's direct run with its subtask run.
func pairs(results []Result) []pair {
	var out []pair
	for _, d := range results {
		if d.Mode != Direct || d.Skipped != "" {
			continue
		}
		for _, s := range results {
			if s.Scenario == d.Scenario && s.Mode == Subtask && s.Skipped == "" {
				out = append(out, pair{d.Scenario, d, s})
			}
		}
	}
	return out
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
