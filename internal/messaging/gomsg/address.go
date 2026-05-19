package gomsg

import (
	"fmt"

	messaging "github.com/hollis-labs/go-messaging"
)

// DefaultAuthority is the authority segment used by a standalone Nanite
// install that has not been assigned a federation identity. Every
// address minted locally carries this authority unless overridden, so a
// zero-config install still produces well-formed, contract-valid URNs.
const DefaultAuthority = "nanite.local"

// userSentinel mirrors messaging.UserSentinel ("user") — the reserved
// agent id that addresses the human user in a session. It is duplicated
// here (rather than imported) to keep gomsg a leaf package with no
// dependency on the legacy internal/messaging types.
const userSentinel = "user"

// AgentAddress maps a Nanite (sessionID, agentID) tuple to a typed
// go-messaging URN Address under the given authority.
//
// A normal agent becomes  msg://agent/<authority>/<sessionID>/<agentID>
// — the session id is the routable ID and the agent id is the SubID, so
// the same agent in two sessions has two distinct addresses (preserving
// the per-(session,agent) inbox semantics of the legacy tuple model).
//
// The user sentinel becomes  msg://user/<authority>/<sessionID>  — the
// AddressKind carries the "this is the human" signal that the legacy
// "user" string sentinel carried.
//
// An empty authority defaults to DefaultAuthority.
func AgentAddress(authority, sessionID, agentID string) messaging.Address {
	if authority == "" {
		authority = DefaultAuthority
	}
	if agentID == userSentinel {
		return messaging.Address{
			Kind:      messaging.KindUser,
			Authority: authority,
			ID:        sessionID,
		}
	}
	return messaging.Address{
		Kind:      messaging.KindAgent,
		Authority: authority,
		ID:        sessionID,
		SubID:     agentID,
	}
}

// Tuple is the inverse of AgentAddress: it recovers the Nanite
// (sessionID, agentID) tuple from a go-messaging Address. A KindUser
// address yields agentID == userSentinel. Address kinds other than
// agent/user have no tuple representation and return an error.
func Tuple(addr messaging.Address) (sessionID, agentID string, err error) {
	switch addr.Kind {
	case messaging.KindUser:
		if addr.ID == "" {
			return "", "", fmt.Errorf("gomsg: user address missing session id: %s", addr.URN())
		}
		return addr.ID, userSentinel, nil
	case messaging.KindAgent:
		if addr.ID == "" || addr.SubID == "" {
			return "", "", fmt.Errorf("gomsg: agent address missing session/agent id: %s", addr.URN())
		}
		return addr.ID, addr.SubID, nil
	default:
		return "", "", fmt.Errorf("gomsg: address kind %q has no (session,agent) tuple", addr.Kind)
	}
}

// IsLocal reports whether addr belongs to the local authority — the
// single question that decides "internal vs external" in the federated
// model. An empty local authority defaults to DefaultAuthority so the
// predicate is well-defined for a zero-config install.
func IsLocal(addr messaging.Address, local string) bool {
	if local == "" {
		local = DefaultAuthority
	}
	return addr.Authority == local
}
