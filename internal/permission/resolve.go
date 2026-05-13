// Package permission — resolve.go implements workspace-relative permission
// pattern resolution (CW-20260512-0120, SP-20260512-0010 Wave 1).
//
// Permission Rule.Pattern fields may use the `./` prefix to mean "relative to
// the session's working_dir". At session start (or profile-load time) the
// rule set is resolved against the concrete working_dir, yielding a new rule
// set whose `./`-prefixed patterns have been canonicalized to absolute paths.
// Patterns without the `./` prefix pass through unchanged (back-compat with
// absolute patterns and with non-path patterns such as shell-command
// substrings like `rm -rf`).
//
// Sharp edges enforced here:
//
//   - `./..` and any other traversal that would escape the working_dir is
//     rejected at resolution time. The whole RuleSet.Resolve call errors out
//     so the caller sees the misconfiguration loudly instead of silently
//     widening the agent's reach.
//
//   - Symlinks in the working_dir prefix are resolved to canonical paths via
//     filepath.EvalSymlinks. This closes the symlink-based scope-escape vector
//     (a working_dir symlinked into /etc/ would otherwise let a `./**` grant
//     reach /etc/**). When the working_dir does NOT exist on disk yet, we
//     fall back to filepath.Abs + filepath.Clean rather than failing — tests
//     and forward-looking config tooling need to be able to resolve patterns
//     against not-yet-materialized directories.
//
//   - The glob suffix on the input pattern (`./**`, `./generated/**`,
//     `./generated/foo.go`) is preserved verbatim after substitution. Glob
//     matching is the consumer's job; this resolver only handles the prefix
//     swap + traversal/symlink hardening.
//
// Downstream consumers of the resolved patterns:
//
//   - W2 / CW-20260512-0118 (path_grants visibility) renders the resolved
//     absolute paths into the workspace/permissions slot so the agent reads
//     concrete paths in its summary.
//
//   - W3 / CW-20260512-0119 (subagent deny forwarding) forwards parent
//     denies — including resolved `./` patterns — into spawned children as
//     their canonical absolute form. Children inherit the same resolved
//     surface their parent operates against, not the un-resolved literal.
package permission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workspaceRelativePrefix is the literal pattern prefix that triggers
// working_dir-relative resolution. Patterns NOT starting with this prefix
// (including absolute `/path/**`, glob-only `dev_*`, or shell command
// substrings) pass through Resolve unchanged.
const workspaceRelativePrefix = "./"

// ErrPatternEscapesWorkingDir is returned when a `./`-prefixed pattern
// resolves to a path outside the working_dir, e.g. `./..` or
// `./../sibling/**`. The whole RuleSet.Resolve call returns this error so
// the caller (session-start wiring) surfaces the misconfiguration loudly
// rather than silently widening the agent's scope.
var ErrPatternEscapesWorkingDir = errors.New("permission: workspace-relative pattern escapes working_dir")

// ErrEmptyWorkingDir is returned when Resolve is called with an empty
// working_dir but the RuleSet contains at least one `./`-prefixed pattern
// that would need resolution. Patterns without the prefix tolerate an
// empty working_dir.
var ErrEmptyWorkingDir = errors.New("permission: working_dir is required to resolve workspace-relative patterns")

// Resolve returns a new RuleSet whose rules have had any `./`-prefixed
// Pattern fields rewritten to absolute paths rooted at workingDir. The
// receiver is not mutated.
//
// Resolution rules per Rule.Pattern:
//
//   - "" or no `./` prefix: copied verbatim. Absolute patterns
//     ("/Users/.../project/**"), shell-command substrings ("rm -rf"), and
//     bare tool-name globs all flow through unchanged.
//
//   - `./<rest>`: rewritten to `<canonical-workingDir>/<rest>`. The glob
//     suffix on `<rest>` (e.g. `**`, `generated/**`, `foo.go`) is preserved
//     verbatim — pattern semantics are the consumer's job, this layer only
//     swaps the prefix.
//
//   - `./..` or `./../...`: rejected with ErrPatternEscapesWorkingDir. The
//     traversal check runs against the canonicalized result, so symlink-
//     based escape attempts are caught the same way as literal `..`.
//
// workingDir resolution: if the directory exists on disk, its canonical
// path is computed via filepath.EvalSymlinks (closes the symlink-based
// scope-escape vector). When the directory does not yet exist, the resolver
// falls back to filepath.Abs + filepath.Clean — required for tests, fresh-
// install bootstrap flows, and forward-looking config tooling that
// resolves patterns before the working_dir is materialized.
//
// Source provenance: each resolved Rule retains its original Source tag, so
// downstream diagnostics (permission summary, deny-forwarding chains) can
// trace a resolved pattern back to the YAML file or in-memory caller that
// produced it.
func (rs *RuleSet) Resolve(workingDir string) (*RuleSet, error) {
	if rs == nil {
		return nil, nil
	}

	// Fast path — no `./` prefixes in the set means we never need to
	// touch the working_dir; just return a shallow copy so the caller
	// can treat the result as a fresh value without aliasing the input.
	if !rs.HasWorkspaceRelative() {
		out := &RuleSet{Mode: rs.Mode, Rules: make([]Rule, len(rs.Rules))}
		copy(out.Rules, rs.Rules)
		return out, nil
	}

	// At least one rule needs working_dir resolution — require a non-empty
	// working_dir or fail loudly. An empty working_dir would resolve `./**`
	// to `/` and silently grant the agent the entire filesystem.
	if strings.TrimSpace(workingDir) == "" {
		return nil, ErrEmptyWorkingDir
	}

	canonical, err := canonicalizeWorkingDir(workingDir)
	if err != nil {
		return nil, fmt.Errorf("permission: canonicalize working_dir %q: %w", workingDir, err)
	}

	out := &RuleSet{Mode: rs.Mode, Rules: make([]Rule, 0, len(rs.Rules))}
	for i := range rs.Rules {
		resolved, err := rs.Rules[i].ResolvePattern(canonical)
		if err != nil {
			return nil, err
		}
		out.Rules = append(out.Rules, resolved)
	}
	return out, nil
}

// HasWorkspaceRelative reports whether the RuleSet contains at least one
// rule whose Pattern uses the `./` workspace-relative prefix. Used by the
// fast path in Resolve and by callers that want to skip resolve-time
// validation entirely when no rule needs it.
func (rs *RuleSet) HasWorkspaceRelative() bool {
	if rs == nil {
		return false
	}
	for i := range rs.Rules {
		if IsWorkspaceRelativePattern(rs.Rules[i].Pattern) {
			return true
		}
	}
	return false
}

// IsWorkspaceRelativePattern reports whether pattern uses the workspace-
// relative `./` prefix. Bare "." or "./" alone count as workspace-relative
// (resolving to the working_dir itself).
func IsWorkspaceRelativePattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "." {
		return true
	}
	return strings.HasPrefix(pattern, workspaceRelativePrefix)
}

// ResolvePattern returns a copy of the Rule with its Pattern resolved
// against canonicalWorkingDir when the pattern uses the `./` prefix. The
// receiver is not mutated.
//
// canonicalWorkingDir is assumed to already be absolute, cleaned, and
// symlink-resolved — call canonicalizeWorkingDir on raw input before
// calling this method directly. RuleSet.Resolve handles canonicalization
// on the caller's behalf.
//
// Returns ErrPatternEscapesWorkingDir if the resolved path is not within
// canonicalWorkingDir (catches `./..`, `./../sibling/`, and any other
// traversal shape).
func (r Rule) ResolvePattern(canonicalWorkingDir string) (Rule, error) {
	out := r
	if !IsWorkspaceRelativePattern(r.Pattern) {
		return out, nil
	}
	if canonicalWorkingDir == "" {
		return out, ErrEmptyWorkingDir
	}

	// Strip the workspace-relative prefix. A bare "." or "./" leaves an
	// empty rest, which resolves to the working_dir itself (a `read: ["."]`
	// or `read: ["./"]` grant is the equivalent of "anything under here"
	// without a glob suffix).
	rest := strings.TrimPrefix(r.Pattern, workspaceRelativePrefix)
	if r.Pattern == "." {
		rest = ""
	}

	// Split off any glob suffix (`**`, `*.go`, `[abc]*`, etc.) so we can
	// validate the literal-path portion against working_dir containment
	// without filepath.Clean folding `**` into something surprising. The
	// glob suffix is identified by the first occurrence of a glob meta
	// character; everything before it is treated as a literal-path prefix
	// for the containment check.
	literalPrefix, globSuffix := splitGlob(rest)

	// Resolve the literal portion against the working_dir. filepath.Join
	// applies filepath.Clean which collapses `..` so `./..` becomes the
	// parent dir; the post-join containment check catches that.
	literalAbs := filepath.Join(canonicalWorkingDir, literalPrefix)

	if !isWithin(canonicalWorkingDir, literalAbs) {
		return out, fmt.Errorf(
			"%w: pattern %q resolves to %q (outside %q)",
			ErrPatternEscapesWorkingDir, r.Pattern, literalAbs, canonicalWorkingDir,
		)
	}

	// Reattach the glob suffix verbatim. We don't filepath.Clean here
	// because the glob meta characters are valid in the consumer-side
	// matcher (matchPathGlob) and Clean would mangle them.
	if globSuffix == "" {
		out.Pattern = literalAbs
	} else {
		// filepath.Join already added the appropriate separator between
		// canonicalWorkingDir and literalPrefix; we just need to glue the
		// glob suffix on with a separator if literalPrefix was non-empty
		// (otherwise the suffix sits directly at the working_dir root).
		sep := string(filepath.Separator)
		if literalPrefix == "" {
			out.Pattern = literalAbs + sep + globSuffix
		} else {
			out.Pattern = literalAbs + sep + globSuffix
		}
	}
	return out, nil
}

// canonicalizeWorkingDir produces an absolute, cleaned, symlink-resolved
// version of workingDir suitable for use as the prefix in pattern
// resolution. When the directory does not exist on disk (typical for fresh
// bootstrap flows or test fixtures) it falls back to filepath.Abs +
// filepath.Clean — the symlink-resolution path can only run against an
// extant directory.
//
// Tilde expansion is intentionally handled at a higher layer (the session
// boot code in internal/runtime/agent passes a fully-expanded absolute
// path). Surfacing `~` at this depth would couple the permission package
// to the home-dir resolver and the absolutize helper in path_grants.go;
// keep the contract narrow.
func canonicalizeWorkingDir(workingDir string) (string, error) {
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)

	// Try to resolve symlinks. If the directory doesn't exist yet, fall
	// back to the abs/clean form — sessions that resolve patterns before
	// materializing the working_dir (e.g. forward-looking config tooling)
	// shouldn't be blocked here.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil
		}
		return "", err
	}
	return resolved, nil
}

// isWithin reports whether candidate is the working_dir itself or a
// descendant of it. Both inputs are expected to be absolute + cleaned.
//
// The check uses HasPrefix against the working_dir with a trailing
// separator appended so that `/foo` doesn't match `/foobar`. An exact
// match (candidate == workingDir) counts as within — `./` alone is a
// valid pattern meaning "the working_dir itself".
func isWithin(workingDir, candidate string) bool {
	if workingDir == "" || candidate == "" {
		return false
	}
	if candidate == workingDir {
		return true
	}
	sep := string(filepath.Separator)
	withSep := workingDir
	if !strings.HasSuffix(withSep, sep) {
		withSep += sep
	}
	return strings.HasPrefix(candidate, withSep)
}

// splitGlob splits a rest-of-pattern (after the `./` prefix has been
// stripped) into a literal-path prefix and a glob suffix. The split point
// is the first path segment that contains a glob meta character (`*`,
// `?`, `[`).
//
// Examples (rest → literalPrefix, globSuffix):
//
//   - "" → "", ""
//   - "**" → "", "**"
//   - "generated/**" → "generated", "**"
//   - "generated/foo.go" → "generated/foo.go", ""
//   - "src/*.go" → "src", "*.go"
//   - "deep/nested/path/*.md" → "deep/nested/path", "*.md"
//
// The literal portion is what we validate against working_dir containment
// (filepath.Join + isWithin); the glob suffix is reattached verbatim to
// the resolved absolute path. Glob matching is the consumer's job
// (matchPathGlob in rules.go).
func splitGlob(rest string) (literalPrefix, globSuffix string) {
	if rest == "" {
		return "", ""
	}
	segments := strings.Split(rest, "/")
	literalEnd := -1
	for i, seg := range segments {
		if containsGlobMeta(seg) {
			literalEnd = i
			break
		}
	}
	if literalEnd == -1 {
		// No glob meta anywhere — entire rest is literal.
		return rest, ""
	}
	literalPrefix = strings.Join(segments[:literalEnd], "/")
	globSuffix = strings.Join(segments[literalEnd:], "/")
	return literalPrefix, globSuffix
}

// containsGlobMeta reports whether s contains any glob meta character
// that would break filepath.Clean / filepath.Join semantics.
func containsGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}
