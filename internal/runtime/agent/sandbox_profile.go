package agent

import (
	"github.com/hollis-labs/go-sandbox/sandbox"
)

// buildSandboxProfile composes the sandbox.Profile applied to the spawned
// child. Phase 3b ships a minimal pass-through that respects WideOpen for
// ModeBackground; Phase 6 layers AllowLoopback + FS allowlists (workdir,
// ~/.nanite, $TMPDIR/nanite-boot-*) + LoopbackForwardPorts on top.
func buildSandboxProfile(base sandbox.Profile, opts Options) sandbox.Profile {
	if opts.Mode == ModeBackground && opts.WideOpen {
		// Privileged primitive: no enforcement. Audited callsites only.
		return sandbox.Profile{}
	}
	return base
}
