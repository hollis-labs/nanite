package evals

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// JSON renders the report as indented JSON. Without wall it leaves the wall
// times out, so an offline report is the same on every run.
func JSON(rep Report, wall bool) ([]byte, error) {
	rep.Results = clean(rep.Results, wall)
	rep.SpentUSD = round(rep.SpentUSD)
	data, err := json.MarshalIndent(rep, "", "  ")
	return append(data, '\n'), err
}

func clean(results []Result, wall bool) []Result {
	out := make([]Result, len(results))
	copy(out, results)
	for i := range out {
		out[i].Why = Shorten(out[i].Why)
		out[i].CostUSD = round(out[i].CostUSD)
		if !wall {
			out[i].WallMS = 0
		}
	}
	return out
}

// Markdown renders the report as a table. For a live run it adds one line per
// scenario comparing each subtask arm's parent context and total tokens with
// the direct run's; a scripted run's numbers are a harness check, not
// evidence, so it makes no comparison.
func Markdown(rep Report, wall bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Model: %s. Price per million tokens: $%g input, $%g output, $%g cache read, $%g cache write. Estimated spend: $%.4f.",
		rep.Model, rep.Price.Input, rep.Price.Output, rep.Price.CacheRead, rep.Price.CacheWrite, rep.SpentUSD)
	if rep.Stopped != "" {
		fmt.Fprintf(&b, " Stopped early: %s.", Shorten(rep.Stopped))
	}
	b.WriteString("\n\n| Scenario | Arm | Success | Parent context tokens | Total tokens | Est. cost (USD) | Wall | Approvals | Parent tool calls | Subtasks | Variants | Compactions |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|\n")
	for _, r := range clean(rep.Results, wall) {
		if r.Skipped != "" {
			fmt.Fprintf(&b, "| %s | %s | skipped: %s | | | | | | | | | |\n", r.Scenario, r.Arm, r.Skipped)
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
		v := strings.Join(r.Variants, ", ")
		if v == "" {
			v = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %.4f | %s | %d | %d | %d | %s | %d |\n",
			r.Scenario, r.Arm, ok, r.ParentContextTokens, r.TotalTokens, r.CostUSD, w, r.Approvals, r.ParentToolCalls, r.Subtasks, v, r.Compactions)
	}
	if rep.Model == ScriptedModel {
		return b.String()
	}
	b.WriteString("\n")
	for _, d := range pairs(rep.Results) {
		fmt.Fprintf(&b, "- %s, %s: parent context %s of the direct run's; total tokens %s.\n",
			d.name, d.sub.Arm, percent(d.sub.ParentContextTokens, d.direct.ParentContextTokens), percent(d.sub.TotalTokens, d.direct.TotalTokens))
	}
	return b.String()
}

type pair struct {
	name        string
	direct, sub Result
}

// pairs matches each scenario's direct run with each of its subtask arms,
// leaving out any pair where either run was skipped or did not succeed.
func pairs(results []Result) []pair {
	var out []pair
	for _, d := range results {
		if d.Mode != Direct || d.Skipped != "" || !d.Success {
			continue
		}
		for _, s := range results {
			if s.Scenario == d.Scenario && s.Mode == Subtask && s.Skipped == "" && s.Success {
				out = append(out, pair{d.Scenario, d, s})
			}
		}
	}
	return out
}

// round keeps six decimals: concurrent subtasks add their costs in any
// order, which changes the last bits of a float sum.
func round(v float64) float64 { return math.Round(v*1e6) / 1e6 }

func percent(a, b int) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(a)/float64(b))
}
