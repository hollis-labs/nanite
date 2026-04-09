package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
)

// agentrcHeading matches any markdown heading ("#" through "######") whose
// label is exactly "agentrc" (case-insensitive), optionally with trailing
// whitespace. Captures the heading hashes in group 1.
var agentrcHeading = regexp.MustCompile(`(?im)^(#{1,6})\s+agentrc\s*$`)

// RemoveAgentrcSection removes a legacy `## agentrc` (or any heading level)
// section from a markdown file along with its body — everything up to the
// next heading of equal-or-higher level or EOF. Returns the cleaned content
// and the removed section (for snapshotting). If no agentrc section exists,
// returns the input unchanged and an empty removed string.
func RemoveAgentrcSection(content string) (cleaned string, removed string) {
	match := agentrcHeading.FindStringIndex(content)
	if match == nil {
		return content, ""
	}

	headingStart := match[0]
	headingEnd := match[1]

	// Determine the heading level from the matched text.
	headingLine := content[headingStart:headingEnd]
	level := 0
	for _, r := range headingLine {
		if r != '#' {
			break
		}
		level++
	}

	// Find the next heading of level <= current, or EOF.
	after := content[headingEnd:]
	siblingPattern := regexp.MustCompile(fmt.Sprintf(`(?m)^#{1,%d}\s`, level))
	siblingMatch := siblingPattern.FindStringIndex(after)

	var sectionEnd int
	if siblingMatch == nil {
		sectionEnd = len(content)
	} else {
		sectionEnd = headingEnd + siblingMatch[0]
	}

	removed = content[headingStart:sectionEnd]

	// Trim trailing whitespace from the preceding content so we don't leave
	// a dangling blank line or accumulated "\n\n\n".
	pre := strings.TrimRight(content[:headingStart], "\n")
	if pre != "" {
		pre += "\n"
	}

	post := content[sectionEnd:]
	if post == "" {
		return pre, removed
	}

	// If the remaining content starts with newlines, normalize to a single
	// separator so the join doesn't double up.
	post = strings.TrimLeft(post, "\n")
	if post == "" {
		return pre, removed
	}
	return pre + "\n" + post, removed
}

// CLAUDESnapshotOpts controls where snapshot files are written.
type CLAUDESnapshotOpts struct {
	Dir string
}

// CLAUDEUpdateReport summarizes what UpdateCLAUDEmd did.
type CLAUDEUpdateReport struct {
	Created               bool
	RemovedAgentrcSection bool
}

// UpdateCLAUDEmd writes managedContent into the nanite:start/end section of
// the CLAUDE.md at path. If the file doesn't exist, it's created with just
// the managed section. If a legacy `## agentrc` section is present, it's
// removed and (if snap is provided) snapshotted to
// {snap.Dir}/removed-claude-section.md before the managed section is added.
func UpdateCLAUDEmd(path string, managedContent string, snap *CLAUDESnapshotOpts) (*CLAUDEUpdateReport, error) {
	report := &CLAUDEUpdateReport{}

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Delegate to the helper — it creates a fresh file with just the block.
		if err := agent.WriteManagedSection(path, managedContent); err != nil {
			return nil, fmt.Errorf("write managed section to fresh file: %w", err)
		}
		report.Created = true
		return report, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read CLAUDE.md: %w", err)
	}

	content := string(existing)

	// Remove any legacy agentrc section.
	cleaned, removed := RemoveAgentrcSection(content)
	if removed != "" {
		report.RemovedAgentrcSection = true
		if snap != nil && snap.Dir != "" {
			if err := os.MkdirAll(snap.Dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir snapshot dir: %w", err)
			}
			snapshotPath := filepath.Join(snap.Dir, "removed-claude-section.md")
			if err := os.WriteFile(snapshotPath, []byte(removed), 0o644); err != nil {
				return nil, fmt.Errorf("write removed-claude-section snapshot: %w", err)
			}
		}
	}

	// Write cleaned content back only if it changed.
	if cleaned != content {
		if err := os.WriteFile(path, []byte(cleaned), 0o644); err != nil {
			return nil, fmt.Errorf("write cleaned CLAUDE.md: %w", err)
		}
	}

	// Delegate the managed-block write to the existing helper. It handles
	// missing markers, existing markers, and malformed (start-without-end)
	// markers by appending a fresh block.
	if err := agent.WriteManagedSection(path, managedContent); err != nil {
		return nil, fmt.Errorf("write managed section: %w", err)
	}

	return report, nil
}
