package agent

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// workRootInstructionsMax caps the inlined text so a huge AGENTS.md cannot
// flood the instruction file.
const workRootInstructionsMax = 16 * 1024

// workRootInstructions returns the work root's own AGENTS.md (or CLAUDE.md) so
// a CLI session, which runs in the boot dir, still sees the repo's rules (gap
// C1, docs/architecture/cli-planting-gaps.md). It returns "" when the root is
// unset, is a root the CLI deny-list refuses (home, ~/.tether, ...), or has no
// such regular file. The text is data appended under a labelled heading; it
// carries no authority.
func workRootInstructions(root string) string {
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	home, _ := os.UserHomeDir()
	if CLIRootDenyReason(root, home) != "" {
		return ""
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		p := filepath.Join(root, name)
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(f, workRootInstructionsMax+1))
		f.Close()
		truncated := len(raw) > workRootInstructionsMax
		if truncated {
			raw = raw[:workRootInstructionsMax]
			// Cut on a rune boundary so a multi-byte character is not split.
			for len(raw) > 0 && !utf8.Valid(raw) {
				raw = raw[:len(raw)-1]
			}
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}
		if truncated {
			text += "\n\n[truncated: " + name + " is longer than 16 KiB; the rest is not shown here]"
		}
		return "## Instructions from the work root (" + name + ")\n\n" + text
	}
	return ""
}
