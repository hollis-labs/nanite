//go:build !devmode

package devmode

import "testing"

// TestHostDevSigningBypass_ProductionDefault asserts that in a default
// (production) build the signing-bypass flag is off. This is the
// safe-by-default invariant Track J.2 encodes: no runtime path can flip it.
func TestHostDevSigningBypass_ProductionDefault(t *testing.T) {
	if HostDevSigningBypass {
		t.Fatal("HostDevSigningBypass must be false in production builds")
	}
}
