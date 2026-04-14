//go:build devmode

package devmode

import "testing"

// TestHostDevSigningBypass_DevmodeBuild asserts that when compiled with the
// devmode build tag, the bypass flag flips on. Paired with
// TestHostDevSigningBypass_ProductionDefault, this guards the build-tag
// contract for Track J.2.
func TestHostDevSigningBypass_DevmodeBuild(t *testing.T) {
	if !HostDevSigningBypass {
		t.Fatal("HostDevSigningBypass must be true under the devmode build tag")
	}
}
