// Package stash caches full tool definitions per session so the Tools slot
// can hold a compact pointer-summary by default and hydrate full defs only
// when intent is detected.
//
// The stash is per-process and session-keyed. It is not persisted to the
// database — on restart, the next turn rebuilds it from the current tool
// selection. Rebuilds are triggered by a change in the selection-hash,
// detected via sorted tool-name SHA-256.
package stash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CategoryOther is the fallback category used when a Categorizer returns empty
// for a tool. Plugin-authored tools without a declared category land here until
// the plugin manifest grows a `category:` field (tracked follow-up).
const CategoryOther = "other"

// Categorizer reports the category name for a tool. Implementations are
// typically thin wrappers over the broker registry's Category() method.
// Returning empty for an unknown name causes the stash to bucket the tool
// under CategoryOther.
type Categorizer interface {
	Categorize(toolName string) string
}

// CategorizerFunc adapts a function to the Categorizer interface.
type CategorizerFunc func(toolName string) string

func (f CategorizerFunc) Categorize(toolName string) string { return f(toolName) }

// Stash holds the cached full tool definitions and derived metadata for a
// single session's current tool selection. Stashes are immutable once built;
// selection changes produce a new Stash.
type Stash struct {
	SessionID     string
	SelectionHash string
	FullDefs      map[string]llmtypes.ToolDefinition // keyed by tool name
	Categories    map[string][]string                // category → []tool names (sorted)
	CategoryOf    map[string]string                  // tool name → category
	SummaryText   string                             // D5 pointer-summary
	BuiltAt       time.Time
}

// CategoriesList returns the category names present in this stash, sorted.
func (s *Stash) CategoriesList() []string {
	out := make([]string, 0, len(s.Categories))
	for c := range s.Categories {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// DefsForCategories returns the full definitions for tools in the given
// categories, preserving name order. Unknown categories contribute no tools.
func (s *Stash) DefsForCategories(cats []string) []llmtypes.ToolDefinition {
	wanted := make(map[string]struct{}, len(cats))
	for _, c := range cats {
		wanted[c] = struct{}{}
	}
	names := make([]string, 0)
	for c := range wanted {
		names = append(names, s.Categories[c]...)
	}
	sort.Strings(names)
	defs := make([]llmtypes.ToolDefinition, 0, len(names))
	for _, n := range names {
		if d, ok := s.FullDefs[n]; ok {
			defs = append(defs, d)
		}
	}
	return defs
}

// Manager is a per-process, session-keyed store of Stashes.
type Manager struct {
	mu          sync.RWMutex
	byID        map[string]*Stash
	categorizer Categorizer
	now         func() time.Time
}

// NewManager returns a Manager that uses cat to classify tools. If cat is nil,
// all tools land in CategoryOther.
func NewManager(cat Categorizer) *Manager {
	if cat == nil {
		cat = CategorizerFunc(func(string) string { return "" })
	}
	return &Manager{
		byID:        make(map[string]*Stash),
		categorizer: cat,
		now:         time.Now,
	}
}

// Get returns the current stash for a session, or nil if there is none or the
// selection-hash has drifted. Callers that need guaranteed freshness should
// prefer GetOrBuild.
func (m *Manager) Get(sessionID string, selected []llmtypes.ToolDefinition) *Stash {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byID[sessionID]
	if !ok {
		return nil
	}
	if s.SelectionHash != SelectionHash(selected) {
		return nil
	}
	return s
}

// GetOrBuild returns the session's current stash, rebuilding it if missing or
// stale. This is the common path from AssembleSlots.
func (m *Manager) GetOrBuild(sessionID string, selected []llmtypes.ToolDefinition) *Stash {
	if s := m.Get(sessionID, selected); s != nil {
		return s
	}
	return m.Build(sessionID, selected)
}

// Build constructs a fresh stash for the session and stores it, replacing any
// prior entry.
func (m *Manager) Build(sessionID string, selected []llmtypes.ToolDefinition) *Stash {
	s := m.buildStash(sessionID, selected)
	m.mu.Lock()
	m.byID[sessionID] = s
	m.mu.Unlock()
	return s
}

// Invalidate drops any stash for the session. Next Get/GetOrBuild rebuilds.
func (m *Manager) Invalidate(sessionID string) {
	m.mu.Lock()
	delete(m.byID, sessionID)
	m.mu.Unlock()
}

func (m *Manager) buildStash(sessionID string, selected []llmtypes.ToolDefinition) *Stash {
	fullDefs := make(map[string]llmtypes.ToolDefinition, len(selected))
	categoryOf := make(map[string]string, len(selected))
	categories := make(map[string][]string)

	for _, def := range selected {
		fullDefs[def.Name] = def
		cat := m.categorizer.Categorize(def.Name)
		if cat == "" {
			cat = CategoryOther
		}
		categoryOf[def.Name] = cat
		categories[cat] = append(categories[cat], def.Name)
	}
	for c := range categories {
		sort.Strings(categories[c])
	}

	return &Stash{
		SessionID:     sessionID,
		SelectionHash: SelectionHash(selected),
		FullDefs:      fullDefs,
		Categories:    categories,
		CategoryOf:    categoryOf,
		SummaryText:   buildSummary(selected, categories),
		BuiltAt:       m.now(),
	}
}

// SelectionHash returns a deterministic hash of the tool names in selected.
// Order-independent. Used to detect when the broker's output has changed for
// a session, triggering a stash rebuild.
func SelectionHash(selected []llmtypes.ToolDefinition) string {
	if len(selected) == 0 {
		return "empty"
	}
	names := make([]string, len(selected))
	for i, d := range selected {
		names[i] = d.Name
	}
	sort.Strings(names)
	h := sha256.Sum256([]byte(strings.Join(names, "\x00")))
	return hex.EncodeToString(h[:8]) // 16 hex chars — plenty of collision resistance for session-scoped detection
}

// buildSummary renders the D5 pointer-summary text. Target < 300 tokens
// regardless of tool count; grows O(category-count), not O(tool-count).
func buildSummary(selected []llmtypes.ToolDefinition, categories map[string][]string) string {
	if len(selected) == 0 {
		return ""
	}
	total := len(selected)
	cats := make([]string, 0, len(categories))
	for c := range categories {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	var b strings.Builder
	fmt.Fprintf(&b, "[%d tool%s available — pointer]\n", total, plural(total))
	b.WriteString("Categories: ")
	for i, c := range cats {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s(%d)", c, len(categories[c]))
	}
	b.WriteString("\nIf you need tools, say so and full definitions will load. Common signals:\n")
	for _, c := range cats {
		if hint := categoryHint(c); hint != "" {
			fmt.Fprintf(&b, "- %s → %s tools\n", hint, c)
		}
	}
	b.WriteString("You can also force-load with `/tools on` or force-unload with `/tools off`.")
	return b.String()
}

// categoryHint returns a short natural-language trigger for a category. Empty
// string means no hint (category is still listed, but no example line).
func categoryHint(category string) string {
	switch category {
	case "search":
		return `"search for X", "find X", "look up X"`
	case "core-io", "file-io":
		return `"read|show|write|edit X"`
	case "code-exec":
		return `"run X", "execute X", "bash X"`
	case "http":
		return `"fetch X", "GET/POST X"`
	case "agent":
		return `"spawn agent", "delegate X"`
	case "session":
		return `"sessions", "resume", "history"`
	case "context":
		return `"recall X", "what do you know about X"`
	case "mode":
		return `"switch mode", "plan/code/chat"`
	case "mcp":
		return "tool names prefixed `mcp_`"
	default:
		return ""
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
