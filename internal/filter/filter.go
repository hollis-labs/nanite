// Package filter provides configurable output filters that transform LLM
// responses before they are displayed to the user or persisted. Filters run
// post-LLM-response, pre-display.
package filter

import (
	"strings"
	"unicode"
)

// FilterFunc transforms output text. Every filter is a simple string->string function.
type FilterFunc func(string) string

// Chain holds an ordered list of output filters. Filters execute in order;
// each receives the output of the previous filter.
type Chain struct {
	filters []namedFilter
}

type namedFilter struct {
	name string
	fn   FilterFunc
}

// NewChain creates an empty filter chain.
func NewChain() *Chain {
	return &Chain{}
}

// Add appends a named filter to the chain.
func (c *Chain) Add(name string, fn FilterFunc) {
	c.filters = append(c.filters, namedFilter{name: name, fn: fn})
}

// Apply runs every filter in sequence on the input text.
func (c *Chain) Apply(text string) string {
	for _, f := range c.filters {
		text = f.fn(text)
	}
	return text
}

// Len returns the number of filters in the chain.
func (c *Chain) Len() int {
	return len(c.filters)
}

// Names returns the names of all filters in the chain.
func (c *Chain) Names() []string {
	names := make([]string, len(c.filters))
	for i, f := range c.filters {
		names[i] = f.name
	}
	return names
}

// Registry maps filter names to their constructor functions, making it easy
// to build a Chain from a configuration list.
var Registry = map[string]FilterFunc{
	"no_emoji": NoEmoji,
}

// FromNames builds a Chain from a list of filter names. Unknown names are
// silently skipped (callers can compare Chain.Len() with input length).
func FromNames(names []string) *Chain {
	c := NewChain()
	for _, name := range names {
		if fn, ok := Registry[name]; ok {
			c.Add(name, fn)
		}
	}
	return c
}

// ---------------------------------------------------------------------------
// Built-in filters
// ---------------------------------------------------------------------------

// NoEmoji strips Unicode emoji characters from text. It covers:
//   - Emoticons (U+1F600..U+1F64F)
//   - Miscellaneous Symbols & Pictographs (U+1F300..U+1F5FF)
//   - Transport & Map Symbols (U+1F680..U+1F6FF)
//   - Supplemental Symbols & Pictographs (U+1F900..U+1F9FF)
//   - Symbols & Pictographs Extended-A (U+1FA00..U+1FA6F, U+1FA70..U+1FAFF)
//   - Dingbats (U+2702..U+27B0)
//   - Miscellaneous Symbols (U+2600..U+26FF)
//   - Variation Selectors (U+FE00..U+FE0F)
//   - Zero-Width Joiner (U+200D)
//   - Regional Indicator Symbols (U+1F1E0..U+1F1FF)
//   - Skin tone modifiers (U+1F3FB..U+1F3FF)
//   - Keycap combining (#\uFE0F\u20E3 sequences via U+20E3)
func NoEmoji(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if isEmoji(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isEmoji returns true if the rune is in a Unicode emoji range.
func isEmoji(r rune) bool {
	switch {
	// Emoticons
	case r >= 0x1F600 && r <= 0x1F64F:
		return true
	// Miscellaneous Symbols and Pictographs
	case r >= 0x1F300 && r <= 0x1F5FF:
		return true
	// Transport and Map Symbols
	case r >= 0x1F680 && r <= 0x1F6FF:
		return true
	// Supplemental Symbols and Pictographs
	case r >= 0x1F900 && r <= 0x1F9FF:
		return true
	// Symbols and Pictographs Extended-A
	case r >= 0x1FA00 && r <= 0x1FAFF:
		return true
	// Regional Indicator Symbols
	case r >= 0x1F1E0 && r <= 0x1F1FF:
		return true
	// Skin tone modifiers
	case r >= 0x1F3FB && r <= 0x1F3FF:
		return true
	// Dingbats
	case r >= 0x2702 && r <= 0x27B0:
		return true
	// Miscellaneous Symbols
	case r >= 0x2600 && r <= 0x26FF:
		return true
	// Variation Selectors
	case r >= 0xFE00 && r <= 0xFE0F:
		return true
	// Zero-Width Joiner
	case r == 0x200D:
		return true
	// Combining Enclosing Keycap
	case r == 0x20E3:
		return true
	// CJK Symbols that are used as emoji
	case r == 0x3030 || r == 0x303D:
		return true
	// Copyright, Registered, TM (often rendered as emoji)
	case r == 0x00A9 || r == 0x00AE || r == 0x2122:
		return true
	// Various arrows and symbols used as emoji
	case r >= 0x2194 && r <= 0x21AA:
		return true
	// Information source, other misc emoji
	case r >= 0x2139 && r <= 0x2149:
		return true
	// Enclosed alphanumeric supplement
	case r >= 0x1F100 && r <= 0x1F1FF:
		return true
	// Playing cards, mahjong
	case r == 0x1F004 || r == 0x1F0CF:
		return true
	// Avoid stripping normal printable text, letters, digits, punctuation
	default:
		// Catch remaining symbols in the Supplementary Multilingual Plane
		// that are commonly used as emoji (miscellaneous symbols blocks).
		if r > 0xFFFF && unicode.Is(unicode.So, r) {
			return true
		}
		return false
	}
}
