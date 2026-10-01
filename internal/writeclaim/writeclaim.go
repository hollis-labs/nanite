// Package writeclaim finds replies that claim a completed write and cite an
// identifier for it. It is pure text analysis: whether a write tool actually ran
// is the caller's fact to supply.
//
// A claim needs both halves, in the same paragraph or an adjacent one: a
// completed-write phrase about the assistant's own action
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
	"unicode"

	"golang.org/x/text/unicode/norm"
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

	// Ids are matched case-insensitively and reported in a canonical case, so
	// "01m3q..." and "01M3Q..." are the same id. The candidate patterns are
	// loose; idShaped applies the content rules that keep ordinary words out.
	reULID    = regexp.MustCompile(`(?i)\b[0-9A-HJKMNP-TV-Z]{26}\b`)
	reUUID    = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reTracker = regexp.MustCompile(`(?i)\b[A-Z]{2,4}-\d{8}-\d{2,4}\b`)
	reHex     = regexp.MustCompile(`(?i)\b[0-9a-f]{16,64}\b`)

	// A claim that names its id ("the returned ID", "id above") may take one
	// from an adjacent paragraph; a bare write phrase may not.
	reRefersToID = regexp.MustCompile(`(?i)\b(?:id|ids|identifier|key|handle|reference|returned)\b`)

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

	// A record being "updated" is evidence of activity, not a claim that the
	// assistant updated it. Accept explicit first-person actions and terse
	// outcome statements, while leaving third-person activity reports alone.
	reOwnAction   = regexp.MustCompile(`(?i)\b(?:i|we|i've|we've|i'd|we'd)(?:\s+(?:have|had|just|also|already|successfully|now|actually|previously))*\s+$`)
	reOwnPassive  = regexp.MustCompile(`(?i)\bby\s+(?:me|us)\b`)
	reTerseAction = regexp.MustCompile(`(?i)^(?:(?:also|done|recap)\s*[:,.-]?\s*)?$`)

	reParagraphs = regexp.MustCompile(`\n\s*\n`)
	reSentences  = regexp.MustCompile(`[.!?]+\s+|\n`)
)

// confusables maps letters from other scripts that render like Latin letters
// to the Latin letter. NFKC folds fullwidth and compatibility forms but not
// these, and a fabricated id written with them would otherwise slip past the
// patterns and past a grounded-id comparison.
var confusables = map[rune]rune{
	// Cyrillic
	'А': 'A', 'В': 'B', 'С': 'C', 'Е': 'E', 'Н': 'H', 'К': 'K', 'М': 'M', 'О': 'O', 'Р': 'P', 'Т': 'T', 'Х': 'X', 'І': 'I', 'Ѕ': 'S', 'Ј': 'J',
	'а': 'a', 'с': 'c', 'е': 'e', 'о': 'o', 'р': 'p', 'х': 'x', 'у': 'y', 'і': 'i', 'ѕ': 's', 'ј': 'j',
	// Greek
	'Α': 'A', 'Β': 'B', 'Ε': 'E', 'Ζ': 'Z', 'Η': 'H', 'Ι': 'I', 'Κ': 'K', 'Μ': 'M', 'Ν': 'N', 'Ο': 'O', 'Ρ': 'P', 'Τ': 'T', 'Υ': 'Y', 'Χ': 'X',
	'ο': 'o', 'ν': 'v',
	// dashes and minus signs
	'\u2010': '-', '\u2011': '-', '\u2012': '-', '\u2013': '-', '\u2014': '-', '\u2015': '-', '\u2212': '-', '\uFE58': '-', '\uFE63': '-', '\uFF0D': '-',
}

// Normalize folds text to the form the patterns match: NFKC, zero-width and
// format characters removed, lookalike letters and dashes mapped to ASCII.
func Normalize(text string) string {
	text = norm.NFKC.String(text)
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if unicode.Is(unicode.Cf, r) { // zero-width space/joiner, BOM, soft hyphen, bidi marks
			continue
		}
		if m, ok := confusables[r]; ok {
			r = m
		}
		b.WriteRune(r)
	}
	return b.String()
}

// idShaped applies the content rules that separate an id from a word or a
// number: an id-length token with no digit is prose, and a long hex run needs
// both a digit and a letter (a bare digit string is a number, not a digest).
func idShaped(tok string, kind int) bool {
	digits, letters := 0, 0
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case unicode.IsLetter(r):
			letters++
		}
	}
	switch kind {
	case 0: // ULID
		return digits >= 4
	case 3: // hex digest
		return digits >= 1 && letters >= 1
	}
	return true
}

// canonicalID puts an id in the case its kind is written in, so a lowercase
// copy of an id compares equal to the original.
func canonicalID(tok string) string {
	if strings.Count(tok, "-") == 4 && len(tok) == 36 { // UUID
		return strings.ToLower(tok)
	}
	if strings.Contains(tok, "-") { // tracker id
		return strings.ToUpper(tok)
	}
	if len(tok) == 26 { // ULID
		return strings.ToUpper(tok)
	}
	return strings.ToLower(tok) // hex digest
}

// IDs returns the id-shaped tokens in text, canonicalized, in order of
// appearance, without duplicates. Text is normalized first, so lookalike and
// fullwidth characters do not hide an id.
func IDs(text string) []string {
	text = Normalize(text)
	type hit struct {
		pos int
		s   string
	}
	var hits []hit
	for kind, re := range []*regexp.Regexp{reULID, reUUID, reTracker, reHex} {
		for _, m := range re.FindAllStringIndex(text, -1) {
			tok := text[m[0]:m[1]]
			if idShaped(tok, kind) {
				hits = append(hits, hit{m[0], canonicalID(tok)})
			}
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

// Detect reports the write claims in reply as one Finding: every id cited by a
// claiming paragraph, not just the first claim's. grounded holds ids the caller
// has independent evidence for (ones that appeared in a successful
// write-capable tool result this turn or earlier in the session; never the
// user's message). They are reported, not hidden, so the caller can decide what
// a grounded id means. Grounded ids are compared in canonical case.
func Detect(reply string, grounded map[string]bool) (Finding, bool) {
	// Compare in canonical form whatever case or script the caller's set was
	// built in.
	canon := make(map[string]bool, len(grounded))
	for id, ok := range grounded {
		if ok {
			for _, c := range IDs(id) {
				canon[c] = true
			}
		}
	}
	clean := reFence.ReplaceAllString(Normalize(reply), "")
	paras := reParagraphs.Split(clean, -1)
	var f Finding
	seen := map[string]bool{}
	for i, para := range paras {
		for _, sentence := range reSentences.Split(para, -1) {
			phrase := claimPhrase(sentence)
			if phrase == "" {
				continue
			}
			// Bind receipts to the claiming sentence. Other sentences in an
			// activity summary can cite records the assistant only read.
			ids := IDs(sentence)
			if len(ids) == 0 && reRefersToID.MatchString(sentence) {
				ids = IDs(para)
				if len(ids) == 0 {
					lo, hi := max(0, i-1), min(len(paras), i+2)
					ids = IDs(strings.Join(paras[lo:hi], "\n\n"))
				}
			}
			if len(ids) == 0 {
				continue
			}
			if f.Phrase == "" {
				f.Phrase, f.Paragraph = phrase, strings.TrimSpace(para)
			}
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				f.IDs = append(f.IDs, id)
				if !canon[id] {
					f.Ungrounded = append(f.Ungrounded, id)
				}
			}
		}
	}
	return f, len(f.IDs) > 0
}

// claimPhrase returns a completed-write phrase asserting the assistant's own
// action, rather than a third-party record's history.
func claimPhrase(sentence string) string {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" || strings.HasSuffix(sentence, "?") {
		return ""
	}
	for _, clause := range reClauseSplit.Split(sentence, -1) {
		if reNotAClaim.MatchString(clause) {
			continue
		}
		for _, match := range reDone.FindAllStringIndex(clause, -1) {
			phrase := clause[match[0]:match[1]]
			prefix := strings.TrimLeft(strings.TrimSpace(clause[:match[0]]), "-*#> ")
			// Multi-word matches explicitly report a successful write, such
			// as "the write succeeded" or "task creation was successful".
			if strings.ContainsAny(phrase, " \t") || reOwnAction.MatchString(clause[:match[0]]) || reOwnPassive.MatchString(clause[match[1]:]) || reTerseAction.MatchString(prefix) {
				return phrase
			}
		}
	}
	return ""
}
