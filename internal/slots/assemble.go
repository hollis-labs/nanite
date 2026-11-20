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
		text := ""
		for _, e := range entries {
			pick := Decision{Owner: e.Owner, Key: e.Key, Disposition: Ship}
			candidate := appendEntry(text, e, e.Content, len(entries) > 1)
			if slot != Agent && budget > 0 && s.est.Estimate(candidate) > budget {
				candidate = appendEntry(text, e, e.Pointer, len(entries) > 1)
				pick.Disposition, pick.Reason = Pointer, "content exceeds slot budget"
				if e.Pointer == "" || s.est.Estimate(candidate) > budget {
					// Never drop an entry silently: ship as much as fits, marked.
					trimmed, ok := trimToFit(s.est, text, e, slot, len(entries) > 1, budget)
					if ok {
						candidate = appendEntry(text, e, trimmed, len(entries) > 1)
						pick.Disposition, pick.Reason = Trim, "content cut to fit slot budget"
					} else {
						candidate = text
						pick.Disposition, pick.Reason = Skip, "no room left in slot budget"
					}
				}
			}
			out.Decisions[slot] = append(out.Decisions[slot], pick)
			text = candidate
		}
	}
	return out, nil
}

// trimToFit returns the longest leading part of e's content, plus a marker,
// that appended to text stays within budget. It reports false when less
// than minTrimTokens of room is left.
func trimToFit(est ctxpkg.TokenEstimator, text string, e Entry, slot Name, header bool, budget int) (string, bool) {
	if est.Estimate(appendEntry(text, e, "x", header)) > budget-minTrimTokens {
		return "", false
	}
	marker := fmt.Sprintf("\n[trimmed to fit the %s slot budget of %d tokens]", slot, budget)
	runes := []rune(e.Content)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if est.Estimate(appendEntry(text, e, string(runes[:mid])+marker, header)) <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return "", false
	}
	return string(runes[:lo]) + marker, true
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
		var text strings.Builder
		var applied []Decision
		for _, e := range entries {
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
				if limit <= 0 {
					body = e.Content
					break
				}
				trimmed, ok := trimToFit(est, text.String(), e, slot, len(entries) > 1, limit)
				if !ok {
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
				text.WriteString(appendEntry("", e, body, len(entries) > 1))
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
