package permission

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDeriveSubagentRuleSet_ParentDenyForwarded asserts the H1 acceptance:
// a parent with deny /sensitive/** dispatching a subagent that tries to
// grant /sensitive/sub/ → subagent's effective set still denies the path.
// This is the load-bearing case the ticket calls out as the spec acceptance.
func TestDeriveSubagentRuleSet_ParentDenyForwarded(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/sensitive/**", Behavior: DecisionDeny, Source: "parent profile"},
		},
	}
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/sensitive/sub/", Behavior: DecisionAllow, Source: "subagent profile"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{
		Parent:   parent,
		Subagent: subagent,
	})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// Evaluate a path under the denied subtree — the derived set's
	// Evaluate must return Deny (deny > allow priority preserved).
	result := derived.Evaluate("dev_read", map[string]any{"path": "/Users/u/sensitive/sub/secret.txt"})
	if result == nil {
		t.Fatal("expected derived ruleset to evaluate the path, got nil")
	}
	if result.Decision != DecisionDeny {
		t.Errorf("expected deny (parent's /sensitive/** wins over subagent's /sensitive/sub/), got %s", result.Decision)
	}
	if result.MatchedRule == nil {
		t.Fatal("expected matched rule, got nil")
	}
	if !strings.Contains(result.MatchedRule.Source, "(via parent)") {
		t.Errorf("expected matched deny to be tagged as forwarded from parent, got Source=%q", result.MatchedRule.Source)
	}
}

// TestDeriveSubagentRuleSet_SubagentAllowsDoNotBypassParentDenies confirms
// the rule even when the subagent uses an EXACT pattern match the parent
// would have denied via glob.
func TestDeriveSubagentRuleSet_SubagentAllowsDoNotBypassParentDenies(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_*", Pattern: "/etc/**", Behavior: DecisionDeny, Source: "parent"},
		},
	}
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/etc/hosts", Behavior: DecisionAllow, Source: "subagent"},
			{Tool: "dev_read", Pattern: "/etc/passwd", Behavior: DecisionAllow, Source: "subagent"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent, Subagent: subagent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	for _, path := range []string{"/etc/hosts", "/etc/passwd", "/etc/anything"} {
		result := derived.Evaluate("dev_read", map[string]any{"path": path})
		if result == nil || result.Decision != DecisionDeny {
			t.Errorf("path %s: expected deny, got %v", path, result)
		}
	}
}

// TestDeriveSubagentRuleSet_LegitimateSubagentGrantsStand confirms the
// "don't over-restrict" sharp edge: a subagent's allow that doesn't
// conflict with any parent deny still works.
func TestDeriveSubagentRuleSet_LegitimateSubagentGrantsStand(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/secrets/**", Behavior: DecisionDeny, Source: "parent"},
		},
	}
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/project_a/**", Behavior: DecisionAllow, Source: "subagent"},
			{Tool: "dev_write", Pattern: "/Users/u/project_a/build/**", Behavior: DecisionAllow, Source: "subagent"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent, Subagent: subagent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// Legitimate subagent grant — no parent conflict.
	result := derived.Evaluate("dev_read", map[string]any{"path": "/Users/u/project_a/main.go"})
	if result == nil {
		t.Fatal("expected a match against the subagent allow, got nil")
	}
	if result.Decision != DecisionAllow {
		t.Errorf("expected allow for non-conflicting subagent grant, got %s", result.Decision)
	}

	// Parent deny still bites the secrets path.
	result = derived.Evaluate("dev_read", map[string]any{"path": "/Users/u/secrets/keys.txt"})
	if result == nil || result.Decision != DecisionDeny {
		t.Errorf("expected deny under /secrets, got %v", result)
	}
}

// TestDeriveSubagentRuleSet_ParentAllowsDoNotPropagate confirms that
// allows from the parent are NOT forwarded. The subagent's scope is
// decided by its own profile.
func TestDeriveSubagentRuleSet_ParentAllowsDoNotPropagate(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/big/area/**", Behavior: DecisionAllow, Source: "parent"},
		},
	}
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/big/area/docs/**", Behavior: DecisionAllow, Source: "subagent"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent, Subagent: subagent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// Count allows in the derived set — should be exactly the subagent's 1.
	var allowCount int
	for _, r := range derived.Rules {
		if r.Behavior == DecisionAllow {
			allowCount++
			if !strings.Contains(r.Source, "subagent") {
				t.Errorf("unexpected allow source %q — parent allows should not propagate", r.Source)
			}
		}
	}
	if allowCount != 1 {
		t.Errorf("expected exactly 1 allow (subagent's own), got %d", allowCount)
	}
}

// TestDeriveSubagentRuleSet_AsksForwarded confirms ask-rules forward
// (encoding parent-level "prompt before X" intent into the subagent).
func TestDeriveSubagentRuleSet_AsksForwarded(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_bash", Pattern: "rm -rf", Behavior: DecisionAsk, Source: "parent"},
		},
	}
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_bash", Pattern: "git push --force", Behavior: DecisionAsk, Source: "subagent"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent, Subagent: subagent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// Both asks should be present in the derived set.
	gotParentAsk := false
	gotSubagentAsk := false
	for _, r := range derived.Rules {
		if r.Behavior != DecisionAsk {
			continue
		}
		if strings.Contains(r.Source, "parent (via parent)") {
			gotParentAsk = true
		}
		if r.Source == "subagent" {
			gotSubagentAsk = true
		}
	}
	if !gotParentAsk {
		t.Error("expected forwarded parent ask, missing")
	}
	if !gotSubagentAsk {
		t.Error("expected subagent's own ask, missing")
	}
}

// TestDeriveSubagentRuleSet_DefaultSafetyDeniesAppended confirms the
// reserved hook for future Plan-Mode-style port. Currently unused in
// production wiring; this test asserts the hook works when wired.
func TestDeriveSubagentRuleSet_DefaultSafetyDeniesAppended(t *testing.T) {
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/docs/**", Behavior: DecisionAllow, Source: "subagent"},
		},
	}
	safety := []Rule{
		{Tool: "task", Pattern: "*", Behavior: DecisionDeny, Source: "default safety policy"},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{
		Subagent:            subagent,
		DefaultSafetyDenies: safety,
	})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	result := derived.Evaluate("task", map[string]any{"path": "any"})
	if result == nil || result.Decision != DecisionDeny {
		t.Errorf("expected safety deny on `task`, got %v", result)
	}
}

// TestDeriveSubagentRuleSet_NilInputs confirms the nil-safe contract.
func TestDeriveSubagentRuleSet_NilInputs(t *testing.T) {
	derived, err := DeriveSubagentRuleSet(DerivationInput{})
	if err != nil {
		t.Fatalf("derive on empty input: %v", err)
	}
	if derived == nil {
		t.Fatal("expected non-nil empty RuleSet, got nil")
	}
	if len(derived.Rules) != 0 {
		t.Errorf("expected zero rules from empty input, got %d", len(derived.Rules))
	}
}

// TestDeriveSubagentRuleSet_ModeFallback confirms the Mode resolution:
// subagent wins, parent fallback.
func TestDeriveSubagentRuleSet_ModeFallback(t *testing.T) {
	cases := []struct {
		name       string
		parentMode Mode
		subMode    Mode
		want       Mode
	}{
		{"subagent_wins", ModeDefault, ModePlan, ModePlan},
		{"parent_fallback", ModeAcceptEdits, "", ModeAcceptEdits},
		{"both_unset", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &RuleSet{Mode: tc.parentMode}
			s := &RuleSet{Mode: tc.subMode}
			d, err := DeriveSubagentRuleSet(DerivationInput{Parent: p, Subagent: s})
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			if d.Mode != tc.want {
				t.Errorf("mode: got %q, want %q", d.Mode, tc.want)
			}
		})
	}
}

// TestDeriveSubagentRuleSet_GlobNormalizationParentDirDeniesChildPath is
// the "path semantics" sharp edge from the ticket. A parent's deny on
// /sensitive/** must propagate to children matched by the SAME glob —
// the derivation forwards the rule verbatim and the matcher in
// matchPathGlob handles the rest.
func TestDeriveSubagentRuleSet_GlobNormalizationParentDirDeniesChildPath(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/sensitive/**", Behavior: DecisionDeny, Source: "parent"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	for _, candidate := range []string{
		"/Users/u/sensitive/file.txt",
		"/Users/u/sensitive/sub/file.txt",
		"/Users/u/sensitive/sub/deep/nested/file.txt",
	} {
		result := derived.Evaluate("dev_read", map[string]any{"path": candidate})
		if result == nil || result.Decision != DecisionDeny {
			t.Errorf("path %s: expected deny via /sensitive/** glob, got %v", candidate, result)
		}
	}
}

// TestDeriveSubagentRuleSet_ThreeLevelChain is the load-bearing test
// asserted by the ticket's "Test fixture: 3-level subagent chain — top
// denies propagate to leaf". Parent → Child → Grandchild; deny set at
// the top-level parent must appear in the grandchild's effective denies.
//
// The chain is simulated by composing derive calls: parent_rules →
// derived_for_child → derived_for_grandchild. Each call uses the
// previous step's output as the new parent.
func TestDeriveSubagentRuleSet_ThreeLevelChain(t *testing.T) {
	topParent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/top-secret/**", Behavior: DecisionDeny, Source: "top-parent profile"},
		},
	}

	// Top parent dispatches a child subagent with its own rules.
	childProfile := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/child-area/**", Behavior: DecisionAllow, Source: "child profile"},
		},
	}
	derivedForChild, err := DeriveSubagentRuleSet(DerivationInput{
		Parent:   topParent,
		Subagent: childProfile,
	})
	if err != nil {
		t.Fatalf("derive child: %v", err)
	}

	// Child dispatches a grandchild subagent with its own rules.
	grandchildProfile := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/u/grandchild-area/**", Behavior: DecisionAllow, Source: "grandchild profile"},
			// Grandchild *tries* to grant a path the top-parent denies.
			{Tool: "dev_read", Pattern: "/Users/u/top-secret/leaked.txt", Behavior: DecisionAllow, Source: "grandchild profile"},
		},
	}
	derivedForGrandchild, err := DeriveSubagentRuleSet(DerivationInput{
		Parent:   derivedForChild,
		Subagent: grandchildProfile,
	})
	if err != nil {
		t.Fatalf("derive grandchild: %v", err)
	}

	// 1) Top deny propagated through two derivations to the grandchild.
	result := derivedForGrandchild.Evaluate("dev_read", map[string]any{"path": "/Users/u/top-secret/leaked.txt"})
	if result == nil {
		t.Fatal("expected grandchild's eval to match the top-parent's deny")
	}
	if result.Decision != DecisionDeny {
		t.Errorf("top-secret deny did NOT propagate to grandchild — decision=%s, rule=%+v", result.Decision, result.MatchedRule)
	}

	// 2) Source provenance shows the chain depth — the top deny ended up
	//    tagged "<orig> (via parent) (via parent)" after two forwarding
	//    steps, so operators can read the chain depth from the rendered
	//    summary.
	if result.MatchedRule == nil {
		t.Fatal("expected matched rule on grandchild deny eval")
	}
	suffixCount := strings.Count(result.MatchedRule.Source, parentSourceSuffix)
	if suffixCount != 2 {
		t.Errorf("expected 2 `(via parent)` suffix applications after 3-level chain, got %d in Source=%q",
			suffixCount, result.MatchedRule.Source)
	}

	// 3) Grandchild's own legitimate grant still works (sanity for the
	//    "don't over-restrict" sharp edge across multiple levels).
	result = derivedForGrandchild.Evaluate("dev_read", map[string]any{"path": "/Users/u/grandchild-area/local.go"})
	if result == nil || result.Decision != DecisionAllow {
		t.Errorf("grandchild's own legitimate grant blocked — got %v", result)
	}
}

// TestDeriveSubagentRuleSet_W4IntegrationCanonicalForwarding asserts the
// W4 integration shape: subagent `./` patterns are resolved against the
// child's working_dir before merging, and parent patterns are forwarded
// verbatim (they should already be resolved per the W4 contract).
func TestDeriveSubagentRuleSet_W4IntegrationCanonicalForwarding(t *testing.T) {
	parentWorkingDir := t.TempDir()
	childWorkingDir := t.TempDir()

	// Parent's rules — already in canonical absolute form (per W4 contract).
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: filepath.Join(parentWorkingDir, "sensitive") + "/**", Behavior: DecisionDeny, Source: "parent.yaml"},
		},
	}

	// Subagent profile uses `./` patterns that should resolve against the
	// CHILD's working_dir, not the parent's.
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "./local/**", Behavior: DecisionAllow, Source: "subagent.yaml"},
			{Tool: "dev_write", Pattern: "./build/**", Behavior: DecisionAllow, Source: "subagent.yaml"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{
		Parent:             parent,
		ParentWorkingDir:   parentWorkingDir,
		Subagent:           subagent,
		SubagentWorkingDir: childWorkingDir,
	})
	if err != nil {
		t.Fatalf("derive with W4 integration: %v", err)
	}

	// Parent deny present verbatim (absolute path).
	gotParentDeny := false
	for _, r := range derived.Rules {
		if r.Behavior != DecisionDeny {
			continue
		}
		if strings.Contains(r.Pattern, "sensitive") && filepath.IsAbs(r.Pattern) {
			gotParentDeny = true
		}
	}
	if !gotParentDeny {
		t.Errorf("parent deny pattern not forwarded as absolute path; rules=%+v", derived.Rules)
	}

	// Subagent allows resolved against CHILD working_dir.
	// EvalSymlinks may resolve the temp dir to a different prefix
	// (e.g. /var/folders/... vs /private/var/folders/...), so canonicalize
	// before comparison.
	canonicalChildDir, err := canonicalizeWorkingDir(childWorkingDir)
	if err != nil {
		t.Fatalf("canonicalize child working dir: %v", err)
	}
	for _, r := range derived.Rules {
		if r.Behavior != DecisionAllow {
			continue
		}
		if strings.HasPrefix(r.Pattern, "./") {
			t.Errorf("subagent `./` pattern not resolved: %q", r.Pattern)
		}
		if !strings.HasPrefix(r.Pattern, canonicalChildDir) {
			t.Errorf("subagent pattern %q not anchored at child working_dir %q", r.Pattern, canonicalChildDir)
		}
	}
}

// TestDeriveSubagentRuleSet_SubagentWorkspaceRelativeNeedsWorkingDir
// confirms that workspace-relative subagent patterns require
// SubagentWorkingDir.
func TestDeriveSubagentRuleSet_SubagentWorkspaceRelativeNeedsWorkingDir(t *testing.T) {
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "./local/**", Behavior: DecisionAllow, Source: "subagent.yaml"},
		},
	}
	_, err := DeriveSubagentRuleSet(DerivationInput{Subagent: subagent})
	if err == nil {
		t.Error("expected error when subagent has `./` patterns but no SubagentWorkingDir, got nil")
	}
}

// TestDeriveSubagentRuleSet_ParentSourceProvenancePreserved confirms each
// forwarded parent rule has its original Source preserved with the
// parentSourceSuffix appended — this is the load-bearing W2 integration
// (the rendered summary attributes denies to their lineage).
func TestDeriveSubagentRuleSet_ParentSourceProvenancePreserved(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/etc/**", Behavior: DecisionDeny, Source: "parent profile.yaml"},
			{Tool: "dev_bash", Pattern: "rm -rf", Behavior: DecisionAsk, Source: "parent profile.yaml"},
		},
	}

	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	for _, r := range derived.Rules {
		if !strings.HasPrefix(r.Source, "parent profile.yaml") {
			t.Errorf("forwarded rule lost its original Source prefix: %q", r.Source)
		}
		if !strings.HasSuffix(r.Source, parentSourceSuffix) {
			t.Errorf("forwarded rule missing parent-suffix: %q", r.Source)
		}
	}
}

// TestDeriveSubagentRuleSet_EmptySourcGetsParentTag — defensive check
// that forwarded rules with empty Source still render as "parent (via
// parent)" rather than dropping into the renderer's "unknown source"
// branch (which would lose lineage information).
func TestDeriveSubagentRuleSet_EmptySourceGetsParentTag(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/foo/**", Behavior: DecisionDeny},
		},
	}
	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(derived.Rules) != 1 {
		t.Fatalf("expected 1 forwarded rule, got %d", len(derived.Rules))
	}
	if !strings.Contains(derived.Rules[0].Source, parentSourceSuffix) {
		t.Errorf("expected parent suffix even for empty source, got %q", derived.Rules[0].Source)
	}
}

// TestDeriveSubagentRuleSet_RuleOrderingDenyFirst confirms the output
// ordering documented in the package: denies precede asks precede allows.
// This is for diagnostic readability — Evaluate re-sorts internally.
func TestDeriveSubagentRuleSet_RuleOrderingDenyFirst(t *testing.T) {
	parent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/p-deny/**", Behavior: DecisionDeny, Source: "parent"},
			{Tool: "dev_bash", Pattern: "rm", Behavior: DecisionAsk, Source: "parent"},
		},
	}
	subagent := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/s-allow/**", Behavior: DecisionAllow, Source: "subagent"},
			{Tool: "dev_read", Pattern: "/s-deny/**", Behavior: DecisionDeny, Source: "subagent"},
			{Tool: "dev_bash", Pattern: "force", Behavior: DecisionAsk, Source: "subagent"},
		},
	}
	derived, err := DeriveSubagentRuleSet(DerivationInput{Parent: parent, Subagent: subagent})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// Find the indices of each behavior — denies should precede asks should
	// precede allows.
	var firstAsk, firstAllow, lastDeny int = -1, -1, -1
	for i, r := range derived.Rules {
		switch r.Behavior {
		case DecisionDeny:
			lastDeny = i
		case DecisionAsk:
			if firstAsk == -1 {
				firstAsk = i
			}
		case DecisionAllow:
			if firstAllow == -1 {
				firstAllow = i
			}
		}
	}
	if firstAsk != -1 && lastDeny >= firstAsk {
		t.Errorf("denies should precede asks; lastDeny=%d, firstAsk=%d", lastDeny, firstAsk)
	}
	if firstAllow != -1 && firstAsk != -1 && firstAsk >= firstAllow {
		t.Errorf("asks should precede allows; firstAsk=%d, firstAllow=%d", firstAsk, firstAllow)
	}
}
