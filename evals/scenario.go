// Package evals runs Code Mode evaluation scenarios: each task is run once with
// direct tool use and once with the subtask tool, and the turn's trace records
// give the metrics compared.
//
// A scenario is an agent directory with a fixture workspace (testdata/scenarios),
// a task prompt, a grader that checks the answer and the workspace, and a script
// for the offline run. The offline run uses a scripted provider and runs in CI;
// the live run uses the configured provider and runs only on request (see
// cmd/nanite-eval).
package evals

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed testdata/scenarios
var fixtures embed.FS

// Mode is how a scenario is run.
type Mode string

const (
	// Direct offers the file tools but not subtask: the agent does the work
	// in its own conversation.
	Direct Mode = "direct"
	// Subtask also offers the subtask tool, so the agent can delegate.
	Subtask Mode = "subtask"
)

// Modes are the modes every scenario is compared in, in report order.
var Modes = []Mode{Direct, Subtask}

// Scenario is one task the evals run.
type Scenario struct {
	// Name is the fixture directory under testdata/scenarios.
	Name string
	// Writes says whether the task changes workspace files; the agent is then
	// offered the write tools and asks for approval to use them.
	Writes bool
	// Grade checks the final answer and the workspace the run left behind. It
	// returns whether the task succeeded and, when not, why.
	Grade func(answer, workspace string) (bool, string)
	// Script is the offline run's scripted model, for one mode.
	Script func(Mode) Script
}

// Task is the scenario's prompt, from task.md.
func (s Scenario) Task() (string, error) {
	data, err := fixtures.ReadFile(path.Join("testdata/scenarios", s.Name, "task.md"))
	return strings.TrimSpace(string(data)), err
}

// install copies the scenario's agent directory and workspace into dir.
func (s Scenario) install(dir string) error {
	root := path.Join("testdata/scenarios", s.Name)
	return fs.WalkDir(fixtures, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		if rel == "task.md" {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := fixtures.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

// fixture reads one fixture file of a scenario.
func fixture(scenario, name string) string {
	data, err := fixtures.ReadFile(path.Join("testdata/scenarios", scenario, name))
	if err != nil {
		panic(fmt.Sprintf("evals: fixture %s/%s: %v", scenario, name, err))
	}
	return string(data)
}

// Scenarios are the checked-in scenarios, in report order.
func Scenarios() []Scenario {
	return []Scenario{codeSearch(), research(), chore(), council()}
}

// Find returns the named scenarios, or all of them for no names.
func Find(names ...string) ([]Scenario, error) {
	all := Scenarios()
	if len(names) == 0 {
		return all, nil
	}
	var out []Scenario
	for _, name := range names {
		i := slices.IndexFunc(all, func(s Scenario) bool { return s.Name == name })
		if i < 0 {
			return nil, fmt.Errorf("evals: no scenario %q", name)
		}
		out = append(out, all[i])
	}
	return out, nil
}

// mentions checks that answer contains every phrase, ignoring case, and names
// the first missing one.
func mentions(answer string, phrases ...string) (bool, string) {
	lower := strings.ToLower(answer)
	for _, p := range phrases {
		if !strings.Contains(lower, strings.ToLower(p)) {
			return false, fmt.Sprintf("answer does not mention %q", p)
		}
	}
	return true, ""
}

func codeSearch() Scenario {
	const goal = "Find every function in src/ that reads the APP_MODE environment variable, and the mode values the code compares it against."
	answer := "loadMode in src/config.go and startServer in src/server.go read APP_MODE. The code compares it against dev, prod and test."
	return Scenario{
		Name: "code-search",
		Grade: func(a, _ string) (bool, string) {
			return mentions(a, "config.go", "loadMode", "server.go", "startServer", "dev", "prod", "test")
		},
		Script: func(m Mode) Script {
			work := []Step{
				Calls(Call("search_files", "pattern", "APP_MODE", "path", "src")),
				Calls(Call("read_file", "path", "src/config.go"), Call("read_file", "path", "src/server.go")),
			}
			if m == Direct {
				return Script{Root: append(work, Say(answer))}
			}
			return Script{
				Root: []Step{
					Calls(Call("subtask", "goal", goal, "variant", "research", "return_spec", "Each function as file:function, then the mode values.")),
					Say(answer),
				},
				Children: map[string][]Step{goal: append(work, Return("ok", "src/config.go:loadMode, src/server.go:startServer. Modes: dev, prod, test."))},
			}
		},
	}
}

func research() Scenario {
	const goal = "From the files in corpus/, find the current default retention period for ledger exports, who decided it and when, and the file that says so."
	answer := "The current default retention period is 30 days. The Data Council decided it on 2026-03-04 (corpus/policy-2026.md), replacing the 90-day rule of 2024."
	return Scenario{
		Name: "research",
		Grade: func(a, _ string) (bool, string) {
			return mentions(a, "30 days", "Data Council", "2026-03-04", "policy-2026.md")
		},
		Script: func(m Mode) Script {
			work := []Step{
				Calls(Call("search_files", "pattern", "retention period", "path", "corpus")),
				Calls(Call("read_file", "path", "corpus/policy-2024.md"), Call("read_file", "path", "corpus/policy-2026.md")),
			}
			if m == Direct {
				return Script{Root: append(work, Say(answer))}
			}
			return Script{
				Root: []Step{
					Calls(Call("subtask", "goal", goal, "variant", "research", "return_spec", "The period, who decided it, the date, and the source file path.")),
					Say(answer),
				},
				Children: map[string][]Step{goal: append(work, Return("ok", "30 days; Data Council; 2026-03-04; source corpus/policy-2026.md (policy-2024.md is superseded)."))},
			}
		},
	}
}

var choreNotes = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}

const reviewedLine = "Reviewed: 2026-11-20"

func chore() Scenario {
	const goal = "Append the line \"Reviewed: 2026-11-20\" at the end of every Markdown file in notes/, keeping the rest of each file, and report how many files changed."
	answer := "I added \"Reviewed: 2026-11-20\" to all 6 files in notes/."
	return Scenario{
		Name:   "chore",
		Writes: true,
		Grade: func(a, ws string) (bool, string) {
			for _, n := range choreNotes {
				name := filepath.Join(ws, "notes", n+".md")
				data, err := os.ReadFile(name) //nolint:gosec // a file of the run's own copy of the workspace
				if err != nil {
					return false, err.Error()
				}
				want := strings.TrimRight(fixture("chore", "notes/"+n+".md"), "\n") + "\n" + reviewedLine
				if strings.TrimRight(string(data), "\n") != want {
					return false, fmt.Sprintf("notes/%s.md does not end with the reviewed line, or other text changed", n)
				}
			}
			return mentions(a, "6")
		},
		Script: func(m Mode) Script {
			var reads, writes []ToolUse
			for _, n := range choreNotes {
				p := "notes/" + n + ".md"
				reads = append(reads, Call("read_file", "path", p))
				writes = append(writes, Call("write_file", "path", p, "content", strings.TrimRight(fixture("chore", p), "\n")+"\n"+reviewedLine+"\n"))
			}
			work := []Step{Calls(Call("list_dir", "path", "notes")), Calls(reads...), Calls(writes...)}
			if m == Direct {
				return Script{Root: append(work, Say(answer))}
			}
			return Script{
				Root: []Step{
					Calls(Call("subtask", "goal", goal, "variant", "worker", "return_spec", "The number of files changed.")),
					Say(answer),
				},
				Children: map[string][]Step{goal: append(work, Return("ok", "Changed 6 files."))},
			}
		},
	}
}

func council() Scenario {
	const goal = "Review src/payment.go for bugs. Return each bug with its line number and a one-line fix."
	angles := []string{"correctness", "safety", "clarity"}
	answer := "Two bugs in src/payment.go. Line 17: the loop condition i <= len(items) is an off-by-one that reads past the end; use i < len(items). Line 25: Refund returns a negative amount unchecked; refuse or clamp negative amounts."
	return Scenario{
		Name: "council",
		Grade: func(a, _ string) (bool, string) {
			if ok, why := mentions(a, "17", "negative"); !ok {
				return ok, why
			}
			if !strings.Contains(a, "off-by-one") && !strings.Contains(a, "< len(items)") {
				return false, "answer does not name the off-by-one loop"
			}
			return true, ""
		},
		Script: func(m Mode) Script {
			if m == Direct {
				return Script{Root: []Step{Calls(Call("read_file", "path", "src/payment.go")), Say(answer)}}
			}
			children := map[string][]Step{}
			var reviews []ToolUse
			for _, angle := range angles {
				g := "Review src/payment.go from the " + angle + " angle. Return each finding with its line and a one-line fix."
				reviews = append(reviews, Call("subtask", "goal", g, "variant", "reviewer", "return_spec", "Findings as line: problem: fix."))
				children[g] = []Step{
					Calls(Call("read_file", "path", "src/payment.go")),
					Return("ok", "17: off-by-one, i <= len(items) reads past the end: use i < len(items). 25: negative amounts are not refused: check Amount >= 0."),
				}
			}
			children[goal] = []Step{
				Calls(reviews...),
				Return("ok", "Merged (all three reviewers): line 17 off-by-one loop, use i < len(items); line 25 negative refund not refused, check Amount >= 0."),
			}
			return Script{
				Root: []Step{
					Calls(Call("subtask", "goal", goal, "variant", "council", "return_spec", "Each bug as line: problem: fix.")),
					Say(answer),
				},
				Children: children,
			}
		},
	}
}
