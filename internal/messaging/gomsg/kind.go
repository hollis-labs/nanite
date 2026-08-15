package gomsg

import messaging "github.com/hollis-labs/go-messaging"

// Nanite's legacy messaging Kind vocabulary (internal/messaging/types.go)
// is narrower than the closed go-messaging Kind enum. These maps are the
// agreed alignment between the two, used by internal/messaging's
// envelope_bridge.go to translate legacy `agent_messages` rows.
//
// Legacy → shared:
//
//	request          → request
//	reply            → response
//	notification     → notice
//	handoff          → handoff
//	subagent_result  → status_update
//
// The shared enum additionally has escalation, which has no legacy Kind
// equivalent — the legacy model expressed that as the row-level `type`
// column (status_update, help_request). FromGoKind folds it back onto
// "notification" so a round-trip never produces an invalid legacy Kind.
// status_update round-trips onto subagent_result (CW-20260512-0019) —
// it is the only legacy producer of that shared kind today.

var legacyToGo = map[string]messaging.Kind{
	"request":         messaging.MsgKindRequest,
	"reply":           messaging.MsgKindResponse,
	"notification":    messaging.MsgKindNotice,
	"handoff":         messaging.MsgKindHandoff,
	"subagent_result": messaging.MsgKindStatusUpdate,
}

var goToLegacy = map[messaging.Kind]string{
	messaging.MsgKindRequest:      "request",
	messaging.MsgKindResponse:     "reply",
	messaging.MsgKindNotice:       "notification",
	messaging.MsgKindHandoff:      "handoff",
	messaging.MsgKindStatusUpdate: "subagent_result",
	messaging.MsgKindEscalation:   "notification",
}

// ToGoKind maps a legacy Nanite Kind string to a go-messaging Kind.
// An unrecognized legacy value maps to MsgKindNotice — the safe,
// side-effect-free default in the shared reactor model.
func ToGoKind(legacy string) messaging.Kind {
	if k, ok := legacyToGo[legacy]; ok {
		return k
	}
	return messaging.MsgKindNotice
}

// FromGoKind maps a go-messaging Kind back to a legacy Nanite Kind
// string. Shared kinds with no legacy equivalent fold onto
// "notification" so the result is always a valid legacy Kind.
func FromGoKind(k messaging.Kind) string {
	if legacy, ok := goToLegacy[k]; ok {
		return legacy
	}
	return "notification"
}
