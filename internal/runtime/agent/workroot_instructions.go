package agent

import (
	"os"
	"path/filepath"
	"strings"
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
		buf := make([]byte, workRootInstructionsMax+1)
		n, _ := f.Read(buf)
		f.Close()
		text := strings.TrimSpace(string(buf[:n]))
		if text == "" {
			continue
		}
		if n > workRootInstructionsMax {
			text = strings.TrimSpace(string(buf[:workRootInstructionsMax])) + "\n\n[truncated at 16 KiB]"
		}
		return "## Instructions from the work root (" + name + ")\n\n" + text
	}
	return ""
}
