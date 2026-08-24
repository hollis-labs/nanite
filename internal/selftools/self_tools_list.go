package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// naniteToolListDefinition is the cheap discovery primitive (SP6 —
// CW-20260430-0006). It complements tool_describe: where describe
// returns full per-tool detail (schema + golden examples + relations),
// list returns just `name + one-line summary` for the full self-tool
// inventory, with an optional substring filter. Cost target: unfiltered
// ≤ a few KiB, filtered ≤ ~500 B — agents can browse the catalog without
// burning turns on inference-of-tool-names.
//
// The motivating evidence is c120 (2026-04-29), where the agent burned
// turns guessing tool names (`nanite_reminder_create` → `nanite_reminder`
// → `reminder_set`) and its self-listing of tools missed
// `reminder_set` entirely.
//
// CW-20260501-0001 (SP6 follow-up): the original implementation only
// enumerated tools defined in `self_tools.go`, which silently hid tools
// registered on sibling MCP servers (e.g. Vanta's `memory_recall`).
// The list now sources from the full MCP Manager surface (via the
// ToolInventoryLookup interface).
//
// Output is the FULL inventory regardless of caller — this primitive is
// a catalog, not a permission check. Whether any specific tool is
// actually reachable for the calling agent is governed elsewhere: the
// agent profile's tool permissions, the dev-mode gate, and project /
// session preload policy. Agents (and humans reading the result) should
// not infer per-agent reach from the presence or absence of a tool in
// this output.
//
// Reactive posture: this is a tool the agent reaches for, not a gate it
// passes through. No "must call before X" rule. See
// docs/architecture/agent-context-architecture.md for the rationale.
func naniteToolListDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "tool_list",
		// Description is the canonical base string declared once in
		// self_tools_describer.go (toolListBaseDescription); the per-call
		// describeToolList Describer extends the same string with the
		// caller's slug context. Sharing the constant guarantees the
		// registration-time and per-call surfaces can't drift.
		Description: toolListBaseDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Optional case-insensitive substring filter applied to BOTH the tool name and its one-line summary. Empty/omitted returns the full inventory.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Optional cap on how many tools an unfiltered call returns. Defaults to %d — if the full inventory is larger, the response is soft-truncated with a hint to narrow via filter. Ignored when filter is set. Pass a higher value (or a value ≥ the true total) to see everything anyway.", defaultToolListLimit),
				},
			},
		},
	}
}

// ToolInventoryLookup is the narrow registry surface tool_list
// uses to enumerate every tool registered with the MCP manager,
// regardless of which server it lives on. *mcp.Manager satisfies this
// via GetAllToolsUnfiltered; tests can substitute a stub.
//
// This is the cross-server complement to ToolSchemaLookup (which
// resolves a single tool's input schema by name). Both interfaces stay
// narrow on purpose so SelfToolsTransport doesn't gain a hard
// dependency on *Manager — the import direction is mcp → mcp,
// satisfied by an interface.
type ToolInventoryLookup interface {
	// GetAllToolsUnfiltered returns every discovered tool across every
	// registered MCP server, regardless of loadType. Names are uniform
	// (post-internalization, no `mcp__server__` prefix). The slice and
	// its elements are caller-owned; the implementation is expected to
	// return a fresh slice on each call so the caller can mutate it
	// without racing the manager.
	GetAllToolsUnfiltered() []llmtypes.ToolDefinition
}

// summaryMaxBytes caps the per-tool summary length so the unfiltered
// list payload stays compact. The first sentence of a description is
// usually shorter than this; this is the hard fallback for descriptions
// without a clean sentence break (e.g. one long run-on sentence). 80
// chars is just enough to convey "what this tool does" — agents call
// tool_describe for the rest.
const summaryMaxBytes = 80

// defaultToolListLimit is the soft threshold for an unfiltered tool_list
// call (CW-20260815-0019). Discovery no longer caps how many tools a
// connected server can advertise, so a single very large server (or a
// handful of moderately large ones) can make the unfiltered inventory
// itself large. This is a second-layer nudge, not an enforcement cap: past
// this many entries, an unfiltered response is soft-truncated with a hint
// to narrow via `filter` (or pass an explicit `limit` to see more/all).
const defaultToolListLimit = 100

// firstSentenceSummary extracts a short summary from a tool description.
// Strategy: find the earliest of `. `, `.\n`, `\n\n`, `\n`, or end-of-
// string. Cap the result at summaryMaxBytes (UTF-8 safe — we cut on a
// rune boundary). Trims leading/trailing whitespace and strips the
// trailing period for tightness.
//
// The function does not strip Markdown emphasis or code-fence markers
// — every existing tool description starts with prose, so first-
// sentence extraction lands clean. If a description ever opens with
// `**When to use:**` we'd want to revisit; the test suite catches
// that regression.
func firstSentenceSummary(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}

	// Find earliest sentence/paragraph break. We look for `. ` (period
	// + space) and `.\n` (period + newline) as sentence boundaries; a
	// double newline `\n\n` is a paragraph boundary that we treat as
	// "end of summary" even without a period.
	cut := len(desc)
	for _, sep := range []string{". ", ".\n", "\n\n"} {
		if idx := strings.Index(desc, sep); idx >= 0 && idx < cut {
			cut = idx + 1 // include the period (or first newline of \n\n)
		}
	}
	// Bare single newline is also a soft summary cutoff — most
	// descriptions put `\n\n` between sections, but a one-line
	// description may end on a single `\n`.
	if idx := strings.IndexByte(desc, '\n'); idx >= 0 && idx < cut {
		cut = idx
	}
	out := desc[:cut]

	// Hard byte cap; cut on a rune boundary so we don't split a multi-
	// byte glyph. The descriptions we ship are ASCII, but emoji or non-
	// ASCII punctuation could appear in user-defined tools later.
	if len(out) > summaryMaxBytes {
		out = safeTruncate(out, summaryMaxBytes)
	}

	out = strings.TrimSpace(out)
	out = strings.TrimRight(out, ".")
	return out
}

// safeTruncate returns s clipped to at most n bytes, walking back to a
// rune boundary if the byte cut would land inside a multi-byte UTF-8
// sequence.
func safeTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Walk back from n until we land on a rune-start byte. UTF-8 rune-
	// start bytes are either <0x80 (ASCII) or have the form 11xxxxxx;
	// continuation bytes are 10xxxxxx. We back off while we see
	// continuation bytes.
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// inventoryEntry is the {name, description} pair callToolList iterates
// over after merging the cross-server inventory with the local self-
// tool definitions. Keeping this minimal lets the same loop run over
// either source (Manager-fed or selfToolDefinitions-fed).
type inventoryEntry struct {
	name        string
	description string
}

// gatherInventory returns the deduplicated set of tools to consider for
// tool_list, sourced from the cross-server MCP inventory when
// available and falling back to the in-process self-tools when not.
//
// Output is the full inventory regardless of caller. Per-agent reach
// (permissions, dev-mode gate, project/session policy) is enforced
// elsewhere; this function is a registry view, not a filter. ctx is
// reserved for future per-call signals (request-scoped logging,
// cancellation when an inventory source becomes async); the current
// implementation is fully synchronous.
//
// Determinism: results are sorted by name so the rendered list is
// reproducible across runs (the broker / manager iteration order is
// not stable).
func (st *SelfToolsTransport) gatherInventory(_ context.Context) []inventoryEntry {
	seen := make(map[string]struct{})
	var out []inventoryEntry

	add := func(name, desc string) {
		if name == "" {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, inventoryEntry{name: name, description: desc})
	}

	// Primary source: the MCP manager's full inventory across every
	// registered server (self, nanite-memory, dev, general, code,
	// plugins). Each entry already carries the uniform agent-facing
	// name and its description.
	if st.Inventory != nil {
		for _, t := range st.Inventory.GetAllToolsUnfiltered() {
			add(t.Name, t.Description)
		}
	}

	// Fallback / belt-and-suspenders: include selfToolDefinitions()
	// directly. This makes the primitive useful in tests that wire a
	// SelfToolsTransport without a manager, and ensures the self
	// surface is visible even on the rare path where DiscoverTools
	// hasn't run yet (early init, manager-less unit tests).
	for _, d := range selfToolDefinitions() {
		add(d.Name, d.Description)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// callToolList handles tool_list. Iterates the cross-server tool
// inventory, builds {name, summary} pairs (summary = first-sentence of
// the tool's description, capped at summaryMaxBytes), and applies the
// optional filter to BOTH name and summary case-insensitively. Returns
// `{tools, count}`. Empty match returns `count:0` and an empty list, NOT
// an error — the agent reading the result decides whether to widen the
// filter.
//
// Soft truncation (CW-20260815-0019): an UNFILTERED call whose match set
// exceeds the effective limit (defaultToolListLimit, or the caller's
// explicit `limit`) is truncated to that many entries, with `truncated`,
// `total`, and a `hint` field added to the response nudging the agent to
// narrow via `filter` — or raise `limit` to see more. This is advisory
// only: an explicit `limit` at or above the true total returns everything.
// A filtered call is never truncated — filtering is already the narrowing
// step.
func (st *SelfToolsTransport) callToolList(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	filter := strings.ToLower(strings.TrimSpace(strArg(args, "filter", "")))
	limit := intArgFull(args, "limit", defaultToolListLimit)
	if limit <= 0 {
		limit = defaultToolListLimit
	}

	inv := st.gatherInventory(ctx)
	type entry struct {
		Name    string `json:"name"`
		Summary string `json:"summary"`
	}
	tools := make([]entry, 0, len(inv))
	for _, d := range inv {
		summary := firstSentenceSummary(d.description)
		if filter != "" {
			lname := strings.ToLower(d.name)
			lsumm := strings.ToLower(summary)
			if !strings.Contains(lname, filter) && !strings.Contains(lsumm, filter) {
				continue
			}
		}
		tools = append(tools, entry{Name: d.name, Summary: summary})
	}

	out := map[string]any{
		"count": len(tools),
	}
	if filter == "" && len(tools) > limit {
		total := len(tools)
		out["tools"] = tools[:limit]
		out["count"] = limit
		out["total"] = total
		out["truncated"] = true
		out["hint"] = fmt.Sprintf(
			"showing %d of %d tools — narrow with `filter` to see specific tools, or pass a higher `limit` to see more (or all %d).",
			limit, total, total,
		)
	} else {
		out["tools"] = tools
	}

	body, err := json.Marshal(out)
	if err != nil {
		// Marshalling a slice of two-string structs cannot realistically
		// fail — fall back to a structured error so the caller still
		// gets a uniform shape.
		return mcp.ErrorResult("tool_list: marshal result"), nil //nolint:nilerr // Tool errors travel in the MCP result payload.
	}
	return mcp.TextResult(string(body)), nil
}
