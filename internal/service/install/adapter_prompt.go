package install

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// promptInput is the parameter bag for promptAdapterSelection. Bundling
// the inputs into a struct makes the call sites and tests less noisy.
type promptInput struct {
	Detected      []string
	Current       []string
	IsReconfigure bool
	Stdin         io.Reader
	Stdout        io.Writer
}

// promptAdapterSelection runs the interactive adapter-selection prompt
// and returns the chosen list. Re-prompts on invalid input up to 3 times
// before falling back (fresh install: empty list; reconfigure: current).
//
// Behavior matrix:
//
//   - Fresh install, detection found something: prompt Y/n/e
//   - Fresh install, detection found nothing (or e from above): numbered list,
//     empty input → empty list
//   - --reconfigure: numbered list with * marking current, empty input → current
func promptAdapterSelection(in promptInput) ([]string, error) {
	r := bufio.NewReader(in.Stdin)

	// Sort detected for stable display.
	detected := append([]string(nil), in.Detected...)
	sort.Strings(detected)

	// --reconfigure path: skip the Y/n/e shortcut, go straight to the list.
	if in.IsReconfigure {
		return promptNumberedList(in.Stdout, r, in.Current, true)
	}

	// Fresh install with detection: offer the Y/n/e shortcut.
	if len(detected) > 0 {
		fmt.Fprintf(in.Stdout, "Detected CLI tools in this project: %s\n", strings.Join(detected, ", "))
		fmt.Fprintln(in.Stdout, "Manage these with Nanite? [Y]es / [n]o / [e]dit list")
		fmt.Fprint(in.Stdout, "> ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("read prompt: %w", err)
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "", "y", "yes":
			return detected, nil
		case "n", "no":
			return []string{}, nil
		case "e", "edit":
			// Fall through to the numbered list.
		default:
			fmt.Fprintf(in.Stdout, "invalid choice %q, falling through to edit list\n", choice)
		}
	}

	// Fresh install with no detection (or fell through from "e"): numbered list.
	return promptNumberedList(in.Stdout, r, in.Current, false)
}

// promptNumberedList renders the numbered adapter list and reads a
// space-separated index selection. Up to 3 invalid attempts before
// falling back.
//
// If isReconfigure is true:
//   - current adapters are marked with " *"
//   - empty input keeps current
//
// If isReconfigure is false:
//   - no markers
//   - empty input returns []
func promptNumberedList(w io.Writer, r *bufio.Reader, current []string, isReconfigure bool) ([]string, error) {
	currentSet := make(map[string]bool, len(current))
	for _, c := range current {
		currentSet[c] = true
	}

	var marker string
	var emptyHint string
	if isReconfigure {
		fmt.Fprintln(w, "Select CLI tools to manage with Nanite (* = currently enabled):")
		emptyHint = "empty to keep current"
	} else {
		fmt.Fprintln(w, "Select CLI tools to manage with Nanite:")
		emptyHint = "empty for none"
	}
	for i, slug := range userSelectableAdapters {
		marker = ""
		if isReconfigure && currentSet[slug] {
			marker = " *"
		}
		fmt.Fprintf(w, "  %d) %s%s\n", i+1, slug, marker)
	}
	fmt.Fprintf(w, "Enter numbers separated by spaces (%s):\n", emptyHint)

	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(w, "> ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("read prompt: %w", err)
		}
		raw := strings.TrimSpace(line)
		if raw == "" {
			if isReconfigure {
				return current, nil
			}
			return []string{}, nil
		}
		picked, perr := parseNumberSelection(raw)
		if perr != nil {
			fmt.Fprintf(w, "invalid input: %v\n", perr)
			continue
		}
		return picked, nil
	}

	// Fallback after 3 invalid attempts.
	fmt.Fprintln(w, "too many invalid attempts; using fallback")
	if isReconfigure {
		return current, nil
	}
	return []string{}, nil
}

// parseNumberSelection parses a space-separated list of 1-indexed numbers
// referencing userSelectableAdapters and returns the corresponding slugs.
// Returns an error on any unparseable token or out-of-range index.
func parseNumberSelection(raw string) ([]string, error) {
	tokens := strings.Fields(raw)
	out := make([]string, 0, len(tokens))
	seen := make(map[int]bool, len(tokens))
	for _, tok := range tokens {
		n, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("not a number: %q", tok)
		}
		if n < 1 || n > len(userSelectableAdapters) {
			return nil, fmt.Errorf("out of range: %d (valid 1..%d)", n, len(userSelectableAdapters))
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, userSelectableAdapters[n-1])
	}
	return out, nil
}
