package api

import (
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/store"
)

// The host runtime feed's SSE frames. Field order is the store record's.
// These are a versioned wire protocol (schema_version, and the
// host_runtime.*.v1 event names), so each must stay byte-equal to the record
// it is built from.

// HostRuntimeHeadView is the host_runtime.head.v1 frame.
type HostRuntimeHeadView struct {
	SchemaVersion          string `json:"schema_version"`
	SessionID              string `json:"session_id"`
	LatestCursor           int64  `json:"latest_cursor"`
	PrunedThroughCursor    int64  `json:"pruned_through_cursor"`
	RetentionDropped       int64  `json:"retention_dropped"`
	RuntimeGenerationFloor int64  `json:"runtime_generation_floor"`
	CurrentRuntimeRunID    string `json:"current_runtime_run_id,omitempty"`
}

func hostRuntimeHeadToView(h store.HostRuntimeHead) HostRuntimeHeadView {
	return HostRuntimeHeadView{
		SchemaVersion:          h.SchemaVersion,
		SessionID:              h.SessionID,
		LatestCursor:           h.LatestCursor,
		PrunedThroughCursor:    h.PrunedThroughCursor,
		RetentionDropped:       h.RetentionDropped,
		RuntimeGenerationFloor: h.RuntimeGenerationFloor,
		CurrentRuntimeRunID:    h.CurrentRuntimeRunID,
	}
}

// HostRuntimeGapView is the host_runtime.gap.v1 frame.
type HostRuntimeGapView struct {
	SchemaVersion          string `json:"schema_version"`
	SessionID              string `json:"session_id"`
	Reason                 string `json:"reason"`
	RequestedCursor        int64  `json:"requested_cursor"`
	OldestAvailable        int64  `json:"oldest_available"`
	LatestCursor           int64  `json:"latest_cursor"`
	MissingCursorSpan      int64  `json:"missing_cursor_span"`
	RetentionDropped       int64  `json:"retention_dropped"`
	RuntimeGenerationFloor int64  `json:"runtime_generation_floor"`
	CurrentRuntimeRunID    string `json:"current_runtime_run_id,omitempty"`
}

func hostRuntimeGapToView(g *store.HostRuntimeGap) HostRuntimeGapView {
	return HostRuntimeGapView{
		SchemaVersion:          g.SchemaVersion,
		SessionID:              g.SessionID,
		Reason:                 g.Reason,
		RequestedCursor:        g.RequestedCursor,
		OldestAvailable:        g.OldestAvailable,
		LatestCursor:           g.LatestCursor,
		MissingCursorSpan:      g.MissingCursorSpan,
		RetentionDropped:       g.RetentionDropped,
		RuntimeGenerationFloor: g.RuntimeGenerationFloor,
		CurrentRuntimeRunID:    g.CurrentRuntimeRunID,
	}
}

// HostRuntimeEventSourceView is an event's source.
type HostRuntimeEventSourceView struct {
	Channel    string `json:"channel"`
	Confidence string `json:"confidence,omitempty"`
}

// HostRuntimeProcessView is an event's process. It is a value, not a
// pointer, in HostRuntimeEventView: omitempty does not drop a struct, so
// "process" is always present (as {} when empty), as the record encodes.
type HostRuntimeProcessView struct {
	Provider          string `json:"provider,omitempty"`
	Runtime           string `json:"runtime,omitempty"`
	ProviderSessionID string `json:"provider_session_id,omitempty"`
}

// HostRuntimeEventView is the host_runtime.v1 frame. Payload passes through
// verbatim; a nil payload encodes as null.
type HostRuntimeEventView struct {
	SchemaVersion     string                     `json:"schema_version"`
	Cursor            int64                      `json:"cursor"`
	SessionID         string                     `json:"session_id"`
	RuntimeRunID      string                     `json:"runtime_run_id"`
	RuntimeGeneration int64                      `json:"runtime_generation"`
	SourceEventID     string                     `json:"source_event_id"`
	SourceSequence    uint64                     `json:"source_sequence"`
	Kind              string                     `json:"kind"`
	OccurredAt        string                     `json:"occurred_at"`
	TurnID            string                     `json:"turn_id,omitempty"`
	ParentID          string                     `json:"parent_id,omitempty"`
	Source            HostRuntimeEventSourceView `json:"source"`
	Process           HostRuntimeProcessView     `json:"process,omitempty"`
	Payload           json.RawMessage            `json:"payload"`
	PayloadTruncated  bool                       `json:"payload_truncated,omitempty"`
	PayloadVisibility string                     `json:"payload_visibility"`
}

func hostRuntimeEventToView(e *store.HostRuntimeEvent) HostRuntimeEventView {
	return HostRuntimeEventView{
		SchemaVersion:     e.SchemaVersion,
		Cursor:            e.Cursor,
		SessionID:         e.SessionID,
		RuntimeRunID:      e.RuntimeRunID,
		RuntimeGeneration: e.RuntimeGeneration,
		SourceEventID:     e.SourceEventID,
		SourceSequence:    e.SourceSequence,
		Kind:              e.Kind,
		OccurredAt:        e.OccurredAt,
		TurnID:            e.TurnID,
		ParentID:          e.ParentID,
		Source:            HostRuntimeEventSourceView{Channel: e.Source.Channel, Confidence: e.Source.Confidence},
		Process: HostRuntimeProcessView{
			Provider:          e.Process.Provider,
			Runtime:           e.Process.Runtime,
			ProviderSessionID: e.Process.ProviderSessionID,
		},
		Payload:           e.Payload,
		PayloadTruncated:  e.PayloadTruncated,
		PayloadVisibility: e.PayloadVisibility,
	}
}
