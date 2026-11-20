// Package evals runs Code Mode evaluation scenarios: each task is run in
// several arms (direct tool use, the subtask tool, and subtask settings such
// as last_n and the result cap), and the turns' trace records give the
// metrics compared.
//
// A scenario is an agent directory with a fixture workspace (an fs.FS), an
// optional prelude of earlier messages, a task prompt, a grader that checks
// the answer and the workspace, and a script for the offline run. The offline
// run uses a scripted model and runs in CI; the live run uses the configured
// provider and runs only on request (see cmd/nanite-eval).
//
// The package is experimental: its API may change while the N6-10 evals work
// builds on it.
package evals

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hollis-labs/nanite/agent"
)

//go:embed testdata/scenarios
var fixtures embed.FS

// Mode is whether an arm offers the subtask tool.
type Mode string

const (
	// Direct offers the file tools but not subtask: the agent does the work
	// in its own conversation.
	Direct Mode = "direct"
	// Subtask also offers the subtask tool, so the agent can delegate.
	Subtask Mode = "subtask"
)

// Modes are the modes, in report order.
var Modes = []Mode{Direct, Subtask}

// Arm is one way of running a scenario.
type Arm struct {
	// Name labels the arm in reports, such as "subtask last_n=1".
	Name string
	Mode Mode
	// LastN and ResultCap record the setting the arm varies, for the report;
	// zero is the default.
	LastN, ResultCap int
	// Prepare edits the run's copy of the agent directory before the agent
	// opens, for example to change a variant's last_n.
	Prepare func(dir string) error
	// Options adjusts the agent's options, for example SubtaskResultCap. It
	// may not add named providers: every request must go through the meter.
	Options func(*agent.Options)
	// VariesLastN marks an arm testing last_n: a run where a subtask call
	// chose its own context (context.last_n or context.full) is invalid.
	VariesLastN bool
	// SeedLacks are facts a subtask must not be given in its first message:
	// a run where it was given one is invalid, because the subtask never had
	// to look for it (a last_n arm testing history retrieval).
	SeedLacks []string
	// NeedsStored marks an arm that only tests something when a subtask's
	// result is stored behind a pointer and the parent fetches it (a small
	// result cap); a run where that did not happen is invalid.
	NeedsStored bool
}

// DefaultArms are the arms every scenario runs: direct, and subtask with the
// default settings.
func DefaultArms() []Arm {
	return []Arm{{Name: string(Direct), Mode: Direct}, {Name: string(Subtask), Mode: Subtask}}
}

// Scenario is one task the evals run.
type Scenario struct {
	// Name identifies the scenario.
	Name string
	// Files holds the agent directory and workspace, and task.md (and
	// prelude.txt, one earlier message a line, when there is one).
	Files fs.FS
	// Writes says whether the task changes workspace files; the agent is then
	// offered the write tools and asks for approval to use them.
	Writes bool
	// Requires names variants a subtask arm must run: a subtask run that
	// does not is invalid, not a success.
	Requires []string
	// Stubs are host tools offered in every arm, with canned results.
	Stubs []Stub
	// Arms are run after DefaultArms.
	Arms []Arm
	// Grade checks the final answer and the workspace the run left behind. It
	// returns whether the task succeeded and, when not, why.
	Grade func(answer, workspace string) (bool, string)
	// Script is the offline run's scripted model, for one arm.
	Script func(Arm) Script
}

// AllArms are DefaultArms, each replaced by the scenario's arm of the same
// name, followed by the scenario's other arms.
func (s Scenario) AllArms() []Arm {
	arms := DefaultArms()
	for _, a := range s.Arms {
		if i := slices.IndexFunc(arms, func(d Arm) bool { return d.Name == a.Name }); i >= 0 {
			arms[i] = a
		} else {
			arms = append(arms, a)
		}
	}
	return arms
}

// Task is the scenario's prompt, from task.md.
func (s Scenario) Task() (string, error) {
	data, err := fs.ReadFile(s.Files, "task.md")
	return strings.TrimSpace(string(data)), err
}

// Prelude are the messages sent before the task, from prelude.txt.
func (s Scenario) Prelude() []string {
	data, err := fs.ReadFile(s.Files, "prelude.txt")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// install copies the scenario's agent directory and workspace into dir.
func (s Scenario) install(dir string) error {
	return fs.WalkDir(s.Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "task.md" || name == "prelude.txt" {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := fs.ReadFile(s.Files, name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

// read reads one file of a scenario.
func (s Scenario) read(name string) string {
	data, err := fs.ReadFile(s.Files, name)
	if err != nil {
		panic(fmt.Sprintf("evals: %s/%s: %v", s.Name, name, err))
	}
	return string(data)
}

func files(name string) fs.FS {
	sub, err := fs.Sub(fixtures, "testdata/scenarios/"+name)
	if err != nil {
		panic(err)
	}
	return sub
}

// Scenarios are the checked-in scenarios, in report order.
func Scenarios() []Scenario {
	return []Scenario{codeSearch(), research(), chore(), council(), history(), inventory()}
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

// word matches phrase in text as whole words, ignoring case.
func word(text, phrase string) bool {
	return regexp.MustCompile(`(?i)(^|[^\w])` + regexp.QuoteMeta(phrase) + `($|[^\w])`).MatchString(text)
}

// mentions checks that answer names every phrase as whole words and none of
// the decoys, and says which one failed.
func mentions(answer string, phrases []string, decoys ...string) (bool, string) {
	for _, p := range phrases {
		if !word(answer, p) {
			return false, fmt.Sprintf("answer does not mention %q", p)
		}
	}
	for _, d := range decoys {
		if word(answer, d) {
			return false, fmt.Sprintf("answer names the decoy %q", d)
		}
	}
	return true, ""
}

// answerLine is the answer's last line starting "ANSWER:", without the
// prefix. The tasks ask for one, so a grader checks the facts there and not
// in the explanation around it, which may name a decoy to rule it out.
func answerLine(answer string) (string, bool) {
	lines := strings.Split(answer, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[i]), "*`"))
		if len(line) >= 7 && strings.EqualFold(line[:7], "ANSWER:") {
			return strings.TrimSpace(line[7:]), true
		}
	}
	return "", false
}

// fields splits an ANSWER line on ";" into trimmed fields.
func fields(line string) []string {
	parts := strings.Split(line, ";")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// need checks that a line has at least n fields.
func need(f []string, n int) (bool, string) {
	if len(f) < n {
		return false, fmt.Sprintf("ANSWER line has %d of %d fields", len(f), n)
	}
	return true, ""
}

// graded runs check on the answer line.
func graded(check func(line string) (bool, string)) func(string, string) (bool, string) {
	return func(answer, _ string) (bool, string) {
		line, ok := answerLine(answer)
		if !ok {
			return false, "no ANSWER: line"
		}
		return check(line)
	}
}

var dates = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// says checks that answer states the number n, as a digit or a word, outside
// any date.
func says(answer string, n int, name string) (bool, string) {
	text := dates.ReplaceAllString(answer, " ")
	if word(text, strconv.Itoa(n)) || (name != "" && word(text, name)) {
		return true, ""
	}
	return false, fmt.Sprintf("answer does not state %d", n)
}

func codeSearch() Scenario {
	const goal = "Find every function in src/ that reads the APP_MODE environment variable, and the mode values the code compares it against."
	answer := "loadMode and startServer read APP_MODE; describeMode only mentions it and legacyModes reads APP_MODES.\nANSWER: src/config.go:loadMode, src/server.go:startServer; modes: dev, prod, test"
	return Scenario{
		Name:  "code-search",
		Files: files("code-search"),
		Grade: graded(func(line string) (bool, string) {
			f := fields(line)
			if ok, why := need(f, 2); !ok {
				return ok, why
			}
			// A decoy anywhere on the line is a wrong answer.
			if ok, why := mentions(line, nil, "describeMode", "legacyModes", "docs.go"); !ok {
				return ok, why
			}
			if ok, why := mentions(f[0], []string{"config.go", "loadMode", "server.go", "startServer"}); !ok {
				return false, "functions: " + why
			}
			if ok, why := mentions(f[1], []string{"dev", "prod", "test"}); !ok {
				return false, "modes: " + why
			}
			return true, ""
		}),
		Script: func(arm Arm) Script {
			work := []Step{
				Calls(Call("search_files", "pattern", "APP_MODE", "path", "src")),
				Calls(Call("read_file", "path", "src/config.go"), Call("read_file", "path", "src/server.go"), Call("read_file", "path", "src/docs.go")),
			}
			if arm.Mode == Direct {
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
	answer := "The 2024 rule (90 days, Finance Operations) is superseded.\nANSWER: 30 days; Data Council; 2026-03-04; corpus/policy-2026.md"
	return Scenario{
		Name:  "research",
		Files: files("research"),
		// The superseded policy's decider is the decoy.
		Grade: graded(func(line string) (bool, string) {
			f := fields(line)
			if ok, why := need(f, 4); !ok {
				return ok, why
			}
			if !thirtyDays.MatchString(f[0]) {
				return false, fmt.Sprintf("period %q is not 30 days", f[0])
			}
			if ok, why := mentions(f[1], []string{"Data Council"}, "Finance Operations"); !ok {
				return false, "decider: " + why
			}
			if ok, why := mentions(f[2], []string{"2026-03-04"}); !ok {
				return false, "date: " + why
			}
			if ok, why := mentions(f[3], []string{"policy-2026.md"}, "policy-2024.md"); !ok {
				return false, "source: " + why
			}
			return true, ""
		}),
		Script: func(arm Arm) Script {
			work := []Step{
				Calls(Call("search_files", "pattern", "retention period", "path", "corpus")),
				Calls(Call("read_file", "path", "corpus/policy-2024.md"), Call("read_file", "path", "corpus/policy-2026.md")),
			}
			if arm.Mode == Direct {
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

var thirtyDays = regexp.MustCompile(`(?i)\b30[- ]?(days?|calendar days)\b|\bthirty days\b`)

var choreNotes = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}

const reviewedLine = "Reviewed: 2026-11-20"

func chore() Scenario {
	const goal = "Append the line \"Reviewed: 2026-11-20\" at the end of every Markdown file in notes/, keeping the rest of each file, and report how many files changed."
	answer := "Every note now ends with Reviewed: 2026-11-20.\nANSWER: 6"
	s := Scenario{Name: "chore", Files: files("chore"), Writes: true}
	s.Grade = func(a, ws string) (bool, string) {
		for _, n := range choreNotes {
			data, err := os.ReadFile(filepath.Join(ws, "notes", n+".md")) //nolint:gosec // a file of the run's own copy of the workspace
			if err != nil {
				return false, err.Error()
			}
			want := strings.TrimRight(s.read("notes/"+n+".md"), "\n") + "\n" + reviewedLine
			if strings.TrimRight(string(data), "\n") != want {
				return false, fmt.Sprintf("notes/%s.md does not end with the reviewed line, or other text changed", n)
			}
		}
		line, ok := answerLine(a)
		if !ok {
			return false, "no ANSWER: line"
		}
		return says(line, 6, "six")
	}
	s.Script = func(arm Arm) Script {
		var reads, writes []ToolUse
		for _, n := range choreNotes {
			p := "notes/" + n + ".md"
			reads = append(reads, Call("read_file", "path", p))
			writes = append(writes, Call("write_file", "path", p, "content", strings.TrimRight(s.read(p), "\n")+"\n"+reviewedLine+"\n"))
		}
		work := []Step{Calls(Call("list_dir", "path", "notes")), Calls(reads...), Calls(writes...)}
		if arm.Mode == Direct {
			return Script{Root: append(work, Say(answer))}
		}
		return Script{
			Root: []Step{
				Calls(Call("subtask", "goal", goal, "variant", "worker", "return_spec", "The number of files changed.")),
				Say(answer),
			},
			Children: map[string][]Step{goal: append(work, Return("ok", "Changed 6 files."))},
		}
	}
	return s
}

func council() Scenario {
	const goal = "Review src/payment.go for bugs. Return each bug with its line number and a one-line fix."
	angles := []string{"correctness", "safety", "clarity"}
	answer := "Two bugs in src/payment.go; use i < len(items) and refuse negative amounts.\nANSWER: 17: off-by-one loop reads past the end; 25: negative refund not refused"
	return Scenario{
		Name:     "council",
		Files:    files("council"),
		Requires: []string{"council"},
		Grade: graded(func(line string) (bool, string) {
			loop, refund := false, false
			for _, f := range fields(line) {
				lower := strings.ToLower(f)
				if loopLine.MatchString(f) && loopBug.MatchString(lower) {
					loop = true
				}
				if refundLine.MatchString(f) && refundBug.MatchString(lower) && !strings.Contains(lower, "fine") && !strings.Contains(lower, "missing test") {
					refund = true
				}
			}
			switch {
			case !loop:
				return false, "no field names the line-17 loop running past the end"
			case !refund:
				return false, "no field names the negative refund (lines 23-26)"
			}
			return true, ""
		}),
		Script: func(arm Arm) Script {
			if arm.Mode == Direct {
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

var (
	loopLine   = regexp.MustCompile(`(?i)(^|[^\d])L?17([^\d]|$)`)
	loopBug    = regexp.MustCompile(`off[- ]by[- ]one|<=\s*len|out of range|out of bounds`)
	refundLine = regexp.MustCompile(`(?i)(^|[^\d])L?2[3-6]([^\d]|$)`)
	refundBug  = regexp.MustCompile(`negative|below zero|<\s*0`)
)

// history needs facts from early in the conversation: a subtask sees the
// last last_n messages, so the arms vary the scribe variant's last_n.
func history() Scenario {
	const goal = "Write notes/release.md with exactly three lines: the codename, the ship date and the owner of the release discussed earlier in this conversation."
	release := "Codename: Juniper\nShip date: 2026-12-05\nOwner: Dana Reyes\n"
	answer := "I wrote notes/release.md with the codename, ship date and owner.\nANSWER: Juniper"
	s := Scenario{Name: "history", Files: files("history"), Writes: true, Requires: []string{"scribe"}}
	// With last_n 6 (the variant's) or 1 the facts are outside what the
	// subtask is given, so a run where its first message held them did not
	// test retrieval. With 20 they are given.
	facts := []string{"Juniper", "Dana Reyes"}
	s.Arms = []Arm{
		{Name: "subtask", Mode: Subtask, LastN: 6, VariesLastN: true, SeedLacks: facts},
		{Name: "subtask last_n=1", Mode: Subtask, LastN: 1, VariesLastN: true, Prepare: setLastN("variants/scribe.md", 1), SeedLacks: facts},
		{Name: "subtask last_n=20", Mode: Subtask, LastN: 20, VariesLastN: true, Prepare: setLastN("variants/scribe.md", 20)},
	}
	s.Grade = func(a, ws string) (bool, string) {
		data, err := os.ReadFile(filepath.Join(ws, "notes", "release.md")) //nolint:gosec // a file of the run's own copy of the workspace
		if err != nil {
			return false, "notes/release.md was not written"
		}
		var lines []string
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) != 3 {
			return false, fmt.Sprintf("notes/release.md has %d lines, want 3", len(lines))
		}
		if ok, why := mentions(string(data), []string{"Juniper", "2026-12-05", "Dana Reyes"}, "Cedar"); !ok {
			return false, "notes/release.md: " + why
		}
		line, ok := answerLine(a)
		if !ok {
			return false, "no ANSWER: line"
		}
		return mentions(line, []string{"Juniper"}, "Cedar")
	}
	s.Script = func(arm Arm) Script {
		var root []Step
		for range s.Prelude() {
			root = append(root, Say("Noted."))
		}
		write := Calls(Call("write_file", "path", "notes/release.md", "content", release))
		if arm.Mode == Direct {
			return Script{Root: append(root, write, Say(answer))}
		}
		return Script{
			Root: append(root,
				Calls(Call("subtask", "goal", goal, "variant", "scribe", "return_spec", "What was written.")),
				Say(answer)),
			Children: map[string][]Step{goal: {
				Calls(Call("parent_history_search", "query", "codename ship date owner correction")),
				write,
				Return("ok", strings.TrimSpace(release)),
			}},
		}
	}
	return s
}

// setLastN rewrites a variant's last_n.
func setLastN(variant string, n int) func(string) error {
	return func(dir string) error {
		p := filepath.Join(dir, filepath.FromSlash(variant))
		data, err := os.ReadFile(p) //nolint:gosec // a file of the run's own copy of the agent directory
		if err != nil {
			return err
		}
		out := regexp.MustCompile(`(?m)^last_n: \d+$`).ReplaceAllString(string(data), "last_n: "+strconv.Itoa(n))
		return os.WriteFile(p, []byte(out), 0o600) //nolint:gosec // the run's own copy of the agent directory
	}
}

var todoLine = regexp.MustCompile(`// TODO\((\w+)\): (.*)`)

// inventory returns a list longer than a small result cap: the arms vary
// SubtaskResultCap, and over the cap the parent has to fetch the stored
// result.
func inventory() Scenario {
	const goal = "List every TODO comment in src/ as file:line: owner: text, one per line."
	s := Scenario{Name: "inventory", Files: files("inventory"), Requires: []string{"research"}}
	s.Arms = []Arm{{Name: "subtask result_cap=1500", Mode: Subtask, ResultCap: 1500, NeedsStored: true,
		Options: func(o *agent.Options) { o.SubtaskResultCap = 1500 }}}
	// The list and the counts, from the fixture.
	var list []string
	counts := map[string]int{}
	names, _ := fs.Glob(s.Files, "src/*.go")
	sort.Strings(names)
	for _, name := range names {
		for i, line := range strings.Split(s.read(name), "\n") {
			if m := todoLine.FindStringSubmatch(line); m != nil {
				list = append(list, fmt.Sprintf("%s:%d: %s: %s", name, i+1, m[1], m[2]))
				counts[m[1]]++
			}
		}
	}
	top := ""
	for o, n := range counts {
		if top == "" || n > counts[top] || (n == counts[top] && o < top) {
			top = o
		}
	}
	total := len(list)
	answer := fmt.Sprintf("%s has %d of them.\nANSWER: %d; %s", top, counts[top], total, top)
	s.Grade = graded(func(line string) (bool, string) {
		f := fields(line)
		if ok, why := need(f, 2); !ok {
			return ok, why
		}
		if ok, why := says(f[0], total, ""); !ok {
			return ok, why
		}
		if !strings.EqualFold(strings.Trim(f[1], " .*`"), top) {
			return false, fmt.Sprintf("owner %q is not %s", f[1], top)
		}
		return true, ""
	})
	s.Script = func(arm Arm) Script {
		search := Calls(Call("search_files", "pattern", `TODO\(`, "path", "src"))
		if arm.Mode == Direct {
			return Script{Root: []Step{search, Say(answer)}}
		}
		return Script{
			Root: []Step{
				Calls(Call("subtask", "goal", goal, "variant", "research", "return_spec", "Every TODO as file:line: owner: text, one per line.")),
				// Over the cap the result is stored and the parent reads it.
				FetchIfStored(Say(answer)),
				Say(answer),
			},
			Children: map[string][]Step{goal: {search, Return("ok", strings.Join(list, "\n"))}},
		}
	}
	return s
}
