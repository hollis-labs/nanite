// Package writeclaim finds replies that claim a completed write and cite an
// identifier for it. It is pure text analysis: whether a write tool actually ran
// is the caller's fact to supply.
//
// A claim needs both halves, in the same paragraph or an adjacent one: a
// completed-write phrase
// ("wrote", "created", "the write succeeded") and an id-shaped token (a ULID, a
// UUID, a tracker id such as CW-20260919-0004, or a long hex digest). Either
// alone is ordinary prose: "confirmed CW-20260919-0011 is in review" reports a
// read, and "I created a plan" cites nothing. Negated, future, conditional and
// interrogative sentences are not claims, and fenced code is ignored.
package writeclaim

import (
	"regexp"
	"sort"
	"strings"
)

// Finding is one detected write claim.
type Finding struct {
	// IDs are the id-shaped tokens near the claim, in order.
	IDs []string
	// Ungrounded are those IDs that are not in the grounded set: the ones the
	// caller has no other evidence for. Empty means every cited id is grounded.
	Ungrounded []string
	// Phrase is the completed-write phrase that matched.
	Phrase string
	// Paragraph is the paragraph holding the claim phrase, for the audit log.
	Paragraph string
}

var (
	reFence = regexp.MustCompile("(?s)```.*?```")

	reULID    = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)
	reUUID    = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	reTracker = regexp.MustCompile(`\b[A-Z]{2,4}-\d{8}-\d{2,4}\b`)
	reHex     = regexp.MustCompile(`\b[0-9a-f]{16,64}\b`)

	// A completed write: a past or perfect verb, or "<write noun/verb> ...
	// succeeded / completed / verified".
	reDone = regexp.MustCompile(`(?i)\b(?:` +
		`wrote|written|created|captured|saved|stored|persisted|recorded|filed|logged|updated|` +
		`committed|posted|deployed|merged|pushed|inserted|registered|published|added` +
		`)\b|(?i:\b(?:writ\w*|sav\w*|creat\w*|captur\w*|insert\w*|updat\w*|post(?:s|ed|ing)?|fil(?:e|es|ed|ing)|record(?:s|ed|ing)?|commit\w*|deploy\w*|push\w*)\s+` +
		`(?:was\s+|is\s+|has\s+been\s+|had\s+been\s+)?` +
		`(?:successful(?:ly)?|succeeded|completed?|done|confirmed|verified)\b)`)

	// A clause with any of these is not a claim of something that happened.
	// It is applied per clause, not per sentence: a modal or negation after the
	// claim ("... and you should see it", "... which will appear") must not
	// disarm a write verb that precedes it.
	reNotAClaim = regexp.MustCompile(`(?i)\b(?:not|never|unable|failed|fails|failing|couldn'?t|can'?t|cannot|didn'?t|wasn'?t|isn'?t|haven'?t|hasn'?t|` +
		`will|would|could|should|shall|might|may|going\s+to|plan\s+to|want\s+me\s+to|if\s+you|once|when\s+you|to\s+be)\b|n't\b`)

	// Clause boundaries inside a sentence.
	reClauseSplit = regexp.MustCompile(`(?i);|,|\s[-\x{2013}\x{2014}]+\s|\b(?:and|which|but|once|so|while|whereas|because|although|though|then)\b`)

	reParagraphs = regexp.MustCompile(`\n\s*\n`)
	reSentences  = regexp.MustCompile(`[.!?]+\s+|\n`)
)

// IDs returns the id-shaped tokens in text, in order of appearance, without
// duplicates.
func IDs(text string) []string {
	type hit struct {
		pos int
		s   string
	}
	var hits []hit
	for _, re := range []*regexp.Regexp{reULID, reUUID, reTracker, reHex} {
		for _, m := range re.FindAllStringIndex(text, -1) {
			hits = append(hits, hit{m[0], text[m[0]:m[1]]})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if !seen[h.s] {
			seen[h.s] = true
			out = append(out, h.s)
		}
	}
	return out
}

// Detect reports the first write claim in reply. grounded holds ids the caller
// has independent evidence for (ones that appeared in a successful write-capable
// tool result this turn or earlier in the session; never the user's message); they are reported, not hidden, so the caller can
// decide what a grounded id means.
func Detect(reply string, grounded map[string]bool) (Finding, bool) {
	clean := reFence.ReplaceAllString(reply, "")
	paras := reParagraphs.Split(clean, -1)
	for i, para := range paras {
		phrase := claimPhrase(para)
		if phrase == "" {
			continue
		}
		// The id may sit in the claim's own paragraph or the one either side
		// ("the write succeeded" ... "ID: 01M...").
		lo, hi := max(0, i-1), min(len(paras), i+2)
		ids := IDs(strings.Join(paras[lo:hi], "\n\n"))
		if len(ids) == 0 {
			continue
		}
		f := Finding{IDs: ids, Phrase: phrase, Paragraph: strings.TrimSpace(para)}
		for _, id := range ids {
			if !grounded[id] {
				f.Ungrounded = append(f.Ungrounded, id)
			}
		}
		return f, true
	}
	return Finding{}, false
}

// claimPhrase returns the completed-write phrase in the first sentence of para
// that asserts one, or "".
func claimPhrase(para string) string {
	for _, s := range reSentences.Split(para, -1) {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasSuffix(s, "?") {
			continue
		}
		for _, clause := range reClauseSplit.Split(s, -1) {
			if reNotAClaim.MatchString(clause) {
				continue
			}
			if m := reDone.FindString(clause); m != "" {
				return m
			}
		}
	}
	return ""
}
