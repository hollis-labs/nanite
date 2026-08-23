package selftools

import "github.com/hollis-labs/nanite/internal/dispatch"

// PanelSignalSink is the narrow stream surface used to emit panel-control
// signals onto the originating session.
type PanelSignalSink interface {
	BroadcastPanelSignal(sessionID, signalType, jsonPayload string) int
}

// PanelLookup returns currently registered plugin panel IDs.
type PanelLookup func() []string

// PanelTrustResolver is the shared H1 trust seam for plugin panel access.
type PanelTrustResolver = dispatch.TrustResolver

// PresentationTools owns card render-target policy and panel/mode signal
// behavior together so both paths share one security-relevant access gate.
type PresentationTools struct {
	SignalSink    PanelSignalSink
	PanelLookup   PanelLookup
	TrustResolver PanelTrustResolver
}

func NewPresentationTools(sink PanelSignalSink, lookup PanelLookup, trust PanelTrustResolver) *PresentationTools {
	return &PresentationTools{SignalSink: sink, PanelLookup: lookup, TrustResolver: trust}
}
