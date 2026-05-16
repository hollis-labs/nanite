package bootprofile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// varPattern matches `{{name}}` literals with optional surrounding
// whitespace inside the braces. Names are alphanumeric / underscore /
// dot to match Tether-side variable naming conventions
// (e.g. `{{identity.work_root}}`).
var varPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`)

// Substitute replaces every `{{name}}` literal in src with the matching
// value from vars. Names not present in vars produce an error — the
// "unknown variable" policy is strict so a typo in the catalog can't
// silently land an empty section in the boot prompt.
//
// Decision pinned in TestSubstitute_UnknownVariableErrors: leaving the
// literal in place was rejected because it would persist a half-rendered
// `{{var}}` token into the LLM context, which is observable but easy to
// miss in review. An explicit error surfaced through Compile lands the
// failure in the operator's lap immediately.
//
// Variable name resolution: vars is consulted as-is; the compiler
// pre-populates vars with the profile's Identity fields under both flat
// keys (`work_root`, `tracking_root`, ...) and dotted keys
// (`identity.work_root`, ...) so authors can use either style.
func Substitute(src string, vars Vars) (string, error) {
	if !strings.Contains(src, "{{") {
		return src, nil
	}
	var missing []string
	out := varPattern.ReplaceAllStringFunc(src, func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		if v, ok := vars[name]; ok {
			return v
		}
		missing = append(missing, name)
		return match
	})
	if len(missing) > 0 {
		// dedupe + sort so the error message is deterministic.
		seen := map[string]struct{}{}
		var uniq []string
		for _, m := range missing {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			uniq = append(uniq, m)
		}
		sort.Strings(uniq)
		return out, fmt.Errorf("bootprofile: unknown variable(s) in template: %s", strings.Join(uniq, ", "))
	}
	return out, nil
}

// resolveSlot is the compile-time slot resolver. It handles the safe,
// pure-IO subset (`text`, `static`). All other slot types are surfaced
// to the caller as Requirement entries on the LaunchSpec — see compiler.go
// for the dispatch.
//
// The return tuple is (resolved-content, requirement, error):
//
//   - resolved-content set, requirement nil    → fully resolved here.
//   - resolved-content empty, requirement set  → caller must defer.
//   - error non-nil                            → fatal, abort the compile.
//
// All file paths are resolved relative to catalogRoot when not absolute
// and not ~-prefixed, matching Tether's bootgen behavior.
//
// CW-20260515-0024: the mechanical file/inline IO is delegated to the
// shared go-agent-context resolvers via agentcontext_adapter.go. The
// `text` slot routes through the shared inline resolver and a
// single-file `static` slot routes through the shared static_file
// resolver. Nanite-specific behavior — strict `{{var}}` substitution,
// the `### filename` directory concat format, and the deferred
// Requirement model — stays in this file.
func resolveSlot(name string, src SlotSource, catalogRoot string, vars Vars) (string, *Requirement, error) {
	switch src.Type {
	case "text":
		// Mechanical inline-content resolution via the shared resolver;
		// Nanite's strict {{var}} substitution is applied on top.
		raw, err := resolveInlineViaShared(name, src.Content)
		if err != nil {
			return "", nil, fmt.Errorf("slot %q: %w", name, err)
		}
		content, err := Substitute(raw, vars)
		if err != nil {
			return "", nil, fmt.Errorf("slot %q: %w", name, err)
		}
		return content, nil, nil
	case "static":
		content, err := resolveStatic(name, src, catalogRoot)
		if err != nil {
			return "", nil, fmt.Errorf("slot %q: %w", name, err)
		}
		// Static slot bodies may also contain `{{var}}` literals — file
		// contents are templated the same way inline text is.
		content, err = Substitute(content, vars)
		if err != nil {
			return "", nil, fmt.Errorf("slot %q: %w", name, err)
		}
		return content, nil, nil
	case "":
		return "", nil, fmt.Errorf("slot %q: missing 'type' field", name)
	case "role_summary", "skill_index", "cmd", "http":
		// Deferred: surface as a Requirement so the launch-time
		// resolver (CW-20260514-0048) can pick them up. The compiler
		// stays pure — no shell, no network, no role-discovery scan.
		return "", requirementFromSlot(name, src), nil
	default:
		return "", nil, fmt.Errorf("slot %q: unknown source type %q (supported: text, static, role_summary, skill_index, cmd, http)", name, src.Type)
	}
}

// resolveStatic reads a file or globs a directory under the catalog
// root. Globbing matches Tether's bootgen semantics for behavioral
// compatibility — glob defaults to "*.md", Limit caps the match count,
// and matches are concatenated with the "### filename" + "---" pattern
// so the resulting markdown is readable when injected into a slot.
//
// CW-20260515-0024: the single-file branch delegates the os.ReadFile +
// tilde/relative path resolution to the shared go-agent-context
// static_file resolver. Nanite still owns the empty-catalog-root guard
// (a stricter, reproducibility-driven rule the shared resolver does not
// enforce) and the directory-glob `### filename` concat format (an
// app-specific layout pinned by Nanite tests and downstream prompt
// rendering — the shared static_dir resolver emits a plain "\n\n" join
// without the per-file headings).
func resolveStatic(slotName string, src SlotSource, catalogRoot string) (string, error) {
	if src.Path == "" {
		return "", fmt.Errorf("static slot missing 'path' field")
	}
	path, err := resolvePath(src.Path, catalogRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		// Single-file read via the shared static_file resolver. The
		// path is already absolute here (resolvePath expanded ~ and
		// rebased relatives onto catalogRoot), so the shared resolver's
		// own path handling is a no-op pass-through and the read
		// semantics are identical to the previous os.ReadFile.
		return resolveStaticFileViaShared(slotName, path, catalogRoot)
	}
	glob := src.Glob
	if glob == "" {
		glob = "*.md"
	}
	matches, err := filepath.Glob(filepath.Join(path, glob))
	if err != nil {
		return "", fmt.Errorf("glob %s/%s: %w", path, glob, err)
	}
	sort.Strings(matches) // deterministic order across OSes
	limit := src.Limit
	if limit <= 0 || limit > len(matches) {
		limit = len(matches)
	}
	var parts []string
	for _, m := range matches[:limit] {
		b, readErr := os.ReadFile(m) //nolint:gosec // catalog-sourced path
		if readErr != nil {
			return "", fmt.Errorf("read %s: %w", m, readErr)
		}
		parts = append(parts, fmt.Sprintf("### %s\n\n%s", filepath.Base(m), string(b)))
	}
	return strings.Join(parts, "\n\n---\n\n"), nil
}

// resolvePath expands ~ and resolves relative paths against the catalog
// root. Env-var expansion is intentionally NOT applied here — the
// compiler must be reproducible, and ${VANTA_URL}-style references
// belong in deferred slot types (http) anyway.
//
// Two failure modes return an explicit error rather than silently
// producing a wrong path (PR #169 round 1):
//
//   - ~-prefixed path when os.UserHomeDir() fails: previously the
//     unresolved "~/..." literal was joined onto catalogRoot, which
//     silently pointed at the wrong file.
//   - relative path with empty catalogRoot: previously fell through to
//     the process working directory, breaking the "resolved relative
//     to catalog root" contract and making compile non-reproducible.
func resolvePath(path, catalogRoot string) (string, error) {
	if path == "" {
		return path, nil
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: home directory unavailable: %w", path, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		if catalogRoot == "" {
			return "", fmt.Errorf("resolve relative path %q: catalog root unset (caller must pass an absolute path or a catalog root)", path)
		}
		path = filepath.Join(catalogRoot, path)
	}
	return path, nil
}

// requirementFromSlot lifts a SlotSource onto a Requirement, dropping
// the inline `Content` / `Path`-as-static / `Glob` / `Limit` knobs that
// the deferred resolver does not need. Keeping Requirement narrow so
// future extraction can stabilize its shape independent of SlotSource.
func requirementFromSlot(name string, src SlotSource) *Requirement {
	r := &Requirement{
		Slot: name,
		Type: src.Type,
	}
	switch src.Type {
	case "cmd":
		r.Run = src.Run
		r.Timeout = src.Timeout
	case "http":
		r.URL = src.URL
		r.ResponseFormat = src.ResponseFormat
	case "role_summary":
		r.Path = src.Path
	case "skill_index":
		r.Limit = src.Limit
	}
	return r
}
