package slots

// Placement says where a slot's assembled content goes in a request. Slots
// are the logical layer hosts and the engine write to; placement compiles
// them into what is sent.
type Placement uint8

const (
	// InSystem puts the slot in the system blocks, the cached prefix every
	// request shares. A change there invalidates the cache for everything
	// after it, the conversation included, so it suits content that rarely
	// changes.
	InSystem Placement = iota
	// InTurn puts the slot's content in front of the user's message that
	// starts the turn, stored with it, so the system blocks and the earlier
	// messages stay byte-identical and the cache holds. It suits content
	// that changes from turn to turn.
	InTurn
)

// DefaultPlacement is where a slot goes unless a host says otherwise.
// UserContext, the per-turn context a host passes with a message, goes in
// the turn; every other slot is in the system prefix.
func DefaultPlacement(name Name) Placement {
	if name == UserContext {
		return InTurn
	}
	return InSystem
}
