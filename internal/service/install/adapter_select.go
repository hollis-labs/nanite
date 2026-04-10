package install

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// ResolveOpts controls how ResolveAdapters picks the active adapter list.
// Stdin/Stdout are injection points for the interactive prompt (wired in
// Task 10) and are unused in the non-interactive paths.
type ResolveOpts struct {
	Flag        string    // value of --adapters, empty = unset
	NoAdapters  bool      // --no-adapters
	Reconfigure bool      // --reconfigure
	Interactive bool      // isStdinTTY() result, set by the caller
	Stdin       io.Reader // for prompt injection in tests
	Stdout      io.Writer // for prompt output in tests
}

// userSelectableAdapters is the set of adapter slugs that ResolveAdapters
// will accept from user input (CLI flag or prompt). nanite-native is
// excluded — it's loaded internally by the install service and is not a
// CLI integration.
var userSelectableAdapters = []string{"claude", "codex", "gemini", "opencode"}

// ResolveAdapters computes the active adapter list for an install run
// using the priority order:
//
//  1. NoAdapters flag → []
//  2. Flag (--adapters) → parsed list (validated against registry)
//  3. cfg.Adapters present and !Reconfigure → cfg.Adapters as-is
//  4. Detection-then-prompt (interactive) or detection-only (non-interactive)
//
// Returns (resolved, previous, error). The caller is responsible for
// cleanup (using `previous - resolved`) and persistence. ResolveAdapters
// is read-only — it does not write to disk.
func ResolveAdapters(cfg *projectConfig, projectDir string, opts ResolveOpts) (resolved []string, previous []string, err error) {
	if cfg.Adapters != nil {
		previous = append(previous, (*cfg.Adapters)...)
	}

	// 1. --no-adapters flag wins.
	if opts.NoAdapters {
		return []string{}, previous, nil
	}

	// 2. --adapters flag wins.
	if opts.Flag != "" {
		parsed, perr := parseAdapterFlag(opts.Flag)
		if perr != nil {
			return nil, previous, perr
		}
		return parsed, previous, nil
	}

	// 3. Config wins unless --reconfigure.
	if !opts.Reconfigure && cfg.Adapters != nil {
		return previous, previous, nil // resolved == previous (no-op case)
	}

	// 4. Detection (with prompt in interactive mode).
	detected := DetectAdapters(projectDir)
	sort.Strings(detected)

	if opts.Interactive {
		// Prompt integration is wired in Task 10. For now, fall through
		// to detection-only behavior — Task 10 will replace this branch
		// with promptAdapterSelection().
		return detected, previous, nil
	}

	// Non-interactive --reconfigure with existing config: keep current.
	if opts.Reconfigure && cfg.Adapters != nil {
		return previous, previous, nil
	}

	// Non-interactive fresh: use detection result.
	return detected, previous, nil
}

// parseAdapterFlag parses a comma-separated --adapters value, validates
// each slug against the user-selectable adapter list, and returns the
// resulting slice. Returns an error if any slug is unknown or if
// nanite-native is requested (it's not user-selectable).
func parseAdapterFlag(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, raw := range parts {
		slug := strings.TrimSpace(raw)
		if slug == "" {
			continue
		}
		if !isUserSelectable(slug) {
			return nil, fmt.Errorf("unknown adapter %q (available: %s)", slug, strings.Join(userSelectableAdapters, ", "))
		}
		out = append(out, slug)
	}
	return out, nil
}

func isUserSelectable(slug string) bool {
	for _, s := range userSelectableAdapters {
		if s == slug {
			return true
		}
	}
	return false
}
