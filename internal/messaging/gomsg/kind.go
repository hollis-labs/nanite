package gomsg

import messaging "github.com/hollis-labs/go-messaging"

// Nanite's legacy messaging Kind vocabulary (internal/messaging/types.go)
// is narrower than the closed go-messaging Kind enum. These maps are the
// agreed alignment between the two, used by internal/messaging's
// envelope_bridge.go to translate legacy `agent_messages` rows.
//
// Legacy → shared:
//
//	request      → request
//	reply        → response
//	notification → notice
//	handoff      → handoff
//
// The shared enum additionally has status_update and escalation, which
// have no legacy Kind equivalent — the legacy model expressed those as
// the row-level `type` column (status_update, help_request). FromGoKind
// folds them back onto "notification" so a round-trip never produces an
// invalid legacy Kind.

var legacyToGo = map[string]messaging.Kind{
	"request":      messaging.MsgKindRequest,
	"reply":        messaging.MsgKindResponse,
	"notification": messaging.MsgKindNotice,
	"handoff":      messaging.MsgKindHandoff,
}

var goToLegacy = map[messaging.Kind]string{
	messaging.MsgKindRequest:      "request",
	messaging.MsgKindResponse:     "reply",
	messaging.MsgKindNotice:       "notification",
	messaging.MsgKindHandoff:      "handoff",
	messaging.MsgKindStatusUpdate: "notification",
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
