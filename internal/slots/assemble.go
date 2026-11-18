package slots

import (
	"context"
	"fmt"
	"maps"
	"strings"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
)

type Mode uint8

const (
	ModeShip Mode = iota
	ModePointer
	ModeSkip
)

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
	Owner, Key string
	Mode       Mode
	Reason     string
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
	for _, slot := range names() {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		entries := in.entries[slot]
		budget, limited := budgets[slot]
		text := ""
		for _, e := range entries {
			pick := Decision{Owner: e.Owner, Key: e.Key, Mode: ModeShip}
			candidate := appendEntry(text, e, e.Content, len(entries) > 1)
			if slot != Agent && limited && s.est.Estimate(candidate) > budget {
				candidate = appendEntry(text, e, e.Pointer, len(entries) > 1)
				pick.Mode, pick.Reason = ModePointer, "content exceeds slot budget"
				if e.Pointer == "" || s.est.Estimate(candidate) > budget {
					candidate = text
					pick.Mode, pick.Reason = ModeSkip, "entry exceeds remaining slot budget"
				}
			}
			out.Decisions[slot] = append(out.Decisions[slot], pick)
			text = candidate
		}
	}
	return out, nil
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
}

func Assemble(ctx context.Context, in Snapshot, plan Plan, prev map[string]string, windowSize int, est ctxpkg.TokenEstimator) (Assembled, error) {
	window := ctxpkg.NewContextWindow(windowSize, est)
	window.PrevHashes = maps.Clone(prev)
	report := make(Report)
	for _, slot := range names() {
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
				d = Decision{Owner: e.Owner, Key: e.Key, Mode: ModeSkip, Reason: "omitted by selector"}
			}
			if slot == Agent {
				d.Mode, d.Reason = ModeShip, ""
			}
			var body string
			switch d.Mode {
			case ModeShip:
				body = e.Content
			case ModePointer:
				body = e.Pointer
			case ModeSkip:
			default:
				return Assembled{}, fmt.Errorf("slot %q has invalid selection mode %d", slot, d.Mode)
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
		report[slot] = SlotReport{Tokens: state.TokenCount, Changed: prev[name] != state.CacheKey, Decisions: applied}
	}
	return Assembled{Blocks: window.Assemble(), Window: window, Report: report}, nil
}
