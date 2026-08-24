// Package permission — derivation.go implements parent → subagent permission
// RuleSet derivation (CW-20260512-0119, SP-20260512-0010 Wave 3).
//
// Mirrors the opencode reference implementation at
// `packages/opencode/src/agent/subagent-permissions.ts:17-33`
// (issue #26514 — "subagents bypass Plan mode via naive inheritance").
//
// # The derivation rule
//
// When a parent dispatches a subagent, the subagent's effective RuleSet is:
//
//	denies  = (parent denies)  ∪  (subagent denies)
//	asks    = (parent asks)    ∪  (subagent asks)
//	allows  = subagent allows   (parent allows do NOT propagate)
//
// Denies always inherit; allows only inherit through the subagent's own
// explicit opt-in. This is the structural fix that closes opencode #26514:
// a parent's Plan-Mode deny is forwarded into every spawned child even when
// the child's own profile would otherwise grant the same path.
//
// Asks are forwarded for the same reason as denies — the parent's prompt
// gate (e.g. "ask before shell rm -rf") is a parent-level intent that the
// child must respect.
//
// # Why allows don't propagate
//
// A parent whose profile grants "/Users/u/project_a/**" is signaling its
// own scope, not the subagent's. The subagent's profile decides what the
// subagent can reach (typically narrower — e.g. researcher allowed only
// under "/docs/**"). Propagating parent allows would widen the subagent
// beyond its profile's design intent. Denies are different — they encode
// "no one in this lineage may touch this path" and MUST inherit.
//
// # Default safety denies
//
// Per opencode #26514 the derivation also appends default safety denies for
// `task` and `todowrite` when the subagent's own ruleset doesn't grant
// them. We mirror the shape but parameterize it via DefaultSafetyDenies so
// the call site can decide which defaults apply to which subagent role.
// The current production wiring passes nil (no safety denies) since
// nanite's subagent surface filtering happens at the toolclient broker
// rather than the RuleSet — keeping the hook here means a future "Plan
// Mode in nanite" port can wire safety denies without re-plumbing the
// derivation.
//
// # Canonical-form forwarding (W4 integration)
//
// The derivation runs Resolve(workingDir) on the parent's RuleSet BEFORE
// forwarding so a `./sensitive/**` pattern in the parent becomes the
// concrete absolute path the child should be denied — even when the child
// runs in a different working_dir. This is the "canonical patterns inherit,
// not literal `./` shapes" contract documented in resolve.go.
//
// Source provenance: each forwarded parent rule is tagged with a "(via
// parent)" suffix on Rule.Source so downstream renderers (W2's
// RenderPermissionSummary) display the lineage at the point of decision —
// the subagent reads "denied by parent profile via parent" and learns
// why the constraint exists rather than seeing an unattributed deny.
package permission

import (
	"fmt"
	"strings"
)

// parentSourceSuffix marks forwarded parent rules in the derived RuleSet
// so renderers (W2 RenderPermissionSummary) can attribute "denied by
// parent profile" in the rendered summary. The suffix is appended once
// per forwarding step — a deny that forwards through Parent → Child →
// Grandchild ends up tagged "<original> (via parent) (via parent)" so
// the rendered output shows the depth of the chain explicitly.
const parentSourceSuffix = " (via parent)"

// DerivationInput aggregates the inputs to DeriveSubagentRuleSet.
//
// Parent is the parent session's effective RuleSet (already resolved
// against the parent's working_dir). Pass nil when the parent has no
// per-session ruleset.
//
// ParentWorkingDir is the working_dir the parent session resolved its
// rules against. Used only for the "parent already resolved" invariant
// docstring — the derivation itself doesn't re-resolve parent rules
// because they should already be in canonical absolute form. Leaving
// this field on the input struct makes the W4 contract reviewable at
// the call site.
//
// Subagent is the subagent's own profile RuleSet (un-resolved). The
// derivation runs Resolve(SubagentWorkingDir) on it before merging so
// subagent `./` patterns become absolute against the child's working_dir.
// Pass nil when the subagent has no profile-level ruleset.
//
// SubagentWorkingDir is the working_dir the child session will run
// against. Required when Subagent contains workspace-relative patterns;
// optional otherwise (Resolve fast-paths nil rulesets and zero `./`
// rule counts).
//
// DefaultSafetyDenies is an optional slice of denies appended to the
// derived set unconditionally. Currently unused in production wiring
// (nanite's subagent tool surface filtering happens at the toolclient
// broker) but reserved for a future Plan-Mode-style port. Each rule's
// Source is taken verbatim — callers are expected to populate Source
// with a self-describing string ("default safety policy").
type DerivationInput struct {
	Parent              *RuleSet
	ParentWorkingDir    string
	Subagent            *RuleSet
	SubagentWorkingDir  string
	DefaultSafetyDenies []Rule
}

// DeriveSubagentRuleSet computes the effective RuleSet for a spawned
// subagent's child session per the design rule documented at the package
// docstring above.
//
// Output ordering:
//
//  1. Parent denies (Source tagged "<orig> (via parent)")
//  2. Subagent denies (Source preserved verbatim)
//  3. DefaultSafetyDenies (Source preserved verbatim)
//  4. Parent asks (Source tagged "<orig> (via parent)")
//  5. Subagent asks (Source preserved verbatim)
//  6. Subagent allows (Source preserved verbatim)
//
// Within each category the input order is preserved so callers can reason
// about precedence by reading their input. RuleSet.Evaluate's grouping
// pass (deny > ask > allow) re-sorts at evaluation time, so the cross-
// category order in the output is for diagnostic readability, not policy.
//
// Mode resolution: the derived RuleSet's Mode is taken from Subagent.Mode
// when set, falling back to Parent.Mode. The subagent's profile-level
// mode wins because the subagent is the one being dispatched — the
// parent's mode is a session-level setting, not a profile setting.
//
// Returns ErrPatternEscapesWorkingDir or ErrEmptyWorkingDir when
// SubagentWorkingDir resolution fails. Parent rules are NOT re-resolved
// here — callers must pass an already-resolved parent ruleset (see W4
// resolve.go's package doc). A parent containing un-resolved `./`
// patterns would forward those patterns verbatim into the child, which
// is the bug class the W4 work was meant to close.
//
// Nil-safe: nil Parent + nil Subagent + empty DefaultSafetyDenies returns
// an empty RuleSet (not nil) so downstream renderers can iterate without
// the nil-check papercut.
func DeriveSubagentRuleSet(in DerivationInput) (*RuleSet, error) {
	// Resolve the subagent's own rules against its working_dir BEFORE
	// merging so the derived set is uniformly canonical. The parent
	// rules are assumed already resolved per the W4 contract.
	resolvedSubagent, err := in.Subagent.Resolve(in.SubagentWorkingDir)
	if err != nil {
		return nil, fmt.Errorf("permission: derive subagent rules: %w", err)
	}

	out := &RuleSet{}

	// Mode resolution — subagent wins, parent fallback.
	switch {
	case resolvedSubagent != nil && resolvedSubagent.Mode != "":
		out.Mode = resolvedSubagent.Mode
	case in.Parent != nil && in.Parent.Mode != "":
		out.Mode = in.Parent.Mode
	}

	// 1) Parent denies — forward with provenance tag.
	if in.Parent != nil {
		for _, r := range in.Parent.Rules {
			if r.Behavior == DecisionDeny {
				out.Rules = append(out.Rules, tagAsForwarded(r))
			}
		}
	}

	// 2) Subagent denies — Source preserved verbatim.
	if resolvedSubagent != nil {
		for _, r := range resolvedSubagent.Rules {
			if r.Behavior == DecisionDeny {
				out.Rules = append(out.Rules, r)
			}
		}
	}

	// 3) DefaultSafetyDenies — appended unconditionally. Source kept as
	//    caller authored.
	for _, r := range in.DefaultSafetyDenies {
		if r.Behavior == "" {
			r.Behavior = DecisionDeny
		}
		out.Rules = append(out.Rules, r)
	}

	// 4) Parent asks — forwarded with provenance tag. Asks encode parent
	//    intent ("prompt me before X") that the child must respect.
	if in.Parent != nil {
		for _, r := range in.Parent.Rules {
			if r.Behavior == DecisionAsk {
				out.Rules = append(out.Rules, tagAsForwarded(r))
			}
		}
	}

	// 5) Subagent asks — Source preserved verbatim.
	if resolvedSubagent != nil {
		for _, r := range resolvedSubagent.Rules {
			if r.Behavior == DecisionAsk {
				out.Rules = append(out.Rules, r)
			}
		}
	}

	// 6) Subagent allows — Source preserved verbatim. Parent allows do
	//    NOT propagate; the subagent's profile decides its own scope.
	if resolvedSubagent != nil {
		for _, r := range resolvedSubagent.Rules {
			if r.Behavior == DecisionAllow {
				out.Rules = append(out.Rules, r)
			}
		}
	}

	return out, nil
}

// tagAsForwarded returns a copy of r with parentSourceSuffix appended to
// Source so renderers can attribute the rule's lineage. Idempotent at
// the chain-shape level — applying the suffix N times produces
// "<orig> (via parent) (via parent) ... (via parent)" so the
// Parent → Child → Grandchild chain reads as two suffix applications,
// making chain depth observable at the rendered output.
func tagAsForwarded(r Rule) Rule {
	out := r
	src := strings.TrimSpace(r.Source)
	if src == "" {
		out.Source = "parent" + parentSourceSuffix
		return out
	}
	out.Source = src + parentSourceSuffix
	return out
}
