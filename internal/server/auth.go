package server

import (
	"fmt"
	"net"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

// Auth is the server's authentication wiring (CW-20261011-0036). Every
// request to /api/ is authenticated by Guard; there is no unauthenticated
// mode other than the explicit development flag on the Guard.
type Auth struct {
	// Guard enforces credentials. A nil Guard (or one with no Store) refuses
	// every protected request: the server fails closed.
	Guard *apiauth.Guard
	// OperatorTokenPath is rewritten when the operator credential is rotated
	// through the API.
	OperatorTokenPath string
}

// validateBind refuses every non-loopback bind. Nanite has no TLS, and a
// bearer credential must never cross a network in the clear, so there is no
// off-box mode until the TLS task (CW-20261011-0043) lands.
func validateBind(bind string) error {
	if bind == "localhost" {
		return nil
	}
	ip := net.ParseIP(bind)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to bind %q: Nanite listens on loopback only until it has TLS (see CW-20261011-0043); reach it through an SSH forward or a TLS-terminating proxy on the same machine", bind)
}
