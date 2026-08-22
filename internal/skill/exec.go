package skill

// exec.go — TASKS/skills/08
// (TASKS/skills/08-rebuild-inline-marker-and-scripts-execution.md):
// docs/engineering/architecture/20-skills.md's "Materialization pipeline"
// section's "Inline deterministic execution" and "Scripts" bullets. This
// file is a fresh build, not a resurrection of internal/skill/context.go
// (deleted whole by TASKS/skills/01) — see "What changed from the old
// marker" below for the recap this task's own context section asked to
// keep here rather than in a now-deleted file's comments.
//
// # What changed from the old marker
//
// The old `` !`([^`]+)` `` regex (context.go:16, now gone) matched anywhere
// in a skill's Prompt body with zero awareness of markdown structure. A
// documentation example inside a fenced code block containing the literal
// text `` !`some command` `` executed identically to a real marker — a
// real accidental-execution bug, confirmed (not hypothetical) by this
// batch's planning session. It also shelled out directly
// (`exec.Command("/bin/sh", "-c", cmd)`, context.go:41) with no
// sandboxing, policy, or allowlist of any kind.
//
// This file fixes both problems structurally:
//
//  1. FindInlineMarkers only reports a marker when the line it's on is
//     outside any fenced (```/~~~) code block — see "Fence tracking"
//     below for the scanner's exact rules and its deliberate scope limits.
//  2. Every marker or scripts/ file execution in this file is a black-box
//     call through GatedExecutor — TASKS/skills/09's not-yet-landed
//     sandbox/capability-policy gate. There is no `exec.Command` (or any
//     equivalent direct-subprocess-spawn call) anywhere in this file; see
//     exec_test.go's TestExec_NoDirectSubprocessSpawn for the static proof
//     this file's own Done-means requires.
//
// # Fence tracking
//
// classifyFenceLines is a minimal, line-by-line scanner — not a full
// CommonMark implementation, per this task's own explicit "doesn't need to
// be a full markdown AST, just enough to track fence open/close state"
// instruction. It tracks exactly one piece of state (in-fence vs.
// out-of-fence, plus the opening fence's char/length) and:
//
//   - Recognizes an opening fence as a line whose first non-space run
//     (0-3 leading spaces tolerated) is 3+ backticks or 3+ tildes.
//   - Recognizes a closing fence as a line, while already in-fence, whose
//     fence run uses the *same* character and is *at least as long* as the
//     opener's, with nothing but trailing whitespace after it — matching
//     CommonMark's own closing-fence rule closely enough for this
//     scanner's stated purpose.
//   - Never scans a fence delimiter line itself for markers (a delimiter
//     line essentially never contains one, and excluding it costs
//     nothing).
//   - Deliberately does NOT track 4-space-indented code blocks (a
//     different CommonMark block type) or single-backtick inline code
//     spans — out of this task's stated scope ("track fence state —
//     inside vs. outside a ```/~~~ block").
//
// # Marker scope: one line, not the whole body
//
// FindInlineMarkers matches markerPattern against each fence-eligible line
// independently, never across a line boundary. The old flat-regex
// implementation technically allowed a match to span multiple lines
// (`[^`]` matches `\n`), but every real convention this batch found —
// this project's own retired marker, and the public Agent-Skills-spec
// (`` !`git diff HEAD` `` on its own line) — writes a marker as a single
// line. Restricting matches to one line is also what makes fence-awareness
// itself well-defined per-line, so this is a deliberate tightening, not an
// accidental behavior change.
//
// # Scripts: an explicit, named invocation — not a bulk unconditional run
//
// The real Agent-Skills-spec convention for scripts/ (confirmed against
// the public Claude Code skills docs during this task, not assumed) is
// that a script is "executed, not loaded" — it runs only when something
// explicitly names it, either via an inline `` !`...` `` marker in the
// body referencing the script's path directly (e.g.
// `` !`python3 ${CLAUDE_SKILL_DIR}/scripts/visualize.py .` ``) or via the
// agent's own tool call naming the exact command line. There is no
// spec convention for "run every file under scripts/ unconditionally at
// materialization time" — this file does not invent one. ExecuteScript is
// instead a first-class, explicit-invocation entry point: a future caller
// (the still-unbuilt end-to-end Materializer that ties this file, task 06's
// Resolver, and task 07's compose.go together, or TASKS/skills/11's
// `skill_get` self-tool) names one of a package's declared Scripts entries
// plus the exact command line to run it with, and this file validates and
// gates that specific execution. A marker that itself references a
// scripts/ path (the first form above) runs through ResolveInlineMarkers
// exactly like any other marker — this file does not special-case that
// case, since mechanically it already is just a marker.
//
// One real, load-bearing constraint this decision is built around:
// internal/skillvendor.Store.Write always stages files at mode 0644
// (store.go's stageFiles) and Store.Path's own contract treats the
// returned directory as read-only, so a vendored script is never
// executable on disk and this package must never chmod it to work around
// that. ExecuteScript's command parameter must therefore already be a
// complete, self-sufficient invocation (an interpreter included where the
// script needs one, e.g. "sh", "scripts/run.sh" or "python3",
// "scripts/run.py") — this file never infers or injects one.
import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/safego"
)

// DefaultMarkerTimeout bounds a single inline `` !`cmd` `` marker
// execution, matching the old marker's own 10-second precedent
// (context.go's dynamicContextTimeout) — still the right default for a
// short, deterministic "compute this value" command.
const DefaultMarkerTimeout = 10 * time.Second

// DefaultScriptTimeout bounds a single scripts/ file execution. Scripts
// are real, potentially heavier work (a package's own shipped tool, not a
// one-line shell computation) so this default is deliberately longer than
// DefaultMarkerTimeout rather than reusing one value for both — both
// ResolveInlineMarkers and ExecuteScript accept an ExecOptions.Timeout
// override per call for a caller that needs something different from
// either default.
const DefaultScriptTimeout = 60 * time.Second

// ExecKind identifies whether a GatedExecutor request originated from an
// inline marker parsed out of a SKILL.md body, or from an explicit
// scripts/ file invocation. Carried through purely for attribution/logging
// on both sides of the gate (this file's own error messages, and task 09's
// gate decision log, matching docs/engineering/architecture/20-skills.md's
// note that a policy decision should be attributable) — it does not change
// how this file builds a request.
type ExecKind string

const (
	ExecKindMarker ExecKind = "inline-marker"
	ExecKindScript ExecKind = "script"
)

// ExecRequest is what this file hands to a GatedExecutor for every marker
// or script execution.
type ExecRequest struct {
	// SkillSlug identifies the skill this execution belongs to. This is
	// the skill's slug (Definition.Slug / store.Skill.Slug), the same
	// identifier internal/store/agent_known_skills.go's AgentKnownSkill
	// grant rows are keyed on (its SkillName column holds a slug, per
	// TASKS/skills/02's Work Log — "keyed by the Skill's slug") — not
	// store.Skill.ID's opaque row identifier. A GatedExecutor
	// implementation (task 09) looks up the invoking agent's grant row by
	// (AgentID, SkillSlug), so this must be the slug for that lookup to
	// find anything.
	SkillSlug string
	// AgentID is the invoking agent's ID — the other half of the
	// (AgentID, SkillSlug) grant-row lookup key task 09's gate performs.
	AgentID string
	// Command is the full argv to execute — e.g.
	// []string{"/bin/sh", "-c", "git diff HEAD"} for an inline marker, or
	// a caller-supplied interpreter-prefixed argv for a script (see this
	// file's package doc, "Scripts", for why a script's argv can't rely on
	// the vendored file's own executable bit). This package never invokes
	// anything itself — Command is inert data until a GatedExecutor turns
	// it into a real subprocess.
	Command []string
	// WorkDir is the directory the command should execute in — the
	// skill's vendored package directory for a script, or whatever working
	// directory the caller resolved for a marker (matching the old
	// ResolveDynamicContext's own workingDir parameter).
	WorkDir string
	// Kind and Label are attribution only (see ExecKind's doc comment).
	// Label is the marker's command text for ExecKindMarker, or the
	// script's package-relative path for ExecKindScript.
	Kind  ExecKind
	Label string
}

// ExecResult is what a GatedExecutor returns on success.
type ExecResult struct {
	Stdout string
	Stderr string
}

// GatedExecutor is the black-box interface this file calls into for every
// marker or script execution — TASKS/skills/09's sandbox/capability-policy
// gate, not yet landed as of this file's own task (08 is Wave 5; 09 is
// Wave 6, per TASKS/skills/README.md's parallelization plan). Per this
// task's own scope boundary with 09 ("this task defines what gets executed
// ... and when. Task 09 defines how it's executed safely"), this file
// treats GatedExecutor purely as an injected dependency — production code
// never constructs a request and executes it itself; only a real
// GatedExecutor implementation (or a test double, in test files only) may
// ever reach an actual subprocess.
//
// A real implementation is expected to honor ctx cancellation/deadline by
// killing its own underlying subprocess (e.g. via exec.CommandContext) —
// this file's own timeout enforcement (runGated) depends on that
// contract: it derives a deadline context from a call's Timeout and hands
// it to ExecuteGated, but can only *stop waiting* on the gate at that
// deadline, not force a cooperating-but-slow gate to actually stop
// running. See runGated's doc comment for the exact mechanics.
//
// Deliberately does not carry an ExecProfile/sandbox.Profile-shaped
// parameter, unlike this task file's own illustrative placeholder
// signature — TASKS/skills/09's own task file (read directly, not
// guessed) is explicit that the gate derives its sandbox profile itself
// from the (AgentID, SkillSlug) grant row's CapabilitiesGranted column,
// not from a profile the caller hands it. Passing one in here would let a
// caller dictate its own sandboxing, which is exactly what the
// policy/sandbox gate exists to prevent.
type GatedExecutor interface {
	ExecuteGated(ctx context.Context, req ExecRequest) (ExecResult, error)
}

// ExecOptions configures a single ResolveInlineMarkers or ExecuteScript
// call.
type ExecOptions struct {
	// Timeout overrides the relevant default (DefaultMarkerTimeout or
	// DefaultScriptTimeout) for this call. Zero means "use the default."
	Timeout time.Duration
}

// ExecutionError is returned when a marker or script execution fails,
// including on timeout — never silently substituted with empty output.
// Typed (rather than a bare fmt.Errorf) so a caller can attribute a
// failure to the specific skill and specific marker/script, matching this
// project's "don't fail open, don't fail silent" discipline and
// resolver.go's own MissingSkillParameterError precedent.
type ExecutionError struct {
	// Skill is the skill slug/label this execution was for.
	Skill string
	// Kind and Label identify exactly which marker or script failed —
	// Label is the marker's command text, or the script's relative path.
	Kind  ExecKind
	Label string
	// Line is the 1-based line number the marker was found on. Zero for a
	// script execution (a script isn't tied to a specific body line).
	Line int
	// Err is the underlying cause: the gate's own error, or a timeout.
	Err error
}

func (e *ExecutionError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("skill %s: %s %q (line %d): %v", e.Skill, e.Kind, e.Label, e.Line, e.Err)
	}
	return fmt.Sprintf("skill %s: %s %q: %v", e.Skill, e.Kind, e.Label, e.Err)
}

func (e *ExecutionError) Unwrap() error { return e.Err }

// markerPattern matches `` !`command` `` markers — the same shape the old,
// now-deleted context.go used (`` !`([^`]+)` ``). Applied per fence-eligible
// line only (see FindInlineMarkers), never against the whole body at once.
var markerPattern = regexp.MustCompile("!`([^`]+)`")

// fenceRunPattern matches a fence delimiter line's leading run: up to 3
// spaces of indentation, then 3+ backticks or 3+ tildes.
var fenceRunPattern = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// InlineMarker is one real (non-fenced) `` !`cmd` `` marker found in a
// SKILL.md body. Start/End are byte offsets into the original body string
// (End exclusive) — the exact span ResolveInlineMarkers replaces with the
// command's output.
type InlineMarker struct {
	Command string
	Start   int
	End     int
	// Line is the 1-based line number the marker was found on, for
	// attribution in ExecutionError.
	Line int
}

// lineSpan is one line of a body string, plus the byte offset in the
// original string where that line begins (excluding any line-ending
// characters).
type lineSpan struct {
	text  string
	start int
}

// splitLines splits body into lineSpans, preserving each line's original
// byte offset. Handles a final line with no trailing newline. Does not
// strip "\r" — callers trim it themselves where it would affect matching
// (fence detection), since InlineMarker's own offsets must stay exact
// against the untouched original body.
func splitLines(body string) []lineSpan {
	spans := make([]lineSpan, 0, strings.Count(body, "\n")+1)
	start := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			spans = append(spans, lineSpan{text: body[start:i], start: start})
			start = i + 1
		}
	}
	spans = append(spans, lineSpan{text: body[start:], start: start})
	return spans
}

// fenceDelimiter reports whether line (already "\r"-trimmed) opens or
// could close a fence, returning the fence character and run length when
// it does.
func fenceDelimiter(line string) (char byte, length int, ok bool) {
	m := fenceRunPattern.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	run := m[1]
	return run[0], len(run), true
}

// isClosingFence reports whether line (already "\r"-trimmed) closes a fence
// opened with openChar/openLen — same character, run length at least
// openLen, and nothing but trailing whitespace after the run (matching
// CommonMark's closing-fence rule: no info string on a closing line).
func isClosingFence(line string, openChar byte, openLen int) bool {
	m := fenceRunPattern.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	run := m[1]
	if run[0] != openChar || len(run) < openLen {
		return false
	}
	rest := strings.TrimSpace(line[len(m[0]):])
	return rest == ""
}

// classifyFenceLines returns, for each of spans, whether that line is
// eligible for marker scanning: true outside any fenced code block, false
// for every line inside a fence (including both delimiter lines
// themselves). See this file's package doc, "Fence tracking," for the
// exact rules and this scanner's deliberate scope limits.
func classifyFenceLines(spans []lineSpan) []bool {
	eligible := make([]bool, len(spans))
	inFence := false
	var fenceChar byte
	var fenceLen int

	for i, ln := range spans {
		trimmed := strings.TrimRight(ln.text, "\r")
		if !inFence {
			if char, length, ok := fenceDelimiter(trimmed); ok {
				inFence = true
				fenceChar = char
				fenceLen = length
				eligible[i] = false
				continue
			}
			eligible[i] = true
			continue
		}

		eligible[i] = false
		if isClosingFence(trimmed, fenceChar, fenceLen) {
			inFence = false
		}
	}
	return eligible
}

// FindInlineMarkers returns every real `` !`cmd` `` marker in body — every
// occurrence outside a fenced (```/~~~) code block. A marker whose literal
// text appears inside a fenced code block (a documentation example, most
// commonly) is never returned — this is the exact regression this task
// exists to fix; see exec_test.go's TestFindInlineMarkers_CodeFenceAwareness.
func FindInlineMarkers(body string) []InlineMarker {
	spans := splitLines(body)
	eligible := classifyFenceLines(spans)

	var markers []InlineMarker
	for i, ln := range spans {
		if !eligible[i] {
			continue
		}
		for _, idx := range markerPattern.FindAllStringSubmatchIndex(ln.text, -1) {
			markers = append(markers, InlineMarker{
				Command: ln.text[idx[2]:idx[3]],
				Start:   ln.start + idx[0],
				End:     ln.start + idx[1],
				Line:    i + 1,
			})
		}
	}
	return markers
}

// ResolveInlineMarkers replaces every real (non-fenced) `` !`cmd` `` marker
// in body with the trimmed output of executing that command through gate —
// matching the old ResolveDynamicContext's own ReplaceAllStringFunc-style
// substitution behavior, but gated and fence-aware. body is not necessarily
// def.Prompt verbatim: a future end-to-end Materializer may call this after
// task 07's compose.go has already spliced inline dependencies in, so body
// is accepted as its own parameter rather than always read off def.
//
// Returns body unchanged, without ever calling gate, when it contains no
// markers at all — matching resolver.go's own "nothing dynamic is ever
// looked up when nothing needs it" convention.
//
// The first marker that fails to execute (including on timeout) aborts the
// whole call and returns a *ExecutionError — this file never substitutes
// empty output for a failed marker (the old marker's own behavior; see this
// file's package doc), matching this project's "don't fail open, don't
// fail silent" discipline.
func ResolveInlineMarkers(ctx context.Context, gate GatedExecutor, def Definition, agentID, workDir, body string, opts ExecOptions) (string, error) {
	markers := FindInlineMarkers(body)
	if len(markers) == 0 {
		return body, nil
	}
	if gate == nil {
		return "", fmt.Errorf("skill: %s: body has %d inline marker(s) but no GatedExecutor was configured", skillLabel(def), len(markers))
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultMarkerTimeout
	}

	var out strings.Builder
	last := 0
	for _, m := range markers {
		out.WriteString(body[last:m.Start])

		req := ExecRequest{
			SkillSlug: def.Slug,
			AgentID:   agentID,
			Command:   []string{"/bin/sh", "-c", m.Command},
			WorkDir:   workDir,
			Kind:      ExecKindMarker,
			Label:     m.Command,
		}
		res, err := runGated(ctx, gate, req, timeout)
		if err != nil {
			return "", &ExecutionError{Skill: skillLabel(def), Kind: ExecKindMarker, Label: m.Command, Line: m.Line, Err: err}
		}
		out.WriteString(strings.TrimSpace(res.Stdout))
		last = m.End
	}
	out.WriteString(body[last:])
	return out.String(), nil
}

// declaresScript reports whether rel (package-root-relative, e.g.
// "scripts/run.sh") appears in def.Scripts — the frontmatter's own
// declared list, already confirmed by internal/skillinstall's
// DefaultValidator (TASKS/skills/04) to exist in the package at install
// time. Compared path-cleaned so "scripts/run.sh" and "scripts/./run.sh"
// are treated the same.
func declaresScript(def Definition, rel string) bool {
	clean := path.Clean(filepath.ToSlash(rel))
	for _, s := range def.Scripts {
		if path.Clean(filepath.ToSlash(s)) == clean {
			return true
		}
	}
	return false
}

// ExecuteScript runs command — an explicit, caller-built invocation of one
// of a skill package's declared scripts/ files — gated through the same
// GatedExecutor every marker execution routes through. See this file's
// package doc, "Scripts," for why command must already be a complete,
// self-sufficient invocation (interpreter included where needed) rather
// than a bare script path this function could exec directly.
//
// pkgDir is the skill's live vendored package directory (e.g. from
// internal/skillvendor.Store.Path) — this function does not read the
// vendored store itself, keeping it decoupled from that package the same
// way ResolveInlineMarkers is decoupled from Definition.Prompt.
// scriptRelPath must both (a) appear in def.Scripts and (b) exist as a
// real, non-directory file under pkgDir — a caller cannot use this entry
// point to run an undeclared or nonexistent file under the guise of
// "script execution." scriptRelPath is resolved via internal/pathsafe so a
// path-traversal attempt is rejected before ever reaching the gate.
func ExecuteScript(ctx context.Context, gate GatedExecutor, def Definition, agentID, pkgDir, scriptRelPath string, command []string, opts ExecOptions) (string, error) {
	if !declaresScript(def, scriptRelPath) {
		return "", fmt.Errorf("skill: %s: script %q is not declared in this package's scripts: frontmatter", skillLabel(def), scriptRelPath)
	}
	resolved, err := pathsafe.ResolveUnder(pkgDir, scriptRelPath)
	if err != nil {
		return "", fmt.Errorf("skill: %s: resolve script path %q: %w", skillLabel(def), scriptRelPath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("skill: %s: script %q not found in vendored package: %w", skillLabel(def), scriptRelPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("skill: %s: script %q is a directory, not a file", skillLabel(def), scriptRelPath)
	}
	if len(command) == 0 {
		return "", fmt.Errorf("skill: %s: script %q: empty command", skillLabel(def), scriptRelPath)
	}
	if gate == nil {
		return "", fmt.Errorf("skill: %s: script %q: no GatedExecutor configured", skillLabel(def), scriptRelPath)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultScriptTimeout
	}

	req := ExecRequest{
		SkillSlug: def.Slug,
		AgentID:   agentID,
		Command:   command,
		WorkDir:   pkgDir,
		Kind:      ExecKindScript,
		Label:     scriptRelPath,
	}
	res, err := runGated(ctx, gate, req, timeout)
	if err != nil {
		return "", &ExecutionError{Skill: skillLabel(def), Kind: ExecKindScript, Label: scriptRelPath, Err: err}
	}
	return res.Stdout, nil
}

// runGated calls gate.ExecuteGated with a timeout-bounded child context,
// and — critically — never blocks this function's own caller past timeout
// even if gate itself ignores ctx cancellation and keeps running. It does
// this by running the gate call on its own goroutine and racing it against
// execCtx.Done(): a well-behaved gate (expected to build its subprocess via
// exec.CommandContext(execCtx, ...) or equivalent) is killed promptly by
// execCtx's own cancellation once this function returns and its deferred
// cancel() fires; a gate that doesn't cooperate leaves an abandoned
// goroutine (bounded: it writes once to a buffered channel and exits) but
// still can't make this function — or its caller — wait past timeout. This
// mirrors the old marker's own select-on-time.After pattern (context.go's
// runContextCommand), adapted for a gate this file no longer owns the
// underlying process of.
func runGated(ctx context.Context, gate GatedExecutor, req ExecRequest, timeout time.Duration) (ExecResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		res ExecResult
		err error
	}
	done := make(chan outcome, 1)
	safego.Go(context.Background(), "skill.exec.runGated", func() {
		res, err := gate.ExecuteGated(execCtx, req)
		done <- outcome{res: res, err: err}
	})

	select {
	case o := <-done:
		return o.res, o.err
	case <-execCtx.Done():
		return ExecResult{}, fmt.Errorf("timed out after %s: %w", timeout, execCtx.Err())
	}
}
