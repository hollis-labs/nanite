package service

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// applyModeToolOverridesToTools filters a tool slice against a session-mode
// ToolOverrideSpec. Meta-tools (request_tools, fetch_tool_result,
// search_tool_result, …) are exempt — they're the agent's escape hatches and
// must remain reachable regardless of mode policy. The underlying resolution
// (deny > allow, explicit > pattern) lives in store.ApplyToolOverrides; this
// is a thin adapter for llmtypes.ToolDefinition slices.
//
// Empty spec returns the input unchanged. F1 (CW-20260429-0001).
func applyModeToolOverridesToTools(
	tools []llmtypes.ToolDefinition,
	spec store.ToolOverrideSpec,
) []llmtypes.ToolDefinition {
	if isEmptySpec(spec) || len(tools) == 0 {
		return tools
	}

	// Split off meta-tools so they pass through unfiltered, then run the
	// remainder through the canonical resolution helper by name.
	regularNames := make([]string, 0, len(tools))
	metaIdx := make(map[string]struct{}, 4)
	for _, t := range tools {
		if isMetaTool(t.Name) {
			metaIdx[t.Name] = struct{}{}
			continue
		}
		regularNames = append(regularNames, t.Name)
	}
	keptNames := store.ApplyToolOverrides(regularNames, spec)
	keep := make(map[string]struct{}, len(keptNames)+len(metaIdx))
	for _, n := range keptNames {
		keep[n] = struct{}{}
	}
	for n := range metaIdx {
		keep[n] = struct{}{}
	}

	filtered := make([]llmtypes.ToolDefinition, 0, len(keep))
	for _, t := range tools {
		if _, ok := keep[t.Name]; ok {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// applyModeToolOverridesToSummaries filters a tool-summary slice against a
// session-mode ToolOverrideSpec, applying the same meta-tool exemption as
// applyModeToolOverridesToTools. Used to scrub the progressive-discovery
// catalog so the LLM is never told a denied tool is "available". F1
// (CW-20260429-0001).
func applyModeToolOverridesToSummaries(
	summaries []toolclient.ToolSummary,
	spec store.ToolOverrideSpec,
) []toolclient.ToolSummary {
	if isEmptySpec(spec) || len(summaries) == 0 {
		return summaries
	}

	regularNames := make([]string, 0, len(summaries))
	metaIdx := make(map[string]struct{}, 4)
	for _, s := range summaries {
		if isMetaTool(s.Name) {
			metaIdx[s.Name] = struct{}{}
			continue
		}
		regularNames = append(regularNames, s.Name)
	}
	keptNames := store.ApplyToolOverrides(regularNames, spec)
	keep := make(map[string]struct{}, len(keptNames)+len(metaIdx))
	for _, n := range keptNames {
		keep[n] = struct{}{}
	}
	for n := range metaIdx {
		keep[n] = struct{}{}
	}

	filtered := make([]toolclient.ToolSummary, 0, len(keep))
	for _, s := range summaries {
		if _, ok := keep[s.Name]; ok {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// isEmptySpec mirrors store.isEmptyToolOverrideSpec (which is unexported).
// Centralizing the check here lets the service layer skip the filter cheaply
// without re-reading the store package's internals.
func isEmptySpec(spec store.ToolOverrideSpec) bool {
	return len(spec.Allow) == 0 && len(spec.Deny) == 0 &&
		len(spec.AllowPatterns) == 0 && len(spec.DenyPatterns) == 0
}
