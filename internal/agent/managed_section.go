package agent

import (
	"fmt"
	"os"
	"strings"
)

const (
	managedStart  = "<!-- nanite:start -->"
	managedNotice = "<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->"
	managedEnd    = "<!-- nanite:end -->"
)

// buildManagedBlock returns the full managed section block for the given content.
func buildManagedBlock(content string) string {
	return managedStart + "\n" +
		managedNotice + "\n\n" +
		content + "\n\n" +
		managedEnd + "\n"
}

// WriteManagedSection writes content into a Nanite-managed section of a markdown
// file at path. If the file does not exist, it is created with just the managed
// section. If the file exists but has no markers, the managed section is appended.
// If the file already contains markers, the content between them is replaced.
// The operation is idempotent: writing the same content twice produces the same file.
func WriteManagedSection(path string, content string) error {
	block := buildManagedBlock(content)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}

	existing := string(data)

	startIdx := strings.Index(existing, managedStart)
	if startIdx == -1 {
		// No existing markers — append, ensuring a separating newline.
		sep := ""
		if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
			sep = "\n"
		}
		return os.WriteFile(path, []byte(existing+sep+block), 0o644)
	}

	// Find managedEnd *after* managedStart to ensure correct pairing.
	endIdx := strings.Index(existing[startIdx:], managedEnd)
	if endIdx == -1 {
		// Start marker without end marker — append fresh block.
		sep := ""
		if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
			sep = "\n"
		}
		return os.WriteFile(path, []byte(existing+sep+block), 0o644)
	}
	endIdx += startIdx // convert to absolute index

	// Replace everything from managedStart through managedEnd (inclusive).
	before := existing[:startIdx]
	after := existing[endIdx+len(managedEnd):]

	// Trim a single leading newline from `after` so we don't accumulate blank lines.
	after = strings.TrimPrefix(after, "\n")

	var b strings.Builder
	b.WriteString(before)
	b.WriteString(block)
	if after != "" {
		b.WriteString("\n")
		b.WriteString(after)
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ReadManagedSection returns the content between the Nanite-managed markers in the
// file at path. The markers and notice line are stripped. Returns an empty string
// if no markers are found. Returns an error if the file cannot be read.
func ReadManagedSection(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	text := string(data)

	startIdx := strings.Index(text, managedStart)
	if startIdx == -1 {
		return "", nil
	}

	// Find managedEnd after managedStart to ensure correct pairing.
	contentStart := startIdx + len(managedStart)
	relEnd := strings.Index(text[contentStart:], managedEnd)
	if relEnd == -1 {
		return "", nil
	}

	// Inner content: everything after managedStart line up to managedEnd.
	inner := text[contentStart : contentStart+relEnd]

	// Strip the notice line if present.
	inner = strings.ReplaceAll(inner, managedNotice, "")

	// Trim surrounding whitespace/newlines.
	inner = strings.TrimSpace(inner)

	return inner, nil
}

// RemoveManagedSection strips the Nanite-managed section (markers and
// content between them) from the file at path, preserving any user
// content outside the markers.
//
// Returns:
//   - removedAny: true if a managed section was found and removed
//   - becameEmpty: true if the file is empty (or whitespace-only) after removal
//   - err: I/O or parse errors
//
// If the file does not exist, returns (false, false, nil) — no-op.
// If the file has no managed-section markers, returns (false, false, nil)
// and leaves the file untouched.
func RemoveManagedSection(path string) (removedAny bool, becameEmpty bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}

	existing := string(data)

	startIdx := strings.Index(existing, managedStart)
	if startIdx == -1 {
		return false, false, nil
	}

	endIdx := strings.Index(existing[startIdx:], managedEnd)
	if endIdx == -1 {
		// Start marker without end marker — leave the file alone (we don't
		// know where the section ends, so we can't safely strip it).
		return false, false, nil
	}
	endIdx += startIdx + len(managedEnd)

	before := existing[:startIdx]
	after := existing[endIdx:]

	// Trim all leading newlines from `after` — the "\n\n" padding injected
	// below is the sole source of separation between user content before
	// and after the removed block. This prevents triple-newline output
	// when both sides have content.
	after = strings.TrimLeft(after, "\n")

	// Trim trailing whitespace from `before` so we don't leave dangling
	// blank lines either.
	before = strings.TrimRight(before, "\n")
	if before != "" && after != "" {
		before += "\n\n"
	} else if before != "" {
		before += "\n"
	}

	combined := before + after
	trimmed := strings.TrimSpace(combined)
	if trimmed == "" {
		// Whole file is empty after removal.
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			return false, false, fmt.Errorf("truncate %s: %w", path, err)
		}
		return true, true, nil
	}

	if err := os.WriteFile(path, []byte(combined), 0o644); err != nil {
		return false, false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, false, nil
}
