package agent

import (
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
