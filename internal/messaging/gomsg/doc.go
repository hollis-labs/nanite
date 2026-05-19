// Package gomsg makes Nanite a first-class peer on the portfolio-shared
// `github.com/hollis-labs/go-messaging` contract.
//
// Nanite's original messaging subsystem (internal/messaging) addresses
// messages by a divergent (session_id, agent_id) tuple and predates the
// shared library. This package aligns Nanite onto the shared contract
// without rebuilding the working subsystem — the "extend, don't rebuild"
// model from docs/torque-messaging-design.md:
//
//   - SQLStore is a durable, SQLite-backed messaging.Store. It satisfies
//     the go-messaging Store contract identically to the in-memory
//     reference impl — verified by go-messaging's messagingtest.RunContract
//     suite (see sqlstore_test.go).
//
//   - Address maps Nanite's (session_id, agent_id) tuple to and from the
//     typed go-messaging URN Address: msg://<kind>/<authority>/<id>[/<subid>].
//     The `authority` segment is the federation seam.
//
//   - Router is the authority-routing Store decorator: the local
//     authority routes to the local Store; a registered foreign authority
//     routes to its peer Store. A standalone install registers no peers
//     and behaves exactly as the bare local Store — federation is purely
//     additive and zero-config-by-default.
//
// The legacy tuple-addressed Store is left untouched. internal/messaging
// provides envelope_bridge.go to translate legacy `agent_messages` rows
// into go-messaging Envelopes, so the two representations interoperate
// during the migration.
package gomsg
