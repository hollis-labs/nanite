package slots

import (
	"context"
	"fmt"
	"maps"
	"strings"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
)

type Disposition uint8

const (
	Ship Disposition = iota
	Pointer
	Skip
	// Trim ships the start of the entry's content, cut to fit what is left of
	// the slot's budget, with a marker saying so.
	Trim
)

// minTrimTokens is the least room worth trimming an entry into; with less,
// the entry is skipped (and reported).
const minTrimTokens = 32

type Source interface {
	Name() string
	Slots(ctx context.Context, turn TurnInfo) ([]Update, error)
}
type Update struct {
	Slot  Name
	Entry Entry
}
type TurnInfo struct{ ConversationID, TurnID, UserText, Role string }

type Selector interface {
	Select(ctx context.Context, turn TurnInfo, in Snapshot, budgets map[Name]int) (Plan, error)
}
type Plan struct{ Decisions map[Name][]Decision }
type Decision struct {
	Owner, Key  string
	Disposition Disposition
	Reason      string
}

type defaultSelector struct{ est ctxpkg.TokenEstimator }

func DefaultSelector(est ctxpkg.TokenEstimator) Selector {
	if est == nil {
		est = ctxpkg.DefaultEstimator{}
	}
	return defaultSelector{est}
}

func (s defaultSelector) Select(ctx context.Context, _ TurnInfo, in Snapshot, budgets map[Name]int) (Plan, error) {
	out := Plan{Decisions: make(map[Name][]Decision)}
	for _, slot := range Names() {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		entries := in.entries[slot]
		budget := budgets[slot]
		header := len(entries) > 1
		held := reserved(s.est, entries, header)
		text := ""
		for _, e := range entries {
			if e.Reserve {
				held -= reserveCost(s.est, e, header)
			}
			room := budget - held
			pick := Decision{Owner: e.Owner, Key: e.Key, Disposition: Ship}
			candidate := appendEntry(text, e, e.Content, header)
			if slot != Agent && budget > 0 && s.est.Estimate(candidate) > room {
				if e.Reserve {
					candidate, pick = s.selectReserved(text, e, slot, header, room, pick)
				} else {
					candidate = appendEntry(text, e, e.Pointer, header)
					pick.Disposition, pick.Reason = Pointer, "content exceeds slot budget"
					if e.Pointer == "" || s.est.Estimate(candidate) > room {
						// Never drop an entry silently: ship as much as fits, marked.
						trimmed, ok := trimToFit(s.est, text, e, slot, header, room)
						if ok {
							candidate = appendEntry(text, e, trimmed, header)
							pick.Disposition, pick.Reason = Trim, "content cut to fit slot budget"
						} else {
							candidate = text
							pick.Disposition, pick.Reason = Skip, "no room left in slot budget"
						}
					}
				}
			}
			out.Decisions[slot] = append(out.Decisions[slot], pick)
			text = candidate
		}
	}
	return out, nil
}

// selectReserved picks for an entry whose Pointer has room held: a trim of
// its content when one fits, else the Pointer.
func (s defaultSelector) selectReserved(text string, e Entry, slot Name, header bool, room int, pick Decision) (string, Decision) {
	if trimmed, ok := trimToFit(s.est, text, e, slot, header, room); ok {
		pick.Disposition, pick.Reason = Trim, "content cut to fit slot budget"
		return appendEntry(text, e, trimmed, header), pick
	}
	if candidate := appendEntry(text, e, e.Pointer, header); e.Pointer != "" && s.est.Estimate(candidate) <= room {
		pick.Disposition, pick.Reason = Pointer, "content exceeds slot budget; reserved pointer shipped"
		return candidate, pick
	}
	pick.Disposition, pick.Reason = Skip, "no room left in slot budget"
	return text, pick
}

// reserveCost is the room held for a reserved entry's Pointer, with slack for
// an estimator that does not add up exactly across joined entries.
func reserveCost(est ctxpkg.TokenEstimator, e Entry, header bool) int {
	if !e.Reserve || e.Pointer == "" {
		return 0
	}
	return est.Estimate(appendEntry("", e, e.Pointer, header)) + est.Estimate("\n\n") + 1
}

// reserved is the room held for every reserved entry's Pointer.
func reserved(est ctxpkg.TokenEstimator, entries []Entry, header bool) int {
	total := 0
	for _, e := range entries {
		total += reserveCost(est, e, header)
	}
	return total
}

// trimToFit returns the longest leading part of e's content, plus a marker,
// that appended to text stays within budget. It reports false when less
// than minTrimTokens of room is left.
func trimToFit(est ctxpkg.TokenEstimator, text string, e Entry, slot Name, header bool, budget int) (string, bool) {
	if est.Estimate(appendEntry(text, e, "x", header)) > budget-minTrimTokens {
		return "", false
	}
	marker := fmt.Sprintf("\n[trimmed to fit the %s slot budget of %d tokens]", slot, budget)
	// Candidate cut points are rune boundaries, as byte offsets, so the
	// shipped text is always a byte prefix of the original, even when the
	// content holds invalid UTF-8.
	cuts := []int{0}
	for i := range e.Content {
		if i > 0 {
			cuts = append(cuts, i)
		}
	}
	cuts = append(cuts, len(e.Content))
	lo, hi := 0, len(cuts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if est.Estimate(appendEntry(text, e, e.Content[:cuts[mid]]+marker, header)) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return "", false
	}
	return e.Content[:cuts[lo]] + marker, true
}

func appendEntry(dst string, e Entry, content string, header bool) string {
	if content == "" {
		return dst
	}
	if header {
		content = "### " + e.Owner + "/" + e.Key + "\n" + content
	}
	if dst != "" {
		return dst + "\n\n" + content
	}
	return content
}

type SlotReport struct {
	Tokens    int
	Changed   bool
	Decisions []Decision
}
type Report map[Name]SlotReport
type Assembled struct {
	Blocks []ctxpkg.SlotBlock
	Window *ctxpkg.ContextWindow
	Report Report
	Hashes map[string]string
}

// Assemble builds the slot blocks. budgets overrides the library's default
// per-slot budget for the slots it names, and should be the map the selector
// planned with; nil keeps the defaults.
func Assemble(ctx context.Context, in Snapshot, plan Plan, prev map[string]string, windowSize int, est ctxpkg.TokenEstimator, budgets map[Name]int) (Assembled, error) {
	if est == nil {
		est = ctxpkg.DefaultEstimator{}
	}
	window := ctxpkg.NewContextWindow(windowSize, est)
	for name, size := range budgets {
		if s := window.Slot(string(name)); s != nil && size > 0 {
			s.MaxTokens = size
		}
	}
	window.PrevHashes = maps.Clone(prev)
	report := make(Report)
	hashes := make(map[string]string)
	for _, slot := range Names() {
		if err := ctx.Err(); err != nil {
			return Assembled{}, err
		}
		decisions := make(map[ownerKey]Decision)
		for _, d := range plan.Decisions[slot] {
			decisions[ownerKey{d.Owner, d.Key}] = d
		}
		entries := in.entries[slot]
		header := len(entries) > 1
		held := reserved(est, entries, header)
		var text strings.Builder
		var applied []Decision
		for _, e := range entries {
			if e.Reserve {
				held -= reserveCost(est, e, header)
			}
			d, ok := decisions[ownerKey{e.Owner, e.Key}]
			if !ok {
				d = Decision{Owner: e.Owner, Key: e.Key, Disposition: Skip, Reason: "omitted by selector"}
			}
			if slot == Agent {
				d.Disposition, d.Reason = Ship, ""
			}
			var body string
			switch d.Disposition {
			case Ship:
				body = e.Content
			case Pointer:
				body = e.Pointer
			case Trim:
				limit := window.Slot(string(slot)).MaxTokens
				if limit <= 0 || est.Estimate(appendEntry(text.String(), e, e.Content, header)) <= limit-held {
					body = e.Content // it fits; nothing to cut
					break
				}
				trimmed, ok := trimToFit(est, text.String(), e, slot, header, limit-held)
				if !ok {
					if e.Reserve && e.Pointer != "" {
						d.Disposition, d.Reason, body = Pointer, "content exceeds slot budget; reserved pointer shipped", e.Pointer
						break
					}
					d.Disposition, d.Reason = Skip, "no room left in slot budget"
					break
				}
				body = trimmed
			case Skip:
			default:
				return Assembled{}, fmt.Errorf("slot %q has invalid selection mode %d", slot, d.Disposition)
			}
			if body != "" {
				if text.Len() > 0 {
					text.WriteString("\n\n")
				}
				text.WriteString(appendEntry("", e, body, header))
			}
			applied = append(applied, d)
		}
		name := string(slot)
		if text.Len() > 0 {
			window.SetContent(name, text.String())
		}
		state := window.Slot(name)
		if slot != Agent && state.MaxTokens > 0 && state.TokenCount > state.MaxTokens {
			return Assembled{}, fmt.Errorf("selector exceeded %q slot budget: %d > %d", slot, state.TokenCount, state.MaxTokens)
		}
		if text.Len() > 0 {
			hashes[name] = state.CacheKey
		}
		report[slot] = SlotReport{Tokens: state.TokenCount, Changed: prev[name] != state.CacheKey, Decisions: applied}
	}
	return Assembled{Blocks: window.Assemble(), Window: window, Report: report, Hashes: hashes}, nil
}
