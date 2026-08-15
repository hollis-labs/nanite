package a2a

import (
	"crypto/rand"
	"encoding/base32"
	"strings"

	messaging "github.com/hollis-labs/go-messaging"
)

// ── Canonical Address Derivation ─────────────────────────────────────────────
//
// This file wraps go-messaging's Address and ParseURN to provide the one
// canonical place Nanite derives agent msg:// addresses, closing the
// hand-rolled duplicate constant gap (internal/store/agents.go:18, FU-28).
//
// Address format: msg://<kind>/<authority>/<id>[/<subid>]
// Example: msg://agent/nanite/agt_7xp4w9zq2a

const (
	// AuthorityNanite is the authority component for Nanite-hosted agents.
	// Matches the portfolio-wide convention (Hadron uses "hadron", Tether
	// uses "agent-mux"). This replaces the hand-rolled "agent-mux" constant
	// in internal/store/agents.go that was copied without imports.
	AuthorityNanite = "nanite"

	// agentIDPrefix is the prefix for generated agent IDs.
	agentIDPrefix = "agt_"
)

// Address is an alias for go-messaging's Address type, re-exported here for
// zero-import discipline (future a2a clients should not need to import
// go-messaging directly).
type Address = messaging.Address

// AddressKind is an alias for go-messaging's AddressKind.
type AddressKind = messaging.AddressKind

// Address kinds from go-messaging (re-exported for convenience).
//
// KindGroup is deliberately not re-exported here: it only exists in
// go-messaging v0.3.0+, and Nanite is pinned to v0.2.1 (go.mod). Add it
// back if/when the pin moves — see the design doc's explicit non-goal on
// reconciling this version gap.
const (
	KindAgent    = messaging.KindAgent
	KindUser     = messaging.KindUser
	KindService  = messaging.KindService
	KindSession  = messaging.KindSession
	KindWorkflow = messaging.KindWorkflow
)

// ParseURN parses a canonical messaging URN into an Address.
// Returns error for malformed strings or unknown AddressKinds.
//
// Example: "msg://agent/nanite/agt_7xp4w9zq2a" -> Address{Kind: KindAgent, Authority: "nanite", ID: "agt_7xp4w9zq2a"}
func ParseURN(s string) (Address, error) {
	return messaging.ParseURN(s)
}

// GenerateAgentURN creates a new random agent URN with Nanite authority.
// Format: msg://agent/nanite/agt_<10-char-base32>
//
// This is the canonical agent URN generator for Nanite, replacing the
// duplicate logic in internal/store/agents.go (generateAgentURN).
func GenerateAgentURN() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("a2a: rand.Read failed: " + err.Error())
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	id := agentIDPrefix + strings.ToLower(enc[:10])

	addr := Address{
		Kind:      KindAgent,
		Authority: AuthorityNanite,
		ID:        id,
	}
	return addr.URN()
}

// SlugAliasURN constructs an agent URN from a human-readable slug.
// Format: msg://agent/nanite/<slug>
//
// This is the canonical slug-based URN generator for Nanite, replacing
// the duplicate logic in internal/store/agents.go (slugAliasURN).
func SlugAliasURN(slug string) string {
	addr := Address{
		Kind:      KindAgent,
		Authority: AuthorityNanite,
		ID:        slug,
	}
	return addr.URN()
}

// NewAgentAddress constructs an agent Address from an ID.
func NewAgentAddress(id string) Address {
	return Address{
		Kind:      KindAgent,
		Authority: AuthorityNanite,
		ID:        id,
	}
}

// NewWorkflowAddress constructs a workflow Address from an ID.
func NewWorkflowAddress(id string) Address {
	return Address{
		Kind:      KindWorkflow,
		Authority: AuthorityNanite,
		ID:        id,
	}
}

// NewSessionAddress constructs a session Address from an ID.
func NewSessionAddress(id string) Address {
	return Address{
		Kind:      KindSession,
		Authority: AuthorityNanite,
		ID:        id,
	}
}

// IsAgentAddress returns true if the Address is an agent kind.
func IsAgentAddress(addr Address) bool {
	return addr.Kind == KindAgent
}

// IsWorkflowAddress returns true if the Address is a workflow kind.
func IsWorkflowAddress(addr Address) bool {
	return addr.Kind == KindWorkflow
}
