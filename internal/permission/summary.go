// Package permission — summary.go renders a human-readable per-session
// permission summary for inclusion in the slot-based context window.
//
// Background. The fabrication deep-dive (agent-workspaces/execution/nanite/
// sp-fabrication-cleanup/2026-05-12/fabrication-deep-dive.md) identified the
// "permissions invisible to the LLM" gap (H1) as the dominant root cause of
// researcher-subagent fabrication in c160 turn 16. The path-access substrate
// (AllowedPaths + session-scoped PathGrants + lineage-inherited grants) is
// enforced in code but never rendered into the prompt — the agent reasons
// about file access from priors and confabulates when the priors are wrong.
//
// This renderer (CW-20260512-0118, SP-20260512-0010 Wave 2) closes the gap.
// Its output is meant for the dedicated SlotPermissions slot — a sibling of
// the workspace slot, not a sub-section of it, so the AGENTS.md walk-up work
// (CW-20260512-0116) can plug into the workspace slot without merge conflict.
//
// Sharp edges enforced here:
//
//   - "Cannot access" listings are bounded. Enumerating the whole filesystem
//     is infinite in principle; the summary surfaces only the explicit
//     deny-mode rules from the resolved RuleSet, plus a single closing
//     sentence stating the implicit-deny default ("any path not listed
//     above is outside session scope"). Callers MUST resolve workspace-
//     relative `./` patterns via `(*RuleSet).Resolve(workingDir)` before
//     passing the RuleSet here — the renderer prints patterns verbatim and
//     would otherwise leak un-resolved `./` shapes into the prompt.
//
//   - Source provenance is preserved. Each rendered row tags the rule's
//     origin (Rule.Source from rules.go) and each path grant tags its
//     source ("session grants" / "inherited from parent session"). This
//     mirrors what W4 (CW-20260512-0120) made available through the
//     Resolve API and what W3 (CW-20260512-0119) will need when forwarding
//     parent denies into spawned children.
//
//   - Defense in depth. This renderer is the upstream PREVENTION layer —
//     visible constraints so the agent can REFUSE rather than fabricate.
//     The runtime fabrication detection added by CW-20260512-0095 (PR #144)
//     stays as the downstream DETECTION backstop. Both layers compose; this
//     does NOT replace the gate at chat_tool_executor.go or the dev_tools
//     resolveAllowed escape check.
//
//   - Determinism. RenderPermissionSummary is a pure projection of its
//     inputs. The output is stable across turns when the inputs don't
//     change, so the slot's cache key (SHA-256 of content, per
//     internal/context.ComputeCacheKey) is per-session-stable until
//     path_grants shift or the resolved RuleSet changes. This keeps
//     the slot's contribution to the Anthropic cacheable prefix intact.
package permission

import (
	"path/filepath"
	"sort"
	"strings"
)

// SummaryInput aggregates the data sources the permission summary draws
// from. All fields are optional — an empty input produces an empty string
// from RenderPermissionSummary so the slot is silently skipped when the
// agent has no relevant constraints to surface.
//
// WorkingDir is the session's working directory (already canonicalized by
// the caller). Used only to render the "rooted at" hint when path patterns
// reference it; the actual `./` → absolute resolution must have happened
// BEFORE the RuleSet reaches this renderer (call (*RuleSet).Resolve via
// W4's resolve.go API at session start).
//
// Rules carries the resolved permission RuleSet (deny + ask + allow rules)
// with workspace-relative patterns already expanded to absolute form. The
// renderer groups by Behavior (deny → ask → allow) so the agent reads
// constraints before affordances.
//
// AllowedPaths is the binary-scoped static allow-list configured via
// `dev_tools_allowed_paths` in nanite.yaml. These are the directories
// dev_* tools accept by default before consulting session grants. Rendered
// as the baseline READ/WRITE roots.
//
// OwnGrants and InheritedGrants carry the session-scoped explicit-mention
// path grants (cleaned absolute paths). OwnGrants is the session's own
// bucket; InheritedGrants is the union of any ancestor sessions reachable
// via PathGrants.RegisterLineage (parent chat session for a spawned
// subagent). Both are rendered with provenance tags so the agent sees the
// origin of each grant.
//
// SessionScope, when non-empty, is rendered as the closing "any path not
// listed above is outside session scope" sentence's qualifier (e.g.
// "this researcher subagent's scope"). Empty produces the generic phrasing.
type SummaryInput struct {
	WorkingDir       string
	Rules            *RuleSet
	AllowedPaths     []string
	OwnGrants        []string
	InheritedGrants  []string
	SessionScope     string
}

// RenderPermissionSummary returns a human-readable Markdown block describing
// the effective path access for the session. Empty input → empty string
// (the slot is silently skipped). The returned content has no trailing
// newline.
//
// Output shape (sections present only when content exists):
//
//	## Path access
//
//	You can READ under (default workspace allow-list):
//	  - /Users/.../project_a/
//	  - /Users/.../project_b/
//
//	You have explicit grants for (session):
//	  - /Users/.../user-mentioned/path.go  (from session grants)
//
//	You inherit these from the parent session:
//	  - /Users/.../parent-mentioned/  (from parent chat session)
//
//	You CANNOT access (explicitly denied):
//	  - /Users/.../sensitive/**  (from profile.yaml)
//
//	Any path not listed above is outside session scope. If a task needs an
//	out-of-scope path, return a failure naming the path rather than
//	synthesizing an answer from training data.
//
// The closing instruction echoes the universal "Refusal" rules (from
// chat.UniversalRulesBlock) at the point of decision — agents read the
// constraint immediately before deciding whether to attempt a tool call.
//
// Cache behavior. The output is deterministic over (Rules, AllowedPaths,
// OwnGrants, InheritedGrants, SessionScope). When any of those change the
// slot's cache key changes; otherwise it ships verbatim across turns.
func RenderPermissionSummary(in SummaryInput) string {
	allowedPaths := normalizeAllowedPaths(in.AllowedPaths)
	ownGrants := dedupSortedCopy(in.OwnGrants)
	inheritedGrants := subtractAndDedup(in.InheritedGrants, ownGrants)
	denyRules, askRules, allowRules := classifyRules(in.Rules)

	// If nothing to render, return empty so the slot is skipped entirely.
	// The "## Path access" header alone is not worth burning tokens on.
	if len(allowedPaths) == 0 && len(ownGrants) == 0 && len(inheritedGrants) == 0 &&
		len(denyRules) == 0 && len(askRules) == 0 && len(allowRules) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Path access\n")

	// Allow-side — surface affordances first so the agent reads "you can"
	// before "you cannot". The bounded "cannot access" listing follows.
	if len(allowedPaths) > 0 {
		b.WriteString("\nYou can READ under (workspace allow-list):\n")
		for _, p := range allowedPaths {
			b.WriteString("  - ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}

	if len(ownGrants) > 0 {
		b.WriteString("\nYou have explicit access to (session grants):\n")
		for _, p := range ownGrants {
			b.WriteString("  - ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}

	if len(inheritedGrants) > 0 {
		b.WriteString("\nYou inherit these from the parent session:\n")
		for _, p := range inheritedGrants {
			b.WriteString("  - ")
			b.WriteString(p)
			b.WriteString("  (from parent session)\n")
		}
	}

	if len(allowRules) > 0 {
		b.WriteString("\nAdditional allow rules from profile:\n")
		writeRuleList(&b, allowRules)
	}

	// Ask-rule visibility. These aren't outright denies, but the agent
	// should know they will pause and prompt — surface so it can avoid
	// the interrupt or pre-announce the upcoming approval prompt.
	if len(askRules) > 0 {
		b.WriteString("\nThese will require approval:\n")
		writeRuleList(&b, askRules)
	}

	// Deny-side. Bounded enumeration — only explicit deny rules from the
	// resolved RuleSet. The infinite "everything else" is communicated by
	// the closing implicit-deny sentence rather than enumerated.
	if len(denyRules) > 0 {
		b.WriteString("\nYou CANNOT access (explicitly denied):\n")
		writeRuleList(&b, denyRules)
	}

	// Closing refusal hook. Echoes the universal "Refusal" rules at the
	// point of decision so the agent reads the constraint immediately
	// before deciding whether to attempt a tool call.
	scope := strings.TrimSpace(in.SessionScope)
	scopeQualifier := "this session's scope"
	if scope != "" {
		scopeQualifier = scope
	}
	b.WriteString("\nAny path not listed above is outside ")
	b.WriteString(scopeQualifier)
	b.WriteString(". If a task needs an out-of-scope path, return a failure naming the path rather than synthesizing an answer.")

	return b.String()
}

// classifyRules groups rs.Rules by Behavior (deny / ask / allow) in the
// same priority order as Evaluate(). Returns three slices that preserve
// the input order within each group — Source tags ride along so the
// renderer can attribute each row.
//
// Nil-safe: a nil RuleSet returns three empty slices.
func classifyRules(rs *RuleSet) (deny, ask, allow []Rule) {
	if rs == nil {
		return nil, nil, nil
	}
	for _, r := range rs.Rules {
		switch r.Behavior {
		case DecisionDeny:
			deny = append(deny, r)
		case DecisionAsk:
			ask = append(ask, r)
		case DecisionAllow:
			allow = append(allow, r)
		}
	}
	return deny, ask, allow
}

// writeRuleList renders a slice of rules as Markdown bullets, attributing
// each rule's source. The Tool + Pattern shape mirrors the YAML rule the
// user (or agent profile) authored, so debugging back from the rendered
// summary to the source rule is one greptable hop.
//
// Output per rule:
//   - `dev_read` on `/path/**`  (from profile.yaml)
//   - `dev_write` on `./generated/**`  (from session permissions)
//
// When Source is empty (a rule built in-memory at runtime without a
// provenance tag), the suffix is "(unknown source)" so the renderer
// stays deterministic instead of breaking out of the bullet shape.
func writeRuleList(b *strings.Builder, rules []Rule) {
	for _, r := range rules {
		b.WriteString("  - ")
		if r.Tool != "" {
			b.WriteString("`")
			b.WriteString(r.Tool)
			b.WriteString("`")
		} else {
			b.WriteString("`(any tool)`")
		}
		if r.Pattern != "" {
			b.WriteString(" on `")
			b.WriteString(r.Pattern)
			b.WriteString("`")
		}
		source := strings.TrimSpace(r.Source)
		if source == "" {
			source = "unknown source"
		}
		b.WriteString("  (from ")
		b.WriteString(source)
		b.WriteString(")\n")
	}
}

// normalizeAllowedPaths returns a sorted, deduplicated, trimmed-with-
// trailing-separator copy of paths. The trailing separator makes the
// "everything under here" semantic explicit in the rendered output —
// agents read "/foo/bar/" as a directory root, "/foo/bar" as ambiguous.
//
// Empty / whitespace-only entries are dropped. Order is sorted so the
// output is deterministic regardless of the input slice's order.
func normalizeAllowedPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Clean the path; append a trailing OS separator so the rendered
		// row reads as a directory root rather than an ambiguous file.
		clean := filepath.Clean(p)
		sep := string(filepath.Separator)
		if !strings.HasSuffix(clean, sep) {
			clean += sep
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

// dedupSortedCopy returns a sorted, deduplicated copy of paths with
// whitespace-only entries dropped. Used for grant lists where the input
// is already cleaned (PathGrants.ListGrants only stores cleaned absolute
// paths) but order is unspecified.
func dedupSortedCopy(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// subtractAndDedup returns inherited \ own (set difference), sorted +
// deduplicated. A grant the worker already has in its own bucket is
// suppressed from the "inherited" section so each path appears exactly
// once in the rendered output — under "session grants" if local, under
// "inherited" if only present on a parent.
func subtractAndDedup(inherited, own []string) []string {
	if len(inherited) == 0 {
		return nil
	}
	ownSet := make(map[string]struct{}, len(own))
	for _, p := range own {
		ownSet[strings.TrimSpace(p)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(inherited))
	out := make([]string, 0, len(inherited))
	for _, p := range inherited {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := ownSet[p]; dup {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
