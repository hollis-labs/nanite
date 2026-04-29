// Mode classification for B2 (CW-20260428-0010).
//
// This is a sibling of scope.go's ScopeTier / ExecutionPattern primitive but
// answers a different question: "what mode does the user's current message
// imply?" — chat (default conversational), plan (design / outline before
// doing), or work (execute). The output is non-binding; ticket B2 only
// surfaces it as an SSE `mode_suggestion` event for the FE to log. B3 will
// wire confirm-card / auto-apply on top.
//
// The classifier is intentionally deterministic and rules-based for v1
// (matches doc.go D5 — "rules-based MVP; LLM-judge deferred"). An LLM
// tiebreak (e.g. Haiku for ambiguous inputs) is a tracked follow-up and
// MUST NOT be added here without an ADR.
package classify

import (
	"strings"
	"unicode"
)

// ModeSignal labels the rule that contributed to a ModeResult. The constant
// values are stable wire strings — they ride the SSE event into the FE
// inspector and into B3's confirm-card "reason" line, so renaming them is a
// breaking change.
//
// Values are lowercase snake_case and self-describing: the FE renders them
// inline ("we detected '<signal>' in your message") so they MUST be readable
// without further massaging — no machine prefixes, no kebab-case, no
// FE-side prefix-stripping helpers (CW-20260429-0003).
type ModeSignal string

const (
	SignalSlashChat      ModeSignal = "slash_chat"
	SignalSlashPlan      ModeSignal = "slash_plan"
	SignalSlashWork      ModeSignal = "slash_work"
	SignalImperativePlan ModeSignal = "imperative_plan_phrase"
	SignalImperativeWork ModeSignal = "imperative_work_phrase"
	SignalActionVerbWork ModeSignal = "action_verb_work"
	SignalDefaultChat    ModeSignal = "default_chat"
)

// ModeResult is the output of ClassifyMode. Suggested is one of "chat",
// "plan", "work" (or a future custom slug — but v1 emits only those three).
// Confidence is in [0, 1]. Signals lists every rule that fired ordered most
// specific first (slash before phrase before action verb), useful for the FE
// inspector and for B3's confirm-card "reason" line.
//
// Empty input returns the zero ModeResult (Suggested == "", Confidence == 0,
// nil Signals). Callers MUST treat that as "no signal" and skip emission.
type ModeResult struct {
	Suggested  string
	Confidence float64
	Signals    []ModeSignal
}

// imperativePlanPhrases are case-insensitive substring triggers for the
// "plan" mode. Anchored at word boundaries by phraseHit to avoid e.g.
// "planet" matching "plan a".
var imperativePlanPhrases = []string{
	"help me plan",
	"let's plan",
	"let me plan",
	"draft a",
	"draft an",
	"outline",
	"schedule",
	"organize",
	"brainstorm",
	"design a",
	"design an",
	"design the",
	"plan a",
	"plan the",
}

// imperativeWorkPhrases are case-insensitive substring triggers for the
// "work" mode. Same word-boundary semantics as imperativePlanPhrases.
var imperativeWorkPhrases = []string{
	"let's implement",
	"let's build",
	"let's ship",
	"help me fix",
	"go ahead and",
	"please implement",
	"please fix",
}

// actionVerbsWork are first-word triggers for "work" mode. Matched against
// the lower-cased first whitespace-separated token of the trimmed input.
var actionVerbsWork = map[string]bool{
	"implement": true,
	"fix":       true,
	"refactor":  true,
	"build":     true,
	"ship":      true,
	"deploy":    true,
	"merge":     true,
	"commit":    true,
	"push":      true,
	"rebase":    true,
	"revert":    true,
	"test":      true,
	"add":       true,
	"remove":    true,
	"delete":    true,
	"create":    true,
	"update":    true,
	"rename":    true,
}

// ClassifyMode runs deterministic v1 rules over the user's current message
// and returns a non-binding mode suggestion. Priority order (highest first):
//
//  1. Slash prefix       — confidence 1.0
//  2. Imperative phrases — confidence 0.85
//  3. Action verbs       — confidence 0.7
//  4. Default            — confidence 0.0 (chat)
//
// Higher-confidence rules short-circuit lower ones — once a slash prefix or
// imperative phrase fires we return immediately. Empty input returns the
// zero ModeResult (no signal).
func ClassifyMode(text string) ModeResult {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ModeResult{}
	}
	lower := strings.ToLower(trimmed)

	// Rule 1 — slash prefix. Confidence 1.0; the user typed it explicitly.
	switch {
	case strings.HasPrefix(lower, "/chat"):
		return ModeResult{Suggested: "chat", Confidence: 1.0, Signals: []ModeSignal{SignalSlashChat}}
	case strings.HasPrefix(lower, "/plan"):
		return ModeResult{Suggested: "plan", Confidence: 1.0, Signals: []ModeSignal{SignalSlashPlan}}
	case strings.HasPrefix(lower, "/work"):
		return ModeResult{Suggested: "work", Confidence: 1.0, Signals: []ModeSignal{SignalSlashWork}}
	}

	// Rule 2 — imperative phrases. Substring match with word-boundary
	// anchoring to avoid "planet" → "plan a" false positives.
	for _, phrase := range imperativePlanPhrases {
		if phraseHit(lower, phrase) {
			return ModeResult{Suggested: "plan", Confidence: 0.85, Signals: []ModeSignal{SignalImperativePlan}}
		}
	}
	for _, phrase := range imperativeWorkPhrases {
		if phraseHit(lower, phrase) {
			return ModeResult{Suggested: "work", Confidence: 0.85, Signals: []ModeSignal{SignalImperativeWork}}
		}
	}

	// Rule 3 — action verbs as the first token. "implement X" → work,
	// "build the foo" → work, etc.
	first := firstToken(lower)
	if first != "" && actionVerbsWork[first] {
		return ModeResult{Suggested: "work", Confidence: 0.7, Signals: []ModeSignal{SignalActionVerbWork}}
	}

	// Rule 4 — default. Confidence 0 means "no real signal, falling back".
	return ModeResult{Suggested: "chat", Confidence: 0.0, Signals: []ModeSignal{SignalDefaultChat}}
}

// phraseHit reports whether phrase appears in haystack with word-boundary
// anchoring at both ends. A "word boundary" here means start-of-string,
// end-of-string, or any non-letter/non-digit rune. Both haystack and phrase
// are expected to be already lower-cased by the caller.
//
// This is the guard that keeps "planet vs space" from triggering "plan"
// (because "planet" is one token; the "et" after "plan" fails the
// trailing-boundary check).
func phraseHit(haystack, phrase string) bool {
	if phrase == "" || len(haystack) < len(phrase) {
		return false
	}
	start := 0
	for {
		idx := strings.Index(haystack[start:], phrase)
		if idx < 0 {
			return false
		}
		absIdx := start + idx
		if isWordBoundary(haystack, absIdx) && isWordBoundary(haystack, absIdx+len(phrase)) {
			return true
		}
		// Advance past this attempt (at least one rune) and keep searching;
		// a later match in the same string may still satisfy the boundary.
		start = absIdx + 1
		if start >= len(haystack) {
			return false
		}
	}
}

// isWordBoundary reports whether position pos in s is at a word boundary —
// either the start/end of the string, or a position where the surrounding
// runes are not both letters/digits. This treats apostrophes and quotes as
// non-letter so "let's" splits cleanly into "let" + "s" for matching.
func isWordBoundary(s string, pos int) bool {
	if pos <= 0 || pos >= len(s) {
		return true
	}
	prev := rune(s[pos-1])
	next := rune(s[pos])
	return !(isWordRune(prev) && isWordRune(next))
}

// isWordRune is true for letters and digits only — apostrophes, quotes, and
// punctuation are non-word so they create boundaries for phrase matching.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// firstToken returns the first whitespace-separated token of s, stripped of
// leading/trailing punctuation so "implement," → "implement". Returns "" if
// s contains no letter runes.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	end := strings.IndexFunc(s, unicode.IsSpace)
	var tok string
	if end < 0 {
		tok = s
	} else {
		tok = s[:end]
	}
	tok = strings.TrimFunc(tok, func(r rune) bool { return !isWordRune(r) })
	return tok
}
