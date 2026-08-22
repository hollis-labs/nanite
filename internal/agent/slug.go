package agent

import (
	"fmt"
	"regexp"
)

// slugPattern is the canonical URL-safe slug allow-list: lowercase letters,
// digits, and single hyphens between segments. Kept in lock-step, character
// for character, with internal/builders/agent_builder.go's wizard-side
// slugRegexp — that file's Validator is the origin of this pattern and stays
// untouched (it is already correct); this is a second compiled instance of
// the identical pattern string rather than an import of internal/builders,
// so that internal/agent (a foundational package many others already depend
// on) does not take on a dependency on internal/builders (a UI-wizard-layer
// package that itself depends on internal/store). See
// TASKS/audit-remediation/03-agent-slug-traversal/01-canonical-slug-path-validation.md's
// Work Log for the full placement rationale. If this pattern ever needs to
// change, update both copies together.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateSlug rejects any slug that is not a bare, URL-safe identifier —
// lowercase letters, digits, and hyphens only, no path separators, no `..`,
// no leading/trailing hyphen. This is the single canonical gate every
// caller that turns a caller-supplied slug into a filesystem path (agent
// profile, durable-agent config, or any similar identifier elsewhere) must
// call before doing so. See GO-AGENT-001.
func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must contain only lowercase letters, digits, and hyphens (e.g. %q), got %q", "code-reviewer", slug)
	}
	return nil
}
