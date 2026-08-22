package agent

import "testing"

// TestValidateSlug_AcceptsLegitimateSlugs pins that ValidateSlug's pattern
// matches internal/builders/agent_builder.go's wizard-side slugRegexp
// exactly — every slug the wizard already accepts today must still be
// accepted here (see slug.go's doc comment on why this is a second
// compiled instance of the same pattern rather than a shared import).
func TestValidateSlug_AcceptsLegitimateSlugs(t *testing.T) {
	for _, slug := range []string{
		"a",
		"atlas",
		"atlas-curator",
		"code-reviewer",
		"a1b2c3",
		"agent-007",
	} {
		if err := ValidateSlug(slug); err != nil {
			t.Errorf("ValidateSlug(%q) = %v, want nil", slug, err)
		}
	}
}

// TestValidateSlug_RejectsUnsafeSlugs is GO-AGENT-001's core regression: any
// slug that could traverse or otherwise escape a filesystem-path join must
// be rejected by format alone, before pathsafe.ResolveUnder is ever
// consulted.
func TestValidateSlug_RejectsUnsafeSlugs(t *testing.T) {
	for _, slug := range []string{
		"",
		"../evil",
		"..",
		"../../etc/passwd",
		"a/b",
		"/etc/passwd",
		"..%2f..%2fevil",
		"%2e%2e%2fevil",
		"UPPER",
		"with space",
		"under_score",
		"-leading-hyphen",
		"trailing-hyphen-",
		"double--hyphen",
		"slug.with.dots",
		"slug\x00null",
	} {
		if err := ValidateSlug(slug); err == nil {
			t.Errorf("ValidateSlug(%q) = nil, want a rejection error", slug)
		}
	}
}
