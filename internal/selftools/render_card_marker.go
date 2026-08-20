package selftools

import "fmt"

// EmbedRenderCardMarker builds the `<!--ENVELOPE_DATA:...-->` marker string
// a harness-reactive self-tool handler appends to its own ToolResult text
// when internal/selftools/reactions' Fire() Result carries a resolved
// render_card payload (Result.RenderCardPayload(), internal/selftools/
// reactions/result.go).
//
// Per docs/engineering/architecture/11-harness-reactive-self-tools.md's "The
// import-cycle constraint, and what it means for each reaction kind"
// section: internal/selftools/reactions cannot reach the SSE/streaming
// layer itself (that would cycle back through internal/chat/internal/
// service), so its resolver hands the built envelope payload back to the
// calling self-tool handler here in internal/selftools, which is the layer
// that already knows how to signal a card out via this exact marker —
// callShowCard (self_tools_transport.go) has done this inline for
// card_show/todo tools since before the reaction engine existed. This
// helper factors that construction out so a harness-reactive tool's handler
// (built for real in TASKS/harness-reactive-self-tools/
// 07-worked-example-task-update-report.md) doesn't hand-author it again.
//
// Produces output byte-identical to callShowCard's own construction —
// "%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->" — deliberately, not a new
// delimiter or shape. Matching the existing format exactly is what lets the
// three existing marker consumers (internal/service/chat_generate.go's
// captureEnvelopeData, internal/api/tools_call.go's extractEnvelopeMarker,
// internal/mcpserver/handlers.go's convertEnvelopeMarkers) pick this up
// with zero changes to any of them
// (TASKS/harness-reactive-self-tools/04-render-card-construction.md).
//
// label is the human-readable text preceding the marker — e.g. the
// envelope type, optionally suffixed with a title (see callShowCard's own
// label construction for the convention this mirrors).
// resolvedEnvelopeJSON is the JSON already produced by
// reactions.ResolveRenderCard (or Result.RenderCardPayload()) — already in
// the {kind, version, type, data} envelope wire shape. This helper does not
// parse, validate, or reshape it; it only embeds the string verbatim.
func EmbedRenderCardMarker(label string, resolvedEnvelopeJSON string) string {
	return fmt.Sprintf("%s\n<!--ENVELOPE_DATA:%s:ENVELOPE_DATA-->", label, resolvedEnvelopeJSON)
}
