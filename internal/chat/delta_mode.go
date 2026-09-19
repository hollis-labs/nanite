package chat

import (
	"context"
	"fmt"
	"strings"
)

// DeltaMode selects how a turn's provider text deltas reach the SSE stream.
// It is a per-turn delivery choice made by the consumer: it changes when
// deltas are sent and whether they carry a Phase, never what the saved
// message contains.
type DeltaMode string

const (
	// DeltaModePhased is the default. Each provider iteration's deltas are held
	// until its stop reason is known, then flushed tagged PhaseNarration or
	// PhaseFinal (F4 / CW-20260419-0029). The stream is silent for the whole
	// iteration and then delivers it in one burst.
	DeltaModePhased DeltaMode = "phased"

	// DeltaModeLive sends each provider text delta as it arrives, with no
	// Phase. A consumer cannot tell narration from the answer except by the
	// tool_call events between them. Deltas Nanite writes itself (early-stop
	// synthesis, envelope blocks, clarifying questions) are not provider
	// output and still carry PhaseFinal.
	DeltaModeLive DeltaMode = "live"
)

// ParseDeltaMode converts a request value to a DeltaMode. Empty means
// DeltaModePhased. Matching is case-insensitive with surrounding space
// stripped, like effort.Parse; unlike it, an unknown value is an error rather
// than a silent fallback — a mistyped "live" would otherwise get the buffered
// stream back with nothing to say why.
func ParseDeltaMode(s string) (DeltaMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(DeltaModePhased):
		return DeltaModePhased, nil
	case string(DeltaModeLive):
		return DeltaModeLive, nil
	default:
		return DeltaModePhased, fmt.Errorf("invalid delta_mode %q (expected %q or %q)", s, DeltaModePhased, DeltaModeLive)
	}
}

type deltaModeContextKey struct{}

// WithDeltaMode returns a context carrying m. The API handlers stamp it on the
// request context; HandleMessage reads it back and passes it down explicitly,
// because generation runs on a context detached from the request.
func WithDeltaMode(ctx context.Context, m DeltaMode) context.Context {
	return context.WithValue(ctx, deltaModeContextKey{}, m)
}

// DeltaModeFromContext returns the DeltaMode stored by WithDeltaMode, or
// DeltaModePhased when ctx carries none.
func DeltaModeFromContext(ctx context.Context) DeltaMode {
	if ctx == nil {
		return DeltaModePhased
	}
	if m, _ := ctx.Value(deltaModeContextKey{}).(DeltaMode); m == DeltaModeLive {
		return DeltaModeLive
	}
	return DeltaModePhased
}
