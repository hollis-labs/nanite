package classify

import "testing"

func TestScopeTier_String(t *testing.T) {
	cases := []struct {
		tier ScopeTier
		want string
	}{
		{TierTrivial, "trivial"},
		{TierSmall, "small"},
		{TierMedium, "medium"},
		{TierLarge, "large"},
		{TierOpen, "open"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := c.tier.String(); got != c.want {
				t.Fatalf("ScopeTier.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestScopeTier_IsValid(t *testing.T) {
	for _, tier := range []ScopeTier{TierTrivial, TierSmall, TierMedium, TierLarge, TierOpen} {
		if !tier.IsValid() {
			t.Fatalf("tier %v should be valid", tier)
		}
	}
	if ScopeTier(99).IsValid() {
		t.Fatal("ScopeTier(99) should be invalid")
	}
}

func TestExecutionPattern_String(t *testing.T) {
	cases := []struct {
		pattern ExecutionPattern
		want    string
	}{
		{PatternInline, "inline"},
		{PatternSubagent, "subagent"},
		{PatternBackground, "background"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := c.pattern.String(); got != c.want {
				t.Fatalf("ExecutionPattern.String() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExecutionPattern_IsValid(t *testing.T) {
	for _, p := range []ExecutionPattern{PatternInline, PatternSubagent, PatternBackground} {
		if !p.IsValid() {
			t.Fatalf("pattern %v should be valid", p)
		}
	}
	if ExecutionPattern(99).IsValid() {
		t.Fatal("ExecutionPattern(99) should be invalid")
	}
}

func TestIntentSignals_Zero(t *testing.T) {
	var s IntentSignals
	if s.Message != "" || s.MessageTokenEst != 0 || s.HasAttachments || s.ToolsAvailable != 0 {
		t.Fatalf("zero IntentSignals should be all-zero, got %+v", s)
	}
}
