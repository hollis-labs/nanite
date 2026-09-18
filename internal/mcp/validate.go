package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TrustTier classifies the blast radius of an MCP server. Validators in this
// file pick per-tier limits keyed off the tier value.
//
// Persisted on store.MCPServerConfig.TrustTier. Built-in and plugin-registered
// servers carry their tier at runtime via the Manager registration API; rows
// in the mcp_servers table default to TierThirdPartyHTTP (D4 fail-closed).
type TrustTier string

const (
	TierBuiltin        TrustTier = "builtin"
	TierPluginStdio    TrustTier = "plugin_stdio"
	TierPluginHTTP     TrustTier = "plugin_http"
	TierThirdPartyHTTP TrustTier = "third_party_http"
)

// Limits holds the per-tier resource ceilings enforced at MCP discovery and
// tool-execution time. These are code-level defaults only — there is no
// live per-agent/per-launch override today (a prior version of this comment
// claimed "configurable at runtime via UserSettings"; nothing in the
// codebase implements that, so the claim was aspirational and has been
// removed rather than left to mislead the next reader).
//
// Default posture, and why: a real multi-turn tool-calling harness (Claude,
// Codex, ...) routinely hands agents thorough, multi-KB tool descriptions
// and results ranging from a few KB to several MB — neither Anthropic's,
// OpenAI's, nor the MCP spec's own guidance publishes a hard per-tool byte
// ceiling, because the actual constraint they manage is total context/token
// budget, not a fixed per-field size (see Anthropic's Tool Search Tool and
// this package's own progressive discovery / SelectForAgent.
// FinalizeToolSelection windowSize pruning, which already do that job).
// These per-tier ceilings are a DIFFERENT thing: a blast-radius/DoS
// backstop against a pathological or hostile tool definition/result, not a
// context-management lever. Conflating the two — using a tiny ceiling to
// "test agent performance with less context" — silently drops ordinary,
// well-written tools (e.g. tesseract_recall's real 5.3KB description
// tripped the old 4KB TierPluginStdio cap the moment Agent Mux was
// reclassified off TierBuiltin) with no operator-visible signal beyond a
// discovery-time log line. Defaults below give every tier generous
// headroom over real observed tool sizes; TrustTier still varies the
// ceiling for genuine blast-radius reasons (an unauthenticated, unknown
// third-party server gets the tightest numbers), it just no longer
// degrades ordinary operation to do it.
//
// MaxToolsPerServer is advisory only (CW-20260815-0019): ValidateToolSet
// still fires a DiscoveryWarning when a server's advertised tool count
// crosses this threshold, but nothing is truncated as a result — an
// operator decides whether an unusually large server is worth keeping
// connected. The other four fields remain hard enforcement ceilings.
type Limits struct {
	MaxToolNameLen      int
	MaxDescriptionLen   int
	MaxInputSchemaBytes int
	MaxResultBytes      int
	MaxToolsPerServer   int
}

// LimitsFor returns the default Limits for the given tier. Unknown tiers
// fall through to TierThirdPartyHTTP (strictest) per D4 fail-closed.
//
// MaxResultBytes across every tier stays at or below the 10 MiB global
// per-transport hard cap (Manager.AddServer's SetMaxResponseBytes wiring,
// hotfix PR #42) — that cap is the true outer bound; these tiers only
// decide how much of it a given trust level gets to use.
func LimitsFor(tier TrustTier) Limits {
	switch tier {
	case TierBuiltin:
		return Limits{
			MaxToolNameLen:      256,
			MaxDescriptionLen:   32 * 1024,
			MaxInputSchemaBytes: 512 * 1024,
			MaxResultBytes:      10 * 1024 * 1024,
			MaxToolsPerServer:   2000,
		}
	case TierPluginStdio:
		return Limits{
			MaxToolNameLen:      256,
			MaxDescriptionLen:   32 * 1024,
			MaxInputSchemaBytes: 256 * 1024,
			MaxResultBytes:      8 * 1024 * 1024,
			MaxToolsPerServer:   1000,
		}
	case TierPluginHTTP:
		return Limits{
			MaxToolNameLen:      256,
			MaxDescriptionLen:   24 * 1024,
			MaxInputSchemaBytes: 128 * 1024,
			MaxResultBytes:      4 * 1024 * 1024,
			MaxToolsPerServer:   500,
		}
	case TierThirdPartyHTTP:
		fallthrough
	default:
		return Limits{
			MaxToolNameLen:      256,
			MaxDescriptionLen:   16 * 1024,
			MaxInputSchemaBytes: 64 * 1024,
			MaxResultBytes:      2 * 1024 * 1024,
			MaxToolsPerServer:   200,
		}
	}
}

// ValidationError describes a single discovery- or execute-time validation
// failure. It is structured so the caller can both log it (Field/Reason) and
// emit it as a discovery warning (Field maps to a warning code).
type ValidationError struct {
	Field  string // structured field name, e.g. "tool_name", "description"
	Value  string // truncated for logging
	Reason string // human-readable explanation
}

// Error implements the error interface so single ValidationError instances
// can be returned from functions that prefer the error idiom.
func (v ValidationError) Error() string {
	if v.Value == "" {
		return fmt.Sprintf("%s: %s", v.Field, v.Reason)
	}
	return fmt.Sprintf("%s (%q): %s", v.Field, v.Value, v.Reason)
}

// Warning codes plumbed up to the plugin manager UI as DiscoveryWarning.Reason
// values. These are the canonical string set so the UI can render them with
// stable formatting.
const (
	WarnInvalidToolName    = "invalid_tool_name"
	WarnDescriptionTooLong = "description_too_long"
	WarnSchemaSizeExceeded = "schema_size_exceeded"
	WarnToolCountHigh      = "tool_count_high"
	WarnDuplicateToolName  = "duplicate_tool_name"
	WarnInvalidBlockType   = "invalid_block_type"
	WarnResultSizeExceeded = "result_size_exceeded"
)

// reValidToolNameStrict is the canonical tool-name charset enforced across
// every tier. Tier only varies the length cap.
var reValidToolNameStrict = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// ValidateToolMeta runs every per-tool discovery-time check and returns the
// list of failures. An empty return means the tool is acceptable.
func ValidateToolMeta(tier TrustTier, tool Tool) []ValidationError {
	limits := LimitsFor(tier)
	var errs []ValidationError

	switch {
	case tool.Name == "":
		errs = append(errs, ValidationError{
			Field:  WarnInvalidToolName,
			Value:  "",
			Reason: "empty tool name",
		})
	case len(tool.Name) > limits.MaxToolNameLen:
		errs = append(errs, ValidationError{
			Field: WarnInvalidToolName,
			Value: truncateForLog(tool.Name, 64),
			Reason: fmt.Sprintf("tool name exceeds %d characters (%d)",
				limits.MaxToolNameLen, len(tool.Name)),
		})
	case !reValidToolNameStrict.MatchString(tool.Name):
		errs = append(errs, ValidationError{
			Field:  WarnInvalidToolName,
			Value:  truncateForLog(tool.Name, 64),
			Reason: "tool name contains invalid characters (allowed: a-zA-Z0-9_-)",
		})
	}

	if len(tool.Description) > limits.MaxDescriptionLen {
		errs = append(errs, ValidationError{
			Field: WarnDescriptionTooLong,
			Value: truncateForLog(tool.Name, 64),
			Reason: fmt.Sprintf("description exceeds %d bytes (%d)",
				limits.MaxDescriptionLen, len(tool.Description)),
		})
	}

	// Schema size: serialize to JSON to measure on-the-wire bytes. nil/empty
	// schemas are permitted (some tools take no arguments).
	if tool.InputSchema != nil {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			errs = append(errs, ValidationError{
				Field:  WarnSchemaSizeExceeded,
				Value:  truncateForLog(tool.Name, 64),
				Reason: fmt.Sprintf("input schema not serialisable: %v", err),
			})
		} else if len(raw) > limits.MaxInputSchemaBytes {
			errs = append(errs, ValidationError{
				Field: WarnSchemaSizeExceeded,
				Value: truncateForLog(tool.Name, 64),
				Reason: fmt.Sprintf("input schema exceeds %d bytes (%d)",
					limits.MaxInputSchemaBytes, len(raw)),
			})
		}
	}

	return errs
}

// ValidateToolSet runs cross-tool discovery-time checks: tool-count
// threshold and duplicate-name detection within the same server.
//
// A high tool count produces at most one ValidationError for the whole
// set (Field=WarnToolCountHigh, Value empty) — advisory only
// (CW-20260815-0019): the server's advertised tool count crosses the tier
// threshold, but nothing is dropped as a result. Duplicate names produce
// one ValidationError per offending tool (Field=WarnDuplicateToolName),
// one for every duplicate after the first occurrence of that name.
func ValidateToolSet(tier TrustTier, tools []Tool) []ValidationError {
	limits := LimitsFor(tier)
	var errs []ValidationError

	if len(tools) > limits.MaxToolsPerServer {
		errs = append(errs, ValidationError{
			Field: WarnToolCountHigh,
			Value: "",
			Reason: fmt.Sprintf("server advertises %d tools, above the %d advisory threshold for this tier — consider whether this server belongs in your active tool set",
				len(tools), limits.MaxToolsPerServer),
		})
	}

	seen := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if _, dup := seen[t.Name]; dup {
			errs = append(errs, ValidationError{
				Field:  WarnDuplicateToolName,
				Value:  truncateForLog(t.Name, 64),
				Reason: "duplicate tool name within server",
			})
			continue
		}
		seen[t.Name] = struct{}{}
	}

	return errs
}

// ValidateResultSize fails when totalBytes exceeds the tier's result cap.
// Intended to wrap the assembled result string at the boundary in
// Manager.ExecuteTool, on top of the global per-transport 10 MiB hard cap
// installed by hotfix PR #42.
func ValidateResultSize(tier TrustTier, totalBytes int) error {
	limits := LimitsFor(tier)
	if totalBytes > limits.MaxResultBytes {
		return ValidationError{
			Field: WarnResultSizeExceeded,
			Reason: fmt.Sprintf("result %d bytes exceeds tier cap %d",
				totalBytes, limits.MaxResultBytes),
		}
	}
	return nil
}

// allowedBlockTypes enumerates the ToolContent.Type values an MCP server is
// permitted to return. Anything else is rejected before the result is
// assembled into the LLM-visible string. The MCP spec defines text, image,
// and resource as the standard block types.
var allowedBlockTypes = map[string]struct{}{
	"text":     {},
	"image":    {},
	"resource": {},
}

// ValidateBlockType returns an error if the block's Type is not in the
// allowlist. Empty type is treated as invalid.
func ValidateBlockType(block ToolContent) error {
	if _, ok := allowedBlockTypes[block.Type]; !ok {
		return ValidationError{
			Field:  WarnInvalidBlockType,
			Value:  truncateForLog(block.Type, 32),
			Reason: "block type not in allowlist (text|image|resource)",
		}
	}
	return nil
}

// reANSI matches the two ANSI escape sequence families that show up in MCP
// tool output:
//
//   - CSI: ESC [ [params] [intermediates] final-byte (final 0x40-0x7E)
//   - OSC: ESC ] ... ST  (ST = BEL 0x07 or ESC \)
//
// Both are stripped because terminal emulators interpret them, and MCP
// results render in a TUI that would otherwise let a server move the cursor
// or spoof other tool output.
var reANSI = regexp.MustCompile(
	"\x1b\\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]" +
		"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)",
)

// StripANSI removes ANSI CSI and OSC escape sequences from s. Applied
// unconditionally on text blocks at the boundary where they flow to the
// LLM (D5). Cheap and reduces UI spoofing risk regardless of trust tier.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return reANSI.ReplaceAllString(s, "")
}

// InjectionHit records a single positive match from ScanInjection. Intended
// for observability (metric + WARN log) — D3 says the caller does not block
// on hits in S4b.
type InjectionHit struct {
	Rule    string // canonical rule name, used as a metric label
	Snippet string // truncated context around the match (≤200 chars)
}

// injectionRule pairs a regex with a canonical name surfaced in metrics
// and log records. Keep this slice append-only so existing dashboards keep
// working when new rules are added.
type injectionRule struct {
	name string
	re   *regexp.Regexp
}

var injectionRules = []injectionRule{
	{
		name: "ignore_previous",
		re:   regexp.MustCompile(`(?i)ignore (?:previous|all) (?:instructions|prompts)`),
	},
	{
		// `system:` at start of line. Tightened to require optional
		// leading whitespace then literal "system:" with no intervening
		// word so "System information: ..." (a benign tool-output
		// pattern) does NOT hit. Match relies on (?m) anchoring ^ at
		// every line break.
		name: "system_prefix",
		re:   regexp.MustCompile(`(?im)^\s*system\s*:`),
	},
	{
		name: "role_tag",
		re:   regexp.MustCompile(`(?i)</?(?:system|assistant|user)>`),
	},
	{
		// ANSI CSI at start of a line — common spoofing pattern that
		// hides text or paints fake tool-call output in a TUI.
		name: "ansi_line_start",
		re:   regexp.MustCompile(`(?m)^\s*\x1b\[`),
	},
}

// ScanInjection runs every injection-detector regex against s and returns
// one InjectionHit per matching rule. Multiple matches of the same rule
// collapse to a single hit (we only need to know the rule fired).
func ScanInjection(s string) []InjectionHit {
	if s == "" {
		return nil
	}
	var hits []InjectionHit
	for _, rule := range injectionRules {
		loc := rule.re.FindStringIndex(s)
		if loc == nil {
			continue
		}
		hits = append(hits, InjectionHit{
			Rule:    rule.name,
			Snippet: snippetAround(s, loc[0], loc[1]),
		})
	}
	return hits
}

// snippetAround returns up to 200 chars of s anchored at the match span,
// padded forward when the match is short. Used by ScanInjection as the
// log-friendly context value.
func snippetAround(s string, start, end int) string {
	const maxLen = 200
	if end-start >= maxLen {
		return s[start : start+maxLen]
	}
	want := maxLen - (end - start)
	from := start - want/2
	if from < 0 {
		from = 0
	}
	to := from + maxLen
	if to > len(s) {
		to = len(s)
	}
	return s[from:to]
}

// truncateForLog trims s to maxLen bytes for safe inclusion in log records
// and ValidationError.Value. Avoids dumping a 10 MiB pathological tool name
// straight into slog output.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
